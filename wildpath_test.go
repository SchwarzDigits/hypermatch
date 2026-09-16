package hypermatch

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestWildcardPaths(t *testing.T) {
	tests := []struct {
		name  string
		rule  ConditionSet
		event []Property
		want  bool
	}{
		{"below", ConditionSet{cond("labels.*", equalsP("prod"))}, []Property{prop("labels.env", "prod")}, true},
		{"other value", ConditionSet{cond("labels.*", equalsP("prod"))}, []Property{prop("labels.env", "dev")}, false},
		{"any of the paths", ConditionSet{cond("labels.*", equalsP("prod"))},
			[]Property{prop("labels.team", "db"), prop("labels.env", "prod")}, true},
		{"deeper", ConditionSet{cond("labels.*", equalsP("prod"))}, []Property{prop("labels.a.b", "prod")}, true},
		{"empty key", ConditionSet{cond("labels.*", equalsP("prod"))}, []Property{prop("labels.", "prod")}, true},
		{"literal star", ConditionSet{cond("labels.*", equalsP("prod"))}, []Property{prop("labels.*", "prod")}, true},
		{"not the path itself", ConditionSet{cond("labels.*", equalsP("prod"))}, []Property{prop("labels", "prod")}, false},
		{"not a longer name", ConditionSet{cond("labels.*", equalsP("prod"))}, []Property{prop("labelsx.env", "prod")}, false},
		{"not further down", ConditionSet{cond("labels.*", equalsP("prod"))}, []Property{prop("x.labels.env", "prod")}, false},
		{"case-sensitive", ConditionSet{cond("Labels.*", equalsP("prod"))}, []Property{prop("labels.env", "prod")}, false},
		{"anythingBut sees all values", ConditionSet{cond("labels.*", anythingButP(equalsP("prod")))},
			[]Property{prop("labels.team", "db"), prop("labels.env", "prod")}, false},
		{"anythingBut", ConditionSet{cond("labels.*", anythingButP(equalsP("prod")))},
			[]Property{prop("labels.team", "db"), prop("env", "prod")}, true},
		{"anythingBut, absent", ConditionSet{cond("labels.*", anythingButP(equalsP("prod")))},
			[]Property{prop("labels", "db")}, false},
		{"allOf across paths", ConditionSet{cond("labels.*", allOfP(equalsP("a"), equalsP("b")))},
			[]Property{prop("labels.x", "a"), prop("labels.y", "b")}, true},
		{"numbers", ConditionSet{cond("metrics.*", gtP("5"))}, []Property{prop("metrics.cpu", "1"), prop("metrics.mem", "10")}, true},
		{"exists", ConditionSet{cond("labels.*", existsP(true))}, []Property{prop("labels.x", "")}, true},
		{"exists, absent", ConditionSet{cond("labels.*", existsP(true))}, []Property{prop("labels", "x")}, false},
		{"absent", ConditionSet{cond("labels.*", existsFalse)}, []Property{prop("labels", "x"), prop("labels.x")}, true},
		{"absent, present", ConditionSet{cond("labels.*", existsFalse)}, []Property{prop("labels.x", "y")}, false},
		{"nested wildcard paths",
			ConditionSet{cond("labels.*", equalsP("a")), cond("labels.x.*", equalsP("b"))},
			[]Property{prop("labels.x.y", "b"), prop("labels.z", "a")}, true},
		{"nested wildcard paths, outside the inner one",
			ConditionSet{cond("labels.*", equalsP("a")), cond("labels.x.*", equalsP("b"))},
			[]Property{prop("labels.y", "b"), prop("labels.z", "a")}, false},
		{"beside the exact path", ConditionSet{cond("labels.*", equalsP("a")), cond("labels.x", equalsP("b"))},
			[]Property{prop("labels.x", "b"), prop("labels.y", "a")}, true},
		{"only a final star", ConditionSet{cond("labels.*.env", equalsP("a"))}, []Property{prop("labels.x.env", "a")}, false},
		{"only a final star, literal", ConditionSet{cond("labels.*.env", equalsP("a"))}, []Property{prop("labels.*.env", "a")}, true},
		{"a star without a dot is literal", ConditionSet{cond("labels*", equalsP("a"))}, []Property{prop("label.x", "a")}, false},
		{"a star alone is literal", ConditionSet{cond("*", equalsP("a"))}, []Property{prop("x", "a")}, false},
		{"a star after a dot alone is literal", ConditionSet{cond(".*", equalsP("a"))}, []Property{prop(".x", "a")}, false},
		{"alternatives", ConditionSet{{Or: []ConditionSet{{cond("labels.*", equalsP("a"))}, {cond("tags.*", equalsP("a"))}}}},
			[]Property{prop("tags.x", "a")}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := New[int]()
			mustAdd(t, h, 1, tt.rule...)
			if got := len(h.Match(tt.event)) == 1; got != tt.want {
				t.Errorf("Match = %v, want %v", got, tt.want)
			}
			if _, got := h.MatchFirst(tt.event); got != tt.want {
				t.Errorf("MatchFirst = %v, want %v", got, tt.want)
			}
			if got := refMatches(tt.rule, tt.event); got != tt.want {
				t.Errorf("reference = %v, want %v", got, tt.want)
			}
			if e, err := Explain(tt.rule, tt.event); err != nil || e.Matched != tt.want {
				t.Errorf("Explain = %v, %v, want %v", e.Matched, err, tt.want)
			}
		})
	}
}

func TestWildcardPathsJSON(t *testing.T) {
	rules := []struct {
		id   string
		cond Condition
	}{
		{"labels", cond("labels.*", equalsP("prod"))},
		{"not-labels", cond("labels.*", anythingButP(equalsP("prod")))},
		{"no-labels", cond("labels.*", existsFalse)},
		{"inner", cond("labels.a.*", equalsP("prod"))},
		{"exact", cond("labels.a", equalsP("prod"))},
		{"star", cond("labels.*", equalsP("*"))},
		{"number", cond("labels.*", gtP("5"))},
		{"dot", cond("labels..*", equalsP("prod"))},
	}
	h := New[string]()
	numbered := New[int]() // for checkMatchJSON
	for i, r := range rules {
		mustAdd(t, h, r.id, r.cond)
		mustAdd(t, numbered, i, r.cond)
	}
	tests := []struct {
		event string
		want  []string
	}{
		{`{"labels": {"env": "prod"}}`, []string{"labels"}},
		{`{"labels": {"env": "dev", "team": "db"}}`, []string{"not-labels"}},
		{`{"labels": {"env": "dev", "team": "PROD"}}`, []string{"labels"}},
		{`{"labels.env": "prod"}`, []string{"labels"}},
		{`{"labels": {"a": "prod"}}`, []string{"labels", "exact"}},
		{`{"labels": {"a": {"b": "prod"}}}`, []string{"labels", "inner"}},
		{`{"labels": {"a.b": "prod"}}`, []string{"labels", "inner"}},
		{`{"labels.a": {"b": "prod"}}`, []string{"labels", "inner"}},
		{`{"labels": [{"env": "prod"}, "x"]}`, []string{"labels"}},
		{`{"labels": {"*": "prod"}}`, []string{"labels"}},
		{`{"labels": {"": "prod"}}`, []string{"labels"}},
		{`{"labels": {".a": {"b": "prod"}}}`, []string{"labels", "dot"}},
		{`{"labels": {"": {"a": "prod"}}}`, []string{"labels", "dot"}},
		{`{"labels": {"a..b": "prod"}}`, []string{"labels", "inner"}},
		{`{"labels": {"x": "*"}}`, []string{"not-labels", "star"}},
		{`{"labels": {"x": 7}}`, []string{"not-labels", "number"}},
		{`{"labels": {"x": [1, {"y": 9}]}}`, []string{"not-labels", "number"}},
		{`{"labels": {}}`, []string{"no-labels"}},
		{`{"labels": {"env": null}}`, []string{"no-labels"}},
		{`{"labels": {"env": []}}`, []string{"no-labels"}},
		{`{"labels": "prod"}`, []string{"no-labels"}},
		{`{"x": {"labels": {"env": "prod"}}}`, []string{"no-labels"}},
		{`{"labels": {"env": "prod"}, "labels": {"team": "db"}}`, []string{"labels"}},
	}
	for _, tt := range tests {
		got, err := h.MatchJSON([]byte(tt.event))
		if err != nil || !slices.Equal(got, tt.want) {
			t.Errorf("MatchJSON(%s) = %v, %v, want %v", tt.event, got, err, tt.want)
		}
		if err := checkMatchJSON(numbered, []byte(tt.event)); err != nil {
			t.Error(err)
		}
	}
	for _, event := range []string{`{"labels": {"x": "\q"}}`, `{"labels": {"x": 01}}`, `{"labels": {"x": tru}}`} {
		if _, err := h.MatchJSON([]byte(event)); !errors.Is(err, ErrInvalidEvent) {
			t.Errorf("MatchJSON(%s) = %v, want ErrInvalidEvent", event, err)
		}
	}
}

// TestWildcardPathsWide matches events whose properties are below more
// wildcard paths than the table of spans starts with, so that it grows.
func TestWildcardPathsWide(t *testing.T) {
	for _, n := range []int{1, 5, 9, 40, 300} {
		// Rule 2i refers to the paths below li, rule 2i+1 to the paths
		// below d.d. ... .d with i+1 times d. The event has a value below all
		// of them.
		h := New[int]()
		var want []int
		for i := range n {
			mustAdd(t, h, 2*i, cond(fmt.Sprintf("l%d.*", i), equalsP(fmt.Sprintf("v%d", i))))
			mustAdd(t, h, 2*i+1, cond(strings.Repeat("d.", i+1)+"*", equalsP("deep")))
			want = append(want, 2*i, 2*i+1)
		}
		event := []Property{prop(strings.Repeat("d.", n)+"d", "deep")}
		var b strings.Builder
		b.WriteString(`{"d": `)
		b.WriteString(strings.Repeat(`{"d": `, n))
		b.WriteString(`"deep"`)
		b.WriteString(strings.Repeat("}", n))
		for i := range n {
			event = append(event, prop(fmt.Sprintf("l%d.k", i), fmt.Sprintf("v%d", i)))
			fmt.Fprintf(&b, `, "l%d": {"k": "v%d"}`, i, i)
		}
		b.WriteString("}")
		if got := h.Match(event); !slices.Equal(got, want) {
			t.Errorf("n=%d: Match = %v, want %v", n, got, want)
		}
		if got, err := h.MatchJSON([]byte(b.String())); err != nil || !slices.Equal(got, want) {
			t.Errorf("n=%d: MatchJSON = %v, %v, want %v", n, got, err, want)
		}
	}
}

func TestWildcardPathsAllocations(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector allocates")
	}
	h := New[int]()
	mustAdd(t, h, 1, cond("labels.*", equalsP("prod")))
	mustAdd(t, h, 2, cond("labels.team", equalsP("db")))
	mustAdd(t, h, 3, cond("labels.*", anythingButP(equalsP("dev"))))
	event := []Property{prop("labels.env", "prod"), prop("labels.team", "db"), prop("host", "x")}
	data := []byte(`{"labels": {"env": "prod", "team": "db", "n": 5}, "host": "x"}`)
	dst := make([]int, 0, 4)
	if allocs := testing.AllocsPerRun(100, func() { dst = h.AppendMatches(dst[:0], event) }); allocs != 0 || len(dst) != 3 {
		t.Errorf("AppendMatches = %v with %v allocations per call, want [1 2 3] and none", dst, allocs)
	}
	if allocs := testing.AllocsPerRun(100, func() { dst, _ = h.AppendMatchesJSON(dst[:0], data) }); allocs != 0 || len(dst) != 3 {
		t.Errorf("AppendMatchesJSON = %v with %v allocations per call, want [1 2 3] and none", dst, allocs)
	}
}

func TestWildcardPathsExplain(t *testing.T) {
	rule := ConditionSet{cond("labels.*", anythingButP(equalsP("dev")))}
	e, err := ExplainJSON(rule, []byte(`{"labels": {"env": "dev", "team": "db"}, "host": "x"}`))
	if err != nil {
		t.Fatal(err)
	}
	c := e.Conditions[0]
	if e.Matched || c.Path != "labels.*" || !slices.Equal(c.Values, []string{"dev", "db"}) || !slices.Equal(c.Result.Values, []string{"dev"}) {
		t.Errorf("ExplainJSON = %+v, want no match because of the value dev among [dev db]", e)
	}
}
