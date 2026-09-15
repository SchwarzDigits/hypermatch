package hypermatch

import (
	"math/rand/v2"
	"slices"
	"sync"
	"testing"
)

// TestConcurrentAddRuleAndMatch runs writers and lock-free readers at the
// same time. Readers must never see a rule match that does not hold, and
// once the writers are done, every reader must see every rule.
func TestConcurrentAddRuleAndMatch(t *testing.T) {
	src := randSource{rand.New(rand.NewPCG(7, 7))}
	rules := make([]ConditionSet, 2000)
	for i := range rules {
		rules[i] = genRule(src)
	}
	events := make([][]Property, 500)
	for i := range events {
		events[i] = genEvent(src)
	}

	h := New[int]()
	var writers, readers sync.WaitGroup
	stop := make(chan struct{})
	for w := range 4 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for i := w; i < len(rules); i += 4 {
				if err := h.AddRule(i, rules[i]); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
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
	writers.Wait()
	close(stop)
	readers.Wait()

	for _, event := range events {
		var want []int
		for id, cs := range rules {
			if refMatches(cs, event) {
				want = append(want, id)
			}
		}
		got := h.Match(event)
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Fatalf("event %s: got %v, want %v", fmtEvent(event), got, want)
		}
	}
}
