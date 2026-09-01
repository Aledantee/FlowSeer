//go:build race

package differential_test

// raceEnabled reports whether the binary was built with the race
// detector. The corpus pass walks every vendored MIB through gosmi in one
// goroutine, because gosmi keeps its module universe in package-level
// state, so there is no concurrency for the detector to find and the only
// thing it contributes is a slowdown past the default test timeout.
const raceEnabled = true
