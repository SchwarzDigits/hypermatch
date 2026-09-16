[![SIT](https://img.shields.io/badge/SIT-awesome-blueviolet.svg)](https://jobs.schwarz)
[![CI](https://github.com/SchwarzDigits/hypermatch/actions/workflows/go-test.yml/badge.svg)](https://github.com/SchwarzDigits/hypermatch/actions/workflows/go-test.yml)
[![Coverage Status](https://coveralls.io/repos/github/SchwarzDigits/hypermatch/badge.svg?branch=main)](https://coveralls.io/github/SchwarzDigits/hypermatch?branch=main)
[![Go Reference](https://pkg.go.dev/badge/github.com/SchwarzDigits/hypermatch/v2.svg)](https://pkg.go.dev/github.com/SchwarzDigits/hypermatch/v2)
![License](https://img.shields.io/github/license/SchwarzDigits/hypermatch)
![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/SchwarzDigits/hypermatch)
[![Mentioned in Awesome Go](https://awesome.re/mentioned-badge.svg)](https://github.com/avelino/awesome-go)  

![hypermatch logo](./logo/logo-small.png)

# What's new in v2 🚀

v2 is a new matching engine, and everything below came with it:

- ⚡ **Fast at any size**: rules are compiled into one shared index, so matching an event depends on the event and on the rules it matches, and hardly on how many rules there are. On typical rule sets v2 matches 20 to 30 times as fast as v1, and with prefix or `anythingBut` conditions far more than that.
- 🔒 **Lock-free matching**: `Match` never blocks and scales with the number of cores, even while rules are added, replaced or removed.
- 🪶 **Lean**: a rule takes a few hundred bytes, `Match` allocates only the slice it returns, and `AppendMatches` nothing at all. No dependencies.
- ✅ **Precise**: every pattern type follows precisely specified semantics, checked continuously against a reference implementation with differential tests, fuzzing and race tests.
- ✨ **Modern API**: generic rule identifiers, validation errors that point at the problem, and results in the order the rules were added.
- 📄 **JSON in, matches out**: `MatchJSON` matches JSON events directly and decodes only the values your rules refer to, several times as fast as decoding them first.
- 🔄 **Live rule updates**: `AddRule`, `ReplaceRule` and `RemoveRule` change the rules at run time. Replacing is atomic, and nothing ever blocks `Match`.
- 🔢 **Numbers and missing fields**: `lt`, `lte`, `gt`, `gte`, `eq` and `between` compare values as numbers, `exists` tests whether a field is there at all, and thousands of ranges on one field are found by binary search.
- 🧩 **Real logic**: `anyOf`, `allOf` and `anythingBut` nest as deeply as you like, and `$or` combines whole conditions, including conditions on different fields.
- ✳️ **Wildcards**: `*` anywhere in a pattern, and `\*` for a literal asterisk.
- 🎯 **Routing**: `MatchFirst` returns only the first matching rule, the one added earliest, and skips everything that cannot come before it. Add the rules in the order they should win, and it routes.
- 🔍 **Explainable**: `Explain` shows condition by condition why a rule matches an event or not, as text or as JSON for a user interface.
- 🏁 **Ahead of the field**: on the same 100,000 rules and the same JSON events, hypermatch matches about 5 times as many events per second as [AWS Event Ruler](https://github.com/aws/event-ruler), the library behind Amazon EventBridge, and about 45 times as many as [quamina](https://github.com/timbray/quamina), with a fraction of the memory per rule. Rules with wildcards, which slow both of them down to a crawl, are where hypermatch pulls furthest ahead. See the [comparison](#performance).

Upgrading from v1? See [Migrating from v1](#migrating-from-v1).

# Introduction

hypermatch matches events against large sets of rules, in Go. Rules are compiled into one shared index, so the time it takes to match an event depends on the event and on the rules it matches, and hardly on how many rules there are.

- **Fast**: hundreds of thousands of rules are no problem. [Benchmarks](#performance)
- **Concurrent**: `Match` is lock-free and scales with the number of cores, even while rules are added, replaced or removed.
- **Correct**: the matching semantics are precisely specified and continuously verified against a reference implementation with differential and fuzz tests.
- **Readable rules**: write them in Go or as plain JSON objects.
- **Expressive rules**: equals, prefix, suffix, wildcard, numeric comparisons, ranges, `exists`, and `anyOf`, `allOf`, `anythingBut` and `$or` nested freely.
- **No dependencies**: only the Go standard library.

An event is a list of fields with their values. A rule links those fields to patterns that decide whether the event matches.

![example](./example.png)

# Installation

```sh
go get github.com/SchwarzDigits/hypermatch/v2
```

hypermatch requires Go 1.24 or later.

# Quick Start

```go
import (
    "fmt"

    "github.com/SchwarzDigits/hypermatch/v2"
)

func main() {
    // Rules are identified by values of any comparable type, here strings.
    hm := hypermatch.New[string]()

    // An event matches a rule if it matches all of its conditions.
    err := hm.AddRule("page-shop-team", hypermatch.ConditionSet{
        hypermatch.Cond("team", hypermatch.Equals("shop")),
        hypermatch.Cond("severity", hypermatch.AnyOf(hypermatch.Equals("critical"), hypermatch.Equals("warning"))),
        hypermatch.Cond("latency_ms", hypermatch.GreaterThan(500)),
    })
    if err != nil {
        panic(err)
    }

    // Match events given as Go values...
    fmt.Println(hm.Match([]hypermatch.Property{
        {Path: "team", Values: []string{"shop"}},
        {Path: "severity", Values: []string{"CRITICAL"}},
        {Path: "latency_ms", Values: []string{"750"}},
    })) // [page-shop-team]

    // ...or as JSON.
    fmt.Println(hm.MatchJSON([]byte(`{"team": "search", "severity": "critical", "latency_ms": 750}`))) // [] <nil>
}
```

`Cond`, `Equals`, `AnyOf` and their siblings only fill in the structs, so `hypermatch.Cond("team", hypermatch.Equals("shop"))` is the same as `hypermatch.Condition{Path: "team", Pattern: hypermatch.Pattern{Type: hypermatch.PatternEquals, Value: "shop"}}`. Rules can also be written as JSON, see [Matching Basics](#matching-basics).

# Use Cases

hypermatch fits wherever many rules have to be checked against a stream of events:

- **Alert routing**: Route alerts from Prometheus, Grafana or any monitoring system to teams, channels and on-call schedules. Each team owns rules like `{"team": {"equals": "shop"}, "severity": {"anyOf": [{"equals": "critical"}, {"equals": "warning"}]}}`, and `MatchFirst` picks the first route that matches, so adding the routes in the order they should win makes it a router.
- **Event-driven automation**: Trigger workflows, webhooks or functions for the events on a message bus such as Kafka, NATS or SQS, similar to the event patterns of AWS EventBridge. `MatchJSON` works directly on the raw messages.
- **Subscriptions and notifications**: Let users subscribe to events with their own filters, for example price alerts like `{"symbol": {"equals": "ACME"}, "price": {"lt": 100}}` or "tell me about new issues labeled bug". Hundreds of thousands of subscriptions are no problem.
- **Feature flags and targeting**: Decide from their properties which users get a feature, for example `{"country": {"anyOf": [{"equals": "de"}, {"equals": "at"}]}, "age": {"gte": 18}, "opt_out": {"exists": false}}`.
- **IoT and telemetry**: Detect sensor readings outside their normal range with `between`, `lt` and `gt`, per device type or site.
- **Security and audit logs**: Flag suspicious entries, such as access to sensitive paths or logins from unusual places, with prefix, suffix and wildcard patterns.
- **Content-based routing**: Route orders, tickets or documents to the queues or services responsible for their content.

The [runnable examples](https://pkg.go.dev/github.com/SchwarzDigits/hypermatch/v2#pkg-examples) show alert routing, subscriptions and feature targeting in code.

# Documentation
## Which Method to Use

| Your events are | Use | Why |
|---|---|---|
| JSON documents, for example from HTTP, Kafka or a message queue | `MatchJSON` | Fastest end to end: it decodes only the values your rules refer to, and you don't need `json.Unmarshal` |
| Go values you already have | `Match` | No encoding needed: build a `[]Property` from your data |

- **Hot loops**: Use `AppendMatchesJSON` or `AppendMatches` and reuse the result slice. Matching then does not allocate at all.
- **Only the best match**: If an event needs just one rule, for example to route it, use `MatchFirst` or `MatchFirstJSON` and add the rules in the order of their priority. They skip everything that cannot beat the best rule found so far.
- **Changing rules**: Add, replace and remove rules at any time with `AddRule`, `ReplaceRule` and `RemoveRule`, even while other goroutines are matching.
- **Debugging rules**: `Explain` shows condition by condition why a rule matches an event or not.

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

A rule is a `ConditionSet`, and an event matches it if it matches **all** of its conditions. Each condition has

- a **path**, the field of the event it looks at, and
- a **pattern**, which decides whether the values at that path match.

These rules hold for every condition:

- **Case-Insensitive Values**: All value comparisons are case-insensitive, including non-ASCII letters (`"ÄRGER"` equals `"ärger"`).
- **Case-Sensitive Paths**: `"Name"` and `"name"` are different paths, just like keys in JSON.
- **Supported Types**: Values are strings or string arrays.
- **Missing Properties**: A condition never matches a property that is absent from the event, except for `{"exists": false}`. This includes `anythingBut`. A property without values counts as absent.
- **Repeated Paths**: Several properties with the same path in an event act as one property with all their values. Several conditions on the same path in a rule must all match, just like `allOf`.

Here’s an example rule that matches the event above:

```go
hypermatch.ConditionSet{
    hypermatch.Cond("status", hypermatch.Equals("firing")),
    hypermatch.Cond("name", hypermatch.AnythingBut(hypermatch.Wildcard("TEST*"))),
    hypermatch.Cond("severity", hypermatch.AnyOf(hypermatch.Equals("critical"), hypermatch.Equals("warning"))),
    hypermatch.Cond("tags", hypermatch.AllOf(hypermatch.Equals("shop"), hypermatch.Equals("backend"))),
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
The `equals` condition checks if a property of the event matches a specified value, case-insensitively.

```javascript
{
    "status": {
        "equals": "firing"
    }
}
```

How it matches:

- **String**: Checks if the value is equal to "firing"
- **String array**: Checks if the array contains an element equal to "firing"

### "prefix" matching
The `prefix` condition checks if a property starts with a specified prefix, case-insensitively.

```javascript
{
    "status": {
        "prefix": "fir"
    }
}
```

How it matches:

- **String**: Checks if the value begins with "fir"
- **String array**: Checks if the array contains an element that begins with "fir"

### "suffix" matching
The `suffix` condition checks if a property ends with a specified suffix, case-insensitively.

```javascript
{
    "status": {
        "suffix": "ing"
    }
}
```

How it matches:

- **String**: Checks if the value ends with "ing"
- **String array**: Checks if the array contains an element that ends with "ing"

In `equals`, `prefix` and `suffix` patterns, `*` and `\` are ordinary characters.

### "wildcard" matching
The `wildcard` condition uses wildcards to match the value of a property, ignoring case.

- Use `*` as a wildcard to match any number of characters (including none).
- You cannot place wildcards directly next to each other.
- The pattern `*` matches every value.
- Use `\*` to match a literal `*` and `\\` to match a literal `\`. No other character may follow a backslash. In JSON, every backslash is itself written as `\\`, so `{"wildcard": "*\\**"}` is the pattern `*\**`, which matches values that contain a `*`.

```javascript
{
    "name": {
        "wildcard": "*parallel requests*"
    }
}
```

How it matches:

- **String**: Checks if the value matches `*parallel requests*`
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

How it matches:

- **String**: Checks if the value is neither "firing" nor starts with "pending"
- **String array**: Checks if *no* element of the array is "firing" or starts with "pending"

Like every condition, `anythingBut` only matches events that contain the property.

### "anyOf" matching
`anyOf` is a boolean "or": the condition matches if **any** of its patterns matches.

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

How it matches:

- **String**: Checks if the value is either "firing" or "resolved"
- **String array**: Checks if the array contains an element equal to "firing" or "resolved" or both.

### "allOf" matching
`allOf` is a boolean "and": the condition matches only if **all** of its patterns match.

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

How it matches:

- **String**: Checks if the value matches all conditions, for example `{"allOf": [{"prefix": "web"}, {"suffix": "shop"}]}`
- **String array**: Checks if the array contains both "shop" and "backend"

### Numeric matching: "lt", "lte", "gt", "gte" and "eq"
Numeric conditions compare a value as a number: `lt` (less than), `lte` (less than or equal), `gt` (greater than), `gte` (greater than or equal) and `eq` (equal).

```javascript
{
    "latency_ms": {
        "gt": 500
    }
}
```

How it matches:

- **String**: Checks if the value is a number greater than 500
- **String array**: Checks if the array contains a number greater than 500

Values are compared as decimal numbers such as `42`, `-1.5`, `.5` or `1e3`, so `"1e3"` and `"1000"` are equal. Values that are not numbers never match a numeric condition. You can write bounds as JSON numbers or as strings.

`eq` is the numeric counterpart of `equals`: `{"eq": 500}` matches `500`, `500.0` and `5e2`, while `{"equals": "500"}` compares text and matches only `500`.

### "between" matching
`between` checks if a value lies between a lower and an upper bound. Each bound is a numeric condition, which decides whether the bound itself is included.

```javascript
{
    "status_code": {
        "between": [{"gte": 500}, {"lt": 600}]
    }
}
```

How it matches:

- **String**: Checks if the value is a number from 500 up to, but not including, 600
- **String array**: Checks if the array contains such a number. Unlike an `allOf` of two numeric conditions, both bounds must hold for the same element.

### "exists" matching
`exists` checks if a property is present or absent.

```javascript
{
    "owner": {
        "exists": false
    }
}
```

- `{"exists": true}` matches if the event contains the property with at least one value, like the wildcard `*`.
- `{"exists": false}` matches if the event does not contain the property, or only without values. With `MatchJSON`, `null` counts as absent, too. It must be the whole condition: it cannot be nested in other patterns or combined with other conditions on the same path.

### Alternatives with "$or"
An event matches a rule if **all** of its conditions match. `$or` adds alternatives: the rule also needs any one of the condition sets in it to match. Unlike `anyOf`, which compares the values of a single property, `$or` combines whole conditions, including conditions on different paths:

```javascript
{
    "env": {"equals": "prod"},
    "$or": [
        {"team": {"equals": "shop"}},
        {"severity": {"equals": "critical"}, "escalated": {"exists": true}}
    ]
}
```

This rule matches production events that either belong to the shop team or are escalated critical ones.

- A condition set contains at most one `$or`, and `$or` can be nested.
- In Go, `$or` is the `Or` field of a `Condition`, which holds the alternative condition sets.
- Rules with `$or` are expanded into their combinations when they are added, so matching them costs nothing extra. A rule that combines into more than 1,024 condition sets is rejected.
- A key `"$or"` whose value is an object is an ordinary condition on the path `$or`.

## Rule Identifiers

`HyperMatch[T]` identifies rules by values of any comparable type `T`, such as strings, integers or structs.

- `Match` returns the identifiers of all matching rules in the order in which they were first added, each at most once, or `nil` if no rule matches.
- Adding several condition sets under the same identifier combines them with a boolean "or": the identifier matches if any of its condition sets matches.
- `ReplaceRule` replaces all condition sets of an identifier atomically: every `Match` sees either the old or the new rule, never both or neither, and the identifier keeps its position in the results.
- `RemoveRule` removes all condition sets of an identifier at run time. Adding the identifier again later counts as adding a new rule.
- `MatchFirst` returns only the first identifier `Match` would return. It does not allocate and skips the parts of the rules that cannot contain an earlier rule.
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
- `AddRule`, `ReplaceRule` and `RemoveRule` calls are serialized. Their effect is visible to every `Match` call that starts after they returned.
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
- **Numbers and literals**: Numbers match with their text as written in the JSON, so `500` matches `{"equals": "500"}`, but not `{"equals": "500.0"}`. Numeric patterns such as `eq` and `gt` compare them as numbers. Booleans match as `true` and `false`, and `null` counts as absent.
- **Errors**: Invalid JSON is rejected with an error wrapping `ErrInvalidEvent`.

`AppendMatchesJSON` appends to a slice you provide, like `AppendMatches`.

## Explaining Matches

`Explain` reports condition by condition how a rule matches an event, which helps when a rule does not do what you expect. `ExplainJSON` does the same for JSON events.

```go
explanation, err := hypermatch.Explain(rule, event)
fmt.Print(explanation)
```

```
no match
  ✓ status: {"equals":"firing"} matched "FIRING"
  ✗ severity: {"anyOf":[{"equals":"critical"},{"equals":"warning"}]} (values ["info"])
      ✗ {"equals":"critical"}
      ✗ {"equals":"warning"}
  ✓ owner: {"exists":false} (absent)
```

`Explain` follows exactly the semantics of `Match`, but it is meant for debugging rather than speed.

For a user interface, the `Explanation` holds the same information in fields and encodes to JSON:

```javascript
{
  "matched": false,
  "conditions": [
    {"path": "status", "matched": true, "values": ["FIRING"],
     "result": {"pattern": {"equals": "firing"}, "matched": true, "values": ["FIRING"]}},
    {"path": "severity", "matched": false, "values": ["info"],
     "result": {"pattern": {"anyOf": [{"equals": "critical"}, {"equals": "warning"}]}, "matched": false,
                "sub": [{"pattern": {"equals": "critical"}, "matched": false},
                        {"pattern": {"equals": "warning"}, "matched": false}]}},
    {"path": "owner", "matched": true, "absent": true,
     "result": {"pattern": {"exists": false}, "matched": true}}
  ]
}
```

- **`matched`** tells whether a condition, pattern or sub-pattern holds.
- **`values`** on a condition lists the values of the property. On a pattern, it lists the values that matched it, or for `anythingBut` the values that excluded the event.
- **`absent`** marks properties the event does not contain.
- **`sub`** holds the results of the sub-patterns of `anyOf`, `allOf` and `anythingBut`.
- **`or`** holds one explanation per alternative of a `$or` condition, which has no `path` and no `result`.

# Performance

On typical rule sets, hypermatch v2 matches 20 to 30 times as fast as v1, and how long it takes hardly depends on the number of rules. Every workload below uses 100,000 rules, see [bench_test.go](bench_test.go) for their definitions. Absolute times belong to the machine they were measured on: these are medians of five runs of `go test -run '^$' -bench . -benchmem` on an Apple M4 Max with Go 1.26.

| Workload | Rules | Time per event | Events per second |
|---|---|---:|---:|
| mixed | 6 conditions of different pattern types; 10 rules match each event | 0.54 µs | 1.9 million |
| nearmiss | Same rules; the events fail only at the last condition | 0.46 µs | 2.2 million |
| equals | 2 `equals` conditions; events with 6 properties | 0.17 µs | 5.8 million |
| wildcard | A different `*-appN-*` wildcard per rule | 0.34 µs | 2.9 million |
| prefix | A different URL prefix per rule | 0.20 µs | 4.9 million |
| numeric | 10 latency thresholds per service; 6 rules match each event | 0.34 µs | 2.9 million |
| anythingbut | 100 exclusion rules per service; 99 match each event | 3.78 µs | 260,000 |

- **Parallel matching**: `Match` needs no locks. On 14 cores, the mixed workload reaches 14 million events per second.
- **Allocations**: `Match` allocates only the slice it returns, and `AppendMatches` does not allocate at all.
- **JSON events**: `MatchJSON` matches the events of the mixed workload, given as JSON, in 0.65 µs. That is 3.5 times as fast as `json.Unmarshal` followed by `Match` (2.27 µs).
- **Memory**: A rule takes 301 to 447 bytes.
- **Adding rules**: Adding 10,000 rules takes 4 to 10 ms.

The [comparison benchmark](_benchmark/benchmark.md) matches events against the same 100,000 rules with hypermatch, [quamina](https://github.com/timbray/quamina) and [AWS Event Ruler](https://github.com/aws/event-ruler), the library behind Amazon EventBridge, on a single core. The rules combine `equals`, `anythingBut` and `anyOf` conditions, and every event matches ten of them. With `MatchJSON`, hypermatch gets exactly the same JSON documents as the other two:

| Candidate | Events per second | Memory per rule |
|---|---:|---:|
| hypermatch `Match` | 1,913,905 | 394 B |
| hypermatch `MatchJSON` | 1,473,252 | 394 B |
| AWS Event Ruler | 281,148 | 1,805 B |
| quamina | 32,612 | 13,590 B |

hypermatch matches about 5 times as many events per second as Event Ruler and about 45 times as many as quamina, and a rule takes a fraction of the memory. Event Ruler runs on the JVM, which matches events for five seconds before the measurement so that the JIT has compiled everything.

Wildcard patterns are the special case where the distance is largest. With a wildcard condition in every rule, hypermatch keeps about three quarters of its throughput, 1,467,174 events per second, while quamina drops to 5 events per second and Event Ruler did not finish building 100,000 such rules within 25 minutes. Two things make the difference: identical conditions exist only once, so all rules share a single automaton, and hypermatch simulates that automaton instead of turning it into a deterministic one, whose states multiply when patterns are combined. Rules with a *different* wildcard each stay fast as well, as the wildcard workload in the table above shows.

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
