package hypermatch_test

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/SchwarzDigits/hypermatch/v2"
)

func Example() {
	hm := hypermatch.New[string]()

	err := hm.AddRule("critical-shop-alerts", hypermatch.ConditionSet{
		{Path: "status", Pattern: hypermatch.Pattern{Type: hypermatch.PatternEquals, Value: "firing"}},
		{Path: "severity", Pattern: hypermatch.Pattern{Type: hypermatch.PatternAnyOf, Sub: []hypermatch.Pattern{
			{Type: hypermatch.PatternEquals, Value: "critical"},
			{Type: hypermatch.PatternEquals, Value: "warning"},
		}}},
		{Path: "tags", Pattern: hypermatch.Pattern{Type: hypermatch.PatternAllOf, Sub: []hypermatch.Pattern{
			{Type: hypermatch.PatternEquals, Value: "shop"},
			{Type: hypermatch.PatternEquals, Value: "backend"},
		}}},
	})
	if err != nil {
		panic(err)
	}

	fmt.Println(hm.Match([]hypermatch.Property{
		{Path: "status", Values: []string{"FIRING"}},
		{Path: "severity", Values: []string{"critical"}},
		{Path: "tags", Values: []string{"shop", "backend", "eu"}},
	}))
	fmt.Println(hm.Match([]hypermatch.Property{
		{Path: "status", Values: []string{"resolved"}},
		{Path: "severity", Values: []string{"critical"}},
		{Path: "tags", Values: []string{"shop", "backend"}},
	}))
	// Output:
	// [critical-shop-alerts]
	// []
}

func ExampleConditionSet_UnmarshalJSON() {
	var rule hypermatch.ConditionSet
	err := json.Unmarshal([]byte(`{
		"name": {"anythingBut": [{"wildcard": "TEST*"}]},
		"env":  {"prefix": "prod"}
	}`), &rule)
	if err != nil {
		panic(err)
	}

	hm := hypermatch.New[int]()
	if err := hm.AddRule(1, rule); err != nil {
		panic(err)
	}
	fmt.Println(hm.Match([]hypermatch.Property{
		{Path: "name", Values: []string{"checkout latency"}},
		{Path: "env", Values: []string{"production"}},
	}))
	// Output: [1]
}

func ExampleHyperMatch_AppendMatches() {
	hm := hypermatch.New[int]()
	for i, env := range []string{"prod", "stage", "dev"} {
		err := hm.AddRule(i, hypermatch.ConditionSet{
			{Path: "env", Pattern: hypermatch.Pattern{Type: hypermatch.PatternEquals, Value: env}},
		})
		if err != nil {
			panic(err)
		}
	}

	// Reusing the result slice makes matching allocation-free.
	var matches []int
	for _, env := range []string{"prod", "dev", "test"} {
		matches = hm.AppendMatches(matches[:0], []hypermatch.Property{{Path: "env", Values: []string{env}}})
		fmt.Println(env, matches)
	}
	// Output:
	// prod [0]
	// dev [2]
	// test []
}

func ExampleValidateRule() {
	err := hypermatch.ValidateRule(hypermatch.ConditionSet{
		{Path: "name", Pattern: hypermatch.Pattern{Type: hypermatch.PatternAnyOf, Sub: []hypermatch.Pattern{
			{Type: hypermatch.PatternEquals, Value: "a"},
			{Type: hypermatch.PatternWildcard, Value: "b**"},
		}}},
	})
	fmt.Println(errors.Is(err, hypermatch.ErrInvalidRule))
	fmt.Println(err)
	// Output:
	// true
	// hypermatch: invalid rule: condition "name": anyOf[1]: [wildcard] must not contain two consecutive wildcards
}

func ExampleHyperMatch_MatchJSON() {
	hm := hypermatch.New[string]()
	err := hm.AddRule("shop-team", hypermatch.ConditionSet{
		{Path: "alert.labels.team", Pattern: hypermatch.Pattern{Type: hypermatch.PatternEquals, Value: "shop"}},
		{Path: "status", Pattern: hypermatch.Pattern{Type: hypermatch.PatternAnyOf, Sub: []hypermatch.Pattern{
			{Type: hypermatch.PatternEquals, Value: "firing"},
			{Type: hypermatch.PatternEquals, Value: "pending"},
		}}},
	})
	if err != nil {
		panic(err)
	}

	matches, err := hm.MatchJSON([]byte(`{
		"status": "FIRING",
		"alert": {"labels": {"team": "shop", "severity": "critical"}},
		"tags": ["checkout", "backend"]
	}`))
	fmt.Println(matches, err)
	// Output: [shop-team] <nil>
}

// Example_alertRouting routes alerts to the first matching route. Specific
// routes are added before general ones, so MatchFirst picks the most
// specific route.
func Example_alertRouting() {
	routes := hypermatch.New[string]()
	add := func(route string, rule hypermatch.ConditionSet) {
		if err := routes.AddRule(route, rule); err != nil {
			panic(err)
		}
	}
	add("shop-oncall", hypermatch.ConditionSet{
		{Path: "team", Pattern: hypermatch.Pattern{Type: hypermatch.PatternEquals, Value: "shop"}},
		{Path: "severity", Pattern: hypermatch.Pattern{Type: hypermatch.PatternEquals, Value: "critical"}},
	})
	add("shop-channel", hypermatch.ConditionSet{
		{Path: "team", Pattern: hypermatch.Pattern{Type: hypermatch.PatternEquals, Value: "shop"}},
	})
	add("catch-all", hypermatch.ConditionSet{
		{Path: "alertname", Pattern: hypermatch.Pattern{Type: hypermatch.PatternExists, Value: "true"}},
	})

	for _, alert := range []string{
		`{"alertname": "CheckoutDown", "team": "shop", "severity": "critical"}`,
		`{"alertname": "SlowSearch", "team": "shop", "severity": "warning"}`,
		`{"alertname": "DiskFull", "team": "infra", "severity": "critical"}`,
	} {
		route, _, err := routes.MatchFirstJSON([]byte(alert))
		if err != nil {
			panic(err)
		}
		fmt.Println(route)
	}
	// Output:
	// shop-oncall
	// shop-channel
	// catch-all
}

// Example_subscriptions notifies users about prices below the limits they
// subscribed to.
func Example_subscriptions() {
	alerts := hypermatch.New[string]()
	subscribe := func(user, symbol, below string) {
		err := alerts.AddRule(user+": "+symbol+" below "+below, hypermatch.ConditionSet{
			{Path: "symbol", Pattern: hypermatch.Pattern{Type: hypermatch.PatternEquals, Value: symbol}},
			{Path: "price", Pattern: hypermatch.Pattern{Type: hypermatch.PatternLessThan, Value: below}},
		})
		if err != nil {
			panic(err)
		}
	}
	subscribe("anna", "ACME", "100")
	subscribe("ben", "ACME", "90")
	subscribe("carla", "GLOBEX", "50")

	fmt.Println(alerts.MatchJSON([]byte(`{"symbol": "ACME", "price": 95.5}`)))
	// Output: [anna: ACME below 100] <nil>
}

// Example_featureTargeting decides which users get a feature from their
// attributes.
func Example_featureTargeting() {
	features := hypermatch.New[string]()
	err := features.AddRule("new-checkout", hypermatch.ConditionSet{
		{Path: "country", Pattern: hypermatch.Pattern{Type: hypermatch.PatternAnyOf, Sub: []hypermatch.Pattern{
			{Type: hypermatch.PatternEquals, Value: "de"},
			{Type: hypermatch.PatternEquals, Value: "at"},
		}}},
		{Path: "age", Pattern: hypermatch.Pattern{Type: hypermatch.PatternBetween, Sub: []hypermatch.Pattern{
			{Type: hypermatch.PatternGreaterThanOrEqual, Value: "18"},
			{Type: hypermatch.PatternLessThan, Value: "65"},
		}}},
		{Path: "opt_out", Pattern: hypermatch.Pattern{Type: hypermatch.PatternExists, Value: "false"}},
	})
	if err != nil {
		panic(err)
	}

	for _, user := range []string{
		`{"country": "DE", "age": 34}`,
		`{"country": "DE", "age": 34, "opt_out": true}`,
		`{"country": "FR", "age": 34}`,
	} {
		matches, _ := features.MatchJSON([]byte(user))
		fmt.Println(matches)
	}
	// Output:
	// [new-checkout]
	// []
	// []
}

func ExampleHyperMatch_ReplaceRule() {
	hm := hypermatch.New[string]()
	for _, name := range []string{"first", "second"} {
		err := hm.AddRule(name, hypermatch.ConditionSet{
			{Path: "env", Pattern: hypermatch.Pattern{Type: hypermatch.PatternEquals, Value: "prod"}},
		})
		if err != nil {
			panic(err)
		}
	}

	// The replacement is atomic and keeps the position of "first".
	err := hm.ReplaceRule("first", hypermatch.ConditionSet{
		{Path: "env", Pattern: hypermatch.Pattern{Type: hypermatch.PatternPrefix, Value: "pr"}},
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(hm.Match([]hypermatch.Property{{Path: "env", Values: []string{"prod"}}}))
	fmt.Println(hm.Match([]hypermatch.Property{{Path: "env", Values: []string{"preview"}}}))
	// Output:
	// [first second]
	// [first]
}

func ExampleExplain() {
	rule := hypermatch.ConditionSet{
		{Path: "status", Pattern: hypermatch.Pattern{Type: hypermatch.PatternEquals, Value: "firing"}},
		{Path: "severity", Pattern: hypermatch.Pattern{Type: hypermatch.PatternAnyOf, Sub: []hypermatch.Pattern{
			{Type: hypermatch.PatternEquals, Value: "critical"},
			{Type: hypermatch.PatternEquals, Value: "warning"},
		}}},
		{Path: "owner", Pattern: hypermatch.Pattern{Type: hypermatch.PatternExists, Value: "false"}},
	}
	explanation, err := hypermatch.Explain(rule, []hypermatch.Property{
		{Path: "status", Values: []string{"FIRING"}},
		{Path: "severity", Values: []string{"info"}},
	})
	if err != nil {
		panic(err)
	}
	fmt.Print(explanation)
	// Output:
	// no match
	//   ✓ status: {"equals":"firing"} matched "FIRING"
	//   ✗ severity: {"anyOf":[{"equals":"critical"},{"equals":"warning"}]} (values ["info"])
	//       ✗ {"equals":"critical"}
	//       ✗ {"equals":"warning"}
	//   ✓ owner: {"exists":false} (absent)
}

func ExampleHyperMatch_RemoveRule() {
	hm := hypermatch.New[string]()
	for _, team := range []string{"shop", "search"} {
		err := hm.AddRule(team, hypermatch.ConditionSet{
			{Path: "team", Pattern: hypermatch.Pattern{Type: hypermatch.PatternPrefix, Value: "s"}},
		})
		if err != nil {
			panic(err)
		}
	}

	event := []hypermatch.Property{{Path: "team", Values: []string{"shop"}}}
	fmt.Println(hm.Match(event))
	hm.RemoveRule("shop")
	fmt.Println(hm.Match(event))
	// Output:
	// [shop search]
	// [search]
}
