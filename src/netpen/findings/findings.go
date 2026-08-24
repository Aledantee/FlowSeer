// Package findings holds netpen's typed findings model: the records the
// JSONL machine contract emits (KTD10) and the in-memory stream the runner
// delivers to output modes. Every record carries a schema version; the
// record kinds enumerate meta, finding, progress, summary, error, refusal,
// and the per-attack resisted/skipped/pending rollups.
//
// The package enforces the secret-material rule (KTD9) at the model level:
// a [Secret] wraps captured credential material (a community string, a
// key, a hash) and its JSON encoding emits only the protocol name and the
// byte length — never the value. Redaction is not a convention the output
// writer remembers; it is a property of the type.
package findings

import (
	"encoding/json"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// SchemaVersion is the machine contract's major version (KTD10). Within a
// major version evolution is additive-only: new optional fields and record
// kinds may appear; nothing is renamed, removed, or retyped.
const SchemaVersion = 1

// Kind is the record type carried on every JSONL line. The kinds enumerate
// the machine-mode surface so the output mode loses nothing the TUI shows
// (KTD10): a run header, per-module findings, progress, summary, error and
// refusal, and the per-attack resisted/skipped/pending rollups.
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

// Record is one JSONL line of the machine contract. Every record carries
// the schema version; the Kind selects which optional field holds the
// payload. Records are JSON-serializable via [Record.MarshalJSON].
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

// Meta is the run-header payload (KTD10): the tool identity, its version,
// the configured legs, and the run start time.
type Meta struct {
	Tool      string    `json:"tool"`
	Version   string    `json:"version"`
	AttackLeg string    `json:"attack_leg"`
	WatchLeg  string    `json:"watch_leg,omitempty"`
	Started   time.Time `json:"started"`
}

// Finding is a per-module typed payload. The Module names the protocol
// family (arp, dhcp, stp, …); Detail carries the module-specific fields a
// behavior emits, serialized as-is. Secrets ride under the "secrets" key
// as [Secret] values, so the redaction rule applies transitively.
type Finding struct {
	Module string          `json:"module"`
	Detail json.RawMessage `json:"detail"`
}

// Progress reports an attack's phase advancement.
type Progress struct {
	Phase  string `json:"phase"`
	Detail string `json:"detail,omitempty"`
}

// Summary closes a run with aggregate counts. Additive-only within
// schema major version 1 (KTD10): optional fields may appear over time
// but no field is renamed, removed, or retyped.
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
type ErrorRecord struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Attack  string `json:"attack,omitempty"`
}

// Refusal is a dispatch-gate refusal record (AE1): the attack, the mode,
// and the reason the gate declined before any frame was emitted.
type Refusal struct {
	Reason string `json:"reason"`
}

// Rollup is the per-attack resisted/skipped/pending verdict (KTD10).
type Rollup struct {
	Verdict Kind   `json:"verdict"`
	Detail  string `json:"detail,omitempty"`
}

// NewRecord returns a Record preloaded with the schema version and the
// given kind. It is the constructor every behavior uses so the schema
// version cannot be forgotten.
func NewRecord(kind Kind) Record {
	return Record{SchemaVersion: SchemaVersion, Kind: kind}
}

// NewErrorRecord builds a [KindError] record from an error, capturing the
// stable code (via [errs.CodeOf]), the internal message, and the attack
// and mode it originated from. The code is empty when the error carries
// no code.
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

// MarshalJSON emits the record as a single JSONL line. It is the standard
// encoding/json behavior; the method exists to document that the schema
// version rides on every record and that [Secret] payloads redact
// automatically through [Secret.MarshalJSON].
func (r Record) MarshalJSON() ([]byte, error) {
	type plain Record
	return json.Marshal(plain(r))
}

// Secret wraps captured credential material — a community string, a key,
// a hash — so that the secret-material rule (KTD9) is enforced at the model
// level. Its JSON encoding emits only the protocol name and the byte
// length; the value never crosses a marshal boundary. Construct one with
// [NewSecret]; the value is held for in-memory use (logging structured
// fields, comparison) but is not serializable through any path that goes
// through [json.Marshal].
type Secret struct {
	protocol string
	value    []byte
}

// NewSecret wraps captured credential material of the given protocol.
// The value is retained for in-memory use; it never escapes through JSON
// marshaling.
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

// secretJSON is the JSON shape a Secret emits: protocol and length only.
// It is unexported because it exists solely to make MarshalJSON readable.
type secretJSON struct {
	Protocol string `json:"protocol"`
	Length   int    `json:"length"`
}

// MarshalJSON emits the secret-material rule shape: protocol and length
// only, never the value. A Secret in any struct field reachable from
// [Record.MarshalJSON] redacts through this method.
func (s Secret) MarshalJSON() ([]byte, error) {
	return json.Marshal(secretJSON{Protocol: s.protocol, Length: len(s.value)})
}
