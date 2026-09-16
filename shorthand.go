package hypermatch

import "strconv"

// The functions in this file build rules in Go with less typing:
//
//	hm.AddRule("page", hypermatch.ConditionSet{
//		hypermatch.Cond("env", hypermatch.Equals("prod")),
//		hypermatch.Cond("severity", hypermatch.AnyOf(hypermatch.Equals("critical"), hypermatch.Equals("warning"))),
//		hypermatch.Cond("latency_ms", hypermatch.GreaterThan(500)),
//	})
//
// They only fill in the structs. AddRule and ValidateRule check the result.

// Cond returns a condition that holds if the values at path match p.
func Cond(path string, p Pattern) Condition {
	return Condition{Path: path, Pattern: p}
}

// Or returns a condition that holds if any of the condition sets holds.
func Or(sets ...ConditionSet) Condition {
	return Condition{Or: sets}
}

// Equals returns a pattern that matches values equal to v, ignoring case.
func Equals(v string) Pattern {
	return Pattern{Type: PatternEquals, Value: v}
}

// Prefix returns a pattern that matches values that start with v, ignoring
// case.
func Prefix(v string) Pattern {
	return Pattern{Type: PatternPrefix, Value: v}
}

// Suffix returns a pattern that matches values that end with v, ignoring
// case.
func Suffix(v string) Pattern {
	return Pattern{Type: PatternSuffix, Value: v}
}

// Wildcard returns a pattern that matches values against v, in which *
// stands for any number of characters, ignoring case.
func Wildcard(v string) Pattern {
	return Pattern{Type: PatternWildcard, Value: v}
}

// AnyOf returns a pattern that matches if any of patterns matches.
func AnyOf(patterns ...Pattern) Pattern {
	return Pattern{Type: PatternAnyOf, Sub: patterns}
}

// AllOf returns a pattern that matches if all of patterns match.
func AllOf(patterns ...Pattern) Pattern {
	return Pattern{Type: PatternAllOf, Sub: patterns}
}

// AnythingBut returns a pattern that matches if none of the values matches
// any of patterns.
func AnythingBut(patterns ...Pattern) Pattern {
	return Pattern{Type: PatternAnythingBut, Sub: patterns}
}

// LessThan returns a pattern that matches numbers less than v.
func LessThan(v float64) Pattern {
	return number(PatternLessThan, v)
}

// LessThanOrEqual returns a pattern that matches numbers less than or equal
// to v.
func LessThanOrEqual(v float64) Pattern {
	return number(PatternLessThanOrEqual, v)
}

// GreaterThan returns a pattern that matches numbers greater than v.
func GreaterThan(v float64) Pattern {
	return number(PatternGreaterThan, v)
}

// GreaterThanOrEqual returns a pattern that matches numbers greater than or
// equal to v.
func GreaterThanOrEqual(v float64) Pattern {
	return number(PatternGreaterThanOrEqual, v)
}

// NumericEquals returns a pattern that matches numbers equal to v, in any
// notation.
func NumericEquals(v float64) Pattern {
	return number(PatternNumericEquals, v)
}

// Between returns a pattern that matches a number for which both lower and
// upper hold, for example Between(GreaterThanOrEqual(500), LessThan(600)).
func Between(lower, upper Pattern) Pattern {
	return Pattern{Type: PatternBetween, Sub: []Pattern{lower, upper}}
}

// Exists returns a pattern that matches if the property is present.
func Exists() Pattern {
	return Pattern{Type: PatternExists, Value: "true"}
}

// Absent returns a pattern that matches if the property is absent. It must be
// a whole condition.
func Absent() Pattern {
	return Pattern{Type: PatternExists, Value: "false"}
}

// CIDR returns a pattern that matches IP addresses inside prefix, such as
// 10.0.0.0/8 or 2001:db8::/32. A single address matches only itself.
func CIDR(prefix string) Pattern {
	return Pattern{Type: PatternCIDR, Value: prefix}
}

func number(t PatternType, v float64) Pattern {
	return Pattern{Type: t, Value: strconv.FormatFloat(v, 'g', -1, 64)}
}
