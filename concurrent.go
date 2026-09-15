package hypermatch

import (
	"hash/maphash"
	"math/bits"
	"slices"
	"sync/atomic"
)

// The matching structures have a single writer at a time (AddRule holds
// HyperMatch.mu) and any number of concurrent readers (Match) that never
// lock. Every part of the structures that can change after it has been
// published is one of the types below. They publish changes with atomic
// stores, so readers always see a consistent, possibly slightly outdated,
// snapshot and never wait for the writer.

// list is an append-only slice. Elements below the published length are
// never written again, and a grown backing array contains all published
// elements, so readers only need the length and the current array.
type list[T any] struct {
	data atomic.Pointer[[]T] // backing array, len(data) == cap(data)
	n    atomic.Int32        // published length
}

// load returns the published elements. The result must not be modified.
func (l *list[T]) load() []T {
	// The length must be loaded first: the array it was published with,
	// or any later one, holds at least that many elements.
	n := l.n.Load()
	if n == 0 {
		return nil
	}
	return (*l.data.Load())[:n]
}

// add appends v. Readers never access the slot written here, because it
// lies beyond the length they loaded.
func (l *list[T]) add(v T) {
	n := int(l.n.Load())
	var data []T
	if p := l.data.Load(); p != nil {
		data = *p
	}
	if n == len(data) {
		grown := make([]T, max(2*n, 1))
		copy(grown, data)
		l.data.Store(&grown)
		data = grown
	}
	data[n] = v
	l.n.Store(int32(n + 1))
}

// bitset is a set of numbers that only grows.
type bitset struct {
	p atomic.Pointer[[]uint64]
}

// load returns the words of the set, or nil if it is empty. Words must be
// read with bitsHas.
func (b *bitset) load() []uint64 {
	if p := b.p.Load(); p != nil {
		return *p
	}
	return nil
}

func (b *bitset) has(i uint32) bool {
	return bitsHas(b.load(), i)
}

// bitsHas reports whether the words returned by bitset.load contain i.
func bitsHas(words []uint64, i uint32) bool {
	w := int(i >> 6)
	return w < len(words) && atomic.LoadUint64(&words[w])&(1<<(i&63)) != 0
}

// set adds i. Writer only.
func (b *bitset) set(i uint32) {
	words := b.load()
	w := int(i >> 6)
	if w >= len(words) {
		grown := make([]uint64, max(2*len(words), w+1))
		for j := range words {
			grown[j] = atomic.LoadUint64(&words[j])
		}
		b.p.Store(&grown)
		words = grown
	}
	atomic.OrUint64(&words[w], 1<<(i&63))
}

// ruleMeta holds the visibility and the order key of the rule numbers that
// ReplaceRule created or retired, in two words per number: from<<32|until
// and 1+order. Such a rule is visible to Match calls that loaded a version
// v with from <= v and, unless until is 0, v < until. Numbers without an
// entry are always visible and ordered by number. Readers need no locks.
type ruleMeta struct {
	p atomic.Pointer[[]uint64]
}

// load returns the words of m, or nil if it has no entries. Words must be
// read with metaSpan and metaOrder.
func (m *ruleMeta) load() []uint64 {
	if p := m.p.Load(); p != nil {
		return *p
	}
	return nil
}

func metaSpan(words []uint64, n uint32) (from, until uint32) {
	i := 2 * int(n)
	if i >= len(words) {
		return 0, 0
	}
	w := atomic.LoadUint64(&words[i])
	return uint32(w >> 32), uint32(w)
}

func metaOrder(words []uint64, n uint32) uint32 {
	if i := 2*int(n) + 1; i < len(words) {
		if o := atomic.LoadUint64(&words[i]); o != 0 {
			return uint32(o - 1)
		}
	}
	return n
}

// set records the visibility and the order key of rule n. Writer only.
func (m *ruleMeta) set(n, from, until, order uint32) {
	words := m.load()
	if i := 2*int(n) + 1; i >= len(words) {
		grown := make([]uint64, max(2*len(words), i+1))
		for j := range words {
			grown[j] = atomic.LoadUint64(&words[j])
		}
		m.p.Store(&grown)
		words = grown
	}
	atomic.StoreUint64(&words[2*int(n)], uint64(from)<<32|uint64(until))
	atomic.StoreUint64(&words[2*int(n)+1], uint64(order)+1)
}

// hashSeed is shared by all hash tables, so a hash computed once can be
// used for lookups in several tables.
var hashSeed = maphash.MakeSeed()

// strMap is a hash map with string keys, using open addressing and linear
// probing. Lookups are lock-free and do not allocate.
type strMap[V any] struct {
	t atomic.Pointer[strTable[V]]
}

type strTable[V any] struct {
	slots []atomic.Pointer[strEntry[V]]
	mask  uint64
	used  int // writer only
}

type strEntry[V any] struct {
	hash uint64
	key  string
	val  V
}

func (m *strMap[V]) get(key string) (v V, ok bool) {
	if t := m.t.Load(); t != nil {
		return t.lookup(maphash.String(hashSeed, key), key)
	}
	return v, false
}

func (m *strMap[V]) getBytes(key []byte) (v V, ok bool) {
	if t := m.t.Load(); t != nil {
		return t.lookupBytes(maphash.Bytes(hashSeed, key), key)
	}
	return v, false
}

// lookup finds key, given its hash h.
func (t *strTable[V]) lookup(h uint64, key string) (v V, ok bool) {
	for i := h & t.mask; ; i = (i + 1) & t.mask {
		e := t.slots[i].Load()
		if e == nil {
			return v, false
		}
		if e.hash == h && e.key == key {
			return e.val, true
		}
	}
}

// lookupBytes finds key, given its hash h.
func (t *strTable[V]) lookupBytes(h uint64, key []byte) (v V, ok bool) {
	for i := h & t.mask; ; i = (i + 1) & t.mask {
		e := t.slots[i].Load()
		if e == nil {
			return v, false
		}
		if e.hash == h && e.key == string(key) {
			return e.val, true
		}
	}
}

// put inserts or replaces the value for key. Writer only.
func (m *strMap[V]) put(key string, val V) {
	t := m.t.Load()
	if t == nil || (t.used+1)*4 > len(t.slots)*3 {
		t = m.grow(t)
	}
	if t.insert(&strEntry[V]{hash: maphash.String(hashSeed, key), key: key, val: val}) {
		t.used++
	}
}

// insert stores e and reports whether it occupied a new slot.
func (t *strTable[V]) insert(e *strEntry[V]) bool {
	for i := e.hash & t.mask; ; i = (i + 1) & t.mask {
		cur := t.slots[i].Load()
		if cur == nil {
			t.slots[i].Store(e)
			return true
		}
		if cur.hash == e.hash && cur.key == e.key {
			t.slots[i].Store(e)
			return false
		}
	}
}

// grow publishes a table twice the size of old containing all its entries.
func (m *strMap[V]) grow(old *strTable[V]) *strTable[V] {
	n := 8
	if old != nil {
		n = len(old.slots) * 2
	}
	t := &strTable[V]{slots: make([]atomic.Pointer[strEntry[V]], n), mask: uint64(n - 1)}
	if old != nil {
		for i := range old.slots {
			if e := old.slots[i].Load(); e != nil {
				t.insert(e)
				t.used++
			}
		}
	}
	m.t.Store(t)
	return t
}

// byteMap maps bytes to values. A 256-bit set of the present keys makes
// lookups constant-time: the rank of a key in the set is its index in vals.
// The map is copied on write, which is cheap because it has at most 256
// entries and usually only a few.
type byteMap[V any] struct {
	p atomic.Pointer[byteTable[V]]
}

type byteTable[V any] struct {
	set  [4]uint64 // present keys
	base [4]uint8  // number of keys in the words of set before each word
	vals []V       // ordered by key
}

// rank returns the index of c in vals and whether c is present.
func (t *byteTable[V]) rank(c byte) (int, bool) {
	w, bit := c>>6, uint64(1)<<(c&63)
	return int(t.base[w]) + bits.OnesCount64(t.set[w]&(bit-1)), t.set[w]&bit != 0
}

func (m *byteMap[V]) get(c byte) (v V, ok bool) {
	t := m.p.Load()
	if t == nil {
		return v, false
	}
	i, ok := t.rank(c)
	if !ok {
		return v, false
	}
	return t.vals[i], true
}

// put inserts or replaces the value for c. Writer only.
func (m *byteMap[V]) put(c byte, v V) {
	t := m.p.Load()
	if t == nil {
		t = new(byteTable[V])
	}
	i, found := t.rank(c)
	n := &byteTable[V]{set: t.set}
	if found {
		n.vals = slices.Clone(t.vals)
		n.vals[i] = v
	} else {
		n.vals = slices.Insert(slices.Clone(t.vals), i, v)
		n.set[c>>6] |= 1 << (c & 63)
	}
	for w := 1; w < len(n.base); w++ {
		n.base[w] = n.base[w-1] + uint8(bits.OnesCount64(n.set[w-1]))
	}
	m.p.Store(n)
}
