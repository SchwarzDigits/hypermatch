package hypermatch

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// ConditionSet is a rule. An event matches it if it matches every Condition.
type ConditionSet []Condition

// MarshalJSON encodes the set as an object keyed by path, for example
// {"status": {"equals": "firing"}}. Several conditions on the same path are
// combined into an equivalent allOf pattern.
func (c ConditionSet) MarshalJSON() ([]byte, error) {
	byPath := make(map[string][]Pattern, len(c))
	for _, cc := range c {
		byPath[cc.Path] = append(byPath[cc.Path], cc.Pattern)
	}
	data := make(map[string]Pattern, len(byPath))
	for path, ps := range byPath {
		if len(ps) == 1 {
			data[path] = ps[0]
		} else {
			data[path] = Pattern{Type: PatternAllOf, Sub: ps}
		}
	}
	return json.Marshal(data)
}

// UnmarshalJSON decodes the object form written by MarshalJSON. The
// conditions are sorted by path.
func (c *ConditionSet) UnmarshalJSON(data []byte) error {
	var r map[string]Pattern
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	cs := make(ConditionSet, 0, len(r))
	for path, p := range r {
		cs = append(cs, Condition{Path: path, Pattern: p})
	}
	slices.SortFunc(cs, func(a, b Condition) int { return strings.Compare(a.Path, b.Path) })
	*c = cs
	return nil
}

// Condition is a single condition of a ConditionSet: the values of the
// property at Path must match Pattern. Paths are case-sensitive.
type Condition struct {
	Path    string  `json:"path"`
	Pattern Pattern `json:"pattern"`
}

// MarshalJSON encodes the condition as {"<path>": <pattern>}.
func (c Condition) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]Pattern{c.Path: c.Pattern})
}

// UnmarshalJSON decodes the object form written by MarshalJSON.
func (c *Condition) UnmarshalJSON(data []byte) error {
	var r map[string]Pattern
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	if len(r) != 1 {
		return fmt.Errorf("hypermatch: condition must have exactly one path, got %d", len(r))
	}
	for path, p := range r {
		*c = Condition{Path: path, Pattern: p}
	}
	return nil
}

// Pattern defines how the values of a property are compared. The literal
// types (equals, prefix, suffix, wildcard) use Value, the compound types
// (anythingBut, anyOf, allOf) use Sub.
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
	if p.Type.HasLiteralValue() {
		return json.Marshal(map[string]string{name: p.Value})
	}
	sub := p.Sub
	if sub == nil {
		sub = []Pattern{}
	}
	return json.Marshal(map[string][]Pattern{name: sub})
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
			var v string
			if err := json.Unmarshal(raw, &v); err != nil {
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
