// Package version holds the netpen binary's build-time version metadata.
//
// The Taskfile release task injects operator-supplied VERSION, COMMIT, and
// BUILDDATE values through -ldflags -X. It does not derive them from Git or the
// clock. Omitted values keep the development defaults.
//
// Run netpen version to print the metadata. A development build prints:
//
//	netpen dev (commit none, built unknown)
//
// Metadata is read-only during normal execution. Tests that assign these
// variables must synchronize with every reader, including String.
package version

// Version is the release version label, or "dev" when not supplied. Set it via
// -ldflags '-X go.aledante.io/FlowSeer/src/edge/netpen/version.Version=...'.
var Version = "dev"

// Commit is the VCS commit hash, or "none" when not supplied. Set it via
// -ldflags '-X go.aledante.io/FlowSeer/src/edge/netpen/version.Commit=...'.
var Commit = "none"

// BuildDate is the supplied RFC3339 build timestamp, or "unknown" when omitted.
// The package does not validate the timestamp or derive it at runtime.
var BuildDate = "unknown"

// String formats netpen <version> (commit <short>, built <date>), shortening
// Commit to its first eight bytes. It adds no newline and does not sanitize
// metadata; build inputs must be single-line strings. Concurrent calls are safe
// while the metadata remains unchanged.
func String() string {
	short := Commit
	if len(short) > 8 {
		short = short[:8]
	}
	return "netpen " + Version + " (commit " + short + ", built " + BuildDate + ")"
}
