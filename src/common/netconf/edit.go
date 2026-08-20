package netconf

import (
	"context"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// edit.go: the config write path. Apply is the F2 orchestration —
// lock, edit-config, validate, commit, unlock, with discard-changes
// plus unlock on any failure — and the primitives underneath are
// exported for callers that need finer control (induced-failure lab
// tests, staged multi-edit transactions).

// Apply submits config XML (a yanggen-rendered subtree, wrapped by
// the library in <config>) through the peer's capability-selected
// edit flow:
//
//   - candidate peer: lock candidate → edit-config → validate (when
//     the peer supports it) → commit → unlock. On any failure after
//     the lock, changes are discarded and the lock released before
//     the device's error is surfaced, so the running config is
//     provably untouched (AE2).
//   - writable-running peer: lock running → edit-config
//     (rollback-on-error when advertised) → unlock.
//
// A peer with neither capability fails with [ErrCodeUnsupported].
func (s *Session) Apply(ctx context.Context, config []byte) error {
	target, ok := s.caps.editTarget()
	if !ok {
		return errs.New().Code(ErrCodeUnsupported).Msg("peer advertises neither candidate nor writable-running; NETCONF edits are unsupported")
	}
	if err := s.Lock(ctx, target); err != nil {
		return err
	}

	if err := s.applyLocked(ctx, target, config); err != nil {
		// Best-effort cleanup: the device's original error is the one
		// the caller needs; discard/unlock failures ride along as
		// attributes rather than replacing it.
		if target == Candidate {
			if derr := s.DiscardChanges(ctx); derr != nil {
				err = errs.From(err).Attr("discard_error", derr.Error()).Msg("edit failed and discard-changes also failed")
			}
		}
		if uerr := s.Unlock(ctx, target); uerr != nil {
			err = errs.From(err).Attr("unlock_error", uerr.Error()).Msg("edit failed and unlock also failed")
		}
		return err
	}

	return s.Unlock(ctx, target)
}

// applyLocked runs the edit sequence that assumes the target lock is
// held.
func (s *Session) applyLocked(ctx context.Context, target Datastore, config []byte) error {
	op := &editConfigOp{Target: dsElem(target), Config: editConfig{Inner: config}}
	if target == Running && s.caps.rollbackOnError {
		op.ErrorOption = "rollback-on-error"
	}
	if err := s.exec(ctx, "edit-config", op, nil); err != nil {
		return err
	}
	if target == Candidate {
		if s.caps.validate {
			if err := s.Validate(ctx, Candidate); err != nil {
				return err
			}
		}
		return s.Commit(ctx)
	}
	return nil
}

// EditConfig sends one raw edit-config against target without lock or
// commit orchestration — the building block for callers staging
// multiple edits under one [Session.Lock].
func (s *Session) EditConfig(ctx context.Context, target Datastore, config []byte) error {
	return s.exec(ctx, "edit-config", &editConfigOp{Target: dsElem(target), Config: editConfig{Inner: config}}, nil)
}

// Lock takes the datastore lock. A lock held elsewhere surfaces as
// the retryable [ErrCodeLockDenied].
func (s *Session) Lock(ctx context.Context, target Datastore) error {
	return s.exec(ctx, "lock", &lockOp{Target: dsElem(target)}, nil)
}

// Unlock releases the datastore lock.
func (s *Session) Unlock(ctx context.Context, target Datastore) error {
	return s.exec(ctx, "unlock", &unlockOp{Target: dsElem(target)}, nil)
}

// Validate asks the peer to validate a datastore's contents. Peers
// without the validate capability reject it with
// [ErrCodeUnsupported] client-side.
func (s *Session) Validate(ctx context.Context, source Datastore) error {
	if !s.caps.validate {
		return errs.New().Code(ErrCodeUnsupported).Msg("peer does not advertise the validate capability")
	}
	return s.exec(ctx, "validate", &validateOp{Source: dsElem(source)}, nil)
}

// Commit commits the candidate datastore to running.
func (s *Session) Commit(ctx context.Context) error {
	if !s.caps.candidate {
		return errs.New().Code(ErrCodeUnsupported).Msg("peer does not advertise the candidate capability")
	}
	return s.exec(ctx, "commit", &commitOp{}, nil)
}

// DiscardChanges reverts the candidate datastore to running.
func (s *Session) DiscardChanges(ctx context.Context) error {
	if !s.caps.candidate {
		return errs.New().Code(ErrCodeUnsupported).Msg("peer does not advertise the candidate capability")
	}
	return s.exec(ctx, "discard-changes", &discardChangesOp{}, nil)
}
