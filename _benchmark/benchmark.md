# Comparison Benchmark

This suite compares hypermatch with [quamina](https://github.com/timbray/quamina), another Go library that matches events against rules, and with [AWS Event Ruler](https://github.com/aws/event-ruler), the Java library behind Amazon EventBridge. Run the Go candidates with `go run .` in this folder, and Event Ruler with `eventruler/run.sh`. The suite always benchmarks the hypermatch code of this repository.

Every candidate gets 100,000 rules like the following one. Ten rules share each `number`, so every event matches ten rules.

```javascript
{
    "env":    {"equals": "prod"},
    "number": {"equals": "4711"},
    "tags":   {"allOf": [{"equals": "tag1"}, {"equals": "tag2"}]},
    "region": {"anythingBut": [{"equals": "moon"}]},
    "type":   {"anyOf": [{"equals": "app"}, {"equals": "database"}]}
}
```

The suite then matches events on a single core for five seconds. The candidates are:

- **hypermatch**: rules and events are Go values.
- **hypermatch-matchjson**: the same rules, but the events are the same JSON documents the other libraries get, matched with `MatchJSON`.
- **hypermatch-json**: every rule and every event is decoded from JSON first, which shows the cost of JSON decoding.
- **quamina**: rules are quamina patterns, events are JSON.
- **event-ruler**: rules and events are JSON, matched with `rulesForJSONEvent`. It runs on the JVM with its default settings and matches events for five seconds before the measurement starts, so that the JIT has compiled everything.

Neither quamina nor Event Ruler has `allOf`, so in both of them the `tags` condition matches an event that contains either tag. Every measurement runs in its own process.

## Results

As of September 16th, 2026, on an Apple M4 Max with 36 GB RAM, Go 1.26.5, quamina 1.5.1, Event Ruler 2.2.0 and OpenJDK 21:

| Candidate | Adding 100,000 rules | Events per second | Memory per rule |
|---|---:|---:|---:|
| hypermatch | 0.13 s | 1,913,905 | 394 B |
| hypermatch-matchjson | 0.13 s | 1,473,252 | 394 B |
| hypermatch-json | 1.68 s | 281,180 | 407 B |
| event-ruler | 0.88 s | 281,148 | 1,805 B |
| quamina | 2.62 s | 32,612 | 13,590 B |

- `hypermatch-matchjson` gets exactly the same JSON documents as the other two libraries. It matches about 5 times as many events per second as Event Ruler and about 45 times as many as quamina.
- A rule takes about 394 bytes in hypermatch, about 4.6 times as much in Event Ruler and about 35 times as much in quamina.
- hypermatch adds rules about 7 times as fast as Event Ruler and about 20 times as fast as quamina.
- `MatchJSON` is 4 to 5 times as fast as decoding every event with `encoding/json` first, which is what hypermatch-json does.

## Wildcards

Wildcard patterns are a special case, and both other libraries warn about them. Adding `"name": {"wildcard": "*-myapp-*"}` to every rule gives:

| Candidate | Adding 100,000 rules | Events per second | Memory per rule |
|---|---:|---:|---:|
| hypermatch | 0.17 s | 1,467,174 | 394 B |
| hypermatch-matchjson | 0.16 s | 1,209,060 | 394 B |
| hypermatch-json | 1.85 s | 266,557 | 404 B |
| quamina | 3.07 s | 5 | 16,299 B |
| event-ruler | did not finish | — | — |

hypermatch loses about a quarter of its throughput. Quamina drops to 5 events per second, and Event Ruler did not finish building the 100,000 wildcard rules within 25 minutes: with 1,000 of them it took 4.6 seconds to build, matched 898 events per second and needed 13.5 KB per rule. Rule sets like this are where the libraries differ most, but they are not the common case.

## Reading the numbers

The absolute numbers belong to this machine; the ratios between the candidates are the point of this benchmark. The throughput includes building every event, which the Go candidates do with `fmt.Sprintf` and the Java candidate with string concatenation. Memory per rule is the live heap after adding the rules divided by their number, measured after a garbage collection. The [README](../README.md#performance) has in-package benchmarks of hypermatch alone, including parallel matching.
