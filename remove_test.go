package hypermatch

import (
	"math/rand/v2"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"testing"
)

func TestRemoveRule(t *testing.T) {
	h := New[string]()
	mustAdd(t, h, "a", cond("f", equalsP("x")))
	mustAdd(t, h, "b", cond("f", prefixP("x")))
	mustAdd(t, h, "c", cond("f", wildcardP("*x*")))
	event := []Property{prop("f", "x")}
	assertMatch(t, h, event, "a", "b", "c")

	if !h.RemoveRule("b") {
		t.Error("RemoveRule(b) = false")
	}
	if h.RemoveRule("b") {
		t.Error("second RemoveRule(b) = true")
	}
	if h.RemoveRule("unknown") {
		t.Error("RemoveRule(unknown) = true")
	}
	assertMatch(t, h, event, "a", "c")
	if n := h.RuleCount(); n != 2 {
		t.Errorf("RuleCount = %d, want 2", n)
	}

	// An identifier added again counts as a new rule.
	mustAdd(t, h, "b", cond("f", equalsP("x")))
	assertMatch(t, h, event, "a", "c", "b")
}

func TestRemoveRuleWithSeveralConditionSets(t *testing.T) {
	h := New[int]()
	mustAdd(t, h, 1, cond("f", equalsP("a")))
	mustAdd(t, h, 1, cond("g", equalsP("b")))
	mustAdd(t, h, 2, cond("f", equalsP("a")))
	h.RemoveRule(1)
	assertMatch(t, h, []Property{prop("f", "a"), prop("g", "b")}, 2)
}

func TestRemoveAllRules(t *testing.T) {
	h := New[int]()
	for i := range 100 {
		mustAdd(t, h, i, cond("f", equalsP(strconv.Itoa(i))), cond("g", anythingButP(wildcardP("*x*"))))
	}
	for i := range 100 {
		if !h.RemoveRule(i) {
			t.Fatalf("RemoveRule(%d) = false", i)
		}
	}
	if n := h.RuleCount(); n != 0 {
		t.Errorf("RuleCount = %d, want 0", n)
	}
	for i := range 100 {
		assertMatch(t, h, []Property{prop("f", strconv.Itoa(i)), prop("g", "y")})
	}
	if groups := h.tab.Load().root.glist.load(); len(groups) != 0 {
		t.Errorf("%d groups left after removing all rules", len(groups))
	}
}

func TestRemoveRuleEdgeCases(t *testing.T) {
	var h HyperMatch[any]
	if h.RemoveRule("x") {
		t.Error("RemoveRule on an empty HyperMatch = true")
	}
	if h.RemoveRule([]int{1}) {
		t.Error("RemoveRule(slice) = true")
	}
	mustAdd(t, &h, nil, cond("f", equalsP("a")))
	if !h.RemoveRule(nil) {
		t.Error("RemoveRule(nil) = false")
	}
	assertMatch(t, &h, []Property{prop("f", "a")})
}

// TestParseKey checks that parseKey inverts the keys of normalized patterns.
func TestParseKey(t *testing.T) {
	src := randSource{rand.New(rand.NewPCG(3, 3))}
	for range 20000 {
		p := genPattern(src, 0)
		e, err := normalizePattern(&p)
		if err != nil {
			t.Fatal(err)
		}
		if got := parseKey(e.key); !reflect.DeepEqual(got, e) {
			t.Fatalf("parseKey(%q) = %q, want %q", e.key, got.key, e.key)
		}
	}
}

func TestBitset(t *testing.T) {
	var b bitset
	if b.has(0) || b.has(1000) {
		t.Error("empty bitset has elements")
	}
	members := []uint32{0, 5, 63, 64, 1000}
	for _, i := range members {
		b.set(i)
	}
	for i := range uint32(1100) {
		if got, want := b.has(i), slices.Contains(members, i); got != want {
			t.Errorf("has(%d) = %v, want %v", i, got, want)
		}
	}
}

// TestConcurrentRemoveRule removes and adds rules while lock-free readers
// match events. Readers must never see a rule match that does not hold, and
// a removed rule must not be reported by a Match that starts after its
// removal returned.
func TestConcurrentRemoveRule(t *testing.T) {
	src := randSource{rand.New(rand.NewPCG(8, 8))}
	rules := make([]ConditionSet, 3000)
	for i := range rules {
		rules[i] = genRule(src)
	}
	events := make([][]Property, 300)
	for i := range events {
		events[i] = genEvent(src)
	}
	// probe[id] is an event matching rule id, if there is one.
	probe := make([][]Property, len(rules))
	for id, cs := range rules {
		for _, e := range events {
			if refMatches(cs, e) {
				probe[id] = e
				break
			}
		}
	}

	const initial = 2000
	h := New[int]()
	for id := range initial {
		mustAdd(t, h, id, rules[id]...)
	}

	var readers sync.WaitGroup
	stop := make(chan struct{})
	for r := range 8 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for i := r; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				event := events[i%len(events)]
				for _, id := range h.Match(event) {
					if !refMatches(rules[id], event) {
						t.Errorf("rule %s matched %s", fmtRule(rules[id]), fmtEvent(event))
						return
					}
				}
			}
		}()
	}

	var writers sync.WaitGroup
	writers.Add(2)
	go func() {
		defer writers.Done()
		for id := 0; id < initial; id += 2 {
			if !h.RemoveRule(id) {
				t.Errorf("RemoveRule(%d) = false", id)
				return
			}
			if probe[id] != nil && slices.Contains(h.Match(probe[id]), id) {
				t.Errorf("rule %d reported after its removal", id)
				return
			}
		}
	}()
	go func() {
		defer writers.Done()
		for id := initial; id < len(rules); id++ {
			if err := h.AddRule(id, rules[id]); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	writers.Wait()
	close(stop)
	readers.Wait()

	for _, event := range events {
		var want []int
		for id, cs := range rules {
			if (id >= initial || id%2 == 1) && refMatches(cs, event) {
				want = append(want, id)
			}
		}
		got := h.Match(event)
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Fatalf("event %s: got %v, want %v", fmtEvent(event), got, want)
		}
	}
	if n, want := h.RuleCount(), len(rules)-initial/2; n != want {
		t.Errorf("RuleCount = %d, want %d", n, want)
	}
}
