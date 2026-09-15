package candidates

import (
	"encoding/json"
	"fmt"

	"github.com/SchwarzDigits/hypermatch"
)

type HypermatchJson struct {
	h        *hypermatch.HyperMatch[int]
	wildcard bool
}

func NewHypermatchJson(wildcard bool) *HypermatchJson {
	return &HypermatchJson{h: hypermatch.New[int](), wildcard: wildcard}
}

func (h *HypermatchJson) Name() string {
	return "hypermatch-json"
}

func (h *HypermatchJson) AddRule(number int, modulo int) {
	name := ""
	if h.wildcard {
		name = `"name": {"wildcard": "*-myapp-*"},`
	}
	jsonStr := fmt.Sprintf(`
		{
			%s
			"env": {"equals": "prod"},
			"number": {"equals": "%d"},
			"tags": {"allOf": [{"equals": "tag1"}, {"equals": "tag2"}]},
			"region": {"anythingBut": [{"equals": "moon"}]},
			"type": {"anyOf": [{"equals": "app"}, {"equals": "database"}]}
		}
	`, name, number%modulo)
	var conditionSet hypermatch.ConditionSet
	if err := json.Unmarshal([]byte(jsonStr), &conditionSet); err != nil {
		panic(err)
	}
	if err := h.h.AddRule(number, conditionSet); err != nil {
		panic(err)
	}
}

func (h *HypermatchJson) Match(number int, modulo int) int {
	eventStr := fmt.Sprintf(`
		[
			{"Path": "name", "Values": ["app-myapp-%d"]},
			{"Path": "env", "Values": ["prod"]},
			{"Path": "number", "Values": ["%d"]},
			{"Path": "tags", "Values": ["tag1", "tag2"]},
			{"Path": "region", "Values": ["earth"]},
			{"Path": "type", "Values": ["app"]}
		]
	`, number, number%modulo)

	var properties []hypermatch.Property
	if err := json.Unmarshal([]byte(eventStr), &properties); err != nil {
		panic(err)
	}

	matches := h.h.Match(properties)
	return len(matches)
}
