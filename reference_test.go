package hypermatch

import (
	"cmp"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
)

// refMatcher is a deliberately naive implementation of the documented
// matching semantics. It is the oracle for the differential tests.
type refMatcher struct {
	rules []refRule
	first map[int]int // position of the first rule of each identifier
}

type refRule struct {
	id int
	cs ConditionSet
}

func (r *refMatcher) add(id int, cs ConditionSet) {
	if r.first == nil {
		r.first = make(map[int]int)
	}
	if _, ok := r.first[id]; !ok {
		r.first[id] = len(r.first)
	}
	r.rules = append(r.rules, refRule{id, cs})
}

func (r *refMatcher) match(event []Property) []int {
	var out []int
	for _, rule := range r.rules {
		if !slices.Contains(out, rule.id) && refMatches(rule.cs, event) {
			out = append(out, rule.id)
		}
	}
	slices.SortFunc(out, func(a, b int) int { return cmp.Compare(r.first[a], r.first[b]) })
	return out
}

// refMatches reports whether event matches the rule cs.
func refMatches(cs ConditionSet, event []Property) bool {
	for _, c := range cs {
		var values []string
		for _, p := range event {
			if p.Path == c.Path {
				for _, v := range p.Values {
					values = append(values, fold(v))
				}
			}
		}
		if len(values) == 0 || !refSatisfies(c.Pattern, values) {
			return false
		}
	}
	return true
}

// refSatisfies reports whether the folded values of a present property
// satisfy p.
func refSatisfies(p Pattern, values []string) bool {
	switch p.Type {
	case PatternAnyOf:
		for _, s := range p.Sub {
			if refSatisfies(s, values) {
				return true
			}
		}
		return false
	case PatternAllOf:
		for _, s := range p.Sub {
			if !refSatisfies(s, values) {
				return false
			}
		}
		return true
	case PatternAnythingBut:
		return !refSatisfies(Pattern{Type: PatternAnyOf, Sub: p.Sub}, values)
	}
	want := fold(p.Value)
	for _, v := range values {
		var ok bool
		switch p.Type {
		case PatternEquals:
			ok = v == want
		case PatternPrefix:
			ok = strings.HasPrefix(v, want)
		case PatternSuffix:
			ok = strings.HasSuffix(v, want)
		case PatternWildcard:
			ok = refGlob(want, v)
		}
		if ok {
			return true
		}
	}
	return false
}

// refGlob reports whether s matches pattern, in which '*' matches any
// sequence of bytes.
func refGlob(pattern, s string) bool {
	// dp[j] reports whether the pattern read so far matches s[:j].
	dp := make([]bool, len(s)+1)
	dp[0] = true
	for i := 0; i < len(pattern); i++ {
		if pattern[i] == '*' {
			for j := 1; j <= len(s); j++ {
				dp[j] = dp[j] || dp[j-1]
			}
			continue
		}
		for j := len(s); j >= 1; j-- {
			dp[j] = dp[j-1] && s[j-1] == pattern[i]
		}
		dp[0] = false
	}
	return dp[len(s)]
}

// source makes the random decisions of the generators below.
type source interface{ intn(n int) int }

type randSource struct{ *rand.Rand }

func (s randSource) intn(n int) int { return s.IntN(n) }

// byteSource draws its decisions from fuzzer input. Exhausted input yields
// zeros, which always terminate the generators.
type byteSource struct{ data []byte }

func (s *byteSource) intn(n int) int {
	if len(s.data) == 0 {
		return 0
	}
	b := s.data[0]
	s.data = s.data[1:]
	return int(b) % n
}

var (
	// A small alphabet makes rules overlap often. "A" is a separate path
	// because paths are case-sensitive.
	genPaths = []string{"a", "b", "c", "A"}
	// genRunes contains upper case, non-ASCII, the Kelvin sign (which
	// folds to the one-byte "k"), invalid UTF-8 and, as last element, "*",
	// which is literal in values and in non-wildcard patterns.
	genRunes = []string{"a", "b", "A", "-", "ä", "Ä", "K", "k", "\xff", "*"}
)

func genString(src source, maxLen int) string {
	var b strings.Builder
	for range src.intn(maxLen + 1) {
		b.WriteString(genRunes[src.intn(len(genRunes))])
	}
	return b.String()
}

func genLiteral(src source) string {
	if s := genString(src, 3); s != "" {
		return s
	}
	return "a"
}

func genWildcard(src source) string {
	var b strings.Builder
	star := false
	for range 1 + src.intn(4) {
		if !star && src.intn(3) == 0 {
			b.WriteByte('*')
			star = true
			continue
		}
		b.WriteString(genRunes[src.intn(len(genRunes)-1)])
		star = false
	}
	return b.String()
}

func genPattern(src source, depth int) Pattern {
	kinds := 7
	if depth >= 2 {
		kinds = 4
	}
	switch k := src.intn(kinds); k {
	case 0:
		return equalsP(genLiteral(src))
	case 1:
		return prefixP(genLiteral(src))
	case 2:
		return suffixP(genLiteral(src))
	case 3:
		return wildcardP(genWildcard(src))
	default:
		sub := make([]Pattern, 1+src.intn(3))
		for i := range sub {
			sub[i] = genPattern(src, depth+1)
		}
		return Pattern{Type: [...]PatternType{PatternAnyOf, PatternAllOf, PatternAnythingBut}[k-4], Sub: sub}
	}
}

func genRule(src source) ConditionSet {
	cs := make(ConditionSet, 1+src.intn(3))
	for i := range cs {
		cs[i] = cond(genPaths[src.intn(len(genPaths))], genPattern(src, 0))
	}
	return cs
}

func genEvent(src source) []Property {
	event := make([]Property, src.intn(5))
	for i := range event {
		values := make([]string, src.intn(4))
		for j := range values {
			values[j] = genString(src, 3)
		}
		event[i] = Property{Path: genPaths[src.intn(len(genPaths))], Values: values}
	}
	return event
}

func fmtPattern(p Pattern) string {
	if p.Type.HasLiteralValue() {
		return fmt.Sprintf("%s(%q)", p.Type, p.Value)
	}
	parts := make([]string, len(p.Sub))
	for i, s := range p.Sub {
		parts[i] = fmtPattern(s)
	}
	return fmt.Sprintf("%s(%s)", p.Type, strings.Join(parts, ", "))
}

func fmtRule(cs ConditionSet) string {
	parts := make([]string, len(cs))
	for i, c := range cs {
		parts[i] = fmt.Sprintf("%q: %s", c.Path, fmtPattern(c.Pattern))
	}
	return "{" + strings.Join(parts, "; ") + "}"
}

func fmtEvent(event []Property) string {
	parts := make([]string, len(event))
	for i, p := range event {
		parts[i] = fmt.Sprintf("%q: %q", p.Path, p.Values)
	}
	return "{" + strings.Join(parts, "; ") + "}"
}
