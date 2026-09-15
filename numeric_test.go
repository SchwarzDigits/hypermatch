package hypermatch

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func ltP(v string) Pattern  { return Pattern{Type: PatternLessThan, Value: v} }
func lteP(v string) Pattern { return Pattern{Type: PatternLessThanOrEqual, Value: v} }
func gtP(v string) Pattern  { return Pattern{Type: PatternGreaterThan, Value: v} }
func gteP(v string) Pattern { return Pattern{Type: PatternGreaterThanOrEqual, Value: v} }

func betweenP(lower, upper Pattern) Pattern {
	return Pattern{Type: PatternBetween, Sub: []Pattern{lower, upper}}
}

func existsP(present bool) Pattern {
	return Pattern{Type: PatternExists, Value: strconv.FormatBool(present)}
}

func TestParseNumber(t *testing.T) {
	for _, s := range []string{"0", "-0", "+5", "42", "-1.5", ".5", "5.", "1e3", "1E-3", "010", "0.1",
		"123456789012345", "1234567890123456789", "1e22", "1e23", "4.9e-324", "1e400", "-1e400",
		"2.5e-10", "9007199254740993", "1.0000000000000002", "0.000001"} {
		want, err := strconv.ParseFloat(s, 64)
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			t.Fatal(err)
		}
		got, ok := parseNumber(s)
		if !ok || math.Float64bits(got) != math.Float64bits(want) {
			t.Errorf("parseNumber(%q) = %v, %v, want %v", s, got, ok, want)
		}
		if got, _ := parseNumber([]byte(s)); math.Float64bits(got) != math.Float64bits(want) {
			t.Errorf("parseNumber([]byte(%q)) = %v, want %v", s, got, want)
		}
	}
	for _, s := range []string{"", "-", "+", ".", "e3", "1e", "1e+", "0x10", "1_000", "inf", "NaN",
		" 1", "1 ", "1.2.3", "--1", "1e3.5", "٣"} {
		if f, ok := parseNumber(s); ok {
			t.Errorf("parseNumber(%q) = %v, want no number", s, f)
		}
	}
}

func FuzzParseNumber(f *testing.F) {
	for _, s := range []string{"1", "-2.5e3", ".5", "1234567890123456789", "1e-400", "0.1e1"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got, ok := parseNumber(s)
		want, wantOK := refNumber(s)
		if ok != wantOK || (ok && math.Float64bits(got) != math.Float64bits(want)) {
			t.Errorf("parseNumber(%q) = %v, %v, want %v, %v", s, got, ok, want, wantOK)
		}
	})
}

func TestNumericAndExistsSemantics(t *testing.T) {
	tests := []struct {
		name  string
		rule  ConditionSet
		event []Property
		want  bool
	}{
		{"gt", ConditionSet{cond("v", gtP("500"))}, []Property{prop("v", "501")}, true},
		{"gt excludes its bound", ConditionSet{cond("v", gtP("500"))}, []Property{prop("v", "500")}, false},
		{"gte includes its bound", ConditionSet{cond("v", gteP("500"))}, []Property{prop("v", "500")}, true},
		{"lt", ConditionSet{cond("v", ltP("0"))}, []Property{prop("v", "-0.5")}, true},
		{"lte", ConditionSet{cond("v", lteP("1e3"))}, []Property{prop("v", "1000")}, true},
		{"any notation", ConditionSet{cond("v", gtP("1000"))}, []Property{prop("v", "1.5E3")}, true},
		{"leading zeros", ConditionSet{cond("v", ltP("10"))}, []Property{prop("v", "007")}, true},
		{"not a number", ConditionSet{cond("v", gtP("0"))}, []Property{prop("v", "abc")}, false},
		{"any value of an array", ConditionSet{cond("v", gtP("10"))}, []Property{prop("v", "1", "20")}, true},
		{"infinite values", ConditionSet{cond("v", gtP("1e300"))}, []Property{prop("v", "1e400")}, true},
		{"between", ConditionSet{cond("v", betweenP(gteP("500"), ltP("600")))}, []Property{prop("v", "503")}, true},
		{"between excludes an open bound", ConditionSet{cond("v", betweenP(gteP("500"), ltP("600")))}, []Property{prop("v", "600")}, false},
		{"between needs a single value", ConditionSet{cond("v", betweenP(gtP("10"), ltP("20")))}, []Property{prop("v", "5", "25")}, false},
		{"allOf of comparisons may use several values", ConditionSet{cond("v", allOfP(gtP("10"), ltP("20")))}, []Property{prop("v", "5", "25")}, true},
		{"anythingBut with a comparison", ConditionSet{cond("v", anythingButP(gtP("5")))}, []Property{prop("v", "1", "2")}, true},
		{"anythingBut with a comparison fails", ConditionSet{cond("v", anythingButP(gtP("5")))}, []Property{prop("v", "1", "9")}, false},
		{"exists true", ConditionSet{cond("v", existsP(true))}, []Property{prop("v", "")}, true},
		{"exists true needs the property", ConditionSet{cond("v", existsP(true))}, []Property{prop("w", "x")}, false},
		{"exists false", ConditionSet{cond("v", existsP(false))}, []Property{prop("w", "x")}, true},
		{"exists false with an empty property", ConditionSet{cond("v", existsP(false))}, []Property{prop("v")}, true},
		{"exists false fails if present", ConditionSet{cond("v", existsP(false))}, []Property{prop("v", "x")}, false},
		{"exists false on an empty event", ConditionSet{cond("v", existsP(false))}, nil, true},
		{"exists false with another condition", ConditionSet{cond("v", existsP(false)), cond("w", equalsP("x"))}, []Property{prop("w", "x")}, true},
		{"exists false with another condition fails", ConditionSet{cond("v", existsP(false)), cond("w", equalsP("x"))}, []Property{prop("v", "1"), prop("w", "x")}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := New[int]()
			mustAdd(t, h, 1, tt.rule...)
			if got := len(h.Match(tt.event)) == 1; got != tt.want {
				t.Errorf("Match = %v, want %v", got, tt.want)
			}
			if got := refMatches(tt.rule, tt.event); got != tt.want {
				t.Errorf("reference = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNumericAndExistsJSONEvents(t *testing.T) {
	h := New[string]()
	mustAdd(t, h, "slow", cond("latency", gtP("500")))
	mustAdd(t, h, "unowned", cond("owner", existsP(false)))
	tests := []struct {
		event string
		want  []string
	}{
		{`{"latency": 501}`, []string{"slow", "unowned"}},
		{`{"latency": "501", "owner": "x"}`, []string{"slow"}},
		{`{"latency": [1, 600], "owner": null}`, []string{"slow", "unowned"}},
		{`{"owner": []}`, []string{"unowned"}},
		{`{"owner": {"name": "x"}}`, []string{"unowned"}},
		{`{"owner": false}`, nil},
	}
	for _, tt := range tests {
		got, err := h.MatchJSON([]byte(tt.event))
		if err != nil || !slices.Equal(got, tt.want) {
			t.Errorf("MatchJSON(%s) = %v, %v, want %v", tt.event, got, err, tt.want)
		}
	}
}

// TestNumericIndexManyBounds uses enough distinct bounds to merge the sorted
// lists of the numeric index several times.
func TestNumericIndexManyBounds(t *testing.T) {
	h := New[int]()
	ref := &refMatcher{}
	add := func(id int, p Pattern) {
		cs := ConditionSet{cond("v", p)}
		mustAdd(t, h, id, cs...)
		ref.add(id, cs)
	}
	types := []PatternType{PatternLessThan, PatternLessThanOrEqual, PatternGreaterThan, PatternGreaterThanOrEqual}
	for i := range 500 {
		v := strconv.Itoa(i)
		add(2*i, Pattern{Type: types[i%4], Value: v})
		add(2*i+1, betweenP(gteP(v), ltP(strconv.Itoa(i+7))))
	}
	for _, x := range []string{"-1", "0", "0.5", "3", "249", "250", "251", "499", "500", "1e9", "abc"} {
		event := []Property{prop("v", x)}
		if got, want := h.Match(event), ref.match(event); !slices.Equal(got, want) {
			t.Errorf("Match(%s) = %d matches, want %d", x, len(got), len(want))
		}
	}
}

func TestNumericAndExistsValidation(t *testing.T) {
	tests := []struct {
		name string
		rule ConditionSet
		msg  string
	}{
		{"not a number", ConditionSet{cond("v", gtP("abc"))}, "[gt] must contain a finite number"},
		{"no bound", ConditionSet{cond("v", ltP(""))}, "[lt] must contain a finite number"},
		{"infinite bound", ConditionSet{cond("v", gteP("1e400"))}, "[gte] must contain a finite number"},
		{"comparison with sub-patterns", ConditionSet{cond("v", Pattern{Type: PatternLessThanOrEqual, Value: "1", Sub: []Pattern{equalsP("a")}})}, "[lte] must not contain sub-patterns"},
		{"between with a value", ConditionSet{cond("v", Pattern{Type: PatternBetween, Value: "1"})}, "[between] must not contain a value"},
		{"between with one bound", ConditionSet{cond("v", Pattern{Type: PatternBetween, Sub: []Pattern{gtP("1")}})}, "must contain a lower (gt, gte) and an upper (lt, lte) bound"},
		{"between with two lower bounds", ConditionSet{cond("v", betweenP(gtP("1"), gteP("2")))}, "must contain a lower (gt, gte) and an upper (lt, lte) bound"},
		{"between with other patterns", ConditionSet{cond("v", betweenP(equalsP("1"), ltP("2")))}, "between[0]: must be lt, lte, gt or gte"},
		{"between with an invalid bound", ConditionSet{cond("v", betweenP(gtP("1"), ltP("x")))}, "between[1]: [lt] must contain a finite number"},
		{"empty between", ConditionSet{cond("v", betweenP(gtP("5"), ltP("5")))}, "[between] must not be empty"},
		{"reversed between", ConditionSet{cond("v", betweenP(gteP("6"), lteP("5")))}, "[between] must not be empty"},
		{"exists with another value", ConditionSet{cond("v", Pattern{Type: PatternExists, Value: "yes"})}, `[exists] must be "true" or "false"`},
		{"nested exists false", ConditionSet{cond("v", anyOfP(equalsP("a"), existsP(false)))}, "anyOf[1]: [exists] false must be a whole condition"},
		{"exists false with another condition", ConditionSet{cond("v", existsP(false)), cond("v", equalsP("a"))}, "[exists] false cannot be combined"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRule(tt.rule)
			if !errors.Is(err, ErrInvalidRule) || !strings.Contains(err.Error(), tt.msg) {
				t.Errorf("ValidateRule = %v, want ErrInvalidRule containing %q", err, tt.msg)
			}
		})
	}
}

func TestNumericAndExistsPatternJSON(t *testing.T) {
	var rule ConditionSet
	err := json.Unmarshal([]byte(`{
		"latency": {"gt": 500},
		"code": {"between": [{"gte": "200"}, {"lt": 300}]},
		"owner": {"exists": false},
		"team": {"exists": "true"}
	}`), &rule)
	if err != nil {
		t.Fatal(err)
	}
	want := ConditionSet{
		cond("code", betweenP(gteP("200"), ltP("300"))),
		cond("latency", gtP("500")),
		cond("owner", existsP(false)),
		cond("team", existsP(true)),
	}
	if !reflect.DeepEqual(rule, want) {
		t.Errorf("Unmarshal = %s, want %s", fmtRule(rule), fmtRule(want))
	}
	data, err := json.Marshal(rule)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"code":{"between":[{"gte":200},{"lt":300}]},"latency":{"gt":500},"owner":{"exists":false},"team":{"exists":true}}`; string(data) != want {
		t.Errorf("Marshal = %s, want %s", data, want)
	}
	for p, want := range map[*Pattern]string{
		{Type: PatternGreaterThan, Value: "abc"}: `{"gt":"abc"}`,
		{Type: PatternGreaterThan}:               `{"gt":""}`,
		{Type: PatternExists, Value: "yes"}:      `{"exists":"yes"}`,
	} {
		if data, err := json.Marshal(*p); err != nil || string(data) != want {
			t.Errorf("Marshal(%s) = %s, %v, want %s", fmtPattern(*p), data, err, want)
		}
	}
	for _, in := range []string{`{"gt": true}`, `{"exists": 1}`, `{"equals": 5}`, `{"between": 5}`} {
		var p Pattern
		if err := json.Unmarshal([]byte(in), &p); err == nil {
			t.Errorf("Unmarshal(%s) succeeded with %s", in, fmtPattern(p))
		}
	}
}
