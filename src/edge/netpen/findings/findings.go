// Package findings holds netpen's typed findings model: the records the
// JSONL machine contract emits and the in-memory stream the runner
// delivers to output modes. Every record carries a schema version; the
// record kinds enumerate meta, finding, progress, summary, error, refusal,
// and the per-attack resisted/skipped/pending rollups.
//
// Wrap captured credential material in [Secret] before marshaling a
// finding's detail. Secret's JSON encoding emits only the protocol name
// and byte length. [Finding.Detail] accepts raw JSON, so callers must
// redact it before assigning it; records do not sanitize arbitrary JSON.
package findings

import (
	"encoding/json"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// SchemaVersion is the machine contract's major version. Within a
// major version evolution is additive-only: new optional fields and record
// kinds may appear; nothing is renamed, removed, or retyped.
const SchemaVersion = 1

// Kind is the record type carried on every JSONL line. The kinds enumerate
// the machine-mode surface so the output mode loses nothing the TUI shows:
// a run header, per-module findings, progress, summary, error and
// refusal, and the per-attack resisted/skipped/pending rollups.
// The zero value names no record kind. Kind values are safe for concurrent use.
type Kind string

const (
	// KindMeta is the run-header record: tool, version, legs, and the
	// start time. It is the first record of every run.
	KindMeta Kind = "meta"
	// KindFinding is a per-module typed finding payload.
	KindFinding Kind = "finding"
	// KindProgress reports an attack's phase advancement.
	KindProgress Kind = "progress"
	// KindSummary closes a run with aggregate counts.
	KindSummary Kind = "summary"
	// KindError is a runtime failure record.
	KindError Kind = "error"
	// KindRefusal is a dispatch-gate refusal (e.g. a permanent-destructive
	// attack without its opt-in acknowledgment).
	KindRefusal Kind = "refusal"
	// KindResisted is the per-attack rollup for a target that resisted an
	// attack — the run completed but the attack did not take effect.
	KindResisted Kind = "resisted"
	// KindSkipped is the per-attack rollup for an attack the gate or recon
	// evidence declined to run.
	KindSkipped Kind = "skipped"
	// KindPending is the per-attack rollup for an attack left without a
	// traversal verdict (indistinguishable from resisted without a watch
	// leg).
	KindPending Kind = "pending"
)

// Record is one JSONL record of the machine contract. Construct it with
// [NewRecord] to set the schema version, then set Time and the payload for
// Kind. Marshaling does not validate kind/payload agreement. Copies share
// payload pointers and detail bytes; callers must synchronize concurrent
// access involving writes to a record or its payload.
type Record struct {
	SchemaVersion int          `json:"schema_version"`
	Kind          Kind         `json:"kind"`
	Time          time.Time    `json:"time"`
	Attack        string       `json:"attack,omitempty"`
	Mode          string       `json:"mode,omitempty"`
	Meta          *Meta        `json:"meta,omitempty"`
	Finding       *Finding     `json:"finding,omitempty"`
	Progress      *Progress    `json:"progress,omitempty"`
	Summary       *Summary     `json:"summary,omitempty"`
	Error         *ErrorRecord `json:"error,omitempty"`
	Refusal       *Refusal     `json:"refusal,omitempty"`
	Rollup        *Rollup      `json:"rollup,omitempty"`
}

// Meta is the run-header payload: the tool identity, its version,
// the configured legs, and the run start time.
// Callers must synchronize concurrent access involving writes.
type Meta struct {
	Tool      string    `json:"tool"`
	Version   string    `json:"version"`
	AttackLeg string    `json:"attack_leg"`
	WatchLeg  string    `json:"watch_leg,omitempty"`
	Started   time.Time `json:"started"`
}

// Finding carries a protocol family's evidence as JSON. Module names the
// family (for example, arp or dhcp). Build Detail by marshaling a payload
// containing [Secret] values before assigning it; arbitrary raw JSON is
// not redacted. Nil Detail encodes as null; empty or malformed JSON causes
// record marshaling to fail. Callers must synchronize concurrent access
// involving writes, including writes to Detail's backing bytes.
type Finding struct {
	Module string          `json:"module"`
	Detail json.RawMessage `json:"detail"`
}

// Progress reports an attack's phase advancement.
// Callers must synchronize concurrent access involving writes.
type Progress struct {
	Phase  string `json:"phase"`
	Detail string `json:"detail,omitempty"`
}

// Summary closes a run with aggregate counts. Additive-only within
// schema major version 1: optional fields may appear over time
// but no field is renamed, removed, or retyped.
// Callers must synchronize concurrent access involving writes.
type Summary struct {
	Attacks  int    `json:"attacks"`
	Findings int    `json:"findings"`
	Resisted int    `json:"resisted"`
	Skipped  int    `json:"skipped"`
	Pending  int    `json:"pending"`
	Errors   int    `json:"errors"`
	SweepNet string `json:"sweep_net,omitempty"`
}

// ErrorRecord is a runtime failure record: the code (stable identity), the
// internal message, and the attack it came from when applicable.
// Callers must synchronize concurrent access involving writes.
type ErrorRecord struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Attack  string `json:"attack,omitempty"`
}

// Refusal explains why the dispatch gate declined before emitting a frame.
// The enclosing [Record] identifies the attack and mode.
// Callers must synchronize concurrent access involving writes.
type Refusal struct {
	Reason string `json:"reason"`
}

// Rollup is the per-attack resisted/skipped/pending verdict.
// Callers must synchronize concurrent access involving writes.
type Rollup struct {
	Verdict Kind   `json:"verdict"`
	Detail  string `json:"detail,omitempty"`
}

// NewRecord sets the schema version and kind, leaving Time and the payload
// unset for the caller. It does not validate kind.
func NewRecord(kind Kind) Record {
	return Record{SchemaVersion: SchemaVersion, Kind: kind}
}

// NewErrorRecord builds a [KindError] record from an error, capturing the
// stable code (via [errs.CodeOf]), the internal message, and the attack
// and mode it originated from. The code is empty when the error carries
// no code. err must be non-nil. The message is copied without redaction;
// callers must ensure it contains no captured credentials. Time remains unset.
func NewErrorRecord(err error, attack, mode string) Record {
	r := NewRecord(KindError)
	r.Attack = attack
	r.Mode = mode
	code := ""
	if c, ok := errs.CodeOf(err); ok {
		code = c.String()
	}
	r.Error = &ErrorRecord{
		Code:    code,
		Message: err.Error(),
		Attack:  attack,
	}
	return r
}

// MarshalJSON encodes the record as a JSON object without a trailing newline.
// It returns encoding errors, including malformed [Finding.Detail] or a time
// outside JSON's supported range. It does not validate record semantics or
// redact raw detail; [Secret] redaction happens when callers build that detail.
func (r Record) MarshalJSON() ([]byte, error) {
	type plain Record
	return json.Marshal(plain(r))
}

// Secret holds captured credential material for in-memory comparison. Its
// JSON encoding exposes only the protocol name and byte length. This
// redaction applies to JSON encoding; callers should log only [Secret.Protocol]
// and [Secret.Length]. Construct one with [NewSecret]. The zero value has an
// empty protocol and no material. Secret values are immutable and safe for
// concurrent use.
type Secret struct {
	protocol string
	value    []byte
}

// NewSecret copies captured credential material for the given protocol.
// Later changes to value do not affect the secret. protocol is public metadata
// and must not contain credentials; it is emitted unchanged in JSON.
func NewSecret(protocol string, value []byte) Secret {
	return Secret{protocol: protocol, value: append([]byte(nil), value...)}
}

// Protocol returns the protocol name (e.g. "snmp", "ntlm") the material
// was captured under.
func (s Secret) Protocol() string { return s.protocol }

// Length returns the byte length of the captured material.
func (s Secret) Length() int { return len(s.value) }

// Equal reports whether two secrets hold the same protocol and value.
// It is the in-memory comparison path; it does not cross a marshal
// boundary.
func (s Secret) Equal(other Secret) bool {
	if s.protocol != other.protocol || len(s.value) != len(other.value) {
		return false
	}
	for i := range s.value {
		if s.value[i] != other.value[i] {
			return false
		}
	}
	return true
}

type secretJSON struct {
	Protocol string `json:"protocol"`
	Length   int    `json:"length"`
}

// MarshalJSON emits protocol and byte length, omitting the captured value.
// It always returns a nil error.
func (s Secret) MarshalJSON() ([]byte, error) {
	return json.Marshal(secretJSON{Protocol: s.protocol, Length: len(s.value)})
}
