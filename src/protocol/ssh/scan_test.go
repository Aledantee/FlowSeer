package ssh

import (
	"regexp"
	"testing"
)

func testPrompts() []Prompt {
	return []Prompt{
		{Name: "user", Pattern: regexp.MustCompile(`(?m)>\s*$`)},
		{Name: "privileged", Pattern: regexp.MustCompile(`(?m)#\s*$`)},
	}
}

func TestScanPromptTable(t *testing.T) {
	more := regexp.MustCompile(`--More--`)
	prompts := testPrompts()

	cases := []struct {
		name       string
		buf        string
		wantOK     bool
		wantKind   scanKind
		wantName   string
		wantOutEnd int
	}{
		{
			name:   "no match",
			buf:    "show version\r\nsome output line\r\n",
			wantOK: false,
		},
		{
			name:       "privileged prompt matches",
			buf:        "output line\r\nswitch#",
			wantOK:     true,
			wantKind:   scanPromptMatch,
			wantName:   "privileged",
			wantOutEnd: len("output line\r\nswitch"),
		},
		{
			name:       "user prompt matches before privileged text appears",
			buf:        "output line\r\nswitch>",
			wantOK:     true,
			wantKind:   scanPromptMatch,
			wantName:   "user",
			wantOutEnd: len("output line\r\nswitch"),
		},
		{
			name:       "more marker wins over a later prompt",
			buf:        "page one\r\n--More--\r\nswitch#",
			wantOK:     true,
			wantKind:   scanMore,
			wantOutEnd: len("page one\r\n"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, ok := scanPrompt([]byte(tc.buf), prompts, more)
			if ok != tc.wantOK {
				t.Fatalf("scanPrompt() ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if res.kind != tc.wantKind {
				t.Errorf("kind = %v, want %v", res.kind, tc.wantKind)
			}
			if res.kind == scanPromptMatch && res.promptName != tc.wantName {
				t.Errorf("promptName = %q, want %q", res.promptName, tc.wantName)
			}
			if res.outputEnd != tc.wantOutEnd {
				t.Errorf("outputEnd = %d, want %d", res.outputEnd, tc.wantOutEnd)
			}
			if res.consumed < res.outputEnd || res.consumed > len(tc.buf) {
				t.Errorf("consumed = %d out of bounds for outputEnd=%d len(buf)=%d", res.consumed, res.outputEnd, len(tc.buf))
			}
		})
	}
}

// FuzzScanPrompt proves the scanner never panics and never reports a
// position outside the scanned buffer, for arbitrary input against a
// fixed set of prompt and pagination patterns.
func FuzzScanPrompt(f *testing.F) {
	seeds := []string{
		"",
		"switch#",
		"switch>",
		"page one\r\n--More--\r\nswitch#",
		"no prompt here at all",
		"###>>>--More--#>",
		"\x00\x01\x02#",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	prompts := testPrompts()
	more := regexp.MustCompile(`--More--`)

	f.Fuzz(func(t *testing.T, buf string) {
		res, ok := scanPrompt([]byte(buf), prompts, more)
		if !ok {
			return
		}
		if res.outputEnd < 0 || res.outputEnd > len(buf) {
			t.Fatalf("outputEnd = %d out of bounds for len(buf)=%d", res.outputEnd, len(buf))
		}
		if res.consumed < res.outputEnd || res.consumed > len(buf) {
			t.Fatalf("consumed = %d out of bounds (outputEnd=%d, len=%d)", res.consumed, res.outputEnd, len(buf))
		}
	})
}
