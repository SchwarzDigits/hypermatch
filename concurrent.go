package hypermatch

import (
	"hash/maphash"
	"slices"
	"sync/atomic"
)

// The matching structures have a single writer at a time (AddRule holds
// HyperMatch.mu) and any number of concurrent readers (Match) that never
// lock. Every part of the structures that can change after it has been
// published is one of the types below. They publish changes with atomic
// stores, so readers always see a consistent, possibly slightly outdated,
// snapshot and never wait for the writer.

// list is an append-only slice.
type list[T any] struct {
	p atomic.Pointer[[]T]
}

// load returns the published elements. The result must not be modified.
func (l *list[T]) load() []T {
	if p := l.p.Load(); p != nil {
		return *p
	}
	return nil
}

// add appends v. Readers holding an older snapshot never access the slot
// written here, because it lies beyond the length they loaded.
func (l *list[T]) add(v T) {
	s := append(l.load(), v)
	l.p.Store(&s)
}

// replace publishes s, which must not be modified afterwards.
func (l *list[T]) replace(s []T) {
	l.p.Store(&s)
}

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
	t := m.t.Load()
	if t == nil {
		return v, false
	}
	h := maphash.String(hashSeed, key)
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

func (m *strMap[V]) getBytes(key []byte) (v V, ok bool) {
	t := m.t.Load()
	if t == nil {
		return v, false
	}
	h := maphash.Bytes(hashSeed, key)
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

// byteMap maps bytes to values. It is copied on write, which is cheap
// because it holds at most 256 entries and usually only a few.
type byteMap[V any] struct {
	p atomic.Pointer[byteTable[V]]
}

type byteTable[V any] struct {
	keys []byte // sorted
	vals []V
}

func (m *byteMap[V]) get(c byte) (v V, ok bool) {
	t := m.p.Load()
	if t == nil {
		return v, false
	}
	lo, hi := 0, len(t.keys)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if t.keys[mid] < c {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(t.keys) && t.keys[lo] == c {
		return t.vals[lo], true
	}
	return v, false
}

// put inserts or replaces the value for c. Writer only.
func (m *byteMap[V]) put(c byte, v V) {
	var keys []byte
	var vals []V
	if t := m.p.Load(); t != nil {
		keys, vals = t.keys, t.vals
	}
	i, found := slices.BinarySearch(keys, c)
	if found {
		vals = slices.Clone(vals)
		vals[i] = v
	} else {
		keys = slices.Insert(slices.Clone(keys), i, c)
		vals = slices.Insert(slices.Clone(vals), i, v)
	}
	m.p.Store(&byteTable[V]{keys: keys, vals: vals})
}
