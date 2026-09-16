package hypermatch

import (
	"encoding/json"
	"errors"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestExplain(t *testing.T) {
	rule := ConditionSet{
		cond("status", equalsP("firing")),
		cond("severity", anyOfP(equalsP("critical"), equalsP("warning"))),
		cond("tags", allOfP(equalsP("shop"), equalsP("backend"))),
		cond("region", anythingButP(equalsP("moon"))),
		cond("owner", existsP(false)),
	}
	e, err := Explain(rule, []Property{
		prop("status", "FIRING"),
		prop("severity", "info"),
		prop("tags", "shop", "eu"),
		prop("region", "earth", "Moon"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if e.Matched {
		t.Error("Matched = true")
	}
	matched := make([]bool, len(e.Conditions))
	for i, c := range e.Conditions {
		matched[i] = c.Result.Matched
	}
	if want := []bool{true, false, false, false, true}; !slices.Equal(matched, want) {
		t.Errorf("condition results = %v, want %v", matched, want)
	}
	if got := e.Conditions[0].Result.Values; !slices.Equal(got, []string{"FIRING"}) {
		t.Errorf("status matched %v, want [FIRING]", got)
	}
	if got := e.Conditions[3].Result.Values; !slices.Equal(got, []string{"Moon"}) {
		t.Errorf("region excluded by %v, want [Moon]", got)
	}
	if c := e.Conditions[4]; !c.Absent || c.Values != nil {
		t.Errorf("owner: Absent = %v, Values = %v, want true, nil", c.Absent, c.Values)
	}
	if e.Conditions[0].Absent {
		t.Error("status: Absent = true")
	}

	want := `no match
  ✓ status: {"equals":"firing"} matched "FIRING"
  ✗ severity: {"anyOf":[{"equals":"critical"},{"equals":"warning"}]} (values ["info"])
      ✗ {"equals":"critical"}
      ✗ {"equals":"warning"}
  ✗ tags: {"allOf":[{"equals":"shop"},{"equals":"backend"}]} (values ["shop" "eu"])
      ✓ {"equals":"shop"} matched "shop"
      ✗ {"equals":"backend"}
  ✗ region: {"anythingBut":[{"equals":"moon"}]} excluded by "Moon"
      ✓ {"equals":"moon"} matched "Moon"
  ✓ owner: {"exists":false} (absent)
`
	if got := e.String(); got != want {
		t.Errorf("String() =\n%s\nwant\n%s", got, want)
	}
}

// TestExplanationJSON checks the JSON form a user interface gets, and that
// it decodes back into the same explanation.
func TestExplanationJSON(t *testing.T) {
	rule := ConditionSet{
		cond("owner", existsP(false)),
		cond("severity", anyOfP(equalsP("critical"), equalsP("warning"))),
		cond("status", equalsP("firing")),
	}
	e, err := Explain(rule, []Property{prop("status", "FIRING"), prop("severity", "info")})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"matched":false,"conditions":[` +
		`{"path":"owner","matched":true,"absent":true,"result":{"pattern":{"exists":false},"matched":true}},` +
		`{"path":"severity","matched":false,"values":["info"],"result":{"pattern":{"anyOf":[{"equals":"critical"},{"equals":"warning"}]},"matched":false,` +
		`"sub":[{"pattern":{"equals":"critical"},"matched":false},{"pattern":{"equals":"warning"},"matched":false}]}},` +
		`{"path":"status","matched":true,"values":["FIRING"],"result":{"pattern":{"equals":"firing"},"matched":true,"values":["FIRING"]}}]}`
	if string(data) != want {
		t.Errorf("Marshal =\n%s\nwant\n%s", data, want)
	}
	var decoded Explanation
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, e) {
		t.Errorf("round trip gave %+v, want %+v", decoded, e)
	}
}

func TestExplainErrors(t *testing.T) {
	if _, err := Explain(ConditionSet{cond("f", Pattern{Type: PatternEquals})}, nil); !errors.Is(err, ErrInvalidRule) {
		t.Errorf("Explain(invalid rule) = %v, want ErrInvalidRule", err)
	}
	for _, event := range []string{`{`, `[]`, `{"a": 1} x`} {
		if _, err := ExplainJSON(ConditionSet{cond("f", equalsP("a"))}, []byte(event)); !errors.Is(err, ErrInvalidEvent) {
			t.Errorf("ExplainJSON(%s) = %v, want ErrInvalidEvent", event, err)
		}
	}
}

func TestGlobMatch(t *testing.T) {
	for _, tt := range []struct {
		pattern, s string
		want       bool
	}{
		{"a*b*c", "abc", true}, {"a*b*c", "aXbYc", true}, {"a*b*c", "acb", false},
		{"*", "", true}, {"*x*", "axb", true}, {"*x*", "ab", false}, {"a*a", "aa", true},
		{"ab", "ab", true}, {"ab", "abc", false}, {"*-mon-*", "s1-mon-mon-mon-test", true},
		{"a*a", "a", false}, {"*ab*b", "abb", true}, {"*ab*b", "ab", false}, {"*a*a*", "aa", true},
		{`a\*b`, "a*b", true}, {`a\*b`, "axb", false}, {`*\**`, "x*y", true}, {`*\**`, "xy", false},
		{`\\*`, `\x`, true}, {`\\*`, "x", false}, {`a\\\*`, `a\*`, true}, {`\**\*`, "*x", false},
	} {
		if got := globMatch(tt.pattern, tt.s); got != tt.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tt.pattern, tt.s, got, tt.want)
		}
		if got := refGlob(tt.pattern, tt.s); got != tt.want {
			t.Errorf("refGlob(%q, %q) = %v, want %v", tt.pattern, tt.s, got, tt.want)
		}
	}
}

// TestExplainDifferential checks that Explain agrees with the reference and
// with Match on random rules and events.
func TestExplainDifferential(t *testing.T) {
	seeds := 3000
	if testing.Short() {
		seeds = 300
	}
	for seed := range seeds {
		src := randSource{rand.New(rand.NewPCG(uint64(seed), 6))}
		rule := genRule(src)
		h := New[int]()
		mustAdd(t, h, 1, rule...)
		for range 10 {
			event := genEvent(src)
			e, err := Explain(rule, event)
			if err != nil {
				t.Fatalf("Explain(%s): %v", fmtRule(rule), err)
			}
			if want := refMatches(rule, event); e.Matched != want {
				t.Fatalf("Explain(%s, %s).Matched = %v, want %v\n%s", fmtRule(rule), fmtEvent(event), e.Matched, want, e)
			}
			if matched := len(h.Match(event)) == 1; e.Matched != matched {
				t.Fatalf("Explain(%s, %s).Matched = %v, but Match says %v", fmtRule(rule), fmtEvent(event), e.Matched, matched)
			}
		}
		var b strings.Builder
		genJSONObject(src, &b, 0)
		data := []byte(b.String())
		jrule := genJSONRule(src)
		e, err := ExplainJSON(jrule, data)
		if err != nil {
			t.Fatalf("ExplainJSON(%s, %s): %v", fmtRule(jrule), data, err)
		}
		jh := New[int]()
		mustAdd(t, jh, 1, jrule...)
		got, err := jh.MatchJSON(data)
		if err != nil || e.Matched != (len(got) == 1) {
			t.Fatalf("ExplainJSON(%s, %s).Matched = %v, but MatchJSON says %v, %v", fmtRule(jrule), data, e.Matched, got, err)
		}
	}
}
