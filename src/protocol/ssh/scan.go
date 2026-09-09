package ssh

import "regexp"

// Prompt is one candidate line a caller expects to see at the tail of
// a command's output: the point where the remote shell is ready for
// the next command. Name is opaque to this package — a caller uses it
// to detect a privilege-level transition (e.g. a different Prompt
// matching after "enable" than matched before it) without this
// package knowing what a privilege level is.
type Prompt struct {
	// Name identifies which prompt matched, returned as
	// Result.MatchedPrompt. Never interpreted by this package.
	Name string
	// Pattern matches the prompt text. The scan takes the earliest match
	// anywhere in the accumulated output, so anchor it (`(?m)` with `$`,
	// and a shape no sibling prompt shares): an unanchored pattern, or one
	// a line of ordinary device output satisfies, produces a false command
	// boundary and a partial transcript that reads as a complete one.
	Pattern *regexp.Regexp
}

// scanKind distinguishes what scanPrompt matched.
type scanKind int

const (
	scanNone scanKind = iota
	scanMore
	scanPromptMatch
)

// scanResult reports where in a scanned buffer a match occurred:
// outputEnd is how much of the buffer, from the start, is real
// command output to keep; consumed is the total prefix (output plus
// the matched marker or prompt text itself) to remove from the ring
// so a later scan never sees it again.
type scanResult struct {
	kind       scanKind
	outputEnd  int
	consumed   int
	promptName string
}

// scanPrompt looks for the earliest of a pagination marker (more) or
// one of prompts in buf. When both could match, the one starting
// earlier in the stream wins, since that is the one the remote shell
// actually produced first. more may be nil to disable pagination
// handling. scanPrompt never panics and every returned offset satisfies
// 0 <= outputEnd <= consumed <= len(buf).
func scanPrompt(buf []byte, prompts []Prompt, more *regexp.Regexp) (scanResult, bool) {
	var moreLoc []int
	if more != nil {
		moreLoc = more.FindIndex(buf)
	}

	promptStart, promptEnd := -1, -1
	var promptName string
	for _, p := range prompts {
		if p.Pattern == nil {
			continue
		}
		loc := p.Pattern.FindIndex(buf)
		if loc == nil {
			continue
		}
		if promptStart == -1 || loc[0] < promptStart {
			promptStart, promptEnd = loc[0], loc[1]
			promptName = p.Name
		}
	}

	switch {
	case moreLoc != nil && (promptStart == -1 || moreLoc[0] <= promptStart):
		return scanResult{kind: scanMore, outputEnd: moreLoc[0], consumed: moreLoc[1]}, true
	case promptStart != -1:
		return scanResult{kind: scanPromptMatch, outputEnd: promptStart, consumed: promptEnd, promptName: promptName}, true
	default:
		return scanResult{}, false
	}
}
