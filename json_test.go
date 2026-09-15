package hypermatch

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestConditionSetJSONRoundTrip(t *testing.T) {
	rule := ConditionSet{
		cond("name", allOfP(
			equalsP("hallo"),
			wildcardP("hallo*"),
			anythingButP(suffixP("test"), prefixP("st")),
			anyOfP(suffixP("te"), prefixP("tet")),
		)),
		cond("type", equalsP("test")),
	}

	data, err := json.Marshal(rule)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ConditionSet
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, rule) {
		t.Errorf("round trip of %s gave %s", fmtRule(rule), fmtRule(decoded))
	}
}

func TestConditionSetUnmarshal(t *testing.T) {
	tests := []struct {
		in   string
		want ConditionSet
	}{
		{`{"production": {"equals": "true"}}`, ConditionSet{cond("production", equalsP("true"))}},
		{`{"name": {"equals": "te\\st"}}`, ConditionSet{cond("name", equalsP(`te\st`))}},
		{`{"b": {"prefix": "1"}, "a": {"anyOf": [{"suffix": "2"}]}}`, ConditionSet{cond("a", anyOfP(suffixP("2"))), cond("b", prefixP("1"))}},
		{`{"a": {"AnythingBut": [{"Wildcard": "*x"}]}}`, ConditionSet{cond("a", anythingButP(wildcardP("*x")))}},
	}
	for _, tt := range tests {
		var got ConditionSet
		if err := json.Unmarshal([]byte(tt.in), &got); err != nil {
			t.Errorf("Unmarshal(%s): %v", tt.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Unmarshal(%s) = %s, want %s", tt.in, fmtRule(got), fmtRule(tt.want))
		}
	}
}

func TestConditionSetMarshalMergesPaths(t *testing.T) {
	rule := ConditionSet{cond("f", equalsP("a")), cond("g", suffixP("c")), cond("f", prefixP("b"))}
	data, err := json.Marshal(rule)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"f":{"allOf":[{"equals":"a"},{"prefix":"b"}]},"g":{"suffix":"c"}}`
	if string(data) != want {
		t.Errorf("Marshal = %s, want %s", data, want)
	}
}

func TestPatternMarshal(t *testing.T) {
	if data, err := json.Marshal(Pattern{Type: PatternAnyOf}); err != nil || string(data) != `{"anyOf":[]}` {
		t.Errorf("Marshal(anyOf without sub-patterns) = %s, %v", data, err)
	}
	if _, err := json.Marshal(Pattern{Type: PatternUnknown}); err == nil {
		t.Error("Marshal(unknown type) succeeded")
	}
}

func TestPatternUnmarshalErrors(t *testing.T) {
	for _, in := range []string{
		`{}`,
		`{"equals": "a", "prefix": "b"}`,
		`{"bogus": "a"}`,
		`{"anyOf": "a"}`,
		`{"equals": ["a"]}`,
		`[]`,
		`"a"`,
	} {
		var p Pattern
		if err := json.Unmarshal([]byte(in), &p); err == nil {
			t.Errorf("Unmarshal(%s) succeeded with %s", in, fmtPattern(p))
		}
	}
}

func TestConditionJSON(t *testing.T) {
	c := cond("f", anyOfP(equalsP("a"), wildcardP("b*")))
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Condition
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, c) {
		t.Errorf("round trip gave %+v, want %+v", decoded, c)
	}
	for _, in := range []string{`{}`, `{"f": {"equals": "a"}, "g": {"equals": "b"}}`} {
		if err := json.Unmarshal([]byte(in), &decoded); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", in)
		}
	}
}

// TestJSONRule checks the example from the README.
func TestJSONRule(t *testing.T) {
	var rule ConditionSet
	err := json.Unmarshal([]byte(`{
		"status": {"equals": "firing"},
		"name": {"anythingBut": [{"wildcard": "TEST*"}]},
		"severity": {"anyOf": [{"equals": "critical"}, {"equals": "warning"}]},
		"tags": {"allOf": [{"equals": "shop"}, {"equals": "backend"}]}
	}`), &rule)
	if err != nil {
		t.Fatal(err)
	}
	h := New[string]()
	mustAdd(t, h, "alert", rule...)

	event := []Property{
		prop("name", "Too many parallel requests on system xy"),
		prop("severity", "critical"),
		prop("status", "firing"),
		prop("message", "Lorem ipsum dolor sit amet, consetetur sadipscing elitr."),
		prop("team", "awesome-team"),
		prop("application", "webshop"),
		prop("component", "backend-service"),
		prop("tags", "shop", "backend"),
	}
	assertMatch(t, h, event, "alert")
	event[0] = prop("name", "test: too many parallel requests")
	assertMatch(t, h, event)
}
