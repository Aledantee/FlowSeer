package catalog

import (
	"go.aledante.io/FlowSeer/src/common/errs"
)

// errs codes for netpen, declared per the errs package convention
// (KTD9): "<package>/<name>", append-only. The repo-wide AST uniqueness
// scan (src/common/errs/code_test.go) WalkDirs the whole repo and
// enforces these from day one in the nested module.

var (
	// ErrCodeUnknownBehavior marks a dispatch lookup for a behavior name
	// that has no catalog entry — a CLI/registration drift.
	ErrCodeUnknownBehavior = errs.NewCode("netpen/unknown-behavior")
	// ErrCodeWatchLegRequired marks a behavior that requires a watch leg
	// (e.g. ghost) invoked without one (R2, AE3).
	ErrCodeWatchLegRequired = errs.NewCode("netpen/watch-leg-required")
	// ErrCodeWatchLegMissing marks a named watch leg that does not exist
	// (R2: a named watch leg that does not exist fails fast for every
	// command).
	ErrCodeWatchLegMissing = errs.NewCode("netpen/watch-leg-missing")
	// ErrCodePermanentRefused marks a permanent-destructive (attack, mode)
	// pair invoked without the per-run opt-in acknowledgment (R15, AE1).
	ErrCodePermanentRefused = errs.NewCode("netpen/permanent-refused")
	// ErrCodeTeardownPartial marks a temporary-restored teardown that
	// could not complete every step — reported by name with a non-zero
	// exit (R14, AE2, KTD13).
	ErrCodeTeardownPartial = errs.NewCode("netpen/teardown-partial")
)
