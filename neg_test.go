package hypermatch

import (
	"fmt"
	"slices"
	"sync"
	"testing"
)

// TestRefutedNegations mixes negations that the leaves refute with others
// that need their formula, across several words of the refuted set.
func TestRefutedNegations(t *testing.T) {
	h := New[int]()
	ref := &refMatcher{}
	add := func(id int, p Pattern) {
		cs := ConditionSet{cond("p", p)}
		mustAdd(t, h, id, cs...)
		ref.add(id, cs)
	}
	id := 0
	for i := range 150 {
		add(id, anythingButP(equalsP(fmt.Sprintf("v%d", i))))
		id++
		switch i % 15 {
		case 0:
			add(id, anythingButP(equalsP(fmt.Sprintf("v%d", i)), prefixP(fmt.Sprintf("w%d", i))))
		case 5:
			add(id, anythingButP(allOfP(prefixP("v"), suffixP(fmt.Sprint(i)))))
		case 10:
			add(id, allOfP(anythingButP(equalsP(fmt.Sprintf("v%d", i))), prefixP("v")))
		default:
			continue
		}
		id++
	}

	g, _ := h.tab.Load().root.groups.get("p")
	var refutable int
	for i, e := range g.neg.load() {
		if k := e.negIndex(); k != 0 {
			refutable++
			if k != uint64(i)+1 {
				t.Errorf("negation %d has the index %d", i, k)
			}
		}
	}
	if total := len(g.neg.load()); refutable != 160 || total != 180 {
		t.Errorf("%d of %d negations are refutable, want 160 of 180", refutable, total)
	}

	for _, values := range [][]string{
		{"v0"}, {"v3"}, {"v5"}, {"v10"}, {"v63"}, {"v64"}, {"v127"}, {"v128"}, {"v149"},
		{"v70", "v140"}, {"w0x"}, {"w15"}, {"x"}, {"v"}, {"V45", "w45"},
	} {
		event := []Property{prop("p", values...)}
		if got, want := h.Match(event), ref.match(event); !slices.Equal(got, want) {
			t.Errorf("Match(%v) = %v, want %v", values, got, want)
		}
		if got, ok := h.MatchFirst(event); !ok || got != ref.match(event)[0] {
			t.Errorf("MatchFirst(%v) = %v, %v", values, got, ok)
		}
	}
}

// TestConcurrentNegations adds negations on one path while events are
// matched. A rule excluding the value of an event must never match it, also
// while its position lies beyond the negations a Match call loaded.
func TestConcurrentNegations(t *testing.T) {
	const n = 3000
	h := New[int]()
	mustAdd(t, h, -1, cond("q", equalsP("x"))) // publishes the table
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for r := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := r; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				v := fmt.Sprintf("v%d", i%n)
				for _, id := range h.Match([]Property{prop("p", v, "w")}) {
					if id == i%n || id%7 == 0 {
						t.Errorf("rule %d matched %s", id, v)
						return
					}
				}
			}
		}()
	}
	for i := range n {
		p := anythingButP(equalsP(fmt.Sprintf("v%d", i)))
		if i%7 == 0 {
			p = anythingButP(equalsP(fmt.Sprintf("v%d", i)), equalsP("w"))
		}
		mustAdd(t, h, i, cond("p", p))
	}
	close(stop)
	wg.Wait()
	var want []int
	for i := range n {
		if i != 1 && i%7 != 0 {
			want = append(want, i)
		}
	}
	if got := h.Match([]Property{prop("p", "v1", "w")}); !slices.Equal(got, want) {
		t.Errorf("Match = %d rules, want %d", len(got), len(want))
	}
}

// TestNegationBeingAdded registers a negation at its leaf without publishing
// it, as AddRule does just before publishing it, when the published
// negations fill a whole word of the refuted set.
func TestNegationBeingAdded(t *testing.T) {
	h := New[int]()
	var want []int
	for i := range 64 {
		mustAdd(t, h, i, cond("p", anythingButP(equalsP(fmt.Sprintf("v%d", i)))))
		if i != 0 {
			want = append(want, i)
		}
	}
	tab := h.tab.Load()
	g, _ := tab.root.groups.get("p")
	l, _ := g.index.equals.get("v0")
	l.addEdge(&edge{id: tab.edgeSeq + 1 | 65<<negShift, next: new(state)})
	if got := h.Match([]Property{prop("p", "v0")}); !slices.Equal(got, want) {
		t.Errorf("Match = %v, want %v", got, want)
	}
}
