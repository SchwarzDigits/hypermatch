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
