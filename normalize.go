package hypermatch

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

// ErrInvalidRule is wrapped by the errors AddRule and ValidateRule return
// for rules that cannot be used.
var ErrInvalidRule = errors.New("hypermatch: invalid rule")

type leafKind uint8

const (
	leafEquals leafKind = iota
	leafPrefix
	leafSuffix
	leafGlob   // a wildcard pattern that none of the simpler kinds can express
	leafExists // the wildcard "*", which matches any value
	leafNumber // a numeric comparison; the value is the key of a numInterval
)

var leafTags = [...]byte{leafEquals: '=', leafPrefix: '^', leafSuffix: '$', leafGlob: '~', leafExists: '?', leafNumber: '#'}

type exprOp uint8

const (
	opLeaf exprOp = iota
	opAnyOf
	opAllOf
	opNot
	opAbsent // {"exists": false}, which only a whole condition can be
)

var opTags = [...]byte{opAnyOf: '|', opAllOf: '&', opNot: '!'}

// absentExpr is the normalized form of {"exists": false}. A condition with it
// holds if its property is absent.
var absentExpr = &expr{op: opAbsent, key: "-"}

// expr is the normalized form of a Pattern. Equivalent patterns, such as
// anyOf lists that differ only in order or duplicates, normalize to
// expressions with the same key, so rules can share compiled conditions.
type expr struct {
	op    exprOp
	kind  leafKind // opLeaf only
	value string   // opLeaf only, case-folded
	subs  []*expr  // opAnyOf, opAllOf: two or more, sorted by key and unique; opNot: exactly one
	key   string   // unambiguous encoding of the expression
}

// condition is a normalized condition. A normalized rule has at most one
// condition per path, and its conditions are sorted by path.
type condition struct {
	path string
	expr *expr
}

func normalizeRule(cs ConditionSet) ([]condition, error) {
	if len(cs) == 0 {
		return nil, fmt.Errorf("%w: no conditions", ErrInvalidRule)
	}
	conds := make([]condition, len(cs))
	for i := range cs {
		c := &cs[i]
		if c.Path == "" {
			return nil, fmt.Errorf("%w: condition %d: empty path", ErrInvalidRule, i)
		}
		e, err := normalizePattern(&c.Pattern)
		if err != nil {
			return nil, fmt.Errorf("%w: condition %q: %w", ErrInvalidRule, c.Path, err)
		}
		conds[i] = condition{path: c.Path, expr: e}
	}
	slices.SortFunc(conds, func(a, b condition) int { return strings.Compare(a.path, b.path) })

	// All conditions on a path must hold, which is what allOf means.
	merged := conds[:0]
	for i := 0; i < len(conds); {
		j := i + 1
		for j < len(conds) && conds[j].path == conds[i].path {
			j++
		}
		c := conds[i]
		if j-i > 1 {
			es := make([]*expr, 0, j-i)
			for _, d := range conds[i:j] {
				if d.expr.op == opAbsent {
					return nil, fmt.Errorf("%w: condition %q: [%s] false cannot be combined with other conditions on the same path", ErrInvalidRule, c.path, PatternExists)
				}
				es = append(es, d.expr)
			}
			c.expr = combine(opAllOf, es)
		}
		merged = append(merged, c)
		i = j
	}
	return merged, nil
}

func normalizePattern(p *Pattern) (*expr, error) {
	switch p.Type {
	case PatternEquals, PatternPrefix, PatternSuffix, PatternWildcard:
		if p.Value == "" {
			return nil, fmt.Errorf("[%s] must contain a value", p.Type)
		}
		if len(p.Sub) > 0 {
			return nil, fmt.Errorf("[%s] must not contain sub-patterns", p.Type)
		}
		v := fold(p.Value)
		switch p.Type {
		case PatternEquals:
			return newLeaf(leafEquals, v), nil
		case PatternPrefix:
			return newLeaf(leafPrefix, v), nil
		case PatternSuffix:
			return newLeaf(leafSuffix, v), nil
		}
		if strings.Contains(v, "**") {
			return nil, fmt.Errorf("[%s] must not contain two consecutive wildcards", p.Type)
		}
		return wildcardLeaf(v), nil
	case PatternLessThan, PatternLessThanOrEqual, PatternGreaterThan, PatternGreaterThanOrEqual, PatternNumericEquals:
		v, err := numericBound(p)
		if err != nil {
			return nil, err
		}
		return newLeaf(leafNumber, boundInterval(p.Type, v).key()), nil
	case PatternBetween:
		iv, err := betweenInterval(p)
		if err != nil {
			return nil, err
		}
		return newLeaf(leafNumber, iv.key()), nil
	case PatternExists:
		if len(p.Sub) > 0 {
			return nil, fmt.Errorf("[%s] must not contain sub-patterns", p.Type)
		}
		switch p.Value {
		case "true":
			return newLeaf(leafExists, ""), nil
		case "false":
			return absentExpr, nil
		}
		return nil, fmt.Errorf(`[%s] must be "true" or "false"`, p.Type)
	case PatternAnyOf, PatternAllOf, PatternAnythingBut:
		if p.Value != "" {
			return nil, fmt.Errorf("[%s] must not contain a value", p.Type)
		}
		if len(p.Sub) == 0 {
			return nil, fmt.Errorf("[%s] must contain sub-patterns", p.Type)
		}
		subs := make([]*expr, len(p.Sub))
		for i := range p.Sub {
			e, err := normalizePattern(&p.Sub[i])
			if err != nil {
				return nil, fmt.Errorf("%s[%d]: %w", p.Type, i, err)
			}
			if e.op == opAbsent {
				return nil, fmt.Errorf("%s[%d]: [%s] false must be a whole condition", p.Type, i, PatternExists)
			}
			subs[i] = e
		}
		switch p.Type {
		case PatternAnyOf:
			return combine(opAnyOf, subs), nil
		case PatternAllOf:
			return combine(opAllOf, subs), nil
		}
		return negate(combine(opAnyOf, subs)), nil
	}
	return nil, fmt.Errorf("unknown pattern type %d", p.Type)
}

// numericBound validates the numeric comparison p and returns its bound.
func numericBound(p *Pattern) (float64, error) {
	if len(p.Sub) > 0 {
		return 0, fmt.Errorf("[%s] must not contain sub-patterns", p.Type)
	}
	v, ok := parseNumber(p.Value)
	if !ok || math.IsInf(v, 0) {
		return 0, fmt.Errorf("[%s] must contain a finite number, got %q", p.Type, p.Value)
	}
	return v, nil
}

// boundInterval returns the numbers the comparison t with bound v matches.
func boundInterval(t PatternType, v float64) numInterval {
	switch t {
	case PatternLessThan:
		return numInterval{lo: math.Inf(-1), hi: v, hiOpen: true}
	case PatternLessThanOrEqual:
		return numInterval{lo: math.Inf(-1), hi: v}
	case PatternGreaterThan:
		return numInterval{lo: v, hi: math.Inf(1), loOpen: true}
	case PatternNumericEquals:
		return numInterval{lo: v, hi: v}
	}
	return numInterval{lo: v, hi: math.Inf(1)}
}

// betweenInterval validates the between pattern p and returns the numbers it
// matches.
func betweenInterval(p *Pattern) (numInterval, error) {
	iv := numInterval{lo: math.Inf(-1), hi: math.Inf(1)}
	if p.Value != "" {
		return iv, fmt.Errorf("[%s] must not contain a value", p.Type)
	}
	bounds := fmt.Errorf("[%s] must contain a lower (gt, gte) and an upper (lt, lte) bound", p.Type)
	var lower, upper bool
	for i := range p.Sub {
		s := &p.Sub[i]
		if !s.Type.isComparison() {
			return iv, fmt.Errorf("%s[%d]: must be lt, lte, gt or gte", p.Type, i)
		}
		v, err := numericBound(s)
		if err != nil {
			return iv, fmt.Errorf("%s[%d]: %w", p.Type, i, err)
		}
		b := boundInterval(s.Type, v)
		if s.Type == PatternGreaterThan || s.Type == PatternGreaterThanOrEqual {
			if lower {
				return iv, bounds
			}
			lower = true
			iv.lo, iv.loOpen = b.lo, b.loOpen
		} else {
			if upper {
				return iv, bounds
			}
			upper = true
			iv.hi, iv.hiOpen = b.hi, b.hiOpen
		}
	}
	if !lower || !upper {
		return iv, bounds
	}
	if iv.lo > iv.hi || (iv.lo == iv.hi && (iv.loOpen || iv.hiOpen)) {
		return iv, fmt.Errorf("[%s] must not be empty", p.Type)
	}
	return iv, nil
}

// wildcardLeaf returns the cheapest leaf equivalent to the folded wildcard
// pattern v.
func wildcardLeaf(v string) *expr {
	if v == "*" {
		return newLeaf(leafExists, "")
	}
	core, lead := strings.CutPrefix(v, "*")
	core, trail := strings.CutSuffix(core, "*")
	switch {
	case strings.Contains(core, "*"):
		return newLeaf(leafGlob, v)
	case !lead && !trail:
		return newLeaf(leafEquals, core)
	case !lead:
		return newLeaf(leafPrefix, core)
	case !trail:
		return newLeaf(leafSuffix, core)
	}
	return newLeaf(leafGlob, v)
}

func newLeaf(kind leafKind, v string) *expr {
	return &expr{op: opLeaf, kind: kind, value: v, key: string(leafTags[kind]) + strconv.Itoa(len(v)) + ":" + v}
}

// combine returns the anyOf or allOf of subs, flattening nested
// expressions of the same kind and removing duplicates.
func combine(op exprOp, subs []*expr) *expr {
	flat := make([]*expr, 0, len(subs))
	for _, s := range subs {
		if s.op == op {
			flat = append(flat, s.subs...)
		} else {
			flat = append(flat, s)
		}
	}
	slices.SortFunc(flat, func(a, b *expr) int { return strings.Compare(a.key, b.key) })
	flat = slices.CompactFunc(flat, func(a, b *expr) bool { return a.key == b.key })
	if len(flat) == 1 {
		return flat[0]
	}
	var b strings.Builder
	b.WriteByte(opTags[op])
	b.WriteByte('(')
	for _, s := range flat {
		b.WriteString(s.key)
	}
	b.WriteByte(')')
	return &expr{op: op, subs: flat, key: b.String()}
}

// negate returns the negation of e. Conditions are only evaluated for
// properties that are present, where double negation cancels out.
func negate(e *expr) *expr {
	if e.op == opNot {
		return e.subs[0]
	}
	return &expr{op: opNot, subs: []*expr{e}, key: "!(" + e.key + ")"}
}

// parseKey returns the expression whose key is key. It inverts newLeaf,
// combine and negate, which lets RemoveRule rebuild the compiled rules from
// the keys stored in them.
func parseKey(key string) *expr {
	e, rest := parseExpr(key)
	if rest != "" {
		panic("hypermatch: invalid expression key " + strconv.Quote(key))
	}
	return e
}

// parseExpr parses the expression at the start of s and returns it together
// with the rest of s.
func parseExpr(s string) (*expr, string) {
	for kind, tag := range leafTags {
		if s[0] == tag {
			colon := strings.IndexByte(s, ':')
			n, err := strconv.Atoi(s[1:colon])
			if err != nil {
				panic("hypermatch: invalid expression key " + strconv.Quote(s))
			}
			end := colon + 1 + n
			return newLeaf(leafKind(kind), s[colon+1:end]), s[end:]
		}
	}
	var op exprOp
	switch s[0] {
	case opTags[opAnyOf]:
		op = opAnyOf
	case opTags[opAllOf]:
		op = opAllOf
	case opTags[opNot]:
		op = opNot
	default:
		panic("hypermatch: invalid expression key " + strconv.Quote(s))
	}
	s = s[2:] // the tag and '('
	var subs []*expr
	for s[0] != ')' {
		var e *expr
		e, s = parseExpr(s)
		subs = append(subs, e)
	}
	if op == opNot {
		return negate(subs[0]), s[1:]
	}
	return combine(op, subs), s[1:]
}

// monotone reports whether e contains no negation. A monotone expression
// can only hold if at least one of its leaves matches.
func (e *expr) monotone() bool {
	if e.op == opNot {
		return false
	}
	for _, s := range e.subs {
		if !s.monotone() {
			return false
		}
	}
	return true
}

// simple reports whether e holds as soon as any of its leaves matches.
func (e *expr) simple() bool {
	if e.op == opLeaf {
		return true
	}
	if e.op != opAnyOf {
		return false
	}
	for _, s := range e.subs {
		if s.op != opLeaf {
			return false
		}
	}
	return true
}
