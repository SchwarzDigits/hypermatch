package hypermatch

import (
	"cmp"
	"fmt"
	"math/rand/v2"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// refMatcher is a deliberately naive implementation of the documented
// matching semantics. It is the oracle for the differential tests.
type refMatcher struct {
	rules []refRule
	order map[int]int // position of each present identifier, by addition
	seq   int
}

type refRule struct {
	id int
	cs ConditionSet
}

func (r *refMatcher) add(id int, cs ConditionSet) {
	if r.order == nil {
		r.order = make(map[int]int)
	}
	if _, ok := r.order[id]; !ok {
		r.order[id] = r.seq
		r.seq++
	}
	r.rules = append(r.rules, refRule{id, cs})
}

// replace replaces the rules of id with cs, keeping its position.
func (r *refMatcher) replace(id int, cs ConditionSet) {
	if _, ok := r.order[id]; !ok {
		r.add(id, cs)
		return
	}
	r.rules = slices.DeleteFunc(r.rules, func(rule refRule) bool { return rule.id == id })
	r.rules = append(r.rules, refRule{id, cs})
}

func (r *refMatcher) remove(id int) bool {
	if _, ok := r.order[id]; !ok {
		return false
	}
	delete(r.order, id)
	r.rules = slices.DeleteFunc(r.rules, func(rule refRule) bool { return rule.id == id })
	return true
}

func (r *refMatcher) match(event []Property) []int {
	var out []int
	for _, rule := range r.rules {
		if !slices.Contains(out, rule.id) && refMatches(rule.cs, event) {
			out = append(out, rule.id)
		}
	}
	slices.SortFunc(out, func(a, b int) int { return cmp.Compare(r.order[a], r.order[b]) })
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
		if c.Pattern.Type == PatternExists && c.Pattern.Value == "false" {
			if len(values) > 0 {
				return false
			}
			continue
		}
		if len(values) == 0 || !refSatisfies(c.Pattern, values) {
			return false
		}
	}
	return true
}

var refNumberSyntax = regexp.MustCompile(`^[+-]?([0-9]+(\.[0-9]*)?|\.[0-9]+)([eE][+-]?[0-9]+)?$`)

// refNumber parses the numbers numeric patterns compare.
func refNumber(s string) (float64, bool) {
	if !refNumberSyntax.MatchString(s) {
		return 0, false
	}
	f, _ := strconv.ParseFloat(s, 64) // too large numbers are infinite
	return f, true
}

// refInRange reports whether x satisfies the numeric pattern p.
func refInRange(p Pattern, x float64) bool {
	if p.Type == PatternBetween {
		for _, s := range p.Sub {
			if !refInRange(s, x) {
				return false
			}
		}
		return true
	}
	bound, _ := strconv.ParseFloat(p.Value, 64)
	switch p.Type {
	case PatternLessThan:
		return x < bound
	case PatternLessThanOrEqual:
		return x <= bound
	case PatternGreaterThan:
		return x > bound
	case PatternNumericEquals:
		return x == bound
	}
	return x >= bound
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
	case PatternExists:
		return true // only "true" gets here, and the property is present
	case PatternLessThan, PatternLessThanOrEqual, PatternGreaterThan, PatternGreaterThanOrEqual, PatternBetween, PatternNumericEquals:
		for _, v := range values {
			if x, ok := refNumber(v); ok && refInRange(p, x) {
				return true
			}
		}
		return false
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
// sequence of bytes and a backslash makes the next byte literal.
func refGlob(pattern, s string) bool {
	// dp[j] reports whether the pattern read so far matches s[:j].
	dp := make([]bool, len(s)+1)
	dp[0] = true
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if c == '*' {
			for j := 1; j <= len(s); j++ {
				dp[j] = dp[j] || dp[j-1]
			}
			continue
		}
		if c == '\\' {
			i++
			c = pattern[i]
		}
		for j := len(s); j >= 1; j-- {
			dp[j] = dp[j-1] && s[j-1] == c
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
	// folds to the one-byte "k"), invalid UTF-8, and `\` and "*", which are
	// literal in values and in non-wildcard patterns.
	genRunes = []string{"a", "b", "A", "-", "ä", "Ä", "K", "k", "\xff", `\`, "*"}
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
		r := genRunes[src.intn(len(genRunes))]
		if r == `\` || r == "*" {
			b.WriteByte('\\') // a literal, which must be escaped
		}
		b.WriteString(r)
		star = false
	}
	return b.String()
}

var (
	// genNumbers are event values for numeric patterns: numbers in several
	// notations, infinite ones, some that need exact rounding, and others.
	genNumbers = []string{"0", "1", "2.5", "-3", "1e2", "010", ".5", "5.", "+4", "100", "1E1", "-0", "7", "10",
		"1e400", "-1e400", "1.0000000000000002", "9007199254740993", "abc", ""}
	genBounds = []string{"0", "1", "2.5", "-3", "100", "1e1", "5", "10", "-0", "7"}
)

func genValue(src source) string {
	if src.intn(3) == 0 {
		return genNumbers[src.intn(len(genNumbers))]
	}
	return genString(src, 3)
}

func genPattern(src source, depth int) Pattern {
	const leaves = 7
	kinds := leaves + 3
	if depth >= 2 {
		kinds = leaves
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
	case 4:
		types := [...]PatternType{PatternLessThan, PatternLessThanOrEqual, PatternGreaterThan, PatternGreaterThanOrEqual, PatternNumericEquals}
		return Pattern{Type: types[src.intn(len(types))], Value: genBounds[src.intn(len(genBounds))]}
	case 5:
		return genBetween(src)
	case 6:
		return Pattern{Type: PatternExists, Value: "true"}
	default:
		sub := make([]Pattern, 1+src.intn(3))
		for i := range sub {
			sub[i] = genPattern(src, depth+1)
		}
		return Pattern{Type: [...]PatternType{PatternAnyOf, PatternAllOf, PatternAnythingBut}[k-leaves], Sub: sub}
	}
}

func genBetween(src source) Pattern {
	lo, hi := genBounds[src.intn(len(genBounds))], genBounds[src.intn(len(genBounds))]
	x, _ := strconv.ParseFloat(lo, 64)
	y, _ := strconv.ParseFloat(hi, 64)
	if x > y {
		lo, hi, x, y = hi, lo, y, x
	}
	lower, upper := PatternGreaterThanOrEqual, PatternLessThanOrEqual
	if x < y { // open bounds would make the range empty otherwise
		if src.intn(2) == 0 {
			lower = PatternGreaterThan
		}
		if src.intn(2) == 0 {
			upper = PatternLessThan
		}
	}
	return Pattern{Type: PatternBetween, Sub: []Pattern{{Type: upper, Value: hi}, {Type: lower, Value: lo}}}
}

var existsFalse = Pattern{Type: PatternExists, Value: "false"}

func genRule(src source) ConditionSet {
	switch src.intn(8) {
	case 0:
		return ConditionSet{cond(genPaths[src.intn(len(genPaths))], existsFalse)}
	case 1:
		cs := genConditions(src)
		path := genPaths[src.intn(len(genPaths))]
		if !slices.ContainsFunc(cs, func(c Condition) bool { return c.Path == path }) {
			cs = append(cs, cond(path, existsFalse))
		}
		return cs
	}
	return genConditions(src)
}

func genConditions(src source) ConditionSet {
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
			values[j] = genValue(src)
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
