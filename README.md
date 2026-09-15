[![SIT](https://img.shields.io/badge/SIT-awesome-blueviolet.svg)](https://jobs.schwarz)
[![CI](https://github.com/SchwarzDigits/hypermatch/actions/workflows/go-test.yml/badge.svg)](https://github.com/SchwarzDigits/hypermatch/actions/workflows/go-test.yml)
[![Coverage Status](https://coveralls.io/repos/github/SchwarzDigits/hypermatch/badge.svg?branch=main)](https://coveralls.io/github/SchwarzDigits/hypermatch?branch=main)
[![Go Report Card](https://goreportcard.com/badge/github.com/SchwarzDigits/hypermatch)](https://goreportcard.com/report/github.com/SchwarzDigits/hypermatch)
[![Go Reference](https://pkg.go.dev/badge/github.com/SchwarzDigits/hypermatch/v2.svg)](https://pkg.go.dev/github.com/SchwarzDigits/hypermatch/v2)
![License](https://img.shields.io/github/license/SchwarzDigits/hypermatch)
![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/SchwarzDigits/hypermatch)
[![Mentioned in Awesome Go](https://awesome.re/mentioned-badge.svg)](https://github.com/avelino/awesome-go)  

![hypermatch logo](./logo/logo-small.png)

# What's new in v2 🚀

hypermatch v2 has a brand-new matching engine:

- ⚡ **20 to 30 times faster** on typical rule sets. An event is matched against 100,000 rules in about half a microsecond.
- 🔒 **Lock-free matching** on all cores, at 14 million events per second on 14 cores, even while rules are being added.
- 🪶 **Lean**: less than 450 bytes per rule, allocation-free matching with `AppendMatches`, no dependencies.
- ✅ **Precise**: every pattern type follows precisely specified semantics, checked continuously with differential tests and fuzzing.
- ✨ **Modern API**: generic rule identifiers, validation errors that point to the problem, and results in insertion order.
- 📄 **JSON in, matches out**: `MatchJSON` matches JSON events directly, 3 to 5 times as fast as decoding them first.
- 🔄 **Live rule updates**: `RemoveRule` removes rules at run time without ever blocking `Match`.
- 🏁 **Ahead of the field**: with 100,000 wildcard rules, hypermatch matches 1.5 million events per second. [quamina](https://github.com/timbray/quamina) matches 5. See the [comparison](#performance).

Upgrading from v1? See [Migrating from v1](#migrating-from-v1).

# Introduction
Hypermatch is a high-performance Go library that matches events against large sets of rules. Rules are compiled into a shared index, so the time it takes to match an event depends on the event and on the rules it matches, and hardly on how many rules there are.

- **Fast**: Matches an event against 100,000 rules in about half a microsecond on a single core, and 14 million events per second on 14 cores. [Benchmarks](#performance)
- **Concurrent**: `Match` is lock-free and scales with the number of cores, even while rules are being added or removed.
- **Correct**: The matching semantics are precisely specified and continuously verified against a reference implementation with differential and fuzz tests.
- **Readable Rule Format**: Write rules in Go or as human-readable JSON objects.
- **Flexible Rule Syntax**: Supports equals, prefix, suffix, wildcard, anything-but, all-of and any-of conditions, which can be nested freely.
- **No Dependencies**: Only the Go standard library.

An event consists of a list of fields, provided as name/value pairs. A rule links these event fields to patterns that determine whether the event matches.

![example](./example.png)

# Installation

```sh
go get github.com/SchwarzDigits/hypermatch/v2
```

Hypermatch requires Go 1.24 or later.

# Quick Start

```go
import (
    "log"

    "github.com/SchwarzDigits/hypermatch/v2"
)

func main() {
    // Rules are identified by values of any comparable type, here strings.
    hm := hypermatch.New[string]()

    // Add a rule
    if err := hm.AddRule("markus_rule", hypermatch.ConditionSet{
        {Path: "firstname", Pattern: hypermatch.Pattern{Type: hypermatch.PatternEquals, Value: "markus"}},
        {Path: "lastname", Pattern: hypermatch.Pattern{Type: hypermatch.PatternEquals, Value: "troßbach"}},
    }); err != nil {
        panic(err)
    }

    // Test with match
    matchedRules := hm.Match([]hypermatch.Property{
        {Path: "firstname", Values: []string{"markus"}},
        {Path: "lastname", Values: []string{"troßbach"}},
    })
    log.Printf("Following rules match: %v", matchedRules) // [markus_rule]

    // Test without match
    matchedRules = hm.Match([]hypermatch.Property{
        {Path: "firstname", Values: []string{"john"}},
        {Path: "lastname", Values: []string{"doe"}},
    })
    log.Printf("Following rules match: %v", matchedRules) // []
}
```

# Documentation
## Which Method to Use

| Your events are | Use | Why |
|---|---|---|
| JSON documents, for example from HTTP, Kafka or a message queue | `MatchJSON` | Fastest end to end: it decodes only the values your rules refer to, and you don't need `json.Unmarshal` |
| Go values you already have | `Match` | No encoding needed: build a `[]Property` from your data |

- **Hot loops**: Use `AppendMatchesJSON` or `AppendMatches` and reuse the result slice. Matching then does not allocate at all.
- **Changing rules**: Add and remove rules at any time with `AddRule` and `RemoveRule`, even while other goroutines are matching.

## Example Event

An event is represented as a JSON object with various fields. Here’s a sample event:

```javascript
{
        "name": "Too many parallel requests on system xy",
        "severity": "critical",
        "status": "firing",
        "message": "Lorem ipsum dolor sit amet, consetetur sadipscing elitr.",
        "team": "awesome-team",
        "application": "webshop",
        "component": "backend-service",
        "tags": [
            "shop",
            "backend"
        ]   
}
```

In Go, an event is a slice of `Property` values, each consisting of a path and its values:

```go
event := []hypermatch.Property{
    {Path: "name", Values: []string{"Too many parallel requests on system xy"}},
    {Path: "severity", Values: []string{"critical"}},
    {Path: "status", Values: []string{"firing"}},
    {Path: "tags", Values: []string{"shop", "backend"}},
    // ...
}
```

**This example will be referenced throughout the documentation.**

## Matching Basics

Rules in Hypermatch are composed of conditions defined by the `ConditionSet` type. An event matches a rule if it matches **all** of its conditions.

Each condition includes:

- **Path**: The field in the event to match against.
- **Pattern**: The pattern used to match the value at the specified path.

The following rules apply to all conditions:

- **Case-Insensitive Values**: All value comparisons are case-insensitive, including non-ASCII letters (`"ÄRGER"` equals `"ärger"`).
- **Case-Sensitive Paths**: `"Name"` and `"name"` are different paths, just like keys in JSON.
- **Supported Types**: Values are strings or string arrays.
- **Missing Properties**: A condition never matches a property that is absent from the event. This includes `anythingBut`. A property without values counts as absent.
- **Repeated Paths**: Several properties with the same path in an event act as one property with all their values. Several conditions on the same path in a rule must all match, just like `allOf`.

Here’s an example rule that matches the event above:

```go
ConditionSet{
    {
        Path: "status",
        Pattern: Pattern{Type: PatternEquals, Value: "firing"},
    },
    {
        Path: "name",
        Pattern: Pattern{Type: PatternAnythingBut, Sub: []Pattern{
                {Type: PatternWildcard, Value: "TEST*"},
            },
        },
    },
    {
        Path: "severity",
        Pattern: Pattern{ Type: PatternAnyOf,
            Sub: []Pattern{
                {Type: PatternEquals, Value: "critical"},
                {Type: PatternEquals, Value: "warning"},
            },
        },
    },
    {
        Path: "tags",
        Pattern: Pattern{ Type: PatternAllOf,
            Sub: []Pattern{
                {Type: PatternEquals, Value: "shop"},
                {Type: PatternEquals, Value: "backend"},
            },
        },
    },
}
```

The rules and conditions are also expressible as JSON objects. The following JSON is the equivalent of the above Go notation for a `ConditionSet`:

```javascript
{
    "status": {
        "equals": "firing"
    },
    "name": {
        "anythingBut": [
            {"wildcard": "TEST*"}
        ]
    },
    "severity": {
        "anyOf": [
            {"equals": "critical"},
            {"equals": "warning"}
        ]
    },
    "tags": {
        "allOf": [
            {"equals": "shop"},
            {"equals": "backend"}
        ]
    }
}
```

`ConditionSet`, `Condition` and `Pattern` implement `json.Marshaler` and `json.Unmarshaler`, so you can store rules as JSON and load them with `json.Unmarshal`. Unknown pattern types are rejected when decoding.

**Note**: For simplicity, all examples in this documentation will be presented in JSON format.

## Matching syntax
### "equals" matching
The `equals` condition checks if an attribute of the event matches a specified value, case-insensitively.

```javascript
{
    "status": {
        "equals": "firing"
    }
}
```

If the attribute value is type of:

- **String**: Checks if the value is equal to "firing"
- **String array**: Checks if the array contains an element equal to "firing"

### "prefix" matching
The `prefix` condition checks if an attribute starts with a specified prefix, case-insensitively.

```javascript
{
    "status": {
        "prefix": "fir"
    }
}
```

If the attribute value is type of:

- **String**: Checks if the value begins with "fir"
- **String array**: Checks if the array contains an element that begins with "fir"

### "suffix" matching
The `suffix` condition checks if an attribute ends with a specified suffix, case-insensitively.

```javascript
{
    "status": {
        "suffix": "ing"
    }
}
```

If the attribute value is type of:

- **String**: Checks if the value ends with "ing"
- **String array**: Checks if the array contains an element that ends with "ing"

In `equals`, `prefix` and `suffix` patterns, `*` is an ordinary character.

### "wildcard" matching
The `wildcard` condition uses wildcards to match the value of an attribute, ignoring case.

- Use `*` as a wildcard to match any number of characters (including none).
- You cannot place wildcards directly next to each other.
- The pattern `*` matches every value.

```javascript
{
    "name": {
        "wildcard": "*parallel requests*"
    }
}
```

If the attribute value is type of:

- **String**: Checks if the value matches the pattern \*parallel requests\*
- **String array**: Checks if any value in the array matches the pattern

### "anythingBut" matching
The `anythingBut` condition negates the match, triggering only if none of the specified patterns matches. It accepts any patterns, including prefixes, wildcards and nested `anyOf` or `allOf` patterns.

```javascript
{
    "status": {
        "anythingBut": [
            {"equals": "firing"},
            {"prefix": "pending"}
        ]
    }
}
```

If the attribute value is type of:

- **String**: Checks if the value is neither "firing" nor starts with "pending"
- **String array**: Checks if *no* element of the array is "firing" or starts with "pending"

Like every condition, `anythingBut` only matches events that contain the attribute.

### "anyOf" matching
`anyOf` does correspond to a boolean "inclusive-or". It checks multiple conditions and matches if **any** of the conditions are true.

```javascript
{
    "status": {
        "anyOf": [
            {"equals": "firing"},
            {"equals": "resolved"}
        ]
    }
}
```

If the attribute value is type of:

- **String**: Checks if the value is either "firing" or "resolved"
- **String array**: Checks if the array contains an element equal to "firing" or "resolved" or both.

### "allOf" matching
`allOf` does correspond to a boolean "and". It checks multiple conditions and matches if **all** the conditions are true.

```javascript
{
    "tags": {
        "allOf": [
            {"equals": "shop"},
            {"equals": "backend"}
        ]
    }
}
```

If the attribute value is type of:

- **String**: Checks if the value matches all conditions, for example `{"allOf": [{"prefix": "web"}, {"suffix": "shop"}]}`
- **String array**: Checks if the array contains both "shop" and "backend"

## Rule Identifiers

`HyperMatch[T]` identifies rules by values of any comparable type `T`, such as strings, integers or structs.

- `Match` returns the identifiers of all matching rules in the order in which they were first added, each at most once, or `nil` if no rule matches.
- Adding several condition sets under the same identifier combines them with a boolean "or": the identifier matches if any of its condition sets matches.
- `RemoveRule` removes all condition sets of an identifier at run time. Adding the identifier again later counts as adding a new rule.
- `RuleCount` returns the number of distinct identifiers.

## Validation

`AddRule` validates every rule and rejects invalid rules without changing the matcher. Use `ValidateRule` to check a rule without adding it, for example when rules are submitted through an API. All validation errors wrap `ErrInvalidRule` and describe the location of the problem:

```go
err := hypermatch.ValidateRule(rule)
if errors.Is(err, hypermatch.ErrInvalidRule) {
    // hypermatch: invalid rule: condition "name": anyOf[1]: [wildcard] must not contain two consecutive wildcards
}
```

## Concurrency

All methods of `HyperMatch` are safe for concurrent use:

- `Match` never blocks. It runs lock-free and scales with the number of cores, even while other goroutines add or remove rules.
- `AddRule` and `RemoveRule` calls are serialized. Their effect is visible to every `Match` call that starts after they returned.
- Once a quarter of the compiled rules have been removed, `RemoveRule` compacts them, which takes about as long as adding the remaining rules again. `Match` keeps running meanwhile.

The zero value of `HyperMatch` is ready to use.

## Allocation-free Matching

`Match` allocates only the slice it returns. `AppendMatches` appends to a slice you provide, so matching does not allocate at all when you reuse it:

```go
var matches []string
for _, event := range events {
    matches = hm.AppendMatches(matches[:0], event)
    // ...
}
```

## Matching JSON Events

`MatchJSON` matches an event given as a JSON object, without decoding it into Go values first. It decodes only the values of paths that rules refer to. That makes it 3 to 5 times as fast as `json.Unmarshal` followed by `Match`, with a single allocation instead of about 40:

```go
matches, err := hm.MatchJSON([]byte(`{
    "status": "firing",
    "alert": {"labels": {"team": "shop"}},
    "tags": ["shop", "backend"]
}`))
```

- **Nested objects**: Keys of nested objects are joined with `.`, so the value `shop` above is at the path `alert.labels.team`.
- **Arrays**: Every element of an array is a value of the same path, so `tags` has the values `shop` and `backend`. The objects in an array contribute to the same paths as well.
- **Numbers and literals**: Numbers match with their text as written in the JSON, so `500` matches `{"equals": "500"}`. Booleans match as `true` and `false`, and `null` counts as absent.
- **Errors**: Invalid JSON is rejected with an error wrapping `ErrInvalidEvent`.

`AppendMatchesJSON` appends to a slice you provide, like `AppendMatches`.

# Performance

hypermatch v2 matches an event against 100,000 rules in well under a microsecond, 20 to 30 times faster than v1 on typical rule sets. Every workload below uses 100,000 rules, see [bench_test.go](bench_test.go) for their definitions. The numbers are means of six runs of `go test -run '^$' -bench . -benchmem` on an Apple M4 Max with Go 1.26.

| Workload | Rules | Time per event | Events per second |
|---|---|---:|---:|
| mixed | 6 conditions using all pattern types; 10 rules match each event | 0.54 µs | 1.9 million |
| nearmiss | Same rules; the events fail only at the last condition | 0.45 µs | 2.2 million |
| equals | 2 `equals` conditions; events with 6 properties | 0.17 µs | 6.0 million |
| wildcard | A different `*-appN-*` wildcard per rule | 0.34 µs | 2.9 million |
| prefix | A different URL prefix per rule | 0.20 µs | 5.1 million |
| anythingbut | 100 exclusion rules per service; 99 match each event | 4.33 µs | 230,000 |

- **Parallel matching**: `Match` needs no locks. On 14 cores, the mixed workload reaches 14 million events per second.
- **Allocations**: `Match` allocates only the slice it returns, and `AppendMatches` does not allocate at all.
- **JSON events**: `MatchJSON` matches the events of the mixed workload, given as JSON, in 0.67 µs. That is 3.4 times as fast as `json.Unmarshal` followed by `Match` (2.27 µs).
- **Memory**: A rule takes 285 to 431 bytes.
- **Adding rules**: Adding 10,000 rules takes 4 to 10 ms.

The [comparison benchmark](_benchmark/benchmark.md) matches events against the same 100,000 rules with hypermatch and [quamina](https://github.com/timbray/quamina), on a single goroutine. With `MatchJSON`, hypermatch gets exactly the same JSON documents as quamina:

| Rules | hypermatch `Match` | hypermatch `MatchJSON` | quamina |
|---|---:|---:|---:|
| With a wildcard condition | 1,520,000 events/s | 1,230,000 events/s | 5 events/s |
| Without the wildcard condition | 1,940,000 events/s | 1,540,000 events/s | 33,200 events/s |

Things to consider to get maximum performance:
- Rules that share conditions are evaluated together. Conditions are ordered by path, so conditions on alphabetically early paths that many rules have in common, such as `"env": {"equals": "prod"}`, are checked only once per event.
- `equals`, `prefix`, `suffix` and wildcards of the form `abc*` or `*abc` are hash lookups. Other wildcards run through an automaton, which is still fast, but costs a little more.
- `anythingBut` conditions are checked for every event that contains their path. Many *different* `anythingBut` conditions at the same position therefore cost time proportional to their number.
- Reuse result slices with `AppendMatches`.

# Migrating from v1

- Import `github.com/SchwarzDigits/hypermatch/v2`. The package is still called `hypermatch`.
- Create matchers with `hypermatch.New[T]()` instead of `hypermatch.NewHyperMatch()`. `RuleIdentifier` no longer exists: choose the identifier type, for example `New[string]()`, or `New[any]()` for the old behavior.
- `Match` returns the identifiers in the order in which the rules were added, each at most once, and `nil` if nothing matches. It no longer reorders the event.
- `AddRule` validates rules and returns an error wrapping `ErrInvalidRule` for unknown pattern types, empty paths, `anythingBut` without sub-patterns and non-comparable identifiers. It no longer reorders the condition set.
- `anythingBut` on string arrays matches only if *no* element matches, as documented. Rules that relied on the v1 behavior may match fewer events.
- Matching is more precise: rules with wildcard, prefix or suffix patterns no longer influence each other.
- `ValidateRule` rejects empty condition sets, like `AddRule` always did.
- Decoding JSON rejects unknown pattern types and patterns or conditions with more than one key. Decoded condition sets are sorted by path, and encoding merges several conditions on the same path into an `allOf` pattern.
