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
- **hypermatch-json**: every rule and every event is decoded from JSON first, which shows the cost of JSON decoding.
- **quamina**: rules are quamina patterns, events are JSON. Quamina has no `allOf`, so its `tags` condition matches if an event contains either tag.

Quamina slows down considerably with many shellstyle (wildcard) patterns, so the suite measures every candidate with and without the `name` condition. Every measurement runs in its own process.

Results as of September 15th, 2026, on an Apple M4 Max with 36 GB RAM, Go 1.26.5 and quamina 1.5.1:

| Rules | Candidate | Adding 100,000 rules | Events per second | Matches per event |
|---|---|---:|---:|---:|
| with wildcard | hypermatch | 0.16 s | 1,485,332 | 10 |
| with wildcard | hypermatch-json | 1.34 s | 273,377 | 10 |
| with wildcard | quamina | 3.12 s | 4 | 10 |
| without wildcard | hypermatch | 0.13 s | 1,855,493 | 10 |
| without wildcard | hypermatch-json | 1.23 s | 285,437 | 10 |
| without wildcard | quamina | 2.63 s | 31,804 | 10 |

- With a wildcard condition in every rule, hypermatch matches about 1.5 million events per second, and quamina 4.
- Without the wildcard condition, hypermatch is about 58 times as fast as quamina.
- hypermatch adds rules about 20 times as fast as quamina.
- Decoding every rule and event from JSON costs more than matching: hypermatch-json processes about 280,000 events per second.

The throughput includes building every event with `fmt.Sprintf`. The [README](../README.md#performance) has in-package benchmarks of hypermatch alone, including parallel matching.
