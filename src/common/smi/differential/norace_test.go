//go:build !race

package differential_test

// raceEnabled is the ordinary build's answer; see race_test.go for why
// the corpus pass cares.
const raceEnabled = false
