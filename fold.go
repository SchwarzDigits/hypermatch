package hypermatch

import (
	"unicode"
	"unicode/utf8"
)

// Values are compared case-insensitively: pattern values and event values
// are both folded rune by rune with unicode.ToLower, exactly like
// strings.ToLower does for valid UTF-8. Unlike strings.ToLower, bytes that
// are not valid UTF-8 are kept unchanged instead of being replaced by
// U+FFFD, so distinct binary values never collide.

// appendFold appends the folded form of s to dst.
func appendFold(dst []byte, s string) []byte {
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			if 'A' <= c && c <= 'Z' {
				c += 'a' - 'A'
			}
			dst = append(dst, c)
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			dst = append(dst, c)
		} else {
			dst = utf8.AppendRune(dst, unicode.ToLower(r))
		}
		i += size
	}
	return dst
}

// fold returns the folded form of s.
func fold(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= utf8.RuneSelf || ('A' <= c && c <= 'Z') {
			return string(appendFold(make([]byte, 0, len(s)), s))
		}
	}
	return s
}
