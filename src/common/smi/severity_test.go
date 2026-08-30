package smi_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/smi"
)

func TestSeverityRoundTrips(t *testing.T) {
	levels := smi.Severities()
	if len(levels) != 7 {
		t.Fatalf("Severities() returned %d levels, want 7", len(levels))
	}

	for i, s := range levels {
		if int(s) != i {
			t.Errorf("Severities()[%d] = %d, want the scale in order", i, s)
		}
		if !s.Valid() {
			t.Errorf("%v is not Valid", s)
		}

		got, err := smi.ParseSeverity(s.String())
		if err != nil {
			t.Errorf("ParseSeverity(%q): %v", s.String(), err)

			continue
		}
		if got != s {
			t.Errorf("ParseSeverity(%q) = %v, want %v", s.String(), got, s)
		}
	}
}

func TestSeverityTagsAreStable(t *testing.T) {
	want := map[smi.Severity]string{
		smi.SeverityInternal: "internal",
		smi.SeverityFatal:    "fatal",
		smi.SeverityError:    "error",
		smi.SeverityMinor:    "minor",
		smi.SeverityChange:   "change",
		smi.SeverityWarning:  "warning",
		smi.SeverityInfo:     "info",
	}

	for s, tag := range want {
		if got := s.String(); got != tag {
			t.Errorf("%d.String() = %q, want %q — these tags reach committed baselines", uint8(s), got, tag)
		}
	}
}

func TestSeverityOffTheScale(t *testing.T) {
	off := smi.Severity(9)

	if off.Valid() {
		t.Error("severity 9 reports Valid")
	}
	if got := off.String(); got != "severity(9)" {
		t.Errorf("String() = %q, want %q", got, "severity(9)")
	}
	if _, err := smi.ParseSeverity("nonsense"); err == nil {
		t.Error("ParseSeverity accepted a tag that is not on the scale")
	}
}

// A baseline waves a diagnostic through, so the levels that mean a file
// or a definition was lost have to be justified in writing and the noisy
// ones must not be.
func TestNeedsBaselineReason(t *testing.T) {
	needs := map[smi.Severity]bool{
		smi.SeverityInternal: true,
		smi.SeverityFatal:    true,
		smi.SeverityError:    true,
		smi.SeverityMinor:    false,
		smi.SeverityChange:   false,
		smi.SeverityWarning:  false,
		smi.SeverityInfo:     false,
	}

	for s, want := range needs {
		if got := s.NeedsBaselineReason(); got != want {
			t.Errorf("%v.NeedsBaselineReason() = %v, want %v", s, got, want)
		}
	}
}
