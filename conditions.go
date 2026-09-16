package hypermatch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// ConditionSet is a rule. An event matches it if it matches every Condition.
type ConditionSet []Condition

// orKey is the key of a condition with alternatives in the JSON form. A key
// "$or" whose value is an object is an ordinary path, because patterns are
// objects and alternatives are always an array.
const orKey = "$or"

// MarshalJSON encodes the set as an object keyed by path, for example
// {"status": {"equals": "firing"}}. Several conditions on the same path are
// combined into an equivalent allOf pattern, and alternatives are encoded
// as "$or".
func (c ConditionSet) MarshalJSON() ([]byte, error) {
	byPath := make(map[string][]Pattern, len(c))
	var or []ConditionSet
	for _, cc := range c {
		if cc.Or != nil {
			if or != nil {
				return nil, fmt.Errorf("hypermatch: a condition set must not contain more than one %q", orKey)
			}
			or = cc.Or
			continue
		}
		byPath[cc.Path] = append(byPath[cc.Path], cc.Pattern)
	}
	data := make(map[string]any, len(byPath)+1)
	for path, ps := range byPath {
		if len(ps) == 1 {
			data[path] = ps[0]
		} else {
			data[path] = Pattern{Type: PatternAllOf, Sub: ps}
		}
	}
	if or != nil {
		if _, taken := data[orKey]; taken {
			return nil, fmt.Errorf("hypermatch: a condition on the path %q cannot be combined with alternatives", orKey)
		}
		data[orKey] = or
	}
	return json.Marshal(data)
}

// UnmarshalJSON decodes the object form written by MarshalJSON. The
// conditions are sorted by path, and the alternatives of "$or" come first.
func (c *ConditionSet) UnmarshalJSON(data []byte) error {
	var r map[string]json.RawMessage
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	cs := make(ConditionSet, 0, len(r))
	for path, raw := range r {
		cond, err := decodeCondition(path, raw)
		if err != nil {
			return err
		}
		cs = append(cs, cond)
	}
	slices.SortFunc(cs, func(a, b Condition) int { return strings.Compare(a.Path, b.Path) })
	*c = cs
	return nil
}

// decodeCondition decodes the condition that the key path with the value raw
// stands for.
func decodeCondition(path string, raw json.RawMessage) (Condition, error) {
	if path == orKey && len(bytes.TrimLeft(raw, " \t\r\n")) > 0 && bytes.TrimLeft(raw, " \t\r\n")[0] == '[' {
		var alternatives []ConditionSet
		if err := json.Unmarshal(raw, &alternatives); err != nil {
			return Condition{}, fmt.Errorf("hypermatch: %q: %w", orKey, err)
		}
		return Condition{Or: alternatives}, nil
	}
	var p Pattern
	if err := json.Unmarshal(raw, &p); err != nil {
		return Condition{}, err
	}
	return Condition{Path: path, Pattern: p}, nil
}

// Condition is a single condition of a ConditionSet: the values of the
// property at Path must match Pattern. Paths are case-sensitive.
type Condition struct {
	Path    string  `json:"path"`
	Pattern Pattern `json:"pattern"`

	// Or holds alternative condition sets, of which at least one must
	// match. A condition with alternatives has no Path and no Pattern, and
	// a condition set contains at most one of them. In JSON they are the
	// key "$or".
	Or []ConditionSet `json:"$or,omitempty"`
}

// MarshalJSON encodes the condition as {"<path>": <pattern>}, or as
// {"$or": [<condition sets>]} if it has alternatives.
func (c Condition) MarshalJSON() ([]byte, error) {
	if c.Or != nil {
		return json.Marshal(map[string][]ConditionSet{orKey: c.Or})
	}
	return json.Marshal(map[string]Pattern{c.Path: c.Pattern})
}

// UnmarshalJSON decodes the object form written by MarshalJSON.
func (c *Condition) UnmarshalJSON(data []byte) error {
	var r map[string]json.RawMessage
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	if len(r) != 1 {
		return fmt.Errorf("hypermatch: condition must have exactly one path, got %d", len(r))
	}
	for path, raw := range r {
		cond, err := decodeCondition(path, raw)
		if err != nil {
			return err
		}
		*c = cond
	}
	return nil
}

// Pattern defines how the values of a property are compared. The literal
// types (equals, prefix, suffix, wildcard, lt, lte, gt, gte, eq, exists) use
// Value, the others (anythingBut, anyOf, allOf, between) use Sub.
type Pattern struct {
	Type  PatternType `json:"type"`
	Value string      `json:"value,omitempty"`
	Sub   []Pattern   `json:"sub,omitempty"`
}

// MarshalJSON encodes the pattern as {"<type>": "<value>"} or
// {"<type>": [<sub-patterns>]}.
func (p Pattern) MarshalJSON() ([]byte, error) {
	name := p.Type.String()
	if name == "" {
		return nil, fmt.Errorf("hypermatch: unknown pattern type %d", p.Type)
	}
	switch {
	case p.Type == PatternExists && (p.Value == "true" || p.Value == "false"):
		return json.Marshal(map[string]bool{name: p.Value == "true"})
	case p.Type.isNumeric() && p.Value != "":
		// Numbers are written as JSON numbers where possible.
		if data, err := json.Marshal(map[string]json.Number{name: json.Number(p.Value)}); err == nil {
			return data, nil
		}
		return json.Marshal(map[string]string{name: p.Value})
	case p.Type.HasLiteralValue():
		return json.Marshal(map[string]string{name: p.Value})
	}
	sub := p.Sub
	if sub == nil {
		sub = []Pattern{}
	}
	return json.Marshal(map[string][]Pattern{name: sub})
}

// literalValue decodes the value of a pattern that uses Value. Numeric
// patterns also accept JSON numbers, and exists accepts booleans.
func literalValue(t PatternType, raw json.RawMessage) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", err
	}
	switch v := v.(type) {
	case string:
		return v, nil
	case json.Number:
		if t.isNumeric() {
			return v.String(), nil
		}
	case bool:
		if t == PatternExists {
			return strconv.FormatBool(v), nil
		}
	}
	return "", fmt.Errorf("unexpected value %s", raw)
}

// UnmarshalJSON decodes the object form written by MarshalJSON.
func (p *Pattern) UnmarshalJSON(data []byte) error {
	var r map[string]json.RawMessage
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	if len(r) != 1 {
		return fmt.Errorf("hypermatch: pattern must have exactly one type, got %d", len(r))
	}
	for name, raw := range r {
		t := PatternTypeFromString(name)
		switch {
		case t == PatternUnknown:
			return fmt.Errorf("hypermatch: unknown pattern type %q", name)
		case t.HasLiteralValue():
			v, err := literalValue(t, raw)
			if err != nil {
				return fmt.Errorf("hypermatch: pattern %q: %w", name, err)
			}
			*p = Pattern{Type: t, Value: v}
		default:
			var sub []Pattern
			if err := json.Unmarshal(raw, &sub); err != nil {
				return fmt.Errorf("hypermatch: pattern %q: %w", name, err)
			}
			*p = Pattern{Type: t, Sub: sub}
		}
	}
	return nil
}
