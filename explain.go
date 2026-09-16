package hypermatch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Explanation describes how a rule matches an event, condition by condition.
// Its JSON form, for example for a user interface, uses the field names in
// lower camel case and omits empty fields.
type Explanation struct {
	Matched    bool              `json:"matched"`    // the event matches the rule
	Conditions []ConditionResult `json:"conditions"` // one per condition of the rule, in its order
}

// ConditionResult describes how a condition of a rule matches an event. A
// condition with alternatives has Or instead of Path, Values and Result.
type ConditionResult struct {
	Path    string        `json:"path,omitempty"`
	Matched bool          `json:"matched"`          // the condition holds
	Absent  bool          `json:"absent,omitempty"` // the event has no value at Path
	Values  []string      `json:"values,omitempty"` // the values of the property
	Result  PatternResult `json:"result,omitzero"`
	Or      []Explanation `json:"or,omitempty"` // one per alternative of "$or"
}

// PatternResult describes how a pattern matches the values of a property.
type PatternResult struct {
	Pattern Pattern `json:"pattern"`
	Matched bool    `json:"matched"`

	// Values are the values that match the pattern. For anythingBut, they
	// are the values that match one of its sub-patterns and so exclude the
	// event.
	Values []string `json:"values,omitempty"`

	Sub []PatternResult `json:"sub,omitempty"` // for anyOf, allOf and anythingBut
}

// Explain reports how rule matches event, condition by condition. It follows
// the semantics of Match exactly, but is meant for understanding and
// debugging rules rather than for speed. It returns an error wrapping
// ErrInvalidRule if the rule is invalid.
func Explain(rule ConditionSet, event []Property) (Explanation, error) {
	if err := ValidateRule(rule); err != nil {
		return Explanation{}, err
	}
	values := make(map[string][]string, len(event))
	for _, p := range event {
		values[p.Path] = append(values[p.Path], p.Values...)
	}
	return explainSet(rule, values), nil
}

// explainSet reports how the valid rule matches the values of an event, by
// path.
func explainSet(rule ConditionSet, values map[string][]string) Explanation {
	e := Explanation{Matched: true, Conditions: make([]ConditionResult, len(rule))}
	for i, c := range rule {
		r := &e.Conditions[i]
		if c.Or != nil {
			r.Or = make([]Explanation, len(c.Or))
			for j, alternative := range c.Or {
				r.Or[j] = explainSet(alternative, values)
				r.Matched = r.Matched || r.Or[j].Matched
			}
			e.Matched = e.Matched && r.Matched
			continue
		}
		vs := values[c.Path]
		folded := make([]string, len(vs))
		for j, v := range vs {
			folded[j] = fold(v)
		}
		p := explainPattern(c.Pattern, vs, folded)
		if len(vs) == 0 && c.Pattern.Type != PatternExists {
			p.Matched = false // only {"exists": false} matches absent properties
		}
		*r = ConditionResult{Path: c.Path, Matched: p.Matched, Absent: len(vs) == 0, Values: vs, Result: p}
		e.Matched = e.Matched && r.Matched
	}
	return e
}

// ExplainJSON is like Explain, but takes the event as a JSON object, like
// MatchJSON. It returns an error wrapping ErrInvalidEvent if event is not a
// valid JSON object.
func ExplainJSON(rule ConditionSet, event []byte) (Explanation, error) {
	props, err := jsonEventProperties(event)
	if err != nil {
		return Explanation{}, err
	}
	return Explain(rule, props)
}

func explainPattern(p Pattern, values, folded []string) PatternResult {
	r := PatternResult{Pattern: p}
	switch p.Type {
	case PatternAnyOf, PatternAllOf, PatternAnythingBut:
		r.Sub = make([]PatternResult, len(p.Sub))
		anyOf, allOf := false, true
		for i, s := range p.Sub {
			r.Sub[i] = explainPattern(s, values, folded)
			anyOf = anyOf || r.Sub[i].Matched
			allOf = allOf && r.Sub[i].Matched
		}
		switch p.Type {
		case PatternAnyOf:
			r.Matched = anyOf
		case PatternAllOf:
			r.Matched = allOf
		default:
			r.Matched = !anyOf
			for i, v := range folded {
				for _, s := range p.Sub {
					if valueMatches(s, v) {
						r.Values = append(r.Values, values[i])
						break
					}
				}
			}
		}
		return r
	case PatternExists:
		r.Matched = (len(values) > 0) == (p.Value == "true")
		return r
	}
	for i, v := range folded {
		if valueMatches(p, v) {
			r.Values = append(r.Values, values[i])
		}
	}
	r.Matched = len(r.Values) > 0
	return r
}

// valueMatches reports whether the folded value v matches p on its own.
func valueMatches(p Pattern, v string) bool {
	switch p.Type {
	case PatternEquals:
		return v == fold(p.Value)
	case PatternPrefix:
		return strings.HasPrefix(v, fold(p.Value))
	case PatternSuffix:
		return strings.HasSuffix(v, fold(p.Value))
	case PatternWildcard:
		return globMatch(fold(p.Value), v)
	case PatternLessThan, PatternLessThanOrEqual, PatternGreaterThan, PatternGreaterThanOrEqual, PatternNumericEquals:
		x, ok := parseNumber(v)
		bound, _ := parseNumber(p.Value)
		return ok && boundInterval(p.Type, bound).contains(x)
	case PatternBetween:
		x, ok := parseNumber(v)
		iv, _ := betweenInterval(&p)
		return ok && iv.contains(x)
	case PatternExists:
		return p.Value == "true"
	case PatternAnyOf:
		for _, s := range p.Sub {
			if valueMatches(s, v) {
				return true
			}
		}
		return false
	case PatternAllOf:
		for _, s := range p.Sub {
			if !valueMatches(s, v) {
				return false
			}
		}
		return true
	case PatternAnythingBut:
		return !valueMatches(Pattern{Type: PatternAnyOf, Sub: p.Sub}, v)
	}
	return false
}

// globMatch reports whether s matches the valid wildcard pattern, in which
// '*' matches any sequence of bytes and `\*` and `\\` match '*' and '\'.
func globMatch(pattern, s string) bool {
	parts, _ := splitWildcard(pattern)
	if len(parts) == 1 {
		return s == parts[0]
	}
	first, last := parts[0], parts[len(parts)-1]
	if len(s) < len(first)+len(last) || !strings.HasPrefix(s, first) || !strings.HasSuffix(s, last) {
		return false
	}
	// Matching every part in between as early as possible leaves the most
	// room for the parts after it.
	s = s[len(first) : len(s)-len(last)]
	for _, p := range parts[1 : len(parts)-1] {
		i := strings.Index(s, p)
		if i < 0 {
			return false
		}
		s = s[i+len(p):]
	}
	return true
}

// String returns a readable account of the explanation, for example:
//
//	no match
//	  ✓ status: {"equals":"firing"} matched "FIRING"
//	  ✗ severity: {"anyOf":[{"equals":"critical"},{"equals":"warning"}]} (values ["info"])
//	      ✗ {"equals":"critical"}
//	      ✗ {"equals":"warning"}
//	  ✓ owner: {"exists":false} (absent)
func (e Explanation) String() string {
	var b strings.Builder
	if e.Matched {
		b.WriteString("match\n")
	} else {
		b.WriteString("no match\n")
	}
	writeConditions(&b, e.Conditions, "  ")
	return b.String()
}

// writeConditions writes the results of the conditions of one condition set,
// indented by indent.
func writeConditions(b *strings.Builder, conditions []ConditionResult, indent string) {
	for _, c := range conditions {
		if c.Or != nil {
			fmt.Fprintf(b, "%s%s any of:\n", indent, mark(c.Matched))
			for i, alternative := range c.Or {
				fmt.Fprintf(b, "%s  %s alternative %d\n", indent, mark(alternative.Matched), i+1)
				writeConditions(b, alternative.Conditions, indent+"    ")
			}
			continue
		}
		fmt.Fprintf(b, "%s%s %s: %s", indent, mark(c.Matched), c.Path, patternText(c.Result.Pattern))
		switch {
		case c.Absent:
			b.WriteString(" (absent)")
		case c.Result.Pattern.Type == PatternExists:
			b.WriteString(" (present)")
		case c.Result.Pattern.Type == PatternAnythingBut && !c.Matched:
			b.WriteString(" excluded by " + quoteAll(c.Result.Values))
		case c.Matched && c.Result.Sub == nil:
			b.WriteString(" matched " + quoteAll(c.Result.Values))
		default:
			fmt.Fprintf(b, " (values %q)", c.Values)
		}
		b.WriteByte('\n')
		if !c.Absent {
			writeSubResults(b, c.Result.Sub, indent+"    ")
		}
	}
}

func writeSubResults(b *strings.Builder, results []PatternResult, indent string) {
	for _, r := range results {
		fmt.Fprintf(b, "%s%s %s", indent, mark(r.Matched), patternText(r.Pattern))
		if r.Sub == nil && r.Matched && len(r.Values) > 0 {
			b.WriteString(" matched " + quoteAll(r.Values))
		}
		b.WriteByte('\n')
		writeSubResults(b, r.Sub, indent+"    ")
	}
}

func mark(matched bool) string {
	if matched {
		return "✓"
	}
	return "✗"
}

func patternText(p Pattern) string {
	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Sprintf("<%v>", err)
	}
	return string(data)
}

func quoteAll(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = strconv.Quote(v)
	}
	return strings.Join(quoted, ", ")
}

// jsonEventProperties flattens a JSON object into properties the way
// MatchJSON sees them, using encoding/json.
func jsonEventProperties(data []byte) ([]Property, error) {
	if !json.Valid(data) {
		return nil, fmt.Errorf("%w: invalid JSON", ErrInvalidEvent)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if tok, _ := dec.Token(); tok != json.Delim('{') {
		return nil, fmt.Errorf("%w: expected an object", ErrInvalidEvent)
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
			props = append(props, Property{Path: path, Values: []string{v}})
		case json.Number:
			props = append(props, Property{Path: path, Values: []string{string(v)}})
		case bool:
			props = append(props, Property{Path: path, Values: []string{strconv.FormatBool(v)}})
		}
	}
	object("", true)
	return props, nil
}
