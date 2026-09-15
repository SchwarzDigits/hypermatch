package hypermatch

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

// diffAgainstReference adds random rules to a HyperMatch and to a
// refMatcher, interleaved with random events, and returns an error as soon as
// their results differ.
func diffAgainstReference(src source, ops int) error {
	h := New[int]()
	ref := &refMatcher{}
	for i := range ops {
		if src.intn(3) == 0 {
			event := genEvent(src)
			if got, want := h.Match(event), ref.match(event); !slices.Equal(got, want) {
				var rules strings.Builder
				for _, r := range ref.rules {
					fmt.Fprintf(&rules, "  %d: %s\n", r.id, fmtRule(r.cs))
				}
				return fmt.Errorf("event %s:\n got %v\nwant %v\nrules:\n%s", fmtEvent(event), got, want, rules.String())
			}
			continue
		}
		id := i
		if i > 0 && src.intn(8) == 0 {
			id = src.intn(i) // several condition sets for one identifier
		}
		cs := genRule(src)
		if err := h.AddRule(id, cs); err != nil {
			return fmt.Errorf("AddRule(%d, %s): %w", id, fmtRule(cs), err)
		}
		ref.add(id, cs)
	}
	return nil
}

func TestDifferential(t *testing.T) {
	seeds := 3000
	if testing.Short() {
		seeds = 300
	}
	for seed := range seeds {
		src := randSource{rand.New(rand.NewPCG(uint64(seed), 1))}
		if err := diffAgainstReference(src, 60); err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
	}
}

// TestDifferentialLargeGroups uses many rules, so that groups with many
// leaves and deep wildcard tries are exercised.
func TestDifferentialLargeGroups(t *testing.T) {
	seeds := 20
	if testing.Short() {
		seeds = 3
	}
	for seed := range seeds {
		src := randSource{rand.New(rand.NewPCG(uint64(seed), 2))}
		if err := diffAgainstReference(src, 1500); err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
	}
}

func FuzzDifferential(f *testing.F) {
	f.Add([]byte(nil))
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9})
	f.Add([]byte("hypermatch differential fuzzing seed"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if err := diffAgainstReference(&byteSource{data}, 40); err != nil {
			t.Fatal(err)
		}
	})
}
