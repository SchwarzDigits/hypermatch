package hypermatch

import (
	"cmp"
	"slices"
	"strings"
	"sync"
)

// Rules are stored in a trie whose edges are normalized conditions. A rule
// is the path of its conditions, sorted by path, and its number is stored in
// the state that path ends in. Rules with common conditions share a prefix
// and are evaluated together, and a Match call visits every state at most
// once.

type trie struct {
	root    state
	edgeSeq uint64 // writer only
}

type state struct {
	groups strMap[*group] // outgoing conditions, by path
	glist  list[*group]   // the same groups, for iteration
	rules  list[uint32]   // numbers of the rules ending here
}

// group holds all conditions leaving one state on one path. Their leaves
// share one value index, so every value of the event is examined once per
// group, regardless of the number of conditions.
type group struct {
	path  string
	index valueIndex
	neg   list[*edge] // conditions containing anythingBut, evaluated whenever the path is present

	// Writer only.
	edges  map[string]*edge // by expression key
	leaves []*leaf          // by id
	byKey  map[string]*leaf // by expression key
}

// leaf is a single-value pattern: equals, prefix, suffix or wildcard.
type leaf struct {
	id    uint32      // index in group.leaves
	edges list[*edge] // monotone conditions that may hold when this leaf matches
}

// edge is a condition leading to the next state.
type edge struct {
	id     uint64
	f      formula
	simple bool // holds whenever a leaf it is registered at matches
	next   *state
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
		g = &group{path: c.path, edges: make(map[string]*edge), byKey: make(map[string]*leaf)}
		s.groups.put(c.path, g)
		s.glist.add(g)
	}
	if e, ok := g.edges[c.expr.key]; ok {
		return e.next
	}
	t.edgeSeq++
	e := &edge{id: t.edgeSeq, f: g.compile(c.expr), simple: c.expr.simple(), next: new(state)}
	g.edges[c.expr.key] = e
	if c.expr.monotone() {
		for _, id := range e.f.triggers() {
			g.leaves[id].edges.add(e)
		}
	} else {
		g.neg.add(e)
	}
	return e.next
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
	if l, ok := g.byKey[e.key]; ok {
		return l
	}
	l := &leaf{id: uint32(len(g.leaves))}
	g.leaves = append(g.leaves, l)
	g.byKey[e.key] = l
	g.index.add(e, l)
	return l
}

// scratch holds the buffers of one Match call. Scratches are pooled, so
// matching does not allocate in the steady state.
type scratch struct {
	props    []property // properties with values, sorted by path
	spans    []span     // one per distinct path
	fbuf     []byte     // folded values
	frefs    []vref     // positions of the folded values in fbuf
	hits     []*leaf
	edges    []*edge // stack of edges to follow
	out      []uint32
	globCur  []*globNode
	globNext []*globNode
}

type property struct {
	path   string
	values []string
}

// span describes the properties props[lo:hi], which share one path. Their
// values are folded on first use into frefs[flo:fhi].
type span struct {
	path     string
	lo, hi   int
	flo, fhi int
	folded   bool
}

type vref struct{ lo, hi int }

var scratchPool = sync.Pool{New: func() any { return new(scratch) }}

func (sc *scratch) reset(event []Property) {
	sc.props = sc.props[:0]
	for i := range event {
		if len(event[i].Values) > 0 {
			sc.props = append(sc.props, property{event[i].Path, event[i].Values})
		}
	}
	slices.SortFunc(sc.props, func(a, b property) int { return strings.Compare(a.path, b.path) })
	sc.spans = sc.spans[:0]
	for i := 0; i < len(sc.props); {
		j := i + 1
		for j < len(sc.props) && sc.props[j].path == sc.props[i].path {
			j++
		}
		sc.spans = append(sc.spans, span{path: sc.props[i].path, lo: i, hi: j})
		i = j
	}
	sc.fbuf = sc.fbuf[:0]
	sc.frefs = sc.frefs[:0]
	sc.out = sc.out[:0]
}

func (sc *scratch) release() {
	clear(sc.props) // do not retain the caller's strings
	if cap(sc.out) > 1<<16 || cap(sc.fbuf) > 1<<20 {
		return // let oversized buffers be collected
	}
	scratchPool.Put(sc)
}

func (sc *scratch) find(path string) *span {
	i, found := slices.BinarySearchFunc(sc.spans, path, func(s span, p string) int { return strings.Compare(s.path, p) })
	if !found {
		return nil
	}
	return &sc.spans[i]
}

// visit collects the rules of s and follows every condition that holds.
func (sc *scratch) visit(s *state) {
	sc.out = append(sc.out, s.rules.load()...)
	groups := s.glist.load()
	if len(groups) <= len(sc.spans) {
		for _, g := range groups {
			if sp := sc.find(g.path); sp != nil {
				sc.evalGroup(g, sp)
			}
		}
		return
	}
	for i := range sc.spans {
		if g, ok := s.groups.get(sc.spans[i].path); ok {
			sc.evalGroup(g, &sc.spans[i])
		}
	}
}

func (sc *scratch) evalGroup(g *group, sp *span) {
	if !sp.folded {
		sp.flo = len(sc.frefs)
		for _, p := range sc.props[sp.lo:sp.hi] {
			for _, v := range p.values {
				lo := len(sc.fbuf)
				sc.fbuf = appendFold(sc.fbuf, v)
				sc.frefs = append(sc.frefs, vref{lo, len(sc.fbuf)})
			}
		}
		sp.fhi = len(sc.frefs)
		sp.folded = true
	}

	hits := sc.hits[:0]
	for _, r := range sc.frefs[sp.flo:sp.fhi] {
		hits = g.index.collect(sc.fbuf[r.lo:r.hi], hits, sc)
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
		sc.edges = append(sc.edges, l.edges.load()...)
	}
	if len(sc.edges)-start > 1 {
		cand := sc.edges[start:]
		slices.SortFunc(cand, func(a, b *edge) int { return cmp.Compare(a.id, b.id) })
		sc.edges = sc.edges[:start+len(slices.Compact(cand))]
	}
	n := start
	for _, e := range sc.edges[start:] {
		if e.simple || e.f.eval(hits) {
			sc.edges[n] = e
			n++
		}
	}
	sc.edges = sc.edges[:n]
	for _, e := range g.neg.load() {
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
