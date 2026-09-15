package candidates

import (
	"fmt"
	"log"

	"github.com/SchwarzDigits/hypermatch/v2"
)

type Hypermatch struct {
	h        *hypermatch.HyperMatch[int]
	wildcard bool
}

func NewHypermatch(wildcard bool) *Hypermatch {
	return &Hypermatch{h: hypermatch.New[int](), wildcard: wildcard}
}

func (h *Hypermatch) Name() string {
	return "hypermatch"
}

func (h *Hypermatch) AddRule(number int, modulo int) {
	rule := hypermatch.ConditionSet{
		{Path: "env", Pattern: hypermatch.Pattern{Type: hypermatch.PatternEquals, Value: "prod"}},
		{Path: "number", Pattern: hypermatch.Pattern{Type: hypermatch.PatternEquals, Value: fmt.Sprintf("%d", number%modulo)}},
		{Path: "tags", Pattern: hypermatch.Pattern{
			Type: hypermatch.PatternAllOf, Sub: []hypermatch.Pattern{
				{Type: hypermatch.PatternEquals, Value: "tag1"},
				{Type: hypermatch.PatternEquals, Value: "tag2"},
			},
		}},
		{Path: "region", Pattern: hypermatch.Pattern{
			Type: hypermatch.PatternAnythingBut, Sub: []hypermatch.Pattern{
				{Type: hypermatch.PatternEquals, Value: "moon"},
			},
		}},
		{Path: "type", Pattern: hypermatch.Pattern{
			Type: hypermatch.PatternAnyOf, Sub: []hypermatch.Pattern{
				{Type: hypermatch.PatternEquals, Value: "app"},
				{Type: hypermatch.PatternEquals, Value: "database"},
			},
		}},
	}
	if h.wildcard {
		rule = append(rule, hypermatch.Condition{Path: "name", Pattern: hypermatch.Pattern{Type: hypermatch.PatternWildcard, Value: "*-myapp-*"}})
	}
	if err := h.h.AddRule(number, rule); err != nil {
		log.Panicln(err)
	}
}

func (h *Hypermatch) Match(number int, modulo int) int {
	event := []hypermatch.Property{
		{
			Path:   "name",
			Values: []string{fmt.Sprintf("app-myapp-%d", number)},
		},
		{
			Path:   "env",
			Values: []string{"prod"},
		},
		{
			Path:   "number",
			Values: []string{fmt.Sprintf("%d", number%modulo)},
		},
		{
			Path:   "tags",
			Values: []string{"tag1", "tag2"},
		},
		{
			Path:   "region",
			Values: []string{"earth"},
		},
		{
			Path:   "type",
			Values: []string{"app"},
		},
	}
	matches := h.h.Match(event)
	return len(matches)
}
