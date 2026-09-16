package hypermatch

import (
	"reflect"
	"testing"
)

func TestShorthands(t *testing.T) {
	tests := []struct {
		got, want any
	}{
		{Cond("a", Equals("x")), Condition{Path: "a", Pattern: Pattern{Type: PatternEquals, Value: "x"}}},
		{Or(ConditionSet{Cond("a", Prefix("x"))}), Condition{Or: []ConditionSet{{{Path: "a", Pattern: Pattern{Type: PatternPrefix, Value: "x"}}}}}},
		{Suffix("x"), Pattern{Type: PatternSuffix, Value: "x"}},
		{Wildcard("x*"), Pattern{Type: PatternWildcard, Value: "x*"}},
		{AnyOf(Equals("a"), Equals("b")), Pattern{Type: PatternAnyOf, Sub: []Pattern{equalsP("a"), equalsP("b")}}},
		{AllOf(Equals("a")), Pattern{Type: PatternAllOf, Sub: []Pattern{equalsP("a")}}},
		{AnythingBut(Equals("a")), Pattern{Type: PatternAnythingBut, Sub: []Pattern{equalsP("a")}}},
		{LessThan(500), ltP("500")},
		{LessThanOrEqual(0.1), lteP("0.1")},
		{GreaterThan(-2.5), gtP("-2.5")},
		{GreaterThanOrEqual(1e21), gteP("1e+21")},
		{NumericEquals(5e2), eqP("500")},
		{Between(GreaterThanOrEqual(500), LessThan(600)), betweenP(gteP("500"), ltP("600"))},
		{Exists(), existsP(true)},
		{Absent(), existsP(false)},
	}
	for i, tt := range tests {
		if !reflect.DeepEqual(tt.got, tt.want) {
			t.Errorf("case %d: got %+v, want %+v", i, tt.got, tt.want)
		}
	}
}

// TestShorthandRule checks that a rule built with the shorthands is valid and
// matches as documented.
func TestShorthandRule(t *testing.T) {
	h := New[string]()
	err := h.AddRule("page", ConditionSet{
		Cond("env", Equals("prod")),
		Cond("latency_ms", Between(GreaterThanOrEqual(500), LessThan(1000))),
		Cond("owner", Absent()),
		Or(
			ConditionSet{Cond("team", AnyOf(Equals("shop"), Prefix("pay")))},
			ConditionSet{Cond("tags", AllOf(Equals("urgent"), Wildcard("*-eu"))), Cond("region", AnythingBut(Equals("moon")))},
		),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertMatch(t, h, []Property{prop("env", "PROD"), prop("latency_ms", "750"), prop("team", "payments")}, "page")
	assertMatch(t, h, []Property{prop("env", "prod"), prop("latency_ms", "7.5e2"), prop("tags", "urgent", "shop-eu"), prop("region", "earth")}, "page")
	assertMatch(t, h, []Property{prop("env", "prod"), prop("latency_ms", "1000"), prop("team", "shop")})
	assertMatch(t, h, []Property{prop("env", "prod"), prop("latency_ms", "750"), prop("team", "shop"), prop("owner", "x")})
}
