package hypermatch

import (
	"hash/maphash"
	"slices"
	"strings"
	"sync/atomic"
)

// valueIndex finds the leaves (single-value patterns) of a group that
// match a value. Each kind of leaf has its own structure:
//
//   - equals: one hash lookup
//   - prefix and suffix: one hash lookup per distinct pattern length
//   - wildcard: a trie of the pattern tokens, simulated as an NFA
//   - exists ("*"): matches whenever the property is present
type valueIndex struct {
	equals     strMap[*leaf]
	prefixes   strMap[*leaf]
	suffixes   strMap[*leaf]
	prefixLens atomic.Pointer[[]int] // sorted, copied on write
	suffixLens atomic.Pointer[[]int] // sorted, copied on write
	glob       atomic.Pointer[globNode]
	exists     atomic.Pointer[leaf]
}

// add registers the leaf l for the leaf expression e. Writer only.
func (x *valueIndex) add(e *expr, l *leaf) {
	switch e.kind {
	case leafEquals:
		x.equals.put(e.value, l)
	case leafPrefix:
		x.prefixes.put(e.value, l)
		addLength(&x.prefixLens, len(e.value))
	case leafSuffix:
		x.suffixes.put(e.value, l)
		addLength(&x.suffixLens, len(e.value))
	case leafGlob:
		root := x.glob.Load()
		if root == nil {
			root = new(globNode)
			x.glob.Store(root)
		}
		root.insert(e.value, l)
	case leafExists:
		x.exists.Store(l)
	}
}

func addLength(lens *atomic.Pointer[[]int], n int) {
	var cur []int
	if p := lens.Load(); p != nil {
		cur = *p
	}
	if i, found := slices.BinarySearch(cur, n); !found {
		s := slices.Insert(slices.Clone(cur), i, n)
		lens.Store(&s)
	}
}

func loadLengths(lens *atomic.Pointer[[]int]) []int {
	if p := lens.Load(); p != nil {
		return *p
	}
	return nil
}

// collect appends the leaves matching the folded value v, located at r, to
// hits. The exists leaf is not included, it is handled per property.
func (x *valueIndex) collect(v []byte, r *vref, hits []*leaf, sc *scratch) []*leaf {
	if t := x.equals.t.Load(); t != nil {
		if !r.hashed {
			r.hash, r.hashed = maphash.Bytes(hashSeed, v), true
		}
		if l, ok := t.lookupBytes(r.hash, v); ok {
			hits = append(hits, l)
		}
	}
	for _, n := range loadLengths(&x.prefixLens) {
		if n > len(v) {
			break
		}
		if l, ok := x.prefixes.getBytes(v[:n]); ok {
			hits = append(hits, l)
		}
	}
	for _, n := range loadLengths(&x.suffixLens) {
		if n > len(v) {
			break
		}
		if l, ok := x.suffixes.getBytes(v[len(v)-n:]); ok {
			hits = append(hits, l)
		}
	}
	if root := x.glob.Load(); root != nil {
		hits = root.match(v, hits, sc)
	}
	return hits
}

// globNode is a node in the token trie of the wildcard patterns of a group.
// A '*' token leads to a spinner, a node that consumes any byte. Because the
// trie is a tree and spinners loop implicitly, patterns never share nodes
// they do not share a token prefix with.
type globNode struct {
	kids    byteMap[*globNode]
	spin    atomic.Pointer[globNode] // target of a '*' token
	accept  atomic.Pointer[globAccept]
	spinner bool // consumes any byte; immutable
}

// globAccept holds the patterns ending at a node. Most nodes have none, so
// it is allocated on demand.
type globAccept struct {
	final list[*leaf] // patterns ending here
	reach list[*leaf] // patterns ending here with a trailing '*'
}

// insert adds the folded wildcard pattern for leaf l. Writer only.
func (n *globNode) insert(pattern string, l *leaf) {
	body, trailing := strings.CutSuffix(pattern, "*")
	for i := 0; i < len(body); i++ {
		if body[i] == '*' {
			s := n.spin.Load()
			if s == nil {
				s = &globNode{spinner: true}
				n.spin.Store(s)
			}
			n = s
			continue
		}
		k, ok := n.kids.get(body[i])
		if !ok {
			k = new(globNode)
			n.kids.put(body[i], k)
		}
		n = k
	}
	a := n.accept.Load()
	if a == nil {
		a = new(globAccept)
		n.accept.Store(a)
	}
	if trailing {
		a.reach.add(l)
	} else {
		a.final.add(l)
	}
}

// match simulates the NFA rooted at n on v and appends the leaves of all
// matching patterns to hits.
func (n *globNode) match(v []byte, hits []*leaf, sc *scratch) []*leaf {
	cur, hits := n.enter(sc.globCur[:0], hits)
	next := sc.globNext[:0]
	for _, c := range v {
		next = next[:0]
		for _, s := range cur {
			if s.spinner {
				next = addSpinner(next, s)
			}
			if k, ok := s.kids.get(c); ok {
				next, hits = k.enter(next, hits)
			}
		}
		cur, next = next, cur
		if len(cur) == 0 {
			break
		}
	}
	for _, s := range cur {
		if a := s.accept.Load(); a != nil {
			hits = append(hits, a.final.load()...)
		}
	}
	sc.globCur, sc.globNext = cur, next
	return hits
}

// enter adds n to set, along with the spinner its '*' transition reaches
// without consuming input.
func (n *globNode) enter(set []*globNode, hits []*leaf) ([]*globNode, []*leaf) {
	set = append(set, n)
	if a := n.accept.Load(); a != nil {
		hits = append(hits, a.reach.load()...)
	}
	if s := n.spin.Load(); s != nil {
		set = addSpinner(set, s)
	}
	return set, hits
}

// addSpinner adds s to set unless it is already there. Only spinners can be
// reached twice in one step: through their self-loop and through their
// owner.
func addSpinner(set []*globNode, s *globNode) []*globNode {
	if slices.Contains(set, s) {
		return set
	}
	return append(set, s)
}
