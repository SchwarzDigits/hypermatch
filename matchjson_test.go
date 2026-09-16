package hypermatch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestMatchJSON(t *testing.T) {
	h := New[string]()
	mustAdd(t, h, "team", cond("alert.labels.team", equalsP("db")))
	mustAdd(t, h, "tags", cond("tags", allOfP(equalsP("shop"), equalsP("backend"))))
	mustAdd(t, h, "items", cond("items.sku", equalsP("a-1")))
	mustAdd(t, h, "status", cond("status", equalsP("500")))
	mustAdd(t, h, "flag", cond("enabled", equalsP("true")))
	mustAdd(t, h, "owner", cond("owner", anythingButP(equalsP("x"))))
	mustAdd(t, h, "unicode", cond("name", prefixP("ärger")))
	mustAdd(t, h, "dotted", cond("a.b", equalsP("1")))

	tests := []struct {
		event string
		want  []string
	}{
		{`{"alert": {"labels": {"team": "DB"}}}`, []string{"team"}},
		{`{"tags": ["shop", "backend", "eu"]}`, []string{"tags"}},
		{`{"tags": ["shop"], "tags": "backend"}`, []string{"tags"}},
		{`{"items": [{"sku": "b-2"}, {"sku": "A-1"}]}`, []string{"items"}},
		{`{"status": 500}`, []string{"status"}},
		{`{"status": 5e2}`, nil},
		{`{"enabled": true}`, []string{"flag"}},
		{`{"owner": null}`, nil},
		{`{"owner": "y"}`, []string{"owner"}},
		{`{"owner": ["y", "X"]}`, nil},
		{`{"name": "Ärgerlich"}`, []string{"unicode"}},
		{`{"a.b": 1}`, []string{"dotted"}},
		{`{"a": {"b": 1}}`, []string{"dotted"}},
		{`{"unused": {"deep": [1, {"x": "😀"}]}, "status": "500"}`, []string{"status"}},
		{" \t\n{ } \r\n", nil},
	}
	for _, tt := range tests {
		got, err := h.MatchJSON([]byte(tt.event))
		if err != nil {
			t.Errorf("MatchJSON(%s): %v", tt.event, err)
			continue
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("MatchJSON(%s) = %v, want %v", tt.event, got, tt.want)
		}
	}
}

func TestMatchJSONErrors(t *testing.T) {
	h := New[int]()
	mustAdd(t, h, 1, cond("a", equalsP("1")))
	for _, event := range []string{
		``, ` `, `[]`, `"a"`, `1`, `{`, `{"a"}`, `{"a":}`, `{"a":1,}`, `{"a":01}`, `{"a":1}x`,
		`{"a":"\x"}`, `{"a":"\u12"}`, "{\"a\":\"\x01\"}", `{"a":tru}`, `{"a":-}`, `{"a":1.}`,
		`{"a":1e}`, `{"a" 1}`, `{"a":1 "b":2}`, `{"b":[1,2,}`, `{"b":{"c":}}`, `{a:1}`, `{"a":"x`,
		`{"a":nul}`, `{"a":1}}`, "\xef\xbb\xbf{}",
	} {
		if json.Valid([]byte(event)) && strings.HasPrefix(strings.TrimSpace(event), "{") {
			t.Fatalf("test case %q is a valid JSON object", event)
		}
		if _, err := h.MatchJSON([]byte(event)); !errors.Is(err, ErrInvalidEvent) {
			t.Errorf("MatchJSON(%q) = %v, want ErrInvalidEvent", event, err)
		}
	}
}

func TestMatchJSONDepth(t *testing.T) {
	h := New[int]()
	mustAdd(t, h, 1, cond("a", equalsP("1")))
	nested := func(depth int) []byte {
		return []byte(strings.Repeat(`{"a":`, depth-1) + "{}" + strings.Repeat("}", depth-1))
	}
	if _, err := h.MatchJSON(nested(maxJSONDepth)); err != nil {
		t.Errorf("MatchJSON at the maximum depth: %v", err)
	}
	if _, err := h.MatchJSON(nested(maxJSONDepth + 1)); !errors.Is(err, ErrInvalidEvent) {
		t.Errorf("MatchJSON beyond the maximum depth = %v, want ErrInvalidEvent", err)
	}
	if !json.Valid(nested(maxJSONDepth)) || json.Valid(nested(maxJSONDepth+1)) {
		t.Error("maxJSONDepth differs from encoding/json")
	}
}

func TestMatchJSONWithoutRules(t *testing.T) {
	var h HyperMatch[int]
	if got, err := h.MatchJSON([]byte(`{"a": [1, {"b": "c"}]}`)); got != nil || err != nil {
		t.Errorf("MatchJSON = %v, %v, want nil, nil", got, err)
	}
	if _, err := h.MatchJSON([]byte(`{"a": }`)); !errors.Is(err, ErrInvalidEvent) {
		t.Errorf("MatchJSON of invalid JSON = %v, want ErrInvalidEvent", err)
	}
}

func TestAppendMatchesJSON(t *testing.T) {
	h := New[int]()
	mustAdd(t, h, 1, cond("f", equalsP("a")))
	mustAdd(t, h, 2, cond("g.h", prefixP("x")))
	event := []byte(`{"f": "A", "g": {"h": ["y", "xyz"]}, "other": {"deep": [1, 2, 3]}}`)
	got, err := h.AppendMatchesJSON([]int{42}, event)
	if err != nil || !slices.Equal(got, []int{42, 1, 2}) {
		t.Errorf("AppendMatchesJSON = %v, %v, want [42 1 2]", got, err)
	}
	if raceEnabled {
		return
	}
	dst := make([]int, 0, 8)
	if allocs := testing.AllocsPerRun(100, func() { dst, _ = h.AppendMatchesJSON(dst[:0], event) }); allocs != 0 {
		t.Errorf("AppendMatchesJSON allocates %v times per call, want 0", allocs)
	}
}

// jsonProperties is the reference for MatchJSON: it flattens a JSON object
// into properties with encoding/json, keeping repeated keys. It reports
// false if data is not a valid JSON object.
func jsonProperties(data []byte) ([]Property, bool) {
	if !json.Valid(data) {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, false
	}
	var props []Property
	var value func(path string)
	object := func(path string, root bool) {
		for dec.More() {
			tok, _ := dec.Token()
			key := tok.(string)
			if !root {
				key = path + "." + key
			}
			value(key)
		}
		_, _ = dec.Token() // '}'
	}
	value = func(path string) {
		tok, _ := dec.Token()
		switch v := tok.(type) {
		case json.Delim:
			if v == '{' {
				object(path, false)
				return
			}
			for dec.More() {
				value(path)
			}
			_, _ = dec.Token() // ']'
		case string:
			props = append(props, prop(path, v))
		case json.Number:
			props = append(props, prop(path, string(v)))
		case bool:
			props = append(props, prop(path, strconv.FormatBool(v)))
		}
	}
	object("", true)
	return props, true
}

var (
	jsonKeys      = []string{"a", "b", "c", "A", "a.b", ""}
	jsonRulePaths = []string{"a", "b", "c", "A", "a.b", "b.a", ".a", "a.a.b"}
	jsonNumbers   = []string{"0", "-1", "42", "1.5", "1E3", "-0.25e-2", "500"}
	jsonEscapes   = []string{`"Ab"`, `"ä"`, `"😀"`, `"\ud800x"`, `"a\/b\n"`, `"\uDC00\ud800􏿿"`}
)

func genJSONRule(src source) ConditionSet {
	cs := make(ConditionSet, 1+src.intn(3))
	for i := range cs {
		cs[i] = cond(jsonRulePaths[src.intn(len(jsonRulePaths))], genPattern(src, 0))
	}
	return cs
}

func genJSONValue(src source, b *strings.Builder, depth int) {
	kinds := 8
	if depth >= 3 {
		kinds = 6
	}
	switch src.intn(kinds) {
	case 0, 1:
		s, _ := json.Marshal(genString(src, 3))
		b.Write(s)
	case 2: // raw, possibly invalid UTF-8
		b.WriteByte('"')
		for range src.intn(4) {
			r := genRunes[src.intn(len(genRunes))]
			if r == `\` {
				r = `\\`
			}
			b.WriteString(r)
		}
		b.WriteByte('"')
	case 3:
		b.WriteString(jsonNumbers[src.intn(len(jsonNumbers))])
	case 4:
		b.WriteString([...]string{"true", "false", "null"}[src.intn(3)])
	case 5:
		b.WriteString(jsonEscapes[src.intn(len(jsonEscapes))])
	case 6:
		b.WriteByte('[')
		for i := range src.intn(4) {
			if i > 0 {
				b.WriteByte(',')
			}
			genJSONValue(src, b, depth+1)
		}
		b.WriteByte(']')
	default:
		genJSONObject(src, b, depth+1)
	}
}

func genJSONObject(src source, b *strings.Builder, depth int) {
	b.WriteByte('{')
	for i := range src.intn(5) {
		if i > 0 {
			b.WriteString(", ")
		}
		k, _ := json.Marshal(jsonKeys[src.intn(len(jsonKeys))])
		b.Write(k)
		b.WriteByte(':')
		genJSONValue(src, b, depth)
	}
	b.WriteByte('}')
}

// checkMatchJSON compares MatchJSON with Match on the properties the
// reference decodes from data.
func checkMatchJSON(h *HyperMatch[int], data []byte) error {
	props, ok := jsonProperties(data)
	got, err := h.MatchJSON(data)
	if !ok {
		if err == nil {
			return fmt.Errorf("MatchJSON(%q) accepted an invalid event", data)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("MatchJSON(%q): %w", data, err)
	}
	if want := h.Match(props); !slices.Equal(got, want) {
		return fmt.Errorf("MatchJSON(%q) = %v, want %v", data, got, want)
	}
	return nil
}

func TestMatchJSONDifferential(t *testing.T) {
	seeds := 2000
	if testing.Short() {
		seeds = 200
	}
	for seed := range seeds {
		src := randSource{rand.New(rand.NewPCG(uint64(seed), 4))}
		h := New[int]()
		for id := range 1 + src.intn(20) {
			mustAdd(t, h, id, genJSONRule(src)...)
		}
		for range 20 {
			var b strings.Builder
			genJSONObject(src, &b, 0)
			if err := checkMatchJSON(h, []byte(b.String())); err != nil {
				t.Fatalf("seed %d: %v", seed, err)
			}
		}
	}
}

func FuzzMatchJSON(f *testing.F) {
	src := randSource{rand.New(rand.NewPCG(5, 5))}
	h := New[int]()
	for id := range 40 {
		if err := h.AddRule(id, genJSONRule(src)); err != nil {
			f.Fatal(err)
		}
	}
	for range 20 {
		var b strings.Builder
		genJSONObject(src, &b, 0)
		f.Add([]byte(b.String()))
	}
	f.Add([]byte(`{"a": {"b": [1, "x", {"": null}]}, "a.b": "\ud83d"}`))
	f.Add([]byte(`{"a": 1,}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if err := checkMatchJSON(h, data); err != nil {
			t.Fatal(err)
		}
	})
}

// TestMatchJSONWideEvent matches events with more paths than the table of
// spans starts with, so that it grows, and repeats a path afterwards.
func TestMatchJSONWideEvent(t *testing.T) {
	for _, paths := range []int{8, 9, 17, 40, 300} {
		h := New[int]()
		for i := range paths {
			mustAdd(t, h, i, cond(fmt.Sprintf("f%d", i), equalsP(fmt.Sprintf("v%d", i))))
		}
		mustAdd(t, h, paths, cond("f0", equalsP("late")))
		mustAdd(t, h, paths+1, cond("n.deep", equalsP("x")))
		var b strings.Builder
		b.WriteString("{")
		for i := range paths {
			fmt.Fprintf(&b, `"f%d": "v%d", `, i, i)
		}
		b.WriteString(`"n": {"deep": "x"}, "f0": "late"}`)
		data := []byte(b.String())
		got, err := h.MatchJSON(data)
		if err != nil {
			t.Fatal(err)
		}
		want := make([]int, paths+2)
		for i := range want {
			want[i] = i
		}
		if !slices.Equal(got, want) {
			t.Fatalf("%d paths: MatchJSON = %v, want %v", paths, got, want)
		}
		if err := checkMatchJSON(h, data); err != nil {
			t.Fatalf("%d paths: %v", paths, err)
		}
	}
}
