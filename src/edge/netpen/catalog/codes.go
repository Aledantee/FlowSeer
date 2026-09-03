package catalog

import (
	"go.aledante.io/FlowSeer/src/common/errs"
)

var (
	// ErrCodeUnknownBehavior marks a dispatch lookup for a behavior name
	// that has no catalog entry — a CLI/registration drift.
	ErrCodeUnknownBehavior = errs.NewCode("netpen/unknown-behavior")
	// ErrCodeWatchLegRequired marks a behavior that requires a watch leg
	// (e.g. ghost) invoked without one.
	ErrCodeWatchLegRequired = errs.NewCode("netpen/watch-leg-required")
	// ErrCodeWatchLegMissing marks a named watch leg that does not exist;
	// a named-but-absent watch leg fails fast for every command.
	ErrCodeWatchLegMissing = errs.NewCode("netpen/watch-leg-missing")
	// ErrCodePermanentRefused marks a permanent-destructive (attack, mode)
	// pair invoked without the per-run opt-in acknowledgment.
	ErrCodePermanentRefused = errs.NewCode("netpen/permanent-refused")
	// ErrCodeTeardownPartial marks a temporary-restored teardown that
	// could not complete every step — reported by name with a non-zero
	// exit.
	ErrCodeTeardownPartial = errs.NewCode("netpen/teardown-partial")

	// ErrCodeWPADPortCollision marks a rogue WPAD proxy that could not
	// bind its embedded PAC listener because the chosen port is already
	// in use. The behavior reports a named error rather than crashing.
	ErrCodeWPADPortCollision = errs.NewCode("netpen/wpad-port-collision")
)
