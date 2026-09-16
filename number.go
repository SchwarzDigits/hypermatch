package hypermatch

import (
	"cmp"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
)

// Numeric patterns compare values as decimal numbers: an optional sign,
// digits with an optional decimal point, and an optional exponent, such as
// 42, -1.5, .5 or 1e3. Values that are not numbers never match numeric
// patterns. Numbers are compared as float64.

// pow10 holds the powers of ten that float64 represents exactly.
var pow10 = [...]float64{1e0, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9, 1e10, 1e11,
	1e12, 1e13, 1e14, 1e15, 1e16, 1e17, 1e18, 1e19, 1e20, 1e21, 1e22}

// parseNumber parses s and reports whether it is a number. Numbers too
// large for float64 parse as infinity.
func parseNumber[S string | []byte](s S) (float64, bool) {
	n, i := len(s), 0
	neg := false
	if i < n && (s[i] == '+' || s[i] == '-') {
		neg = s[i] == '-'
		i++
	}
	start := i
	for i < n && '0' <= s[i] && s[i] <= '9' {
		i++
	}
	intDigits := i - start
	fracDigits := 0
	if i < n && s[i] == '.' {
		i++
		for i < n && '0' <= s[i] && s[i] <= '9' {
			i++
			fracDigits++
		}
	}
	mantEnd := i
	if intDigits == 0 && fracDigits == 0 {
		return 0, false
	}
	exp := 0
	if i < n && (s[i] == 'e' || s[i] == 'E') {
		i++
		expNeg := false
		if i < n && (s[i] == '+' || s[i] == '-') {
			expNeg = s[i] == '-'
			i++
		}
		if i == n || s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		for ; i < n && '0' <= s[i] && s[i] <= '9'; i++ {
			if exp < 100000 {
				exp = exp*10 + int(s[i]-'0')
			}
		}
		if expNeg {
			exp = -exp
		}
	}
	if i != n {
		return 0, false
	}

	// With at most 15 significant digits and a power of ten float64
	// represents exactly, a single multiplication or division gives the
	// correctly rounded result, like strconv.ParseFloat.
	var m uint64
	sig := 0
	for j := start; j < mantEnd; j++ {
		c := s[j]
		if c == '.' || (m == 0 && c == '0') {
			continue
		}
		if sig == 15 {
			return parseNumberSlow(s)
		}
		m = m*10 + uint64(c-'0')
		sig++
	}
	exp -= fracDigits
	f := float64(m)
	switch {
	case m == 0 || exp == 0:
	case 0 < exp && exp < len(pow10):
		f *= pow10[exp]
	case -len(pow10) < exp && exp < 0:
		f /= pow10[-exp]
	default:
		return parseNumberSlow(s)
	}
	if neg {
		f = -f
	}
	return f, true
}

func parseNumberSlow[S string | []byte](s S) (float64, bool) {
	f, err := strconv.ParseFloat(string(s), 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return 0, false
	}
	return f, true
}

// numInterval is the set of numbers a numeric leaf matches. An unbounded
// side has an infinite bound that is included, so that infinite values
// compare like very large ones.
type numInterval struct {
	lo, hi         float64
	loOpen, hiOpen bool
}

func (iv numInterval) contains(x float64) bool {
	return (iv.lo < x || (iv.lo == x && !iv.loOpen)) && (x < iv.hi || (x == iv.hi && !iv.hiOpen))
}

// key returns the canonical text of iv, for example "(5,+Inf]".
func (iv numInterval) key() string {
	lo, hi := "[", "]"
	if iv.loOpen {
		lo = "("
	}
	if iv.hiOpen {
		hi = ")"
	}
	return lo + formatNumber(iv.lo) + "," + formatNumber(iv.hi) + hi
}

func formatNumber(f float64) string {
	if f == 0 {
		f = 0 // no negative zero
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// parseInterval inverts numInterval.key.
func parseInterval(s string) numInterval {
	comma := strings.IndexByte(s, ',')
	lo, _ := strconv.ParseFloat(s[1:comma], 64)
	hi, _ := strconv.ParseFloat(s[comma+1:len(s)-1], 64)
	return numInterval{lo: lo, hi: hi, loOpen: s[0] == '(', hiOpen: s[len(s)-1] == ')'}
}

// numIndex finds the numeric leaves of a group that contain a number. It is
// immutable: the writer publishes a new one for every leaf it adds.
type numIndex struct {
	lower  numList // bounded below only, sorted by lo
	upper  numList // bounded above only, sorted by hi
	ranges numList // bounded on both sides, sorted by lo
	points numList // single numbers, sorted by lo
}

type numEntry struct {
	iv   numInterval
	leaf *leaf
}

// numList holds entries sorted by a bound and up to maxRecent unsorted
// recent additions, so that adding an entry rarely copies the sorted part.
type numList struct {
	sorted []numEntry
	recent []numEntry
	maxHi  []float64 // ranges only: maxHi[i] is the largest hi in sorted[:i+1]
}

const maxRecent = 64

func lowerBound(iv numInterval) float64 { return iv.lo }
func upperBound(iv numInterval) float64 { return iv.hi }

// with returns a copy of n that also contains iv for leaf l.
func (n *numIndex) with(iv numInterval, l *leaf) *numIndex {
	c := *n
	e := numEntry{iv: iv, leaf: l}
	switch {
	case iv.lo == iv.hi:
		c.points = c.points.with(e, lowerBound, false)
	case math.IsInf(iv.hi, 1):
		c.lower = c.lower.with(e, lowerBound, false)
	case math.IsInf(iv.lo, -1):
		c.upper = c.upper.with(e, upperBound, false)
	default:
		c.ranges = c.ranges.with(e, lowerBound, true)
	}
	return &c
}

func (l numList) with(e numEntry, bound func(numInterval) float64, withMaxHi bool) numList {
	recent := append(slices.Clip(l.recent), e)
	if len(recent) < maxRecent {
		return numList{sorted: l.sorted, recent: recent, maxHi: l.maxHi}
	}
	byBound := func(a, b numEntry) int { return cmp.Compare(bound(a.iv), bound(b.iv)) }
	slices.SortFunc(recent, byBound)
	merged := make([]numEntry, 0, len(l.sorted)+len(recent))
	i, j := 0, 0
	for i < len(l.sorted) && j < len(recent) {
		if byBound(l.sorted[i], recent[j]) <= 0 {
			merged = append(merged, l.sorted[i])
			i++
		} else {
			merged = append(merged, recent[j])
			j++
		}
	}
	merged = append(append(merged, l.sorted[i:]...), recent[j:]...)
	m := numList{sorted: merged}
	if withMaxHi {
		m.maxHi = make([]float64, len(merged))
		hi := math.Inf(-1)
		for i := range merged {
			hi = max(hi, merged[i].iv.hi)
			m.maxHi[i] = hi
		}
	}
	return m
}

// collect appends the leaves whose intervals contain x to hits. Except for
// the recent entries, it only looks at entries that contain x and at most
// one more per list, unless ranges overlap.
func (n *numIndex) collect(x float64, hits []*leaf) []*leaf {
	for i := range n.lower.sorted {
		e := &n.lower.sorted[i]
		if e.iv.lo > x {
			break
		}
		if e.iv.contains(x) {
			hits = append(hits, e.leaf)
		}
	}
	for i := len(n.upper.sorted) - 1; i >= 0; i-- {
		e := &n.upper.sorted[i]
		if e.iv.hi < x {
			break
		}
		if e.iv.contains(x) {
			hits = append(hits, e.leaf)
		}
	}
	// The ranges starting at or below x are a prefix of the sorted ones.
	// Scanning it backwards can stop once no earlier range reaches x.
	rs := n.ranges.sorted
	for i := searchLo(rs, x, true) - 1; i >= 0 && n.ranges.maxHi[i] >= x; i-- {
		if rs[i].iv.contains(x) {
			hits = append(hits, rs[i].leaf)
		}
	}
	ps := n.points.sorted
	for i := searchLo(ps, x, false); i < len(ps) && ps[i].iv.lo == x; i++ {
		hits = append(hits, ps[i].leaf)
	}
	for _, recent := range [...][]numEntry{n.lower.recent, n.upper.recent, n.ranges.recent, n.points.recent} {
		for i := range recent {
			if recent[i].iv.contains(x) {
				hits = append(hits, recent[i].leaf)
			}
		}
	}
	return hits
}

// searchLo returns the number of entries in s, which is sorted by lo, whose
// lo is less than x, or less than or equal to x if orEqual is true.
func searchLo(s []numEntry, x float64, orEqual bool) int {
	i, j := 0, len(s)
	for i < j {
		m := int(uint(i+j) >> 1)
		if lo := s[m].iv.lo; lo < x || (orEqual && lo == x) {
			i = m + 1
		} else {
			j = m
		}
	}
	return i
}
