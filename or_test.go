package hypermatch

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func orC(sets ...ConditionSet) Condition { return Condition{Or: sets} }

func TestOrSemantics(t *testing.T) {
	rule := ConditionSet{
		cond("env", equalsP("prod")),
		orC(
			ConditionSet{cond("team", equalsP("shop"))},
			ConditionSet{cond("severity", equalsP("critical")), cond("escalated", existsP(true))},
		),
	}
	tests := []struct {
		name  string
		event []Property
		want  bool
	}{
		{"first alternative", []Property{prop("env", "prod"), prop("team", "shop")}, true},
		{"second alternative", []Property{prop("env", "prod"), prop("severity", "critical"), prop("escalated", "yes")}, true},
		{"both alternatives", []Property{prop("env", "prod"), prop("team", "shop"), prop("severity", "critical"), prop("escalated", "yes")}, true},
		{"no alternative", []Property{prop("env", "prod"), prop("team", "search")}, false},
		{"alternative only in part", []Property{prop("env", "prod"), prop("severity", "critical")}, false},
		{"condition beside the alternatives fails", []Property{prop("env", "dev"), prop("team", "shop")}, false},
		{"empty event", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := New[int]()
			mustAdd(t, h, 1, rule...)
			if got := len(h.Match(tt.event)) == 1; got != tt.want {
				t.Errorf("Match = %v, want %v", got, tt.want)
			}
			if got := refMatches(rule, tt.event); got != tt.want {
				t.Errorf("reference = %v, want %v", got, tt.want)
			}
			if e, err := Explain(rule, tt.event); err != nil || e.Matched != tt.want {
				t.Errorf("Explain = %v, %v, want %v", e.Matched, err, tt.want)
			}
		})
	}
}

// TestOrNested checks alternatives inside alternatives, and that a rule with
// alternatives counts as one rule.
func TestOrNested(t *testing.T) {
	rule := ConditionSet{
		cond("a", equalsP("1")),
		orC(
			ConditionSet{cond("b", equalsP("2"))},
			ConditionSet{orC(
				ConditionSet{cond("c", equalsP("3"))},
				ConditionSet{cond("d", existsP(false))},
			)},
		),
	}
	h := New[string]()
	mustAdd(t, h, "r", rule...)
	if n := h.RuleCount(); n != 1 {
		t.Errorf("RuleCount = %d, want 1", n)
	}
	for _, tt := range []struct {
		event []Property
		want  bool
	}{
		{[]Property{prop("a", "1"), prop("b", "2")}, true},
		{[]Property{prop("a", "1"), prop("c", "3"), prop("d", "x")}, true},
		{[]Property{prop("a", "1"), prop("d", "x")}, false},
		{[]Property{prop("a", "1")}, true}, // d is absent
		{[]Property{prop("b", "2")}, false},
	} {
		if got := len(h.Match(tt.event)) == 1; got != tt.want {
			t.Errorf("Match(%s) = %v, want %v", fmtEvent(tt.event), got, tt.want)
		}
		if got := refMatches(rule, tt.event); got != tt.want {
			t.Errorf("reference(%s) = %v, want %v", fmtEvent(tt.event), got, tt.want)
		}
	}
}

// TestOrRuleUpdates checks that rules with alternatives can be replaced and
// removed like any other rule.
func TestOrRuleUpdates(t *testing.T) {
	h := New[string]()
	rule := ConditionSet{orC(ConditionSet{cond("a", equalsP("1"))}, ConditionSet{cond("b", equalsP("2"))})}
	mustAdd(t, h, "r", rule...)
	mustAdd(t, h, "other", cond("a", equalsP("1")))
	assertMatch(t, h, []Property{prop("b", "2")}, "r")

	if err := h.ReplaceRule("r", ConditionSet{orC(ConditionSet{cond("a", equalsP("9"))}, ConditionSet{cond("b", equalsP("9"))})}); err != nil {
		t.Fatal(err)
	}
	assertMatch(t, h, []Property{prop("b", "2")})
	assertMatch(t, h, []Property{prop("b", "9")}, "r")
	assertMatch(t, h, []Property{prop("a", "1")}, "other")

	if !h.RemoveRule("r") {
		t.Error("RemoveRule = false")
	}
	assertMatch(t, h, []Property{prop("b", "9")})
}

func TestOrValidation(t *testing.T) {
	tests := []struct {
		name string
		rule ConditionSet
		msg  string
	}{
		{"two of them", ConditionSet{orC(ConditionSet{cond("a", equalsP("1"))}), orC(ConditionSet{cond("b", equalsP("2"))})}, `"$or" must not appear twice`},
		{"without condition sets", ConditionSet{cond("a", equalsP("1")), {Or: []ConditionSet{}}}, `"$or" must contain condition sets`},
		{"with a path", ConditionSet{{Path: "a", Or: []ConditionSet{{cond("b", equalsP("1"))}}}}, `"$or" must not have a path or a pattern`},
		{"with a pattern", ConditionSet{{Pattern: equalsP("x"), Or: []ConditionSet{{cond("b", equalsP("1"))}}}}, `"$or" must not have a path or a pattern`},
		{"empty alternative", ConditionSet{orC(ConditionSet{cond("a", equalsP("1"))}, ConditionSet{})}, "$or[1]: no conditions"},
		{"invalid alternative", ConditionSet{orC(ConditionSet{cond("a", Pattern{Type: PatternEquals})})}, `$or[0]: condition "a": [equals] must contain a value`},
		{"absent combined with an alternative", ConditionSet{cond("a", existsP(false)), orC(ConditionSet{cond("a", equalsP("1"))})}, "[exists] false cannot be combined"},
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
		})
	}
}

// TestOrTooMany checks the limits on how far alternatives may combine.
func TestOrTooMany(t *testing.T) {
	// Eleven nested alternatives with two branches each make 2048 sets.
	rule := ConditionSet{cond("a", equalsP("1"))}
	for range 11 {
		rule = ConditionSet{orC(append(ConditionSet{cond("b", equalsP("2"))}, rule...), rule)}
	}
	err := ValidateRule(rule)
	if !errors.Is(err, ErrInvalidRule) || !strings.Contains(err.Error(), "more than 1024 condition sets") {
		t.Errorf("ValidateRule = %v, want an error about the number of condition sets", err)
	}

	deep := ConditionSet{cond("a", equalsP("1"))}
	for range maxOrDepth + 1 {
		deep = ConditionSet{orC(deep)}
	}
	if err := ValidateRule(deep); !errors.Is(err, ErrInvalidRule) || !strings.Contains(err.Error(), "nested more than") {
		t.Errorf("ValidateRule(deeply nested) = %v, want an error about nesting", err)
	}
}

func TestOrJSON(t *testing.T) {
	const in = `{
		"env": {"equals": "prod"},
		"$or": [
			{"team": {"equals": "shop"}},
			{"severity": {"equals": "critical"}, "escalated": {"exists": true}}
		]
	}`
	var rule ConditionSet
	if err := json.Unmarshal([]byte(in), &rule); err != nil {
		t.Fatal(err)
	}
	want := ConditionSet{
		orC(
			ConditionSet{cond("team", equalsP("shop"))},
			ConditionSet{cond("escalated", existsP(true)), cond("severity", equalsP("critical"))},
		),
		cond("env", equalsP("prod")),
	}
	if !reflect.DeepEqual(rule, want) {
		t.Errorf("Unmarshal gave %+v, want %+v", rule, want)
	}
	data, err := json.Marshal(rule)
	if err != nil {
		t.Fatal(err)
	}
	const wantJSON = `{"$or":[{"team":{"equals":"shop"}},{"escalated":{"exists":true},"severity":{"equals":"critical"}}],"env":{"equals":"prod"}}`
	if string(data) != wantJSON {
		t.Errorf("Marshal =\n%s\nwant\n%s", data, wantJSON)
	}
	var again ConditionSet
	if err := json.Unmarshal(data, &again); err != nil || !reflect.DeepEqual(again, rule) {
		t.Errorf("round trip gave %+v, %v", again, err)
	}
}

// TestOrJSONPath checks that "$or" with an object is an ordinary path.
func TestOrJSONPath(t *testing.T) {
	var rule ConditionSet
	if err := json.Unmarshal([]byte(`{"$or": {"equals": "x"}}`), &rule); err != nil {
		t.Fatal(err)
	}
	if want := (ConditionSet{cond("$or", equalsP("x"))}); !reflect.DeepEqual(rule, want) {
		t.Errorf("Unmarshal gave %+v, want %+v", rule, want)
	}
	h := New[int]()
	mustAdd(t, h, 1, rule...)
	assertMatch(t, h, []Property{prop("$or", "x")}, 1)

	if _, err := json.Marshal(ConditionSet{cond("$or", equalsP("x")), orC(ConditionSet{cond("a", equalsP("1"))})}); err == nil {
		t.Error("Marshal of a condition on $or together with alternatives succeeded")
	}
	if _, err := json.Marshal(ConditionSet{orC(ConditionSet{cond("a", equalsP("1"))}), orC(ConditionSet{cond("b", equalsP("2"))})}); err == nil {
		t.Error("Marshal of two sets of alternatives succeeded")
	}
}

func TestOrCondition(t *testing.T) {
	c := orC(ConditionSet{cond("a", equalsP("1"))}, ConditionSet{cond("b", equalsP("2"))})
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"$or":[{"a":{"equals":"1"}},{"b":{"equals":"2"}}]}`; string(data) != want {
		t.Errorf("Marshal = %s, want %s", data, want)
	}
	var decoded Condition
	if err := json.Unmarshal(data, &decoded); err != nil || !reflect.DeepEqual(decoded, c) {
		t.Errorf("round trip gave %+v, %v", decoded, err)
	}
	for _, in := range []string{`{"$or": [{"a": {"bogus": "1"}}]}`, `{"$or": ["a"]}`} {
		if err := json.Unmarshal([]byte(in), &decoded); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", in)
		}
	}
}

// TestOrSharesConditions checks that the combinations of a rule share the
// conditions of other rules instead of matching twice.
func TestOrSharesConditions(t *testing.T) {
	h := New[int]()
	mustAdd(t, h, 1, cond("env", equalsP("prod")), orC(
		ConditionSet{cond("team", equalsP("shop"))},
		ConditionSet{cond("team", equalsP("search"))},
	))
	mustAdd(t, h, 2, cond("env", equalsP("prod")))
	event := []Property{prop("env", "prod"), prop("team", "shop", "search")}
	if got := h.Match(event); !slices.Equal(got, []int{1, 2}) {
		t.Errorf("Match = %v, want [1 2]", got)
	}
	if id, ok := h.MatchFirst(event); !ok || id != 1 {
		t.Errorf("MatchFirst = %v, %v, want 1", id, ok)
	}
}

// TestOrExplanation checks how an explanation shows alternatives, as text
// and as JSON.
func TestOrExplanation(t *testing.T) {
	rule := ConditionSet{
		cond("env", equalsP("prod")),
		orC(
			ConditionSet{cond("team", equalsP("shop"))},
			ConditionSet{cond("severity", equalsP("critical"))},
		),
	}
	e, err := Explain(rule, []Property{prop("env", "prod"), prop("severity", "critical")})
	if err != nil {
		t.Fatal(err)
	}
	const want = `match
  ✓ env: {"equals":"prod"} matched "prod"
  ✓ any of:
    ✗ alternative 1
      ✗ team: {"equals":"shop"} (absent)
    ✓ alternative 2
      ✓ severity: {"equals":"critical"} matched "critical"
`
	if got := e.String(); got != want {
		t.Errorf("String() =\n%s\nwant\n%s", got, want)
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	const wantJSON = `{"matched":true,"conditions":[` +
		`{"path":"env","matched":true,"values":["prod"],"result":{"pattern":{"equals":"prod"},"matched":true,"values":["prod"]}},` +
		`{"matched":true,"or":[` +
		`{"matched":false,"conditions":[{"path":"team","matched":false,"absent":true,"result":{"pattern":{"equals":"shop"},"matched":false}}]},` +
		`{"matched":true,"conditions":[{"path":"severity","matched":true,"values":["critical"],"result":{"pattern":{"equals":"critical"},"matched":true,"values":["critical"]}}]}]}]}`
	if string(data) != wantJSON {
		t.Errorf("Marshal =\n%s\nwant\n%s", data, wantJSON)
	}
}
