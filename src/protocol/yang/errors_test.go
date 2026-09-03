package yang_test

import (
	"slices"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/protocol/yang"
)

// TestErrorCodesRegisteredAndStable pins every exported code to its
// wire form. Codes are append-only: a failure here means a code was
// renamed or removed, which breaks the cross-process contract — add a
// new code instead.
func TestErrorCodesRegisteredAndStable(t *testing.T) {
	want := map[errs.Code]string{
		yang.ErrCodePathParse:    "yang/path-parse",
		yang.ErrCodeValueParse:   "yang/value-parse",
		yang.ErrCodeUnionNoMatch: "yang/union-no-match",
		yang.ErrCodeValueRange:   "yang/value-range",
	}

	registered := errs.Codes()
	for code, wire := range want {
		if code.String() != wire {
			t.Errorf("code %v has wire form %q, want %q", code, code.String(), wire)
		}
		if !slices.Contains(registered, code) {
			t.Errorf("code %q is not registered with errs", wire)
		}
	}
}
