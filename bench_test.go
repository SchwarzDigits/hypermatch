package hypermatch

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
)

// benchWorkload is a reproducible rule set together with an event generator.
// Every generated event matches exactly wantMatches rules.
type benchWorkload struct {
	name        string
	rule        func(i, n int) ConditionSet
	event       func(i, n int) []Property
	wantMatches int
}

var benchSizes = []int{1_000, 10_000, 100_000}

var benchSink atomic.Int64

func mixedRule(i, n int) ConditionSet {
	return ConditionSet{
		{Path: "name", Pattern: wildcardP("*-myapp-*")},
		{Path: "env", Pattern: equalsP("prod")},
		{Path: "number", Pattern: equalsP(strconv.Itoa(i % max(n/10, 1)))},
		{Path: "tags", Pattern: allOfP(equalsP("tag1"), equalsP("tag2"))},
		{Path: "region", Pattern: anythingButP(equalsP("moon"))},
		{Path: "type", Pattern: anyOfP(equalsP("app"), equalsP("database"))},
	}
}

func mixedEvent(i, n int, typ string) []Property {
	return []Property{
		{Path: "name", Values: []string{"app-myapp-" + strconv.Itoa(i)}},
		{Path: "env", Values: []string{"prod"}},
		{Path: "number", Values: []string{strconv.Itoa(i % max(n/10, 1))}},
		{Path: "tags", Values: []string{"tag1", "tag2"}},
		{Path: "region", Values: []string{"earth"}},
		{Path: "type", Values: []string{typ}},
	}
}

var benchWorkloads = []benchWorkload{
	{
		// Mirrors the _benchmark suite: every rule combines all pattern types
		// and ten rules share each "number".
		name:        "mixed",
		rule:        mixedRule,
		event:       func(i, n int) []Property { return mixedEvent(i, n, "app") },
		wantMatches: 10,
	},
	{
		// Same rules as "mixed", but the event fails on the last condition,
		// which forces the deepest possible traversal without a result.
		name:        "nearmiss",
		rule:        mixedRule,
		event:       func(i, n int) []Property { return mixedEvent(i, n, "cache") },
		wantMatches: 0,
	},
	{
		// Plain equality rules on realistic events with unrelated properties.
		name: "equals",
		rule: func(i, n int) ConditionSet {
			envs := [...]string{"prod", "stage", "dev", "test"}
			return ConditionSet{
				{Path: "service", Pattern: equalsP("svc-" + strconv.Itoa(i/4))},
				{Path: "env", Pattern: equalsP(envs[i%4])},
			}
		},
		event: func(i, n int) []Property {
			return []Property{
				{Path: "service", Values: []string{"svc-" + strconv.Itoa(i%(n/4))}},
				{Path: "env", Values: []string{"PROD"}},
				{Path: "host", Values: []string{"host-42"}},
				{Path: "message", Values: []string{"disk usage above threshold"}},
				{Path: "team", Values: []string{"platform"}},
				{Path: "severity", Values: []string{"critical"}},
			}
		},
		wantMatches: 1,
	},
	{
		// Every rule has its own wildcard pattern on the same path.
		name: "wildcard",
		rule: func(i, n int) ConditionSet {
			return ConditionSet{
				{Path: "name", Pattern: wildcardP("*-app" + strconv.Itoa(i) + "-*")},
				{Path: "env", Pattern: equalsP("prod")},
			}
		},
		event: func(i, n int) []Property {
			return []Property{
				{Path: "name", Values: []string{"eu-app" + strconv.Itoa(i%n) + "-db"}},
				{Path: "env", Values: []string{"prod"}},
				{Path: "host", Values: []string{"host-42"}},
			}
		},
		wantMatches: 1,
	},
	{
		// Every rule has its own prefix on the same path.
		name: "prefix",
		rule: func(i, n int) ConditionSet {
			return ConditionSet{
				{Path: "url", Pattern: prefixP("/api/v1/r" + strconv.Itoa(i) + "/")},
				{Path: "method", Pattern: anyOfP(equalsP("get"), equalsP("post"))},
			}
		},
		event: func(i, n int) []Property {
			return []Property{
				{Path: "url", Values: []string{"/api/v1/r" + strconv.Itoa(i%n) + "/items/42"}},
				{Path: "method", Values: []string{"GET"}},
			}
		},
		wantMatches: 1,
	},
	{
		// Exclusion rules: 100 rules per service, each excluding one region.
		name: "anythingbut",
		rule: func(i, n int) ConditionSet {
			return ConditionSet{
				{Path: "region", Pattern: anythingButP(equalsP("r" + strconv.Itoa(i%100)))},
				{Path: "service", Pattern: equalsP("svc-" + strconv.Itoa(i/100))},
			}
		},
		event: func(i, n int) []Property {
			return []Property{
				{Path: "region", Values: []string{"r" + strconv.Itoa(i%100)}},
				{Path: "service", Values: []string{"svc-" + strconv.Itoa((i%n)/100)}},
			}
		},
		wantMatches: 99,
	},
	{
		// Exclusions that all fail: every rule excludes the region of the
		// event as well as one of its own, so the time goes into evaluating
		// the 100 conditions rather than into the rules that match.
		name: "anythingbut-miss",
		rule: func(i, n int) ConditionSet {
			return ConditionSet{
				{Path: "region", Pattern: anythingButP(equalsP("moon"), equalsP("r"+strconv.Itoa(i%100)))},
				{Path: "service", Pattern: equalsP("svc-" + strconv.Itoa(i/100))},
			}
		},
		event: func(i, n int) []Property {
			return []Property{
				{Path: "region", Values: []string{"moon"}},
				{Path: "service", Values: []string{"svc-" + strconv.Itoa((i%n)/100)}},
			}
		},
		wantMatches: 0,
	},
	{
		// Numeric thresholds: ten rules per service with increasing bounds.
		name: "numeric",
		rule: func(i, n int) ConditionSet {
			return ConditionSet{
				{Path: "service", Pattern: equalsP("svc-" + strconv.Itoa(i/10))},
				{Path: "latency", Pattern: Pattern{Type: PatternGreaterThan, Value: strconv.Itoa(i % 10 * 100)}},
			}
		},
		event: func(i, n int) []Property {
			return []Property{
				{Path: "service", Values: []string{"svc-" + strconv.Itoa((i%n)/10)}},
				{Path: "latency", Values: []string{"550"}},
			}
		},
		wantMatches: 6,
	},
	{
		// Network blocks of 16 addresses, one per rule, as /28 or as /30
		// prefixes, so that every address is looked up with two lengths.
		name: "cidr",
		rule: func(i, n int) ConditionSet {
			return ConditionSet{{Path: "ip", Pattern: cidrP(fmt.Sprintf("%s/%d", benchAddr(i, 0), 28+i%2*2))}}
		},
		event: func(i, n int) []Property {
			return []Property{{Path: "ip", Values: []string{benchAddr(i, 1)}}}
		},
		wantMatches: 1,
	},
	{
		// Wildcard paths: ten rules per service, each looking for a value
		// among all labels.
		name: "labels",
		rule: func(i, n int) ConditionSet {
			return ConditionSet{
				{Path: "labels.*", Pattern: equalsP("team-" + strconv.Itoa(i%10))},
				{Path: "service", Pattern: equalsP("svc-" + strconv.Itoa(i/10))},
			}
		},
		event: func(i, n int) []Property {
			return []Property{
				{Path: "labels.env", Values: []string{"prod"}},
				{Path: "labels.team", Values: []string{"team-" + strconv.Itoa(i%10)}},
				{Path: "labels.region", Values: []string{"eu"}},
				{Path: "service", Values: []string{"svc-" + strconv.Itoa((i%n)/10)}},
				{Path: "host", Values: []string{"web-1"}},
			}
		},
		wantMatches: 1,
	},
}

// benchAddr returns the address off in the block of rule i.
func benchAddr(i, off int) string {
	a := 16*i + off
	return fmt.Sprintf("10.%d.%d.%d", a>>16&255, a>>8&255, a&255)
}

func newBenchMatcher(tb testing.TB, w benchWorkload, n int) *HyperMatch[int] {
	tb.Helper()
	h := New[int]()
	for i := range n {
		if err := h.AddRule(i, w.rule(i, n)); err != nil {
			tb.Fatal(err)
		}
	}
	return h
}

// benchEvents returns a pool of events spread over the whole rule range.
func benchEvents(w benchWorkload, n int) [][]Property {
	events := make([][]Property, 1024)
	for j := range events {
		events[j] = w.event((j*7919)%n, n)
	}
	return events
}

func TestBenchWorkloads(t *testing.T) {
	const n = 1_000
	for _, w := range benchWorkloads {
		t.Run(w.name, func(t *testing.T) {
			h := newBenchMatcher(t, w, n)
			for j, e := range benchEvents(w, n)[:64] {
				if got := len(h.Match(e)); got != w.wantMatches {
					t.Fatalf("event %d: got %d matches, want %d", j, got, w.wantMatches)
				}
			}
		})
	}
}

func BenchmarkMatch(b *testing.B) {
	for _, w := range benchWorkloads {
		for _, n := range benchSizes {
			b.Run(fmt.Sprintf("%s/rules=%d", w.name, n), func(b *testing.B) {
				h := newBenchMatcher(b, w, n)
				events := benchEvents(w, n)
				runtime.GC() // measure the steady state, not the garbage of the setup
				b.ReportAllocs()
				var matches, i int
				for b.Loop() {
					matches += len(h.Match(events[i%len(events)]))
					i++
				}
				benchSink.Add(int64(matches))
			})
		}
	}
}

func BenchmarkMatchParallel(b *testing.B) {
	const n = 100_000
	for _, w := range benchWorkloads {
		b.Run(fmt.Sprintf("%s/rules=%d", w.name, n), func(b *testing.B) {
			h := newBenchMatcher(b, w, n)
			runtime.GC()
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				events := benchEvents(w, n)
				var matches, i int
				for pb.Next() {
					matches += len(h.Match(events[i%len(events)]))
					i++
				}
				benchSink.Add(int64(matches))
			})
		})
	}
}

// BenchmarkMemory reports the heap retained per rule by a matcher with
// 100,000 rules.
func BenchmarkMemory(b *testing.B) {
	const n = 100_000
	for _, w := range benchWorkloads {
		if w.name == "nearmiss" {
			continue
		}
		b.Run(w.name, func(b *testing.B) {
			var perRule float64
			for b.Loop() {
				before := heapAlloc()
				h := newBenchMatcher(b, w, n)
				perRule = float64(int64(heapAlloc())-int64(before)) / n
				runtime.KeepAlive(h)
			}
			b.ReportMetric(perRule, "B/rule")
		})
	}
}

// heapAlloc returns the live heap after a full garbage collection.
func heapAlloc() uint64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// jsonEvent encodes an event of a benchmark workload as a JSON object.
func jsonEvent(event []Property) []byte {
	m := make(map[string]any, len(event))
	for _, p := range event {
		if len(p.Values) == 1 {
			m[p.Path] = p.Values[0]
		} else {
			m[p.Path] = p.Values
		}
	}
	data, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return data
}

func jsonBenchEvents(w benchWorkload, n int) [][]byte {
	var events [][]byte
	for _, e := range benchEvents(w, n) {
		events = append(events, jsonEvent(e))
	}
	return events
}

// BenchmarkMatchJSON matches the events of the workloads encoded as JSON.
func BenchmarkMatchJSON(b *testing.B) {
	const n = 100_000
	for _, w := range benchWorkloads {
		b.Run(fmt.Sprintf("%s/rules=%d", w.name, n), func(b *testing.B) {
			h := newBenchMatcher(b, w, n)
			events := jsonBenchEvents(w, n)
			if got, err := h.MatchJSON(events[0]); err != nil || len(got) != w.wantMatches {
				b.Fatalf("MatchJSON = %v, %v, want %d matches", got, err, w.wantMatches)
			}
			runtime.GC()
			b.ReportAllocs()
			var matches, i int
			for b.Loop() {
				got, _ := h.MatchJSON(events[i%len(events)])
				matches += len(got)
				i++
			}
			benchSink.Add(int64(matches))
		})
	}
}

// BenchmarkUnmarshalAndMatch decodes the JSON events of BenchmarkMatchJSON
// with encoding/json and matches the result, which MatchJSON replaces.
func BenchmarkUnmarshalAndMatch(b *testing.B) {
	const n = 100_000
	for _, w := range benchWorkloads {
		if w.name != "mixed" && w.name != "equals" {
			continue
		}
		b.Run(fmt.Sprintf("%s/rules=%d", w.name, n), func(b *testing.B) {
			h := newBenchMatcher(b, w, n)
			events := jsonBenchEvents(w, n)
			runtime.GC()
			b.ReportAllocs()
			var matches, i int
			for b.Loop() {
				var m map[string]any
				if err := json.Unmarshal(events[i%len(events)], &m); err != nil {
					b.Fatal(err)
				}
				props := make([]Property, 0, len(m))
				for k, v := range m {
					switch v := v.(type) {
					case string:
						props = append(props, Property{Path: k, Values: []string{v}})
					case []any:
						values := make([]string, 0, len(v))
						for _, x := range v {
							if s, ok := x.(string); ok {
								values = append(values, s)
							}
						}
						props = append(props, Property{Path: k, Values: values})
					}
				}
				matches += len(h.Match(props))
				i++
			}
			benchSink.Add(int64(matches))
		})
	}
}

// BenchmarkMatchFirst finds only the first matching rule of each event.
func BenchmarkMatchFirst(b *testing.B) {
	const n = 100_000
	for _, w := range benchWorkloads {
		if w.wantMatches == 0 {
			continue
		}
		b.Run(fmt.Sprintf("%s/rules=%d", w.name, n), func(b *testing.B) {
			h := newBenchMatcher(b, w, n)
			events := benchEvents(w, n)
			runtime.GC()
			b.ReportAllocs()
			var found, i int
			for b.Loop() {
				if _, ok := h.MatchFirst(events[i%len(events)]); ok {
					found++
				}
				i++
			}
			benchSink.Add(int64(found))
		})
	}
}

// BenchmarkMatchWithRemovedRules measures the mixed workload after removing
// every fifth of 100,000 rules, which is not enough to compact the matcher.
func BenchmarkMatchWithRemovedRules(b *testing.B) {
	const n = 100_000
	w := benchWorkloads[0]
	h := newBenchMatcher(b, w, n)
	for i := 0; i < n; i += 5 {
		h.RemoveRule(i)
	}
	events := benchEvents(w, n)
	runtime.GC()
	b.ReportAllocs()
	var matches, i int
	for b.Loop() {
		matches += len(h.Match(events[i%len(events)]))
		i++
	}
	benchSink.Add(int64(matches))
}

// BenchmarkRemoveRule measures removing all of 10,000 rules per op,
// including the compactions this triggers.
func BenchmarkRemoveRule(b *testing.B) {
	const n = 10_000
	rules := make([]ConditionSet, n)
	for i := range rules {
		rules[i] = mixedRule(i, n)
	}
	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		h := New[int]()
		for i, r := range rules {
			if err := h.AddRule(i, r); err != nil {
				b.Fatal(err)
			}
		}
		b.StartTimer()
		for i := range n {
			h.RemoveRule(i)
		}
	}
}

// BenchmarkAddRule measures building a matcher with 10,000 rules per op.
func BenchmarkAddRule(b *testing.B) {
	const n = 10_000
	for _, w := range benchWorkloads {
		if w.name == "nearmiss" {
			continue
		}
		b.Run(w.name, func(b *testing.B) {
			rules := make([]ConditionSet, n)
			for i := range rules {
				rules[i] = w.rule(i, n)
			}
			b.ReportAllocs()
			for b.Loop() {
				h := New[int]()
				for i, r := range rules {
					if err := h.AddRule(i, r); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
