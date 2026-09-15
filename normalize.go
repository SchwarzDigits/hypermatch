package hypermatch

import (
	"errors"
	"fmt"
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
)

var leafTags = [...]byte{leafEquals: '=', leafPrefix: '^', leafSuffix: '$', leafGlob: '~', leafExists: '?'}

type exprOp uint8

const (
	opLeaf exprOp = iota
	opAnyOf
	opAllOf
	opNot
)

var opTags = [...]byte{opAnyOf: '|', opAllOf: '&', opNot: '!'}

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
	byPath := make(map[string][]*expr, len(cs))
	for i := range cs {
		c := &cs[i]
		if c.Path == "" {
			return nil, fmt.Errorf("%w: condition %d: empty path", ErrInvalidRule, i)
		}
		e, err := normalizePattern(&c.Pattern)
		if err != nil {
			return nil, fmt.Errorf("%w: condition %q: %w", ErrInvalidRule, c.Path, err)
		}
		byPath[c.Path] = append(byPath[c.Path], e)
	}
	conds := make([]condition, 0, len(byPath))
	for path, es := range byPath {
		e := es[0]
		if len(es) > 1 {
			// All conditions on a path must hold, which is what allOf means.
			e = combine(opAllOf, es)
		}
		conds = append(conds, condition{path: path, expr: e})
	}
	slices.SortFunc(conds, func(a, b condition) int { return strings.Compare(a.path, b.path) })
	return conds, nil
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
