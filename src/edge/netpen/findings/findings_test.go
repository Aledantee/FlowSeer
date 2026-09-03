package findings

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestSchemaVersionIsOne pins the major-version contract: the machine
// contract starts at 1.
func TestSchemaVersionIsOne(t *testing.T) {
	if SchemaVersion != 1 {
		t.Fatalf("SchemaVersion = %d, want 1", SchemaVersion)
	}
}

// TestNewRecordCarriesSchemaVersion proves the constructor cannot produce
// a record that forgets the schema version — the rule every behavior
// relies on.
func TestNewRecordCarriesSchemaVersion(t *testing.T) {
	r := NewRecord(KindFinding)
	if r.SchemaVersion != SchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", r.SchemaVersion, SchemaVersion)
	}
	if r.Kind != KindFinding {
		t.Fatalf("Kind = %q, want %q", r.Kind, KindFinding)
	}
}

// TestRecordMarshalJSONEmitsSchemaVersion pins the machine-contract
// requirement that every JSONL line carries the schema version.
func TestRecordMarshalJSONEmitsSchemaVersion(t *testing.T) {
	r := NewRecord(KindMeta)
	r.Time = time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	r.Meta = &Meta{
		Tool: "netpen", Version: "v0.1.0-dev", AttackLeg: "eth0", Started: r.Time,
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v, _ := got["schema_version"].(float64); v != float64(SchemaVersion) {
		t.Fatalf("schema_version = %v, want %d", got["schema_version"], SchemaVersion)
	}
	if k, _ := got["kind"].(string); k != "meta" {
		t.Fatalf("kind = %q, want meta", k)
	}
}

// TestKindsEnumerate checks that the declared record kinds have distinct
// wire values.
func TestKindsEnumerate(t *testing.T) {
	want := []Kind{
		KindMeta, KindFinding, KindProgress, KindSummary,
		KindError, KindRefusal, KindResisted, KindSkipped, KindPending,
	}
	seen := make(map[Kind]bool)
	for _, k := range want {
		if seen[k] {
			t.Fatalf("duplicate kind %q in enumeration", k)
		}
		seen[k] = true
	}
}

// TestSecretMarshalRedactsValue is the secret-material rule proof:
// a Secret's JSON encoding carries the protocol and the length only; the
// raw value never escapes through marshaling.
func TestSecretMarshalRedactsValue(t *testing.T) {
	const cred = "supersecret-community-string"
	s := NewSecret("snmp", []byte(cred))

	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// The value must not appear in the encoding.
	if strings.Contains(string(b), cred) {
		t.Fatalf("secret value leaked into JSON: %s", b)
	}

	var got secretJSON
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Protocol != "snmp" {
		t.Errorf("protocol = %q, want snmp", got.Protocol)
	}
	if got.Length != len(cred) {
		t.Errorf("length = %d, want %d", got.Length, len(cred))
	}
}

// TestSecretInRecordRedactsValue proves the redaction rule holds
// transitively when a Secret rides inside a finding's Detail payload.
func TestSecretInRecordRedactsValue(t *testing.T) {
	s := NewSecret("ntlm", []byte("P@ssw0rdhash!"))
	detail, err := json.Marshal(struct {
		Host    string
		Secrets []Secret
	}{Host: "10.0.0.1", Secrets: []Secret{s}})
	if err != nil {
		t.Fatalf("marshal detail: %v", err)
	}
	r := NewRecord(KindFinding)
	r.Attack = "llmnr"
	r.Finding = &Finding{Module: "llmnr", Detail: detail}

	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	if strings.Contains(string(b), "P@ssw0rdhash!") {
		t.Fatalf("secret value leaked through record JSON: %s", b)
	}
	if !strings.Contains(string(b), `"protocol":"ntlm"`) {
		t.Fatalf("protocol name missing from record JSON: %s", b)
	}
	if !strings.Contains(string(b), `"length":13`) {
		t.Fatalf("length missing from record JSON: %s", b)
	}
}

// TestSecretEqual proves the in-memory comparison path, which does not
// cross a marshal boundary.
func TestSecretEqual(t *testing.T) {
	a := NewSecret("snmp", []byte("abc"))
	b := NewSecret("snmp", []byte("abc"))
	c := NewSecret("snmp", []byte("abd"))
	d := NewSecret("ntlm", []byte("abc"))
	if !a.Equal(b) {
		t.Error("equal secrets not equal")
	}
	if a.Equal(c) {
		t.Error("different values reported equal")
	}
	if a.Equal(d) {
		t.Error("different protocols reported equal")
	}
}

// TestSecretProtocolAndLength proves the accessors expose only the safe
// metadata, never the value.
func TestSecretProtocolAndLength(t *testing.T) {
	s := NewSecret("snmp", []byte("xyz"))
	if s.Protocol() != "snmp" {
		t.Errorf("Protocol = %q, want snmp", s.Protocol())
	}
	if s.Length() != 3 {
		t.Errorf("Length = %d, want 3", s.Length())
	}
}
