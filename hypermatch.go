package hypermatch

import (
	"fmt"
	"math"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
)

// HyperMatch matches events against a set of rules. Rules are identified by
// values of type T.
//
// All methods are safe for concurrent use. Match never blocks: it runs
// lock-free and scales with the number of cores, even while rules are added
// or removed. AddRule and RemoveRule calls are serialized, and their effect
// is visible to every Match call that starts after they returned.
//
// The zero value is an empty HyperMatch ready to use. A HyperMatch must not
// be copied after first use.
type HyperMatch[T comparable] struct {
	mu      sync.Mutex // serializes writers
	tab     atomic.Pointer[table[T]]
	nums    map[T]uint32 // rule numbers of the present identifiers; guarded by mu
	removed int          // rule numbers of removed identifiers in tab; guarded by mu
	count   atomic.Int64 // number of present identifiers
}

// table holds the compiled rules. RemoveRule replaces it with a compacted
// copy once enough rules have been removed.
type table[T comparable] struct {
	trie
	ids     list[T] // identifiers by rule number
	removed bitset  // numbers of the removed rules
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
	if !isComparable(id) {
		return fmt.Errorf("%w: identifier of type %T is not comparable", ErrInvalidRule, id)
	}
	conds, err := normalizeRule(conditions)
	if err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	tab := h.tab.Load()
	if tab == nil {
		tab = new(table[T])
		h.tab.Store(tab)
	}
	num, known := h.nums[id]
	if !known {
		n := len(tab.ids.load())
		if n == math.MaxUint32 {
			return fmt.Errorf("%w: too many rules", ErrInvalidRule)
		}
		if h.nums == nil {
			h.nums = make(map[T]uint32)
		}
		num = uint32(n)
		tab.ids.add(id) // published before any state refers to num
		h.nums[id] = num
		h.count.Add(1)
	}
	if s := tab.insert(conds); !known || !s.hasRule(num) {
		s.addRule(num)
	}
	return nil
}

// RemoveRule removes all condition sets added under id and reports whether
// there were any. Match calls that start after RemoveRule returned no longer
// report id. Adding id again later counts as adding a new rule.
//
// Removed rules are dropped from the compiled rules once they make up a
// quarter of them: RemoveRule then rebuilds the compiled rules, which takes
// about as long as adding the remaining rules again. Match calls are not
// blocked by this.
func (h *HyperMatch[T]) RemoveRule(id T) bool {
	if !isComparable(id) {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	num, ok := h.nums[id]
	if !ok {
		return false
	}
	tab := h.tab.Load()
	tab.removed.set(num)
	delete(h.nums, id)
	h.count.Add(-1)
	h.removed++
	if h.removed*4 >= len(tab.ids.load()) {
		h.compact(tab)
	}
	return true
}

// compact replaces tab with a copy that contains only the present rules,
// renumbered in their original order.
func (h *HyperMatch[T]) compact(tab *table[T]) {
	fresh := new(table[T])
	ids := tab.ids.load()
	renum := make([]uint32, len(ids))
	for n, id := range ids {
		if tab.removed.has(uint32(n)) {
			renum[n] = noRule
			continue
		}
		renum[n] = uint32(len(fresh.ids.load()))
		fresh.ids.add(id)
		h.nums[id] = renum[n]
	}
	fresh.copyRules(&tab.trie, renum)
	h.removed = 0
	h.tab.Store(fresh)
}

// isComparable reports whether id can be used as a map key. Only types that
// can hold interface values need to be checked at run time.
func isComparable[T comparable](id T) bool {
	if !mayContainInterface(reflect.TypeFor[T]()) {
		return true
	}
	v := reflect.ValueOf(any(id))
	return !v.IsValid() || v.Comparable()
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
// in which the identifiers were added, or nil if no rule matches. The event
// is not modified.
func (h *HyperMatch[T]) Match(event []Property) []T {
	return h.AppendMatches(nil, event)
}

// AppendMatches appends the identifiers Match would return to dst and
// returns the extended slice. Reusing dst makes matching allocation-free.
func (h *HyperMatch[T]) AppendMatches(dst []T, event []Property) []T {
	tab := h.tab.Load()
	if tab == nil {
		return dst
	}
	sc := scratchPool.Get().(*scratch)
	sc.reset(event)
	if len(sc.spans) > 0 {
		sc.visit(&tab.root)
	}
	if out := sc.out; len(out) > 0 {
		if len(out) > 1 {
			slices.Sort(out)
			out = slices.Compact(out)
		}
		// Loaded after the traversal, so every rule number found resolves.
		ids := tab.ids.load()
		removed := tab.removed.load()
		dst = slices.Grow(dst, len(out))
		for _, n := range out {
			if !bitsHas(removed, n) {
				dst = append(dst, ids[n])
			}
		}
	}
	sc.release()
	return dst
}

// RuleCount returns the number of distinct rule identifiers.
func (h *HyperMatch[T]) RuleCount() int {
	return int(h.count.Load())
}
