package hypermatch

import (
	"cmp"
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
// lock-free and scales with the number of cores, even while rules are added,
// replaced or removed. AddRule, ReplaceRule and RemoveRule calls are
// serialized, and their effect is visible to every Match call that starts
// after they returned.
//
// The zero value is an empty HyperMatch ready to use. A HyperMatch must not
// be copied after first use.
type HyperMatch[T comparable] struct {
	mu      sync.Mutex // serializes writers
	tab     atomic.Pointer[table[T]]
	nums    map[T]uint32 // rule numbers of the present identifiers; guarded by mu
	removed int          // rule numbers in tab that were removed or replaced; guarded by mu
	count   atomic.Int64 // number of present identifiers
}

// table holds the compiled rules. RemoveRule and ReplaceRule replace it with
// a compacted copy once enough rules have been removed or replaced.
type table[T comparable] struct {
	trie
	ids     list[T]       // identifiers by rule number
	removed bitset        // numbers of the removed rules
	version atomic.Uint32 // number of rules ReplaceRule has replaced
	meta    ruleMeta      // visibility and order of replaced rules and their replacements
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
	tab := h.table()
	num, known := h.nums[id]
	if !known {
		if num, err = h.newNumber(tab, id); err != nil {
			return err
		}
	}
	if s := tab.insert(conds, metaOrder(tab.meta.load(), num)); !known || !s.hasRule(num) {
		s.addRule(num)
	}
	return nil
}

// ReplaceRule replaces all condition sets of id with conditions, or adds the
// rule if id is not present. The replacement is atomic: every Match call
// sees either the old or the new rule, and id keeps its position in the
// order of the results. Invalid rules are rejected like in AddRule and leave
// the old rule in place.
func (h *HyperMatch[T]) ReplaceRule(id T, conditions ConditionSet) error {
	if !isComparable(id) {
		return fmt.Errorf("%w: identifier of type %T is not comparable", ErrInvalidRule, id)
	}
	conds, err := normalizeRule(conditions)
	if err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	tab := h.table()
	old, known := h.nums[id]
	if !known {
		num, err := h.newNumber(tab, id)
		if err != nil {
			return err
		}
		tab.insert(conds, num).addRule(num)
		return nil
	}
	n := len(tab.ids.load())
	if n == math.MaxUint32 {
		return fmt.Errorf("%w: too many rules", ErrInvalidRule)
	}
	num := uint32(n)
	meta := tab.meta.load()
	from, _ := metaSpan(meta, old)
	order := metaOrder(meta, old)
	v := tab.version.Load() + 1

	// The new rule is visible from version v on and the old one until then.
	// Both are in place before v is published, so every Match call, which
	// loads the version first, sees exactly one of them.
	tab.meta.set(num, v, 0, order)
	tab.ids.add(id)
	tab.insert(conds, order).addRule(num)
	tab.meta.set(old, from, v, order)
	tab.version.Store(v)

	h.nums[id] = num
	h.removed++
	if h.removed*4 >= len(tab.ids.load()) {
		h.compact(tab)
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

// table returns the current table, creating it if necessary. Writer only.
func (h *HyperMatch[T]) table() *table[T] {
	tab := h.tab.Load()
	if tab == nil {
		tab = new(table[T])
		h.tab.Store(tab)
	}
	return tab
}

// newNumber registers id under a new rule number. Writer only.
func (h *HyperMatch[T]) newNumber(tab *table[T], id T) (uint32, error) {
	n := len(tab.ids.load())
	if n == math.MaxUint32 {
		return 0, fmt.Errorf("%w: too many rules", ErrInvalidRule)
	}
	if h.nums == nil {
		h.nums = make(map[T]uint32)
	}
	num := uint32(n)
	tab.ids.add(id) // published before any state refers to num
	h.nums[id] = num
	h.count.Add(1)
	return num, nil
}

// compact replaces tab with a copy that contains only the present rules,
// renumbered in the order of their keys, which keeps the order of results.
func (h *HyperMatch[T]) compact(tab *table[T]) {
	ids := tab.ids.load()
	meta := tab.meta.load()
	version := tab.version.Load()
	live := make([]uint32, 0, len(ids))
	for n := range ids {
		num := uint32(n)
		if tab.removed.has(num) {
			continue
		}
		if _, until := metaSpan(meta, num); until != 0 && until <= version {
			continue // replaced
		}
		live = append(live, num)
	}
	if meta != nil {
		slices.SortFunc(live, func(a, b uint32) int { return cmp.Compare(metaOrder(meta, a), metaOrder(meta, b)) })
	}

	fresh := new(table[T])
	renum := make([]uint32, len(ids))
	for i := range renum {
		renum[i] = noRule
	}
	for i, n := range live {
		renum[n] = uint32(i)
		fresh.ids.add(ids[n])
		h.nums[ids[n]] = uint32(i)
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
	ids := tab.begin(sc)
	sc.reset(event)
	sc.visit(&tab.root) // even without properties: conditions may require absent ones
	dst = tab.results(dst, sc, ids)
	sc.release()
	return dst
}

// MatchFirst returns the identifier Match would return first, the one that
// was added earliest among the matching rules, and reports whether any rule
// matches. It does not allocate and skips the parts of the rules that cannot
// contain an earlier rule. Adding rules in the order of their priority makes
// MatchFirst a router.
func (h *HyperMatch[T]) MatchFirst(event []Property) (id T, ok bool) {
	tab := h.tab.Load()
	if tab == nil {
		return id, false
	}
	sc := scratchPool.Get().(*scratch)
	ids := tab.begin(sc)
	sc.reset(event)
	sc.first, sc.bestKey = true, math.MaxUint32
	sc.visit(&tab.root)
	if sc.bestKey != math.MaxUint32 {
		id, ok = ids[sc.best], true
	}
	sc.release()
	return id, ok
}

// begin loads the rules visible to a Match call into sc and returns their
// identifiers by number. The version is loaded first, see ReplaceRule, and
// everything is loaded before the traversal.
func (tab *table[T]) begin(sc *scratch) []T {
	sc.version = tab.version.Load()
	ids := tab.ids.load()
	sc.limit = uint32(len(ids))
	sc.meta = tab.meta.load()
	sc.removed = tab.removed.load()
	sc.absent = tab.hasAbsent.Load()
	return ids
}

// results appends the identifiers of the visible rules sc matched to dst,
// in the order of their keys.
func (tab *table[T]) results(dst []T, sc *scratch, ids []T) []T {
	out := sc.out
	if len(out) == 0 {
		return dst
	}
	if len(out) > 1 {
		slices.Sort(out)
		out = slices.Compact(out)
	}
	dst = slices.Grow(dst, len(out))
	if sc.removed == nil && sc.meta == nil {
		for _, n := range out {
			if n < sc.limit {
				dst = append(dst, ids[n])
			}
		}
		return dst
	}

	// Rules were removed or replaced.
	k := 0
	for _, n := range out {
		if sc.visible(n) {
			out[k] = n
			k++
		}
	}
	out = out[:k]
	if sc.meta != nil && len(out) > 1 {
		slices.SortFunc(out, func(a, b uint32) int { return cmp.Compare(sc.key(a), sc.key(b)) })
	}
	for _, n := range out {
		dst = append(dst, ids[n])
	}
	return dst
}

// RuleCount returns the number of distinct rule identifiers.
func (h *HyperMatch[T]) RuleCount() int {
	return int(h.count.Load())
}
