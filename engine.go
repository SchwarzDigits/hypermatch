package hypermatch

import (
	"cmp"
	"hash/maphash"
	"slices"
	"sync"
	"sync/atomic"
)

// Rules are stored in a trie whose edges are normalized conditions. A rule
// is the path of its conditions, sorted by path, and its number is stored in
// the state that path ends in. Rules with common conditions share a prefix
// and are evaluated together, and a Match call visits every state at most
// once.

type trie struct {
	root     state
	paths    strMap[string]   // the path of every condition, for MatchJSON
	prefixes strMap[struct{}] // the parts of these paths before each "."
	edgeSeq  uint64           // writer only
}

// addPath records path and its prefixes for MatchJSON. Writer only.
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
	t.paths.put(path, path)
}

type state struct {
	groups strMap[*group] // outgoing conditions, by path
	glist  list[*group]   // the same groups, for iteration
	rule   atomic.Uint32  // 1 + number of the first rule ending here, or 0
	rules  list[uint32]   // numbers of further rules ending here
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

// eval reports whether f holds if exactly the leaves in hits (sorted by id)
// match.
func (f *formula) eval(hits []*leaf) bool {
	switch f.op {
	case opLeaf:
		_, found := slices.BinarySearchFunc(hits, f.leaf, func(l *leaf, id uint32) int { return cmp.Compare(l.id, id) })
		return found
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

// insert adds the path of conds and returns the state it ends in. Writer
// only. Everything a reader can reach is complete before it is published.
func (t *trie) insert(conds []condition) *state {
	s := &t.root
	for _, c := range conds {
		s = t.follow(s, c)
	}
	return s
}

func (t *trie) follow(s *state, c condition) *state {
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
	var dst *state
	for _, n := range s.appendRules(nil) {
		if m := renum[n]; m != noRule {
			if dst == nil {
				dst = t.insert(prefix)
			}
			dst.addRule(m)
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
	edges    []*edge // stack of edges to follow
	out      []uint32
	globCur  []*globNode
	globNext []*globNode

	// MatchJSON only.
	jvals []jval // values found, before they are grouped into spans
	pbuf  []byte // path of the current JSON value
	jtmp  []byte // decoded strings that are not needed
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
// once computed.
type vref struct {
	lo, hi int
	hash   uint64
	hashed bool
}

var scratchPool = sync.Pool{New: func() any { return new(scratch) }}

func (sc *scratch) reset(event []Property) {
	sc.props = sc.props[:0]
	sc.spans = sc.spans[:0]
	size := 8
	for size < 2*len(event) {
		size *= 2
	}
	sc.initSlots(size)
	mask := uint64(size - 1)
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
	}
	sc.fbuf = sc.fbuf[:0]
	sc.frefs = sc.frefs[:0]
	sc.out = sc.out[:0]
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
	// Do not retain the caller's strings.
	clear(sc.props)
	clear(sc.spans)
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
	sc.out = s.appendRules(sc.out)
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
				sc.frefs = append(sc.frefs, vref{lo: lo, hi: len(sc.fbuf)})
			}
		}
		sp.fhi = len(sc.frefs)
		sp.folded = true
	}

	hits := sc.hits[:0]
	for i := sp.flo; i < sp.fhi; i++ {
		r := &sc.frefs[i]
		hits = g.index.collect(sc.fbuf[r.lo:r.hi], r, hits, sc)
	}
	if l := g.index.exists.Load(); l != nil {
		hits = append(hits, l)
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
	n := start
	for _, e := range sc.edges[start:] {
		if e.f == nil || e.f.eval(hits) {
			sc.edges[n] = e
			n++
		}
	}
	sc.edges = sc.edges[:n]
	for _, e := range neg {
		if e.f.eval(hits) {
			sc.edges = append(sc.edges, e)
		}
	}
	end := len(sc.edges)
	for i := start; i < end; i++ {
		sc.visit(sc.edges[i].next)
	}
	sc.edges = sc.edges[:start]
}
