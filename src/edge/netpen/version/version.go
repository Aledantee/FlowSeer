// Package version holds the netpen binary's build-time version metadata.
//
// The fields are injected via -ldflags '-X' at release time by the
// Taskfile `release` task: the Go linker rewrites the string
// values in place, so a release binary carries its git commit, build
// date, and semantic version without a runtime git invocation.
//
// Defaults are dev-placeholders so a bare `go build` or `go test` still
// produces a meaningful `--version` string ("dev") without requiring the
// ldflags ceremony.
package version

// Version is the semantic version string. Overridden at release via
// -ldflags '-X go.aledante.io/FlowSeer/src/edge/netpen/version.Version=...'.
var Version = "dev"

// Commit is the VCS commit hash the binary was built from. Overridden
// at release via -ldflags '-X ...version.Commit=...'.
var Commit = "none"

// BuildDate is the build timestamp (RFC3339). Overridden at release.
var BuildDate = "unknown"

// String returns a single-line version string suitable for `--version`
// output. The shape is: netpen <version> (commit <short>, built <date>).
func String() string {
	short := Commit
	if len(short) > 8 {
		short = short[:8]
	}
	return "netpen " + Version + " (commit " + short + ", built " + BuildDate + ")"
}
