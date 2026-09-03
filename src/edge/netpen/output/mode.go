// Package output provides netpen's JSONL writer and Bubble Tea TUI. Each run
// selects one output mode through [ResolveMode].
//
// JSON output contains records only. Callers emit the header before findings,
// close the stream with a summary, and report returned errors to stderr. The
// TUI renders a bounded feed and per-attack progress from the same records.
package output

// Mode selects JSONL or interactive output. Its zero value is [ModeJSON].
// Values support concurrent reads; mutation requires external synchronization.
type Mode int

const (
	// ModeJSON emits versioned JSONL records on stdout (the machine contract).
	ModeJSON Mode = iota
	// ModeTUI renders the interactive bubbletea findings feed.
	ModeTUI
)

// ResolveMode selects JSON when explicitFlag is nonempty or stdout is not a
// terminal. Every nonempty flag value requests JSON, including "false"; the
// caller must use an empty string when the flag is absent.
func ResolveMode(explicitFlag string, stdoutIsTTY bool) Mode {
	if explicitFlag != "" {
		return ModeJSON
	}
	if !stdoutIsTTY {
		return ModeJSON
	}
	return ModeTUI
}
