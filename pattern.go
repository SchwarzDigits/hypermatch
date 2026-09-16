package hypermatch

import (
	"strings"
)

// PatternType is the kind of a Pattern.
type PatternType int

const (
	PatternEquals PatternType = iota
	PatternPrefix
	PatternSuffix
	PatternWildcard

	PatternAnythingBut
	PatternAnyOf
	PatternAllOf

	PatternUnknown

	// The following types follow PatternUnknown, so that the values of the
	// older types stay the same.

	PatternLessThan           // "lt": a value is a number less than Value
	PatternLessThanOrEqual    // "lte": a value is a number less than or equal to Value
	PatternGreaterThan        // "gt": a value is a number greater than Value
	PatternGreaterThanOrEqual // "gte": a value is a number greater than or equal to Value
	PatternBetween            // "between": a single value satisfies the lower and the upper bound in Sub
	PatternExists             // "exists": the property is present (Value "true") or absent (Value "false")
	PatternNumericEquals      // "eq": a value is a number equal to Value, in any notation
	PatternCIDR               // "cidr": a value is an IP address inside the prefix in Value
)

// AllValues returns all valid pattern types.
func (p PatternType) AllValues() []PatternType {
	return []PatternType{PatternEquals, PatternPrefix, PatternSuffix, PatternWildcard,
		PatternAnythingBut, PatternAnyOf, PatternAllOf,
		PatternLessThan, PatternLessThanOrEqual, PatternGreaterThan, PatternGreaterThanOrEqual,
		PatternBetween, PatternExists, PatternNumericEquals, PatternCIDR}
}

// HasLiteralValue reports whether patterns of type p use Value rather than
// Sub.
func (p PatternType) HasLiteralValue() bool {
	switch p {
	case PatternEquals, PatternPrefix, PatternSuffix, PatternWildcard,
		PatternLessThan, PatternLessThanOrEqual, PatternGreaterThan, PatternGreaterThanOrEqual,
		PatternExists, PatternNumericEquals, PatternCIDR:
		return true
	default:
		return false
	}
}

// isComparison reports whether p compares numbers with a single bound.
func (p PatternType) isComparison() bool {
	switch p {
	case PatternLessThan, PatternLessThanOrEqual, PatternGreaterThan, PatternGreaterThanOrEqual:
		return true
	default:
		return false
	}
}

// isNumeric reports whether p compares values with the number in Value.
func (p PatternType) isNumeric() bool {
	return p.isComparison() || p == PatternNumericEquals
}

func (p PatternType) String() string {
	switch p {
	case PatternEquals:
		return "equals"
	case PatternPrefix:
		return "prefix"
	case PatternSuffix:
		return "suffix"
	case PatternWildcard:
		return "wildcard"
	case PatternAnythingBut:
		return "anythingBut"
	case PatternAnyOf:
		return "anyOf"
	case PatternAllOf:
		return "allOf"
	case PatternLessThan:
		return "lt"
	case PatternLessThanOrEqual:
		return "lte"
	case PatternGreaterThan:
		return "gt"
	case PatternGreaterThanOrEqual:
		return "gte"
	case PatternBetween:
		return "between"
	case PatternExists:
		return "exists"
	case PatternNumericEquals:
		return "eq"
	case PatternCIDR:
		return "cidr"
	default:
		return ""
	}
}

// PatternTypeFromString returns the pattern type with the given name, ignoring
// case, or PatternUnknown.
func PatternTypeFromString(input string) PatternType {
	switch strings.ToLower(input) {
	case "equals":
		return PatternEquals
	case "prefix":
		return PatternPrefix
	case "suffix":
		return PatternSuffix
	case "wildcard":
		return PatternWildcard
	case "anythingbut":
		return PatternAnythingBut
	case "anyof":
		return PatternAnyOf
	case "allof":
		return PatternAllOf
	case "lt":
		return PatternLessThan
	case "lte":
		return PatternLessThanOrEqual
	case "gt":
		return PatternGreaterThan
	case "gte":
		return PatternGreaterThanOrEqual
	case "between":
		return PatternBetween
	case "exists":
		return PatternExists
	case "eq":
		return PatternNumericEquals
	case "cidr":
		return PatternCIDR
	}
	return PatternUnknown
}
