package hypermatch

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFold(t *testing.T) {
	for _, s := range []string{"", "abc", "ABC xyz", "ÄÖÜ äöü ß ẞ", "İSTANBUL", "ΣΊΣΥΦΟΣ", "K", "Hello, 世界"} {
		if got, want := fold(s), strings.ToLower(s); got != want {
			t.Errorf("fold(%q) = %q, want %q", s, got, want)
		}
		if got, want := string(appendFold([]byte("x"), s)), "x"+fold(s); got != want {
			t.Errorf("appendFold(%q) = %q, want %q", s, got, want)
		}
	}
	if got, want := fold("A\xffB\xe2\x82"), "a\xffb\xe2\x82"; got != want {
		t.Errorf("fold kept invalid UTF-8 as %q, want %q", got, want)
	}
}

func FuzzFold(f *testing.F) {
	f.Add("Hello, WORLD")
	f.Add("ÄÖÜ\xffK")
	f.Fuzz(func(t *testing.T, s string) {
		folded := fold(s)
		if utf8.ValidString(s) && folded != strings.ToLower(s) {
			t.Errorf("fold(%q) = %q, strings.ToLower = %q", s, folded, strings.ToLower(s))
		}
		if got := string(appendFold(nil, s)); got != folded {
			t.Errorf("appendFold(%q) = %q, fold = %q", s, got, folded)
		}
	})
}
