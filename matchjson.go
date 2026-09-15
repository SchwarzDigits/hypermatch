package hypermatch

import (
	"errors"
	"fmt"
	"hash/maphash"
	"math"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// ErrInvalidEvent is wrapped by the errors MatchJSON and AppendMatchesJSON
// return for events that are not a valid JSON object.
var ErrInvalidEvent = errors.New("hypermatch: invalid JSON event")

// maxJSONDepth limits the nesting of JSON events, like encoding/json does.
const maxJSONDepth = 10000

// MatchJSON is like Match, but takes the event as a JSON object.
//
// Every value in the object is a value of a property. The values of nested
// objects belong to the keys joined with ".", so {"alert": {"team": "db"}}
// has the value "db" at the path "alert.team". Every element of an array is a
// value of the same path. Strings match with their decoded content, numbers
// with their text as written in the JSON, and booleans as "true" and
// "false"; null counts as absent. Keys that lead to the same path, including
// repeated keys and keys containing ".", contribute to the same property.
//
// Only the values of paths that rules refer to are decoded; the rest of the
// event is only validated. MatchJSON returns an error wrapping
// ErrInvalidEvent if event is not a valid JSON object.
func (h *HyperMatch[T]) MatchJSON(event []byte) ([]T, error) {
	return h.AppendMatchesJSON(nil, event)
}

// AppendMatchesJSON appends the identifiers MatchJSON would return to dst
// and returns the extended slice. Reusing dst makes matching allocation-free.
func (h *HyperMatch[T]) AppendMatchesJSON(dst []T, event []byte) ([]T, error) {
	tab := h.tab.Load()
	t := &emptyTrie
	if tab != nil {
		t = &tab.trie
	}
	sc := scratchPool.Get().(*scratch)
	var ids []T
	if tab != nil {
		// Before parsing: the paths a replacement adds are known before its
		// version is published.
		ids = tab.begin(sc)
	}
	if err := sc.resetJSON(event, t); err != nil {
		sc.release()
		return dst, err
	}
	if tab != nil {
		sc.visit(&tab.root)
		dst = tab.results(dst, sc, ids)
	}
	sc.release()
	return dst, nil
}

// MatchFirstJSON is like MatchFirst, but takes the event as a JSON object,
// like MatchJSON.
func (h *HyperMatch[T]) MatchFirstJSON(event []byte) (id T, ok bool, err error) {
	tab := h.tab.Load()
	t := &emptyTrie
	if tab != nil {
		t = &tab.trie
	}
	sc := scratchPool.Get().(*scratch)
	var ids []T
	if tab != nil {
		ids = tab.begin(sc)
	}
	if err := sc.resetJSON(event, t); err != nil {
		sc.release()
		return id, false, err
	}
	if tab != nil {
		sc.first, sc.bestKey = true, math.MaxUint32
		sc.visit(&tab.root)
		if sc.bestKey != math.MaxUint32 {
			id, ok = ids[sc.best], true
		}
	}
	sc.release()
	return id, ok, nil
}

// emptyTrie stands in for the rules of an empty HyperMatch. It is never
// written.
var emptyTrie trie

// jval is a folded value of a JSON event, located at fbuf[lo:hi].
type jval struct {
	span   int
	lo, hi int
}

// resetJSON prepares sc for matching the JSON object data against the rules
// of t. It collects the folded values of the paths t refers to and groups
// them into spans, like reset does for properties.
func (sc *scratch) resetJSON(data []byte, t *trie) error {
	sc.first = false
	sc.props = sc.props[:0]
	sc.spans = sc.spans[:0]
	sc.initSlots(16)
	sc.fbuf = sc.fbuf[:0]
	sc.frefs = sc.frefs[:0]
	sc.out = sc.out[:0]
	sc.jvals = sc.jvals[:0]
	sc.pbuf = sc.pbuf[:0]

	p := jsonParser{data: data, sc: sc, t: t}
	p.ws()
	if p.pos >= len(data) || data[p.pos] != '{' {
		return p.fail("expected an object")
	}
	if err := p.object(0, 1, true, true); err != nil {
		return err
	}
	p.ws()
	if p.pos != len(data) {
		return p.fail("unexpected data after the object")
	}
	sc.groupJSONValues()
	return nil
}

// groupJSONValues moves the collected values into frefs, grouped by span.
func (sc *scratch) groupJSONValues() {
	for i := range sc.spans {
		sc.spans[i].fhi = 0
	}
	for _, v := range sc.jvals {
		sc.spans[v.span].fhi++
	}
	n := 0
	for i := range sc.spans {
		sp := &sc.spans[i]
		count := sp.fhi
		sp.flo, sp.fhi = n, n
		n += count
	}
	if cap(sc.frefs) < n {
		sc.frefs = make([]vref, n)
	}
	sc.frefs = sc.frefs[:n]
	for _, v := range sc.jvals {
		sp := &sc.spans[v.span]
		sc.frefs[sp.fhi] = vref{lo: uint32(v.lo), hi: uint32(v.hi)}
		sp.fhi++
	}
}

// jsonSpan returns the index of the span of path, whose hash is h, creating
// it if necessary.
func (sc *scratch) jsonSpan(path string, h uint64) int {
	if 2*(len(sc.spans)+1) > len(sc.slots) {
		sc.rehash(2 * len(sc.slots))
	}
	mask := uint64(len(sc.slots) - 1)
	for j := h & mask; ; j = (j + 1) & mask {
		s := sc.slots[j]
		if s == 0 {
			sc.slots[j] = int32(len(sc.spans) + 1)
			sc.spans = append(sc.spans, span{path: path, hash: h, first: -1, last: -1, folded: true})
			return len(sc.spans) - 1
		}
		if sp := &sc.spans[s-1]; sp.hash == h && sp.path == path {
			return int(s - 1)
		}
	}
}

// rehash resizes the span table to size slots.
func (sc *scratch) rehash(size int) {
	sc.initSlots(size)
	mask := uint64(size - 1)
	for i := range sc.spans {
		for j := sc.spans[i].hash & mask; ; j = (j + 1) & mask {
			if sc.slots[j] == 0 {
				sc.slots[j] = int32(i + 1)
				break
			}
		}
	}
}

// jsonParser validates a JSON event and records the values of the paths its
// trie refers to. The path of the current value is sc.pbuf[:path].
type jsonParser struct {
	data []byte
	pos  int
	sc   *scratch
	t    *trie
}

func (p *jsonParser) fail(msg string) error {
	return fmt.Errorf("%w: %s at offset %d", ErrInvalidEvent, msg, p.pos)
}

func (p *jsonParser) ws() {
	for p.pos < len(p.data) {
		switch p.data[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

// value parses the value at p.pos, whose path is sc.pbuf[:path]. depth is
// the number of enclosing objects and arrays. Values are only recorded if
// record is true.
func (p *jsonParser) value(path, depth int, record bool) error {
	p.ws()
	if p.pos >= len(p.data) {
		return p.fail("unexpected end")
	}
	switch c := p.data[p.pos]; {
	case c == '{':
		if depth >= maxJSONDepth {
			return p.fail("nesting too deep")
		}
		return p.object(path, depth+1, record && p.isPrefix(path), false)
	case c == '[':
		if depth >= maxJSONDepth {
			return p.fail("nesting too deep")
		}
		return p.array(path, depth+1, record)
	case c == '"':
		if record {
			if span := p.span(path); span >= 0 {
				return p.str(span)
			}
		}
		var err error
		p.sc.jtmp, err = p.decodeStr(p.sc.jtmp[:0], false)
		return err
	case c == 't':
		return p.literal("true", path, record)
	case c == 'f':
		return p.literal("false", path, record)
	case c == 'n':
		return p.literal("null", path, false) // null counts as absent
	case c == '-' || ('0' <= c && c <= '9'):
		return p.number(path, record)
	}
	return p.fail(fmt.Sprintf("unexpected character %q", p.data[p.pos]))
}

// object parses the object at p.pos. The keys of the root object are paths
// on their own, all other keys are appended to the path with ".".
func (p *jsonParser) object(path, depth int, record, root bool) error {
	p.pos++ // '{'
	p.ws()
	if p.pos < len(p.data) && p.data[p.pos] == '}' {
		p.pos++
		return nil
	}
	for {
		p.ws()
		if p.pos >= len(p.data) || p.data[p.pos] != '"' {
			return p.fail("expected a key")
		}
		child := path
		var err error
		if record {
			buf := p.sc.pbuf[:path]
			if !root {
				buf = append(buf, '.')
			}
			if buf, err = p.decodeStr(buf, false); err != nil {
				return err
			}
			p.sc.pbuf = buf
			child = len(buf)
		} else if p.sc.jtmp, err = p.decodeStr(p.sc.jtmp[:0], false); err != nil {
			return err
		}
		p.ws()
		if p.pos >= len(p.data) || p.data[p.pos] != ':' {
			return p.fail("expected ':'")
		}
		p.pos++
		if err := p.value(child, depth, record); err != nil {
			return err
		}
		p.ws()
		if p.pos >= len(p.data) {
			return p.fail("unexpected end")
		}
		switch p.data[p.pos] {
		case ',':
			p.pos++
		case '}':
			p.pos++
			return nil
		default:
			return p.fail("expected ',' or '}'")
		}
	}
}

// array parses the array at p.pos. Its elements share its path.
func (p *jsonParser) array(path, depth int, record bool) error {
	p.pos++ // '['
	p.ws()
	if p.pos < len(p.data) && p.data[p.pos] == ']' {
		p.pos++
		return nil
	}
	for {
		if err := p.value(path, depth, record); err != nil {
			return err
		}
		p.ws()
		if p.pos >= len(p.data) {
			return p.fail("unexpected end")
		}
		switch p.data[p.pos] {
		case ',':
			p.pos++
		case ']':
			p.pos++
			return nil
		default:
			return p.fail("expected ',' or ']'")
		}
	}
}

// isPrefix reports whether a rule refers to a path below sc.pbuf[:path].
func (p *jsonParser) isPrefix(path int) bool {
	_, ok := p.t.prefixes.getBytes(p.sc.pbuf[:path])
	return ok
}

// span returns the span of the path sc.pbuf[:path], or -1 if no rule
// refers to it.
func (p *jsonParser) span(path int) int {
	tbl := p.t.paths.t.Load()
	if tbl == nil {
		return -1
	}
	key := p.sc.pbuf[:path]
	h := maphash.Bytes(hashSeed, key)
	s, ok := tbl.lookupBytes(h, key)
	if !ok {
		return -1
	}
	return p.sc.jsonSpan(s, h)
}

// str records the folded content of the string at p.pos for span.
func (p *jsonParser) str(span int) error {
	lo := len(p.sc.fbuf)
	buf, err := p.decodeStr(p.sc.fbuf, true)
	if err != nil {
		return err
	}
	p.sc.fbuf = buf
	p.sc.jvals = append(p.sc.jvals, jval{span: span, lo: lo, hi: len(buf)})
	return nil
}

// literal parses lit at p.pos and records it, if record is true.
func (p *jsonParser) literal(lit string, path int, record bool) error {
	if len(p.data)-p.pos < len(lit) || string(p.data[p.pos:p.pos+len(lit)]) != lit {
		return p.fail("invalid literal")
	}
	start := p.pos
	p.pos += len(lit)
	if record {
		p.raw(path, start)
	}
	return nil
}

// number parses the number at p.pos and records its text, if record is
// true.
func (p *jsonParser) number(path int, record bool) error {
	d, start := p.data, p.pos
	if d[p.pos] == '-' {
		p.pos++
	}
	switch {
	case p.pos < len(d) && d[p.pos] == '0':
		p.pos++
	case p.pos < len(d) && '1' <= d[p.pos] && d[p.pos] <= '9':
		p.digits()
	default:
		return p.fail("invalid number")
	}
	if p.pos < len(d) && d[p.pos] == '.' {
		p.pos++
		if !p.digits() {
			return p.fail("invalid number")
		}
	}
	if p.pos < len(d) && (d[p.pos] == 'e' || d[p.pos] == 'E') {
		p.pos++
		if p.pos < len(d) && (d[p.pos] == '+' || d[p.pos] == '-') {
			p.pos++
		}
		if !p.digits() {
			return p.fail("invalid number")
		}
	}
	if record {
		p.raw(path, start)
	}
	return nil
}

// digits skips the digits at p.pos and reports whether there were any.
func (p *jsonParser) digits() bool {
	start := p.pos
	for p.pos < len(p.data) && '0' <= p.data[p.pos] && p.data[p.pos] <= '9' {
		p.pos++
	}
	return p.pos > start
}

// raw records the ASCII text data[start:pos] for the path, folded.
func (p *jsonParser) raw(path, start int) {
	span := p.span(path)
	if span < 0 {
		return
	}
	lo := len(p.sc.fbuf)
	for _, c := range p.data[start:p.pos] {
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		p.sc.fbuf = append(p.sc.fbuf, c)
	}
	p.sc.jvals = append(p.sc.jvals, jval{span: span, lo: lo, hi: len(p.sc.fbuf)})
}

// decodeStr parses the string at p.pos and appends its content to dst,
// folded if fold is true. Like encoding/json, it replaces invalid UTF-8 and
// invalid surrogates with U+FFFD.
func (p *jsonParser) decodeStr(dst []byte, fold bool) ([]byte, error) {
	d := p.data
	i := p.pos + 1
	for {
		if i >= len(d) {
			p.pos = i
			return dst, p.fail("unterminated string")
		}
		switch c := d[i]; {
		case c == '"':
			p.pos = i + 1
			return dst, nil
		case c == '\\':
			if i+1 >= len(d) {
				p.pos = i
				return dst, p.fail("unterminated string")
			}
			var r rune
			switch d[i+1] {
			case '"', '\\', '/':
				r = rune(d[i+1])
			case 'b':
				r = '\b'
			case 'f':
				r = '\f'
			case 'n':
				r = '\n'
			case 'r':
				r = '\r'
			case 't':
				r = '\t'
			case 'u':
				if r = hex4(d, i); r < 0 {
					p.pos = i
					return dst, p.fail("invalid escape")
				}
				if utf16.IsSurrogate(r) {
					if dec := utf16.DecodeRune(r, hex4(d, i+6)); dec != unicode.ReplacementChar {
						r = dec
						i += 6
					} else {
						r = unicode.ReplacementChar
					}
				}
				i += 4
			default:
				p.pos = i
				return dst, p.fail("invalid escape")
			}
			i += 2
			dst = appendRune(dst, r, fold)
		case c < ' ':
			p.pos = i
			return dst, p.fail("control character in string")
		case c < utf8.RuneSelf:
			if fold && 'A' <= c && c <= 'Z' {
				c += 'a' - 'A'
			}
			dst = append(dst, c)
			i++
		default:
			r, size := utf8.DecodeRune(d[i:])
			dst = appendRune(dst, r, fold) // r is U+FFFD for invalid UTF-8
			i += size
		}
	}
}

// hex4 returns the code unit of the escape \uXXXX at d[i:], or -1 if there
// is none.
func hex4(d []byte, i int) rune {
	if len(d)-i < 6 || d[i] != '\\' || d[i+1] != 'u' {
		return -1
	}
	var r rune
	for _, c := range d[i+2 : i+6] {
		switch {
		case '0' <= c && c <= '9':
			c -= '0'
		case 'a' <= c && c <= 'f':
			c -= 'a' - 10
		case 'A' <= c && c <= 'F':
			c -= 'A' - 10
		default:
			return -1
		}
		r = r<<4 | rune(c)
	}
	return r
}

func appendRune(dst []byte, r rune, fold bool) []byte {
	if fold {
		r = unicode.ToLower(r)
	}
	return utf8.AppendRune(dst, r)
}
