// Package integration_test assembles central and an edge agent in one process
// and drives them against each other. It is the only place both trees are
// linked, which is what makes the checks here cheap: they need no fixture of
// their own, only the binary this package already builds.
package integration_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/modules/localnet/access"
	"go.aledante.io/FlowSeer/src/services/device/internal/dispatchapi"
)

// TestRefusalCodesMatchTheLaneModule holds central's copy of the refusal codes
// to the edge access module's originals.
//
// The two sets exist because central depends on nothing else in that module
// and dispatchapi names the strings itself rather than importing it. That is a
// reasonable trade everywhere except at the point where the strings must
// agree: a Refused travels from the lane to central as a string, so a typo or
// a rename on either side is not a compile error anywhere, and the first
// symptom is central quietly declining to dispose a mutation the edge already
// refused. It leaves the row owed and re-dispatches it forever.
//
// Only the three codes that cross the wire are checked. The lane exports
// others that never reach central.
func TestRefusalCodesMatchTheLaneModule(t *testing.T) {
	t.Parallel()

	codes := []struct {
		name    string
		central string
		lane    string
	}{
		{"no-pending-wait", dispatchapi.CodeNoPendingWait, access.ErrCodeNoPendingWait.String()},
		{"unknown-device", dispatchapi.CodeUnknownDevice, access.ErrCodeUnknownDevice.String()},
		{"firmware-epoch", dispatchapi.CodeFirmwareEpoch, access.ErrCodeFirmwareEpoch.String()},
	}

	for _, c := range codes {
		// An empty central constant would equal an empty lane code and pass
		// on nothing. The lane side cannot be empty, since errs.NewCode
		// rejects a malformed name, but central's is a plain string.
		if c.central == "" {
			t.Errorf("%s: central names no code", c.name)
			continue
		}
		if c.central != c.lane {
			t.Errorf("%s: central sends %q, the lane emits %q; a Refused carrying the lane's code reaches central unrecognized",
				c.name, c.central, c.lane)
		}
	}
}
