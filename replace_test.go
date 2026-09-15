package hypermatch

import (
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
)

func TestReplaceRule(t *testing.T) {
	h := New[string]()
	for _, id := range []string{"a", "b", "c"} {
		mustAdd(t, h, id, cond("f", prefixP("x")))
	}
	event := []Property{prop("f", "xyz")}

	// b keeps its position.
	if err := h.ReplaceRule("b", ConditionSet{cond("f", suffixP("z"))}); err != nil {
		t.Fatal(err)
	}
	assertMatch(t, h, event, "a", "b", "c")
	assertMatch(t, h, []Property{prop("f", "abz")}, "b")
	if n := h.RuleCount(); n != 3 {
		t.Errorf("RuleCount = %d, want 3", n)
	}

	// Invalid replacements leave the old rule in place.
	if err := h.ReplaceRule("b", ConditionSet{cond("f", Pattern{Type: PatternEquals})}); !errors.Is(err, ErrInvalidRule) {
		t.Errorf("ReplaceRule(invalid) = %v, want ErrInvalidRule", err)
	}
	assertMatch(t, h, []Property{prop("f", "abz")}, "b")

	// Unknown identifiers are added at the end.
	if err := h.ReplaceRule("d", ConditionSet{cond("f", equalsP("xyz"))}); err != nil {
		t.Fatal(err)
	}
	assertMatch(t, h, event, "a", "b", "c", "d")

	// Replacing many times keeps the order across compactions.
	for i := range 200 {
		if err := h.ReplaceRule("b", ConditionSet{cond("f", prefixP("x")), cond("g", existsP(i%2 == 0))}); err != nil {
			t.Fatal(err)
		}
		if err := h.ReplaceRule("a", ConditionSet{cond("f", wildcardP("*y*"))}); err != nil {
			t.Fatal(err)
		}
	}
	assertMatch(t, h, event, "a", "b", "c", "d")
	if first, ok := h.MatchFirst(event); !ok || first != "a" {
		t.Errorf("MatchFirst = %v, %v, want a", first, ok)
	}
	if n := h.RuleCount(); n != 4 {
		t.Errorf("RuleCount = %d, want 4", n)
	}

	// Removing and adding again moves the identifier to the end.
	h.RemoveRule("a")
	if err := h.ReplaceRule("a", ConditionSet{cond("f", prefixP("x"))}); err != nil {
		t.Fatal(err)
	}
	assertMatch(t, h, event, "b", "c", "d", "a")
}

func TestMatchFirst(t *testing.T) {
	var empty HyperMatch[int]
	if id, ok := empty.MatchFirst([]Property{prop("f", "a")}); ok {
		t.Errorf("MatchFirst on an empty HyperMatch = %d, true", id)
	}

	h := New[int]()
	for i := range 100 {
		mustAdd(t, h, i, cond("n", gteP(strconv.Itoa(i))), cond("tag", anythingButP(equalsP("skip"+strconv.Itoa(i)))))
	}
	tests := []struct {
		event []Property
		want  int
		ok    bool
	}{
		{[]Property{prop("n", "50"), prop("tag", "x")}, 0, true},
		{[]Property{prop("n", "50"), prop("tag", "skip0")}, 1, true},
		{[]Property{prop("n", "-1"), prop("tag", "x")}, 0, false},
	}
	for _, tt := range tests {
		if id, ok := h.MatchFirst(tt.event); id != tt.want || ok != tt.ok {
			t.Errorf("MatchFirst(%s) = %d, %v, want %d, %v", fmtEvent(tt.event), id, ok, tt.want, tt.ok)
		}
	}
	h.RemoveRule(0)
	if id, ok := h.MatchFirst([]Property{prop("n", "50"), prop("tag", "x")}); id != 1 || !ok {
		t.Errorf("MatchFirst after removing 0 = %d, %v, want 1", id, ok)
	}
	// Rule 0 is removed, and the tag skip1 excludes rule 1.
	if id, ok, err := h.MatchFirstJSON([]byte(`{"n": 99, "tag": ["skip1", "y"]}`)); id != 2 || !ok || err != nil {
		t.Errorf("MatchFirstJSON = %d, %v, %v, want 2", id, ok, err)
	}
	if _, _, err := h.MatchFirstJSON([]byte(`{`)); !errors.Is(err, ErrInvalidEvent) {
		t.Errorf("MatchFirstJSON of invalid JSON = %v, want ErrInvalidEvent", err)
	}
}

// TestReplaceRuleIsAtomic replaces a rule over and over while readers match
// events that both the old and the new rule match. Every Match must report
// the rule exactly once.
func TestReplaceRuleIsAtomic(t *testing.T) {
	h := New[string]()
	for i := range 100 {
		mustAdd(t, h, "other-"+strconv.Itoa(i), cond("f", equalsP("y"+strconv.Itoa(i))))
	}
	variants := []ConditionSet{
		{cond("f", equalsP("x"))},
		{cond("f", prefixP("x"))},
		{cond("f", wildcardP("*x")), cond("g", existsP(false))},
		{cond("f", anyOfP(equalsP("x"), equalsP("z"))), cond("h", anythingButP(equalsP("q")))},
	}
	mustAdd(t, h, "x", variants[0]...)
	event := []Property{prop("f", "x"), prop("h", "r")}
	jsonEvent := []byte(`{"f": "x", "h": "r"}`)

	var readers sync.WaitGroup
	stop := make(chan struct{})
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if got := h.Match(event); !slices.Equal(got, []string{"x"}) {
					t.Errorf("Match = %v, want [x]", got)
					return
				}
				if got, ok := h.MatchFirst(event); !ok || got != "x" {
					t.Errorf("MatchFirst = %v, %v, want x", got, ok)
					return
				}
				if got, err := h.MatchJSON(jsonEvent); err != nil || !slices.Equal(got, []string{"x"}) {
					t.Errorf("MatchJSON = %v, %v, want [x]", got, err)
					return
				}
			}
		}()
	}
	for i := range 3000 {
		if err := h.ReplaceRule("x", variants[i%len(variants)]); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	readers.Wait()
	if n := h.RuleCount(); n != 101 {
		t.Errorf("RuleCount = %d, want 101", n)
	}
}
