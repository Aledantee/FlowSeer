// Package output is netpen's output-mode layer: the exclusive pair of a
// versioned JSONL writer (the machine contract) and a bubbletea v2 TUI (the
// interactive surface). Each run picks one; the two never mix on a single run.
//
// Mode selection follows the exit-status and output-mode seams: an explicit
// JSON flag wins; otherwise JSON when stdout is not a tty; TUI only on a tty.
// The JSONL writer owns stdout purity — records only, a run-header meta record
// first, diagnostics on stderr. The TUI renders a live findings feed and a
// per-attack progress map driven by the runner's findings stream.
package output

// Mode is the output mode a run uses. The CLI switches on this to select
// between the JSONL writer and the TUI.
type Mode int

const (
	// ModeJSON emits versioned JSONL records on stdout (the machine contract).
	ModeJSON Mode = iota
	// ModeTUI renders the interactive bubbletea findings feed.
	ModeTUI
)

// ResolveMode selects the output mode per the mode-selection seam: an explicit
// JSON flag wins; otherwise JSON when stdout is not a tty; TUI only on a tty.
// explicitFlag is the value of the --json flag (empty when unset). stdoutIsTTY
// is whether stdout is a terminal.
func ResolveMode(explicitFlag string, stdoutIsTTY bool) Mode {
	if explicitFlag != "" {
		return ModeJSON
	}
	if !stdoutIsTTY {
		return ModeJSON
	}
	return ModeTUI
}
