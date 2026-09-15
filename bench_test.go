package hypermatch

import (
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
