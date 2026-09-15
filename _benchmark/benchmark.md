# Comparison Benchmark

This suite compares hypermatch with [quamina](https://github.com/timbray/quamina), another Go library that matches events against rules. Run it with `go run .` in this folder. It always benchmarks the hypermatch code of this repository.

Every candidate gets 100,000 rules like the following one. Ten rules share each `number`, so every event matches ten rules.

```javascript
{
    "name":   {"wildcard": "*-myapp-*"},
    "env":    {"equals": "prod"},
    "number": {"equals": "4711"},
    "tags":   {"allOf": [{"equals": "tag1"}, {"equals": "tag2"}]},
    "region": {"anythingBut": [{"equals": "moon"}]},
    "type":   {"anyOf": [{"equals": "app"}, {"equals": "database"}]}
}
```

The suite then matches events on a single goroutine for five seconds. The candidates are:

- **hypermatch**: rules and events are Go values.
- **hypermatch-matchjson**: the same rules, but the events are the same JSON documents quamina gets, matched with `MatchJSON`.
- **hypermatch-json**: every rule and every event is decoded from JSON first, which shows the cost of JSON decoding.
- **quamina**: rules are quamina patterns, events are JSON. Quamina has no `allOf`, so its `tags` condition matches if an event contains either tag.

Quamina slows down considerably with many shellstyle (wildcard) patterns, so the suite measures every candidate with and without the `name` condition. Every measurement runs in its own process.

Results as of September 15th, 2026, on an Apple M4 Max with 36 GB RAM, Go 1.26.5 and quamina 1.5.1:

| Rules | Candidate | Adding 100,000 rules | Events per second | Matches per event |
|---|---|---:|---:|---:|
| with wildcard | hypermatch | 0.15 s | 1,520,724 | 10 |
| with wildcard | hypermatch-matchjson | 0.15 s | 1,232,114 | 10 |
| with wildcard | hypermatch-json | 1.32 s | 284,245 | 10 |
| with wildcard | quamina | 3.03 s | 5 | 10 |
| without wildcard | hypermatch | 0.12 s | 1,942,410 | 10 |
| without wildcard | hypermatch-matchjson | 0.13 s | 1,535,831 | 10 |
| without wildcard | hypermatch-json | 1.19 s | 295,550 | 10 |
| without wildcard | quamina | 2.60 s | 33,221 | 10 |

- With a wildcard condition in every rule, hypermatch matches about 1.5 million events per second, or 1.2 million with `MatchJSON`. Quamina matches 5.
- Without the wildcard condition, hypermatch-matchjson gets exactly the same input as quamina and is about 46 times as fast.
- hypermatch adds rules about 20 times as fast as quamina.
- `MatchJSON` is 4 to 5 times as fast as decoding every event with `encoding/json` first, like hypermatch-json does.

The throughput includes building every event with `fmt.Sprintf`. The [README](../README.md#performance) has in-package benchmarks of hypermatch alone, including parallel matching.
