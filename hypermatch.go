package hypermatch

import (
	"fmt"
	"math"
	"reflect"
	"slices"
	"sync"
)

// HyperMatch matches events against a set of rules. Rules are identified by
// values of type T.
//
// All methods are safe for concurrent use. Match never blocks: it runs
// lock-free and scales with the number of cores, even while rules are added.
// AddRule calls are serialized, and a rule is visible to every Match call
// that starts after its AddRule call returned.
//
// The zero value is an empty HyperMatch ready to use. A HyperMatch must not
// be copied after first use.
type HyperMatch[T comparable] struct {
	mu   sync.Mutex // serializes writers
	trie trie
	ids  list[T]      // identifiers by rule number
	nums map[T]uint32 // rule numbers by identifier; guarded by mu
}

// New returns an empty HyperMatch.
func New[T comparable]() *HyperMatch[T] {
	return new(HyperMatch[T])
}

// ValidateRule reports whether conditions form a valid rule. The returned
// error wraps ErrInvalidRule.
func ValidateRule(conditions ConditionSet) error {
	_, err := normalizeRule(conditions)
	return err
}

// AddRule adds a rule. An event matches the rule if it matches every
// condition of the set.
//
// Several condition sets may be added under the same id: the id then matches
// if any of them matches, and it is reported only once. Invalid rules are
// rejected with an error wrapping ErrInvalidRule (see ValidateRule) and leave
// the HyperMatch unchanged. AddRule neither modifies nor retains conditions.
func (h *HyperMatch[T]) AddRule(id T, conditions ConditionSet) error {
	if mayContainInterface(reflect.TypeFor[T]()) {
		if v := reflect.ValueOf(any(id)); v.IsValid() && !v.Comparable() {
			return fmt.Errorf("%w: identifier of type %T is not comparable", ErrInvalidRule, id)
		}
	}
	conds, err := normalizeRule(conditions)
	if err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	num, known := h.nums[id]
	if !known {
		n := len(h.ids.load())
		if n == math.MaxUint32 {
			return fmt.Errorf("%w: too many rules", ErrInvalidRule)
		}
		if h.nums == nil {
			h.nums = make(map[T]uint32)
		}
		num = uint32(n)
		h.ids.add(id) // published before any state refers to num
		h.nums[id] = num
	}
	if s := h.trie.insert(conds); !known || !s.hasRule(num) {
		s.addRule(num)
	}
	return nil
}

// mayContainInterface reports whether values of the comparable type t can
// hold interface values, whose dynamic type may not be comparable.
func mayContainInterface(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Interface:
		return true
	case reflect.Array:
		return mayContainInterface(t.Elem())
	case reflect.Struct:
		for i := range t.NumField() {
			if mayContainInterface(t.Field(i).Type) {
				return true
			}
		}
	}
	return false
}

// Match returns the identifiers of all rules the event matches, in the order
// in which the rules were first added, or nil if no rule matches. The event
// is not modified.
func (h *HyperMatch[T]) Match(event []Property) []T {
	return h.AppendMatches(nil, event)
}

// AppendMatches appends the identifiers Match would return to dst and
// returns the extended slice. Reusing dst makes matching allocation-free.
func (h *HyperMatch[T]) AppendMatches(dst []T, event []Property) []T {
	sc := scratchPool.Get().(*scratch)
	sc.reset(event)
	if len(sc.spans) > 0 {
		sc.visit(&h.trie.root)
	}
	if out := sc.out; len(out) > 0 {
		if len(out) > 1 {
			slices.Sort(out)
			out = slices.Compact(out)
		}
		// Loaded after the traversal, so every rule number found resolves.
		ids := h.ids.load()
		dst = slices.Grow(dst, len(out))
		for _, n := range out {
			dst = append(dst, ids[n])
		}
	}
	sc.release()
	return dst
}

// RuleCount returns the number of distinct rule identifiers.
func (h *HyperMatch[T]) RuleCount() int {
	return len(h.ids.load())
}
