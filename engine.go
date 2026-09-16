package hypermatch

import (
	"cmp"
	"hash/maphash"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

// Rules are stored in a trie whose edges are normalized conditions. A rule
// is the path of its conditions, sorted by path, and its number is stored in
// the state that path ends in. Rules with common conditions share a prefix
// and are evaluated together, and a Match call visits every state at most
// once.

type trie struct {
	root      state
	paths     strMap[string]    // the path of every condition, for MatchJSON
	prefixes  strMap[struct{}]  // the parts of these paths before each "."
	wilds     strMap[*wildPath] // the paths ending in ".*", by the part before
	hasAbsent atomic.Bool       // some condition is {"exists": false}
	edgeSeq   uint64            // writer only
}

// wildPath is a condition path such as "labels.*", which refers to the
// values of all paths that begin with "labels.".
type wildPath struct {
	path string
	hash uint64 // of path
}

// wildBase returns the part of path before its final ".*", and whether
// path is a wildcard path. The path ".*" is not: it has no part before.
func wildBase(path string) (string, bool) {
	if len(path) > 2 && strings.HasSuffix(path, ".*") {
		return path[:len(path)-2], true
	}
	return "", false
}

// addPath records path and its prefixes, for MatchJSON, and wildcard paths,
// for all Match calls. Writer only.
func (t *trie) addPath(path string) {
	if _, ok := t.paths.get(path); ok {
		return
	}
	for i := 0; i < len(path); i++ {
		if path[i] == '.' {
			if _, ok := t.prefixes.get(path[:i]); !ok {
				t.prefixes.put(path[:i], struct{}{})
			}
		}
	}
	if base, ok := wildBase(path); ok {
		t.wilds.put(base, &wildPath{path: path, hash: maphash.String(hashSeed, path)})
	}
	t.paths.put(path, path)
}

type state struct {
	groups strMap[*group]                // outgoing conditions, by path
	glist  list[*group]                  // the same groups, for iteration
	absent atomic.Pointer[[]*absentEdge] // conditions that hold if their path is absent; copied on write
	rule   atomic.Uint32                 // 1 + number of the first rule ending here, or 0
	minKey atomic.Uint32                 // 1 + the smallest order key of the rules in the subtree, or 0 if there are none
	rules  list[uint32]                  // numbers of further rules ending here
}

// lowerMinKey records that a rule with the order key key ends in the subtree
// of s. Writer only.
func (s *state) lowerMinKey(key uint32) {
	if m := s.minKey.Load(); m == 0 || key+1 < m {
		s.minKey.Store(key + 1)
	}
}

// absentEdge is a condition {"exists": false}, which holds if the event has
// no value at path.
type absentEdge struct {
	path string
	hash uint64
	next *state
}

// addRule records that rule num ends in s. Writer only.
func (s *state) addRule(num uint32) {
	if s.rule.Load() == 0 {
		s.rule.Store(num + 1)
		return
	}
	s.rules.add(num)
}

func (s *state) hasRule(num uint32) bool {
	return s.rule.Load() == num+1 || slices.Contains(s.rules.load(), num)
}

// appendRules appends the numbers of the rules ending in s to dst.
func (s *state) appendRules(dst []uint32) []uint32 {
	if r := s.rule.Load(); r != 0 {
		dst = append(dst, r-1)
		dst = append(dst, s.rules.load()...)
	}
	return dst
}

// group holds all conditions leaving one state on one path. Their leaves
// share one value index, so every value of the event is examined once per
// group, regardless of the number of conditions.
type group struct {
	path  string
	hash  uint64 // of path
	index valueIndex
	neg   list[*edge] // conditions containing anythingBut, evaluated whenever the path is present

	// Writer only.
	byKey  map[string]keyed // leaves and edges by expression key
	leaves []*leaf          // by id
}

// keyed holds the leaf and the edge of an expression key. A condition that
// consists of a single pattern has both.
type keyed struct {
	leaf *leaf
	edge *edge
}

// leaf is a single-value pattern: equals, prefix, suffix or wildcard. Most
// leaves belong to a single condition, which is therefore stored inline.
type leaf struct {
	id   uint32               // index in group.leaves
	edge atomic.Pointer[edge] // first monotone condition that may hold when this leaf matches
	more list[*edge]          // further such conditions
}

// addEdge registers e at l. Writer only.
func (l *leaf) addEdge(e *edge) {
	if l.edge.Load() == nil {
		l.edge.Store(e)
		return
	}
	l.more.add(e)
}

// appendEdges appends the conditions registered at l to dst.
func (l *leaf) appendEdges(dst []*edge) []*edge {
	if e := l.edge.Load(); e != nil {
		dst = append(dst, e)
		dst = append(dst, l.more.load()...)
	}
	return dst
}

// edge is a condition leading to the next state.
type edge struct {
	id   uint64
	f    *formula // nil if the condition holds whenever a leaf it is registered at matches
	next *state
}

// formula is the compiled form of an expr over the leaves of a group.
type formula struct {
	op   exprOp
	leaf uint32
	subs []formula
}

// eval reports whether f holds if exactly the leaves whose bit is set in
// hits match. Leaves beyond the last word never have a bit, so they count as
// not matching, which is what the caller means by leaving them out.
func (f *formula) eval(hits []uint64) bool {
	switch f.op {
	case opLeaf:
		w := int(f.leaf >> 6)
		return w < len(hits) && hits[w]&(1<<(f.leaf&63)) != 0
	case opAnyOf:
		for i := range f.subs {
			if f.subs[i].eval(hits) {
				return true
			}
		}
		return false
	case opAllOf:
		for i := range f.subs {
			if !f.subs[i].eval(hits) {
				return false
			}
		}
		return true
	default:
		return !f.subs[0].eval(hits)
	}
}

// triggers returns leaves such that the monotone formula f can only hold if
// one of them matches. The edge of f is registered at exactly these leaves.
func (f *formula) triggers() []uint32 {
	switch f.op {
	case opLeaf:
		return []uint32{f.leaf}
	case opAllOf:
		var best []uint32
		for i := range f.subs {
			if t := f.subs[i].triggers(); best == nil || len(t) < len(best) {
				best = t
			}
		}
		return best
	default:
		var all []uint32
		for i := range f.subs {
			all = append(all, f.subs[i].triggers()...)
		}
		slices.Sort(all)
		return slices.Compact(all)
	}
}

// insert adds the path of conds for a rule with the order key key and
// returns the state it ends in. Writer only. Everything a reader can reach is
// complete before it is published, and the order keys along the path are
// lowered before the caller adds the rule to the returned state.
func (t *trie) insert(conds []condition, key uint32) *state {
	s := &t.root
	s.lowerMinKey(key)
	for _, c := range conds {
		s = t.follow(s, c)
		s.lowerMinKey(key)
	}
	return s
}

func (t *trie) follow(s *state, c condition) *state {
	if c.expr.op == opAbsent {
		return t.followAbsent(s, c.path)
	}
	g, ok := s.groups.get(c.path)
	if !ok {
		g = &group{
			path:  c.path,
			hash:  maphash.String(hashSeed, c.path),
			byKey: make(map[string]keyed),
		}
		s.groups.put(c.path, g)
		s.glist.add(g)
		t.addPath(c.path)
	}
	if e := g.byKey[c.expr.key].edge; e != nil {
		return e.next
	}
	t.edgeSeq++
	f := g.compile(c.expr)
	e := &edge{id: t.edgeSeq, next: new(state)}
	if !c.expr.simple() {
		e.f = &f
	}
	k := g.byKey[c.expr.key] // compile may have added the leaf of this key
	k.edge = e
	g.byKey[c.expr.key] = k
	if c.expr.monotone() {
		for _, id := range f.triggers() {
			g.leaves[id].addEdge(e)
		}
	} else {
		g.neg.add(e)
	}
	return e.next
}

// followAbsent returns the state the condition {"exists": false} on path
// leads to from s, adding it if necessary.
func (t *trie) followAbsent(s *state, path string) *state {
	var cur []*absentEdge
	if p := s.absent.Load(); p != nil {
		cur = *p
	}
	for _, a := range cur {
		if a.path == path {
			return a.next
		}
	}
	a := &absentEdge{path: path, hash: maphash.String(hashSeed, path), next: new(state)}
	next := append(slices.Clip(cur), a)
	t.hasAbsent.Store(true) // before the edge, so that no Match can miss it
	s.absent.Store(&next)
	t.addPath(path) // MatchJSON must see whether the path is present
	return a.next
}

// noRule marks the numbers of the rules copyRules drops.
const noRule = ^uint32(0)

// copyRules adds the rules of src to t, renumbering rule n to renum[n] and
// dropping the rules renumbered to noRule. Conditions no remaining rule uses
// are not copied. The conditions are recovered from their keys, so the
// original condition sets need not be kept. Writer only.
func (t *trie) copyRules(src *trie, renum []uint32) {
	t.copyState(&src.root, nil, renum)
}

func (t *trie) copyState(s *state, prefix []condition, renum []uint32) {
	var live []uint32
	for _, n := range s.appendRules(nil) {
		if m := renum[n]; m != noRule {
			live = append(live, m)
		}
	}
	if len(live) > 0 {
		dst := t.insert(prefix, slices.Min(live)) // the new numbers are the order keys
		for _, m := range live {
			dst.addRule(m)
		}
	}
	if p := s.absent.Load(); p != nil {
		for _, a := range *p {
			t.copyState(a.next, append(prefix, condition{path: a.path, expr: absentExpr}), renum)
		}
	}
	for _, g := range s.glist.load() {
		for key, k := range g.byKey {
			if k.edge != nil {
				t.copyState(k.edge.next, append(prefix, condition{path: g.path, expr: parseKey(key)}), renum)
			}
		}
	}
}

func (g *group) compile(e *expr) formula {
	if e.op == opLeaf {
		return formula{op: opLeaf, leaf: g.leaf(e).id}
	}
	f := formula{op: e.op, subs: make([]formula, len(e.subs))}
	for i, s := range e.subs {
		f.subs[i] = g.compile(s)
	}
	return f
}

func (g *group) leaf(e *expr) *leaf {
	k := g.byKey[e.key]
	if k.leaf != nil {
		return k.leaf
	}
	k.leaf = &leaf{id: uint32(len(g.leaves))}
	g.leaves = append(g.leaves, k.leaf)
	g.byKey[e.key] = k
	g.index.add(e, k.leaf)
	return k.leaf
}

// scratch holds the buffers of one Match call. Scratches are pooled, so
// matching does not allocate in the steady state.
type scratch struct {
	props    []property
	spans    []span  // one per distinct path
	slots    []int32 // hash table of spans by path: index+1, or 0 if empty
	fbuf     []byte  // folded values
	frefs    []vref
	hits     []*leaf
	bits     []uint64 // the ids of hits as a set, for the formulas
	edges    []*edge  // stack of edges to follow
	out      []uint32
	globCur  []*globNode
	globNext []*globNode
	absent   bool // the rules contain {"exists": false} conditions

	// The rules visible to this call, loaded before the traversal.
	version uint32   // of the table, see ReplaceRule
	limit   uint32   // rules from this number on were added during the call
	removed []uint64 // words of the removed rules
	meta    []uint64 // words of the ruleMeta

	// MatchFirst only.
	first   bool   // look for the visible rule with the smallest order key
	best    uint32 // its number
	bestKey uint32 // its order key, or math.MaxUint32 if none was found

	// MatchJSON only.
	jvals []jval     // values found, before they are grouped into spans
	pbuf  []byte     // path of the current JSON value
	jtmp  []byte     // decoded strings that are not needed
	jwild []openWild // the wildcard paths the current JSON value is below
}

// property is an event property with at least one value.
type property struct {
	values []string
	next   int // index of the next property with the same path, or -1
}

// span collects the properties that share a path. Their values are folded
// on first use into frefs[flo:fhi].
type span struct {
	path        string
	hash        uint64
	first, last int
	flo, fhi    int
	folded      bool
}

// vref is the position of a folded value in fbuf, together with its hash
// and its numeric value once computed.
type vref struct {
	lo, hi uint32
	hash   uint64
	num    float64
	hashed bool
	parsed bool // num and isNum are set
	isNum  bool
}

// number returns the value v, located at r, as a number.
func (r *vref) number(v []byte) (float64, bool) {
	if !r.parsed {
		r.num, r.isNum = parseNumber(v)
		r.parsed = true
	}
	return r.num, r.isNum
}

var scratchPool = sync.Pool{New: func() any { return new(scratch) }}

// reset prepares sc for matching event against the rules of t.
func (sc *scratch) reset(event []Property, t *trie) {
	sc.props = sc.props[:0]
	sc.spans = sc.spans[:0]
	size := 8
	for size < 2*len(event) {
		size *= 2
	}
	sc.initSlots(size)
	mask := uint64(size - 1)
	wilds := t.wilds.t.Load() // nil unless a rule has a wildcard path
	for i := range event {
		p := &event[i]
		if len(p.Values) == 0 {
			continue
		}
		idx := len(sc.props)
		sc.props = append(sc.props, property{values: p.Values, next: -1})
		h := maphash.String(hashSeed, p.Path)
		for j := h & mask; ; j = (j + 1) & mask {
			s := sc.slots[j]
			if s == 0 {
				sc.slots[j] = int32(len(sc.spans) + 1)
				sc.spans = append(sc.spans, span{path: p.Path, hash: h, first: idx, last: idx})
				break
			}
			if sp := &sc.spans[s-1]; sp.hash == h && sp.path == p.Path {
				sc.props[sp.last].next = idx
				sp.last = idx
				break
			}
		}
		if wilds != nil {
			sc.addWild(wilds, p)
			mask = uint64(len(sc.slots) - 1) // the table may have grown
		}
	}
	sc.fbuf = sc.fbuf[:0]
	sc.frefs = sc.frefs[:0]
	sc.out = sc.out[:0]
	sc.first = false
}

// addWild adds the values of p to the spans of the wildcard paths p is
// below. If p has a wildcard path itself, its own span already holds them.
func (sc *scratch) addWild(wilds *strTable[*wildPath], p *Property) {
	for i := 0; i < len(p.Path); i++ {
		if p.Path[i] != '.' {
			continue
		}
		base := p.Path[:i]
		w, ok := wilds.lookup(maphash.String(hashSeed, base), base)
		if !ok || w.path == p.Path {
			continue
		}
		idx := len(sc.props)
		sc.props = append(sc.props, property{values: p.Values, next: -1})
		sp := &sc.spans[sc.jsonSpan(w.path, w.hash)]
		if sp.first < 0 {
			sp.first, sp.folded = idx, false // new: its values are not folded yet
		} else {
			sc.props[sp.last].next = idx
		}
		sp.last = idx
	}
}

// visible reports whether rule n is one of the rules this call matches.
func (sc *scratch) visible(n uint32) bool {
	if n >= sc.limit || bitsHas(sc.removed, n) {
		return false
	}
	if sc.meta == nil {
		return true
	}
	from, until := metaSpan(sc.meta, n)
	return from <= sc.version && (until == 0 || sc.version < until)
}

// key returns the order key of rule n: the position of its identifier in
// the results of Match.
func (sc *scratch) key(n uint32) uint32 {
	if sc.meta == nil {
		return n
	}
	return metaOrder(sc.meta, n)
}

// improve updates the best rule of MatchFirst with the rules ending in s
// and reports whether the subtree of s can contain a better one.
func (sc *scratch) improve(s *state) bool {
	if m := s.minKey.Load(); m == 0 || m-1 >= sc.bestKey {
		return false
	}
	if r := s.rule.Load(); r != 0 {
		sc.consider(r - 1)
		for _, n := range s.rules.load() {
			sc.consider(n)
		}
	}
	return true
}

func (sc *scratch) consider(n uint32) {
	if !sc.visible(n) {
		return
	}
	if k := sc.key(n); k < sc.bestKey {
		sc.best, sc.bestKey = n, k
	}
}

// initSlots empties the span table and resizes it to size slots.
func (sc *scratch) initSlots(size int) {
	if cap(sc.slots) < size {
		sc.slots = make([]int32, size)
	} else {
		sc.slots = sc.slots[:size]
		clear(sc.slots)
	}
}

func (sc *scratch) release() {
	// Do not retain the caller's strings or old tables.
	clear(sc.props)
	clear(sc.spans)
	clear(sc.jwild[:cap(sc.jwild)])
	sc.removed, sc.meta = nil, nil
	if cap(sc.out) > 1<<16 || cap(sc.fbuf) > 1<<20 {
		return // let oversized buffers be collected
	}
	scratchPool.Put(sc)
}

// find returns the span of path, whose hash is h, or nil.
func (sc *scratch) find(path string, h uint64) *span {
	mask := uint64(len(sc.slots) - 1)
	for j := h & mask; ; j = (j + 1) & mask {
		s := sc.slots[j]
		if s == 0 {
			return nil
		}
		if sp := &sc.spans[s-1]; sp.hash == h && sp.path == path {
			return sp
		}
	}
}

// visit collects the rules of s and follows every condition that holds.
func (sc *scratch) visit(s *state) {
	if sc.first {
		if !sc.improve(s) {
			return
		}
	} else {
		sc.out = s.appendRules(sc.out)
	}
	if sc.absent {
		if p := s.absent.Load(); p != nil {
			for _, a := range *p {
				if sc.find(a.path, a.hash) == nil {
					sc.visit(a.next)
				}
			}
		}
	}
	groups := s.glist.load()
	if len(groups) <= len(sc.spans) {
		for _, g := range groups {
			if sp := sc.find(g.path, g.hash); sp != nil {
				sc.evalGroup(g, sp)
			}
		}
		return
	}
	// More groups than paths in the event. The table is published before
	// any group is added to glist, so it is not nil here.
	t := s.groups.t.Load()
	for i := range sc.spans {
		sp := &sc.spans[i]
		if g, ok := t.lookup(sp.hash, sp.path); ok {
			sc.evalGroup(g, sp)
		}
	}
}

// hitBits returns the ids of hits, which are sorted, as a set. The caller
// clears the words it touched again, so the buffer starts out empty.
func (sc *scratch) hitBits(hits []*leaf) []uint64 {
	bits := sc.bits
	if words := int(hits[len(hits)-1].id>>6) + 1; words > len(bits) {
		bits = append(bits, make([]uint64, words-len(bits))...)
		sc.bits = bits
	}
	for _, l := range hits {
		bits[l.id>>6] |= 1 << (l.id & 63)
	}
	return bits
}

func (sc *scratch) evalGroup(g *group, sp *span) {
	// Load the conditions with anythingBut before looking up the values.
	// The leaves of a condition are published before the condition, so all
	// leaves of these conditions are visible to the lookups below. Loaded
	// afterwards, a condition added concurrently could appear without the
	// leaf hits that make its negation false.
	neg := g.neg.load()

	if !sp.folded {
		sp.flo = len(sc.frefs)
		for i := sp.first; i >= 0; i = sc.props[i].next {
			for _, v := range sc.props[i].values {
				lo := len(sc.fbuf)
				sc.fbuf = appendFold(sc.fbuf, v)
				sc.frefs = append(sc.frefs, vref{lo: uint32(lo), hi: uint32(len(sc.fbuf))})
			}
		}
		sp.fhi = len(sc.frefs)
		sp.folded = true
	}

	kinds := g.index.kinds.Load() // after neg, for the same reason
	hits := sc.hits[:0]
	if kinds == 1<<leafEquals {
		// Most groups only compare with equals: look the values up directly.
		t := g.index.equals.t.Load()
		for i := sp.flo; i < sp.fhi; i++ {
			r := &sc.frefs[i]
			v := sc.fbuf[r.lo:r.hi]
			if !r.hashed {
				r.hash, r.hashed = maphash.Bytes(hashSeed, v), true
			}
			if l, ok := t.lookupBytes(r.hash, v); ok {
				hits = append(hits, l)
			}
		}
	} else {
		for i := sp.flo; i < sp.fhi; i++ {
			r := &sc.frefs[i]
			hits = g.index.collect(sc.fbuf[r.lo:r.hi], r, hits, sc, kinds)
		}
	}
	if kinds&(1<<leafExists) != 0 {
		if l := g.index.exists.Load(); l != nil {
			hits = append(hits, l)
		}
	}
	if len(hits) > 1 {
		slices.SortFunc(hits, func(a, b *leaf) int { return cmp.Compare(a.id, b.id) })
		hits = slices.Compact(hits)
	}
	sc.hits = hits

	// Collect the conditions that hold. The children are visited only
	// afterwards, because they reuse hits.
	start := len(sc.edges)
	for _, l := range hits {
		sc.edges = l.appendEdges(sc.edges)
	}
	if len(sc.edges)-start > 1 {
		cand := sc.edges[start:]
		slices.SortFunc(cand, func(a, b *edge) int { return cmp.Compare(a.id, b.id) })
		sc.edges = sc.edges[:start+len(slices.Compact(cand))]
	}

	// Formulas ask which leaves matched, as a set of their ids. Most groups
	// have no formula at all, so the set is only built once one needs it.
	var bits []uint64
	n := start
	for _, e := range sc.edges[start:] {
		if e.f == nil {
			sc.edges[n] = e
			n++
			continue
		}
		if bits == nil && len(hits) > 0 {
			bits = sc.hitBits(hits)
		}
		if e.f.eval(bits) {
			sc.edges[n] = e
			n++
		}
	}
	sc.edges = sc.edges[:n]
	if len(neg) > 0 {
		if bits == nil && len(hits) > 0 {
			bits = sc.hitBits(hits)
		}
		for _, e := range neg {
			if e.f.eval(bits) {
				sc.edges = append(sc.edges, e)
			}
		}
	}
	if bits != nil {
		for _, l := range hits {
			bits[l.id>>6] = 0
		}
	}
	end := len(sc.edges)
	if sc.first && end-start > 1 {
		// Visit the subtrees with the smallest order keys first. A minKey of
		// 0 wraps around and sorts last.
		slices.SortFunc(sc.edges[start:end], func(a, b *edge) int {
			return cmp.Compare(a.next.minKey.Load()-1, b.next.minKey.Load()-1)
		})
	}
	for i := start; i < end; i++ {
		sc.visit(sc.edges[i].next)
	}
	sc.edges = sc.edges[:start]
}
