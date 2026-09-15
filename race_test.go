//go:build race

package hypermatch

// The race detector makes sync.Pool drop items at random, so allocation
// counts are not meaningful.
const raceEnabled = true
