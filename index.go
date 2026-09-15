package hypermatch

import (
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
	prefixLens list[int] // sorted
	suffixLens list[int] // sorted
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

func addLength(lens *list[int], n int) {
	cur := lens.load()
	if i, found := slices.BinarySearch(cur, n); !found {
		lens.replace(slices.Insert(slices.Clone(cur), i, n))
	}
}

// collect appends the leaves matching the folded value v to hits. The
// exists leaf is not included, it is handled per property.
func (x *valueIndex) collect(v []byte, hits []*leaf, sc *scratch) []*leaf {
	if l, ok := x.equals.getBytes(v); ok {
		hits = append(hits, l)
	}
	for _, n := range x.prefixLens.load() {
		if n > len(v) {
			break
		}
		if l, ok := x.prefixes.getBytes(v[:n]); ok {
			hits = append(hits, l)
		}
	}
	for _, n := range x.suffixLens.load() {
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
	spinner bool                     // consumes any byte; immutable
	final   list[*leaf]              // patterns ending here
	reach   list[*leaf]              // patterns ending here with a trailing '*'
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
	if trailing {
		n.reach.add(l)
	} else {
		n.final.add(l)
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
		hits = append(hits, s.final.load()...)
	}
	sc.globCur, sc.globNext = cur, next
	return hits
}

// enter adds n to set, along with the spinner its '*' transition reaches
// without consuming input.
func (n *globNode) enter(set []*globNode, hits []*leaf) ([]*globNode, []*leaf) {
	set = append(set, n)
	hits = append(hits, n.reach.load()...)
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
