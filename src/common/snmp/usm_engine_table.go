package snmp

import (
	"context"
	"sync"

	"go.aledante.io/ae"
)

// usm_engine_table.go is the native trap listener's USM security table.
// It resolves an inbound v3 notification's credentials by DIRECT
// lookup on the composite (authoritativeEngineID, userName) key — never by
// trial-decryption. Two facts are kept
// deliberately separate:
//
//   - credentials (localized keys, level): immutable; replace-on-duplicate
//     at the composite-key granularity.
//   - the per-engine §3.2 replay baseline (boots / latest time): mutable; it
//     persists across a credential replace so a passphrase rotation does not
//     silently reset replay protection.

// ErrEngineNeedsID is returned by register when the USMConfig has no
// EngineID — a notification receiver cannot key (or verify) a sender without
// its authoritative engineID.
var ErrEngineNeedsID = ae.Msg("RegisterEngine requires a non-empty EngineID")

// recvBaseline is the per-sender RFC 3414 §3.2 non-authoritative replay
// baseline: the highest (boots, time) the receiver has accepted. It is
// updated only after a full verify+decrypt succeeds (baseline-after-success
// ordering), under its own lock so two concurrent valid notifications from
// one engine advance it monotonically (research I4 TOCTOU).
type recvBaseline struct {
	mu         sync.Mutex
	boots      int32
	latestTime int32
	known      bool
}

// checkAndUpdate applies the §3.2 non-authoritative time check and, on
// accept, advances the baseline. First authenticated contact is
// accept-and-learn. Thereafter a regressed boots, or equal boots with a time
// more than 150s behind the latest seen, is stale (rejected); a future time
// is accepted (the remote clock may lead) and advances the baseline; a boots
// increase (engine reboot) resets the time baseline.
func (b *recvBaseline) checkAndUpdate(boots, etime int32) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.known {
		b.boots, b.latestTime, b.known = boots, etime, true
		return true
	}
	switch {
	case boots < b.boots:
		return false
	case boots > b.boots:
		b.boots, b.latestTime = boots, etime
		return true
	default: // boots == b.boots
		if etime < b.latestTime-150 {
			return false
		}
		if etime > b.latestTime {
			b.latestTime = etime
		}
		return true
	}
}

// engineTable holds the credentials and replay baselines for every
// registered authoritative engine, keyed by (engineID, userName).
type engineTable struct {
	mu        sync.RWMutex
	creds     map[string]*usmContext
	baselines map[string]*recvBaseline
	logCtx    context.Context
}

// newEngineTable returns an empty table. logCtx is used to derive per-entry
// crypto-context logging (e.g. the 3DES weak-key warning).
func newEngineTable(ctx context.Context) *engineTable {
	return &engineTable{
		creds:     make(map[string]*usmContext),
		baselines: make(map[string]*recvBaseline),
		logCtx:    context.WithoutCancel(ctx),
	}
}

// engineKey is the composite (engineID, userName) map key. The NUL
// separator cannot appear in a userName-vs-engineID boundary ambiguously
// because engineID is length-known here (raw octets).
func engineKey(engineID []byte, userName string) string {
	return string(engineID) + "\x00" + userName
}

// register derives the localized keys for cfg and installs them at the
// composite (EngineID, userName) key, replacing any prior credential for the
// same pair. The replay baseline for that pair is preserved across the
// replace. cfg must already be valid and carry an EngineID.
func (t *engineTable) register(cfg USMConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if len(cfg.EngineID) == 0 {
		return ErrEngineNeedsID
	}
	u, err := newUSMContext(t.logCtx, cfg)
	if err != nil {
		return err
	}
	key := engineKey(cfg.EngineID, cfg.Username)

	t.mu.Lock()
	defer t.mu.Unlock()
	t.creds[key] = u
	// Preserve the baseline; create one lazily only if absent.
	if _, ok := t.baselines[key]; !ok {
		t.baselines[key] = &recvBaseline{}
	}
	return nil
}

// lookup resolves the credentials and replay baseline for a sender by direct
// composite-key lookup (never trial-decryption). ok is false on a miss — the
// clean "no engine" drop path.
func (t *engineTable) lookup(engineID []byte, userName string) (*usmContext, *recvBaseline, bool) {
	key := engineKey(engineID, userName)
	t.mu.RLock()
	defer t.mu.RUnlock()
	u, ok := t.creds[key]
	if !ok {
		return nil, nil, false
	}
	return u, t.baselines[key], true
}

// seed registers every entry from the ListenTraps-time USM table.
func (t *engineTable) seed(entries []USMConfig) error {
	for _, cfg := range entries {
		if err := t.register(cfg); err != nil {
			return err
		}
	}
	return nil
}
