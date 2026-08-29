package errs

import (
	"errors"
	"strings"
	"testing"
)

// A stack is captured once, at the origin.
func TestStackCapturedOnceAtOrigin(t *testing.T) {
	origin := New().Msg("origin")
	wrapped := Wrap(Wrap(origin, "middle"), "outer")

	if got, want := len(stacks(origin)), 1; got != want {
		t.Fatalf("origin holds %d stacks, want %d", got, want)
	}
	if got, want := len(stacks(wrapped)), 1; got != want {
		t.Errorf("twice-wrapped chain holds %d stacks, want %d", got, want)
	}
}

func TestSentinelsCaptureNoStack(t *testing.T) {
	sentinel := Msg("sentinel")

	if got := len(stacks(sentinel)); got != 0 {
		t.Errorf("sentinel holds %d stacks, want 0", got)
	}
	if got := len(stacks(Msgf("sentinel %d", 1))); got != 0 {
		t.Errorf("formatted sentinel holds %d stacks, want 0", got)
	}

	wrapped := Wrap(sentinel, "context")
	if got, want := len(stacks(wrapped)), 1; got != want {
		t.Errorf("wrapped sentinel holds %d stacks, want %d — the wrap site is the origin", got, want)
	}
}

// Independently created origins each keep their stack, and the wrapper that
// joins them captures none.
func TestJoinedOriginsKeepTheirStacks(t *testing.T) {
	left := New().Msg("left")
	right := New().Msg("right")

	err := Wrap(errors.Join(left, right), "both legs failed")

	if got, want := len(stacks(err)), 2; got != want {
		t.Errorf("joined tree holds %d stacks, want %d", got, want)
	}
	if got := len(err.(*Error).stack); got != 0 {
		t.Errorf("wrapper captured %d program counters, want 0", got)
	}
}

func TestStackFramesNameTheCallSite(t *testing.T) {
	err := originForStackTest()

	frames := stacks(err)[0].frames()
	if len(frames) == 0 {
		t.Fatal("captured stack symbolizes to no frames")
	}
	if !strings.Contains(frames[0], "originForStackTest") {
		t.Errorf("first frame = %q, want the origin call site", frames[0])
	}
	for _, frame := range frames {
		if strings.Contains(frame, "errs.Builder.") || strings.Contains(frame, "errs.capture") {
			t.Errorf("frame %q exposes errs internals", frame)
		}
	}
}

func TestWrapStackFramesNameTheWrapSite(t *testing.T) {
	err := wrapForStackTest(Msg("sentinel"))

	frames := stacks(err)[0].frames()
	if len(frames) == 0 {
		t.Fatal("captured stack symbolizes to no frames")
	}
	if !strings.Contains(frames[0], "wrapForStackTest") {
		t.Errorf("first frame = %q, want the wrap call site", frames[0])
	}
}

// A stack-carrying error exposes symbolized frames in its log
// record, the only surface stacks reach today.
func TestLogValueExposesStack(t *testing.T) {
	record := logRecord(t, originForStackTest())

	frames, ok := record[stackKey].([]any)
	if !ok {
		t.Fatalf("logged stack = %v, want a frame list", record[stackKey])
	}
	if len(frames) == 0 {
		t.Fatal("logged stack is empty")
	}

	first, ok := frames[0].(string)
	if !ok || !strings.Contains(first, "originForStackTest") {
		t.Errorf("first logged frame = %v, want the origin call site", frames[0])
	}

	if _, ok := logRecord(t, Msg("sentinel"))[stackKey]; ok {
		t.Error("sentinel logged a stack field")
	}
}

func TestLogValueExposesJoinedStacks(t *testing.T) {
	err := Wrap(errors.Join(New().Msg("left"), New().Msg("right")), "both legs failed")

	origins, ok := logRecord(t, err)[stackKey].(map[string]any)
	if !ok {
		t.Fatalf("logged stack = %v, want numbered origins", logRecord(t, err)[stackKey])
	}
	if got, want := len(origins), 2; got != want {
		t.Errorf("logged %d stack origins, want %d", got, want)
	}
}

func originForStackTest() error {
	return New().Attr("port", 161).Msg("origin")
}

func wrapForStackTest(err error) error {
	return Wrapf(err, "dial %s", "10.0.0.1:161")
}
