package hypermatch

import (
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
)

func TestStrMap(t *testing.T) {
	var m strMap[int]
	if _, ok := m.get("a"); ok {
		t.Error("empty map contains a")
	}
	if _, ok := m.getBytes([]byte("a")); ok {
		t.Error("empty map contains a")
	}
	for i := range 1000 {
		m.put(strconv.Itoa(i), i)
	}
	m.put("7", 700)
	for i := range 1000 {
		want := i
		if i == 7 {
			want = 700
		}
		key := strconv.Itoa(i)
		if v, ok := m.get(key); !ok || v != want {
			t.Errorf("get(%q) = %d, %v, want %d", key, v, ok, want)
		}
		if v, ok := m.getBytes([]byte(key)); !ok || v != want {
			t.Errorf("getBytes(%q) = %d, %v, want %d", key, v, ok, want)
		}
	}
	if _, ok := m.get("1000"); ok {
		t.Error("map contains 1000")
	}
}

func TestByteMap(t *testing.T) {
	var m byteMap[int]
	if _, ok := m.get('a'); ok {
		t.Error("empty map contains a")
	}
	for _, c := range []byte("hello, world") {
		m.put(c, int(c))
	}
	m.put('h', 1)
	for c := range 256 {
		v, ok := m.get(byte(c))
		switch {
		case c == 'h':
			if !ok || v != 1 {
				t.Errorf("get(h) = %d, %v, want 1", v, ok)
			}
		case slices.Contains([]byte("elo, wrd"), byte(c)):
			if !ok || v != c {
				t.Errorf("get(%q) = %d, %v, want %d", c, v, ok, c)
			}
		case ok:
			t.Errorf("map contains %q", c)
		}
	}
}

func TestList(t *testing.T) {
	var l list[int]
	if l.load() != nil {
		t.Error("empty list is not empty")
	}
	old := l.load()
	for i := range 100 {
		l.add(i)
		if i == 0 {
			old = l.load()
		}
	}
	if len(old) != 1 || old[0] != 0 {
		t.Errorf("snapshot changed to %v", old)
	}
	got := l.load()
	for i, v := range got {
		if v != i {
			t.Fatalf("load()[%d] = %d", i, v)
		}
	}
	if len(got) != 100 {
		t.Errorf("load returned %d elements, want 100", len(got))
	}
}

func TestLengths(t *testing.T) {
	var lens atomic.Pointer[[]int]
	if got := loadLengths(&lens); got != nil {
		t.Errorf("empty lengths: %v", got)
	}
	for _, n := range []int{5, 3, 9, 3, 1} {
		addLength(&lens, n)
	}
	if got := loadLengths(&lens); !slices.Equal(got, []int{1, 3, 5, 9}) {
		t.Errorf("lengths = %v, want [1 3 5 9]", got)
	}
}

func TestPatternTypeNames(t *testing.T) {
	for _, p := range PatternType(0).AllValues() {
		if got := PatternTypeFromString(p.String()); got != p {
			t.Errorf("PatternTypeFromString(%q) = %d, want %d", p, got, p)
		}
	}
	if got := PatternTypeFromString("bogus"); got != PatternUnknown {
		t.Errorf("PatternTypeFromString(bogus) = %d", got)
	}
	if s := PatternUnknown.String(); s != "" {
		t.Errorf("PatternUnknown.String() = %q", s)
	}
}
