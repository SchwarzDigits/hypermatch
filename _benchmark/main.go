package main

import (
	"benchmark/candidates"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"time"
)

const (
	numberOfRules      = 100000
	eventCheckDuration = 5 * time.Second
)

type Candidate interface {
	Name() string
	AddRule(number int, modulo int)
	Match(number int, modulo int) int
}

var candidateNames = []string{"hypermatch", "hypermatch-matchjson", "hypermatch-json", "quamina"}

func newCandidate(name string, wildcard bool) Candidate {
	switch name {
	case "hypermatch":
		return candidates.NewHypermatch(wildcard)
	case "hypermatch-matchjson":
		return candidates.NewHypermatchMatchJSON(wildcard)
	case "hypermatch-json":
		return candidates.NewHypermatchJson(wildcard)
	case "quamina":
		return candidates.NewQuamina(wildcard)
	}
	log.Fatalf("unknown candidate %q", name)
	return nil
}

func main() {
	candidate := flag.String("candidate", "", "measure only this candidate and print its table row")
	wildcard := flag.Bool("wildcard", true, "add a wildcard condition to every rule")
	flag.Parse()

	if *candidate != "" {
		measure(newCandidate(*candidate, *wildcard), *wildcard)
		return
	}

	// Every measurement runs in its own process, so that the heap left
	// behind by one candidate cannot slow down the next one. Quamina's rules
	// are measured with and without the wildcard condition, because many
	// shellstyle patterns slow it down considerably.
	fmt.Println("| Rules | Candidate | Adding 100,000 rules | Events per second | Matches per event | Memory per rule |")
	fmt.Println("|---|---|---:|---:|---:|---:|")
	for _, wc := range []bool{true, false} {
		for _, name := range candidateNames {
			cmd := exec.Command(os.Args[0], "-candidate", name, "-wildcard="+strconv.FormatBool(wc))
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				log.Fatalf("measuring %s: %v", name, err)
			}
		}
	}
}

func measure(c Candidate, wildcard bool) {
	scenario := "without wildcard"
	if wildcard {
		scenario = "with wildcard"
	}
	log.Printf("---Starting with %s, %s\n", c.Name(), scenario)
	beforeHeap := heapAlloc()
	beforeAddingRules := time.Now()
	for i := 0; i < numberOfRules; i++ {
		c.AddRule(i, numberOfRules/10)
	}
	adding := time.Since(beforeAddingRules)
	log.Printf("adding %d rules took %.5fs\n", numberOfRules, adding.Seconds())

	// The heap of the rules, before matching leaves garbage behind. This also
	// collects the garbage of adding them, so the events are matched in the
	// steady state.
	perRule := (int64(heapAlloc()) - int64(beforeHeap)) / numberOfRules
	log.Printf("the rules take %d bytes each\n", perRule)

	events, matches, elapsed := runEvents(c)
	fmt.Printf("| %s | %s | %.2f s | %s | %.0f | %d |\n", scenario, c.Name(), adding.Seconds(),
		thousands(int64(float64(events)/elapsed.Seconds())), float64(matches)/float64(events), perRule)
}

// heapAlloc returns the live heap after a full garbage collection.
func heapAlloc() uint64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

func runEvents(c Candidate) (events, matches int, elapsed time.Duration) {
	beforeCheckingEvents := time.Now()
	lastPrint := beforeCheckingEvents
	ctx, cancel := context.WithTimeout(context.Background(), eventCheckDuration)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return events, matches, time.Since(beforeCheckingEvents)
		default:
			matches += c.Match(events, numberOfRules/10)
			events++

			if time.Since(lastPrint).Seconds() >= 1 {
				log.Printf("processed %d events with %d matches in %.5fs -> %.5f evt/s\n", events, matches, time.Since(beforeCheckingEvents).Seconds(), float64(events)/time.Since(beforeCheckingEvents).Seconds())
				lastPrint = time.Now()
			}
		}
	}
}

// thousands formats n with commas as thousands separators.
func thousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
