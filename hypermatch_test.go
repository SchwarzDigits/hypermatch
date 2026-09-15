package hypermatch

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func mustAdd[T comparable](t *testing.T, h *HyperMatch[T], id T, cs ...Condition) {
	t.Helper()
	if err := h.AddRule(id, cs); err != nil {
		t.Fatalf("AddRule(%v): %v", id, err)
	}
}

func assertMatch[T comparable](t *testing.T, h *HyperMatch[T], event []Property, want ...T) {
	t.Helper()
	if got := h.Match(event); !slices.Equal(got, want) {
		t.Errorf("Match(%s) = %v, want %v", fmtEvent(event), got, want)
	}
}

func TestMatchSimple(t *testing.T) {
	h := New[int]()
	mustAdd(t, h, 1, cond("y", equalsP("bb")), cond("x", equalsP("aa")))
	mustAdd(t, h, 2, cond("y", equalsP("bb")))
	mustAdd(t, h, 3, cond("x", equalsP("dd")), cond("y", equalsP("cc")))
	mustAdd(t, h, 4, cond("x", prefixP("a")), cond("y", prefixP("b")))
	mustAdd(t, h, 5, cond("x", suffixP("x")), cond("y", suffixP("y")))
	mustAdd(t, h, 6, cond("y", anythingButP(equalsP("bb"))))
	mustAdd(t, h, 7, cond("y", anythingButP(equalsP("b*y")))) // literal, "*" is no wildcard here

	assertMatch(t, h, []Property{prop("x", "aa"), prop("y", "bb")}, 1, 2, 4, 7)
	assertMatch(t, h, []Property{prop("x", "dd"), prop("y", "cc")}, 3, 6, 7)
	assertMatch(t, h, []Property{prop("x", "aax"), prop("y", "bby")}, 4, 5, 6, 7)
	assertMatch(t, h, []Property{prop("x", "aax", "dd"), prop("y", "bby", "cc")}, 3, 4, 5, 6, 7)
}

func TestMatchComplex(t *testing.T) {
	h := New[int]()
	mustAdd(t, h, 1,
		cond("namespace", equalsP("monitoring-schwarz")),
		cond("l1", equalsP("lidl")),
		cond("name", wildcardP("*OS*")),
		cond("priority", anyOfP(equalsP("P1"), equalsP("P2"))),
		cond("hostgroups", allOfP(equalsP("lidl-de"), equalsP("store-servers"))),
	)

	event := []Property{
		prop("namespace", "monitoring-schwarz"),
		prop("l1", "lidl"),
		prop("name", "OS hdd"),
		prop("priority", "P1"),
		prop("hostgroups", "lidl", "lidl-de", "de", "store-servers"),
	}
	assertMatch(t, h, event, 1)

	event[4] = prop("hostgroups", "lidl", "lidl-de", "de")
	assertMatch(t, h, event)
}

func TestMatchSemantics(t *testing.T) {
	tests := []struct {
		name  string
		rules []ConditionSet // identified by index
		event []Property
		want  []int
	}{
		{
			name:  "wildcard does not break equals",
			rules: []ConditionSet{{cond("f", equalsP("ab"))}, {cond("f", wildcardP("a*bc"))}},
			event: []Property{prop("f", "ab")},
			want:  []int{0},
		},
		{
			name:  "equals does not match through wildcard",
			rules: []ConditionSet{{cond("f", wildcardP("a*bc"))}, {cond("f", equalsP("ab"))}},
			event: []Property{prop("f", "axyzb")},
		},
		{
			name:  "wildcard next to equals",
			rules: []ConditionSet{{cond("f", equalsP("ab"))}, {cond("f", wildcardP("a*bc"))}},
			event: []Property{prop("f", "axbc")},
			want:  []int{1},
		},
		{
			name:  "suffix does not break equals",
			rules: []ConditionSet{{cond("f", equalsP("ice"))}, {cond("f", suffixP("ing"))}},
			event: []Property{prop("f", "ice")},
			want:  []int{0},
		},
		{
			name:  "equals does not match through suffix",
			rules: []ConditionSet{{cond("f", suffixP("ing"))}, {cond("f", equalsP("ice"))}},
			event: []Property{prop("f", "xxice")},
		},
		{
			name:  "suffix next to equals",
			rules: []ConditionSet{{cond("f", suffixP("ing"))}, {cond("f", equalsP("ice"))}},
			event: []Property{prop("f", "icing")},
			want:  []int{0},
		},
		{
			name:  "anythingBut considers all values",
			rules: []ConditionSet{{cond("f", anythingButP(equalsP("x")))}},
			event: []Property{prop("f", "x", "y")},
		},
		{
			name:  "anythingBut without excluded values",
			rules: []ConditionSet{{cond("f", anythingButP(equalsP("x")))}},
			event: []Property{prop("f", "y", "z")},
			want:  []int{0},
		},
		{
			name:  "anythingBut needs the property",
			rules: []ConditionSet{{cond("f", anythingButP(equalsP("x")))}},
			event: []Property{prop("g", "y")},
		},
		{
			name:  "property without values is absent",
			rules: []ConditionSet{{cond("f", anythingButP(equalsP("x")))}},
			event: []Property{prop("f")},
		},
		{
			name:  "allOf with anythingBut",
			rules: []ConditionSet{{cond("f", allOfP(anythingButP(equalsP("a")), equalsP("b")))}},
			event: []Property{prop("f", "a", "b")},
		},
		{
			name:  "allOf with anythingBut holds",
			rules: []ConditionSet{{cond("f", allOfP(anythingButP(equalsP("a")), equalsP("b")))}},
			event: []Property{prop("f", "b")},
			want:  []int{0},
		},
		{
			name:  "anyOf with anythingBut",
			rules: []ConditionSet{{cond("f", anyOfP(anythingButP(equalsP("x")), equalsP("y")))}},
			event: []Property{prop("f", "x")},
		},
		{
			name:  "double negation",
			rules: []ConditionSet{{cond("f", anythingButP(anythingButP(equalsP("x"))))}},
			event: []Property{prop("f", "x", "y")},
			want:  []int{0},
		},
		{
			name: "anyOf does not leak into rules sharing a condition",
			rules: []ConditionSet{
				{cond("x", equalsP("a")), cond("y", equalsP("b"))},
				{cond("x", anyOfP(equalsP("a"), equalsP("c")))},
			},
			event: []Property{prop("x", "c"), prop("y", "b")},
			want:  []int{1},
		},
		{
			name:  "several conditions on one path",
			rules: []ConditionSet{{cond("f", equalsP("x")), cond("f", equalsP("y"))}},
			event: []Property{prop("f", "x", "y")},
			want:  []int{0},
		},
		{
			name:  "several conditions on one path must all hold",
			rules: []ConditionSet{{cond("f", equalsP("x")), cond("f", equalsP("y"))}},
			event: []Property{prop("f", "x")},
		},
		{
			name:  "properties with the same path are merged",
			rules: []ConditionSet{{cond("f", allOfP(equalsP("x"), equalsP("y")))}},
			event: []Property{prop("f", "x"), prop("g", "z"), prop("f", "y")},
			want:  []int{0},
		},
		{
			name:  "values are case-insensitive",
			rules: []ConditionSet{{cond("f", equalsP("FiRiNg"))}},
			event: []Property{prop("f", "firING")},
			want:  []int{0},
		},
		{
			name:  "non-ASCII values are case-insensitive",
			rules: []ConditionSet{{cond("f", prefixP("ÄRGER"))}},
			event: []Property{prop("f", "ärgerlich")},
			want:  []int{0},
		},
		{
			name:  "paths are case-sensitive",
			rules: []ConditionSet{{cond("Name", equalsP("x"))}},
			event: []Property{prop("name", "x")},
		},
		{
			name:  "star matches any value",
			rules: []ConditionSet{{cond("f", wildcardP("*"))}},
			event: []Property{prop("f", "")},
			want:  []int{0},
		},
		{
			name:  "star needs the property",
			rules: []ConditionSet{{cond("f", wildcardP("*"))}},
			event: []Property{prop("g", "x")},
		},
		{
			name:  "equals is literal",
			rules: []ConditionSet{{cond("f", equalsP("a*b"))}},
			event: []Property{prop("f", "axb")},
		},
		{
			name:  "prefix is literal",
			rules: []ConditionSet{{cond("f", prefixP("a*"))}},
			event: []Property{prop("f", "ab", "a*c")},
			want:  []int{0},
		},
		{
			name:  "wildcards match in order",
			rules: []ConditionSet{{cond("f", wildcardP("a*b*c"))}},
			event: []Property{prop("f", "acb")},
		},
		{
			name:  "wildcards match empty sequences",
			rules: []ConditionSet{{cond("f", wildcardP("a*b*c"))}},
			event: []Property{prop("f", "abc")},
			want:  []int{0},
		},
		{
			name:  "arbitrary bytes are plain values",
			rules: []ConditionSet{{cond("f", equalsP("a\xf5"))}, {cond("f", equalsP("a"))}, {cond("f", equalsP("a\xf6c"))}},
			event: []Property{prop("f", "a\xf5", "abc")},
			want:  []int{0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := New[int]()
			ref := &refMatcher{}
			for i, cs := range tt.rules {
				if err := h.AddRule(i, cs); err != nil {
					t.Fatal(err)
				}
				ref.add(i, cs)
			}
			if got := h.Match(tt.event); !slices.Equal(got, tt.want) {
				t.Errorf("Match = %v, want %v", got, tt.want)
			}
			if got := ref.match(tt.event); !slices.Equal(got, tt.want) {
				t.Errorf("reference = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMatchDoesNotModifyInput(t *testing.T) {
	newRule := func() ConditionSet {
		return ConditionSet{cond("z", equalsP("1")), cond("a", anyOfP(equalsP("2"), equalsP("1")))}
	}
	rule := newRule()
	h := New[string]()
	mustAdd(t, h, "r", rule...)
	if !reflect.DeepEqual(rule, newRule()) {
		t.Errorf("AddRule modified the rule: %v", rule)
	}

	event := []Property{prop("z", "1"), prop("a", "2")}
	assertMatch(t, h, event, "r")
	if !reflect.DeepEqual(event, []Property{prop("z", "1"), prop("a", "2")}) {
		t.Errorf("Match modified the event: %v", event)
	}
}

func TestMatchOrder(t *testing.T) {
	h := New[string]()
	for _, id := range []string{"c", "a", "b"} {
		mustAdd(t, h, id, cond("f", prefixP("x")))
	}
	mustAdd(t, h, "a", cond("f", equalsP("xyz")))
	assertMatch(t, h, []Property{prop("f", "xyz")}, "c", "a", "b")
}

func TestSeveralRulesPerIdentifier(t *testing.T) {
	h := New[string]()
	mustAdd(t, h, "r", cond("f", equalsP("a")))
	mustAdd(t, h, "r", cond("g", equalsP("b")))
	mustAdd(t, h, "r", cond("f", equalsP("a")))
	if n := h.RuleCount(); n != 1 {
		t.Errorf("RuleCount = %d, want 1", n)
	}
	assertMatch(t, h, []Property{prop("f", "a")}, "r")
	assertMatch(t, h, []Property{prop("g", "b")}, "r")
	assertMatch(t, h, []Property{prop("f", "a"), prop("g", "b")}, "r")
	assertMatch(t, h, []Property{prop("f", "b")})
}

func TestZeroValue(t *testing.T) {
	var h HyperMatch[string]
	assertMatch(t, &h, []Property{prop("f", "a")})
	mustAdd(t, &h, "r", cond("f", equalsP("a")))
	assertMatch(t, &h, []Property{prop("f", "a")}, "r")
}

func TestEmptyEvent(t *testing.T) {
	h := New[int]()
	mustAdd(t, h, 1, cond("f", wildcardP("*")))
	assertMatch(t, h, nil)
	assertMatch(t, h, []Property{})
}

func TestAppendMatches(t *testing.T) {
	h := New[int]()
	mustAdd(t, h, 1, cond("f", equalsP("a")))
	mustAdd(t, h, 2, cond("f", prefixP("a")), cond("g", anythingButP(equalsP("x"))))
	event := []Property{prop("f", "A"), prop("g", "y")}

	if got := h.AppendMatches([]int{42}, event); !slices.Equal(got, []int{42, 1, 2}) {
		t.Errorf("AppendMatches = %v, want [42 1 2]", got)
	}
	if raceEnabled {
		return
	}
	dst := make([]int, 0, 8)
	if allocs := testing.AllocsPerRun(100, func() { dst = h.AppendMatches(dst[:0], event) }); allocs != 0 {
		t.Errorf("AppendMatches allocates %v times per call, want 0", allocs)
	}
}

func TestIdentifierNotComparable(t *testing.T) {
	h := New[any]()
	rule := ConditionSet{cond("f", equalsP("a"))}
	if err := h.AddRule([]int{1}, rule); !errors.Is(err, ErrInvalidRule) {
		t.Errorf("AddRule(slice) = %v, want ErrInvalidRule", err)
	}
	if err := h.AddRule(struct{ v any }{[]int{1}}, rule); !errors.Is(err, ErrInvalidRule) {
		t.Errorf("AddRule(struct with slice) = %v, want ErrInvalidRule", err)
	}
	if n := h.RuleCount(); n != 0 {
		t.Errorf("RuleCount = %d, want 0", n)
	}
	mustAdd(t, h, nil, rule...)
	mustAdd(t, h, any(struct{ v any }{1}), rule...)
	assertMatch(t, h, []Property{prop("f", "a")}, nil, any(struct{ v any }{1}))
}

func TestInvalidRules(t *testing.T) {
	tests := []struct {
		name string
		rule ConditionSet
		msg  string
	}{
		{"nil", nil, "no conditions"},
		{"no conditions", ConditionSet{}, "no conditions"},
		{"empty path", ConditionSet{cond("", equalsP("a"))}, "empty path"},
		{"unknown type", ConditionSet{cond("f", Pattern{Type: PatternUnknown, Value: "a"})}, "unknown pattern type"},
		{"undefined type", ConditionSet{cond("f", Pattern{Type: 42, Value: "a"})}, "unknown pattern type"},
		{"equals without value", ConditionSet{cond("f", Pattern{Type: PatternEquals})}, "[equals] must contain a value"},
		{"equals with sub-patterns", ConditionSet{cond("f", Pattern{Type: PatternEquals, Value: "a", Sub: []Pattern{equalsP("b")}})}, "[equals] must not contain sub-patterns"},
		{"prefix without value", ConditionSet{cond("f", Pattern{Type: PatternPrefix})}, "[prefix] must contain a value"},
		{"suffix with sub-patterns", ConditionSet{cond("f", Pattern{Type: PatternSuffix, Value: "a", Sub: []Pattern{equalsP("b")}})}, "[suffix] must not contain sub-patterns"},
		{"wildcard without value", ConditionSet{cond("f", Pattern{Type: PatternWildcard})}, "[wildcard] must contain a value"},
		{"consecutive wildcards", ConditionSet{cond("f", wildcardP("a**b"))}, "[wildcard] must not contain two consecutive wildcards"},
		{"anyOf with value", ConditionSet{cond("f", Pattern{Type: PatternAnyOf, Value: "a", Sub: []Pattern{equalsP("b")}})}, "[anyOf] must not contain a value"},
		{"anyOf without sub-patterns", ConditionSet{cond("f", Pattern{Type: PatternAnyOf})}, "[anyOf] must contain sub-patterns"},
		{"allOf without sub-patterns", ConditionSet{cond("f", Pattern{Type: PatternAllOf, Sub: []Pattern{}})}, "[allOf] must contain sub-patterns"},
		{"anythingBut with value", ConditionSet{cond("f", Pattern{Type: PatternAnythingBut, Value: "x"})}, "[anythingBut] must not contain a value"},
		{"anythingBut without sub-patterns", ConditionSet{cond("f", Pattern{Type: PatternAnythingBut})}, "[anythingBut] must contain sub-patterns"},
		{"invalid sub-pattern", ConditionSet{cond("f", anyOfP(equalsP("a"), Pattern{Type: PatternEquals}))}, `condition "f": anyOf[1]: [equals] must contain a value`},
		{"invalid nested sub-pattern", ConditionSet{cond("g", equalsP("a")), cond("f", allOfP(anythingButP(wildcardP("**"))))}, `condition "f": allOf[0]: anythingBut[0]: [wildcard] must not contain two consecutive wildcards`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRule(tt.rule)
			if !errors.Is(err, ErrInvalidRule) || !strings.Contains(err.Error(), tt.msg) {
				t.Errorf("ValidateRule = %v, want ErrInvalidRule containing %q", err, tt.msg)
			}
			h := New[int]()
			if err := h.AddRule(1, tt.rule); !errors.Is(err, ErrInvalidRule) {
				t.Errorf("AddRule = %v, want ErrInvalidRule", err)
			}
			if n := h.RuleCount(); n != 0 {
				t.Errorf("RuleCount = %d after invalid rule, want 0", n)
			}
		})
	}
}

func TestValidRules(t *testing.T) {
	for _, rule := range []ConditionSet{
		{cond("f", prefixP("a**"))}, // "*" is literal in prefix, suffix and equals
		{cond("f", equalsP("**"))},
		{cond("f", wildcardP("*")), cond("f", wildcardP("*a*b*"))},
		{cond("f", anythingButP(anyOfP(allOfP(equalsP("a")))))},
	} {
		if err := ValidateRule(rule); err != nil {
			t.Errorf("ValidateRule(%s) = %v", fmtRule(rule), err)
		}
	}
}
