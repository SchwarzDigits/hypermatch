package candidates

import (
	"fmt"
	"log"

	"quamina.net/go/quamina"
)

type Quamina struct {
	q        *quamina.Quamina
	wildcard bool
}

func NewQuamina(wildcard bool) *Quamina {
	q, err := quamina.New(quamina.WithMediaType("application/json"))
	if err != nil {
		panic(err)
	}
	return &Quamina{q: q, wildcard: wildcard}
}

func (q *Quamina) Name() string {
	return "quamina"
}

func (q *Quamina) AddRule(number int, modulo int) {
	name := ""
	if q.wildcard {
		name = `"name": [{"shellstyle": "*-myapp-*"}],`
	}
	// Quamina has no allOf: "tags" matches if the event contains tag1 or tag2.
	str := fmt.Sprintf(`
		{
			%s
			"env": ["prod"],
			"number": ["%d"],
			"tags": ["tag1", "tag2"],
			"region": [{"anything-but": ["moon"]}],
			"type": ["app", "database"]
		}
	`, name, number%modulo)
	err := q.q.AddPattern(number, str)
	if err != nil {
		log.Panicln(err)
	}
}

func (q *Quamina) Match(number int, modulo int) int {
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
	r, _ := q.q.MatchesForEvent([]byte(event))
	return len(r)
}
