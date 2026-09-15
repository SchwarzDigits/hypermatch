// Package hypermatch matches events against large sets of rules, fast.
//
// An event is a slice of [Property] values, each a path with one or more
// string values. A rule is a [ConditionSet]: an event matches the rule if
// it matches every [Condition], that is, if the values at the path of the
// condition match its [Pattern]. Values are compared case-insensitively,
// paths are not.
//
//   - equals, prefix, suffix and wildcard compare single values; a property
//     matches if any of its values does
//   - anyOf, allOf and anythingBut combine patterns on the same path;
//     anythingBut matches if none of the values matches any sub-pattern
//   - lt, lte, gt, gte and between compare values as numbers
//   - exists tests whether the property is present or absent
//
// Except for {"exists": false}, a condition never matches a property that
// is absent from the event.
//
// Rules are compiled into a trie of shared conditions backed by hash and
// automaton indexes, so the cost of matching an event depends on the event
// and on the rules it matches, and hardly on the total number of rules.
// [HyperMatch] is safe for concurrent use, and Match is lock-free. Rules can
// be added and removed at any time with [HyperMatch.AddRule] and
// [HyperMatch.RemoveRule].
//
// Events that arrive as JSON can be matched directly with
// [HyperMatch.MatchJSON], which decodes only the values rules refer to.
// Nested objects become paths joined with ".".
//
// Rules can also be written as JSON (see [ConditionSet.UnmarshalJSON]):
//
//	{
//	    "status":   {"equals": "firing"},
//	    "severity": {"anyOf": [{"equals": "critical"}, {"equals": "warning"}]},
//	    "name":     {"anythingBut": [{"wildcard": "TEST*"}]}
//	}
package hypermatch
