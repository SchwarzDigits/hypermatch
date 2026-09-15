package candidates

import (
	"fmt"
	"log"
)

// HypermatchMatchJSON uses the rules of Hypermatch, but matches the same
// JSON events as Quamina with MatchJSON.
type HypermatchMatchJSON struct {
	*Hypermatch
}

func NewHypermatchMatchJSON(wildcard bool) *HypermatchMatchJSON {
	return &HypermatchMatchJSON{Hypermatch: NewHypermatch(wildcard)}
}

func (h *HypermatchMatchJSON) Name() string {
	return "hypermatch-matchjson"
}

func (h *HypermatchMatchJSON) Match(number int, modulo int) int {
	event := fmt.Sprintf(`
		{
			"name": "app-myapp-%d",
			"env": "prod",
			"number": "%d",
			"tags": ["tag1", "tag2"],
			"region": "earth",
			"type": "app"
		}
	`, number, number%modulo)
	matches, err := h.h.MatchJSON([]byte(event))
	if err != nil {
		log.Panicln(err)
	}
	return len(matches)
}
