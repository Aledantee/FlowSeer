package findings_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
)

var errCodeFixture = errs.NewCode("findings-test/fixture")

// TestRecordDetailJSON checks the raw evidence boundary, including failures
// that an output writer must propagate instead of emitting a partial record.
func TestRecordDetailJSON(t *testing.T) {
	for _, tt := range []struct {
		name    string
		detail  json.RawMessage
		want    string
		wantErr bool
	}{
		{name: "unset", want: "null"},
		{name: "object", detail: json.RawMessage(`{"status":"observed"}`), want: `{"status":"observed"}`},
		{name: "empty", detail: json.RawMessage{}, wantErr: true},
		{name: "malformed", detail: json.RawMessage(`{"unfinished":`), wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := findings.NewRecord(findings.KindFinding)
			r.Finding = &findings.Finding{Module: "fixture", Detail: tt.detail}
			data, err := r.MarshalJSON()
			if tt.wantErr {
				if err == nil || data != nil {
					t.Fatalf("MarshalJSON = (%s, %v), want (nil, error)", data, err)
				}
				var marshalErr *json.MarshalerError
				if !errors.As(err, &marshalErr) {
					t.Fatalf("error = %T, want *json.MarshalerError", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("marshal record: %v", err)
			}
			var decoded findings.Record
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("unmarshal record: %v", err)
			}
			if decoded.Finding == nil || string(decoded.Finding.Detail) != tt.want {
				t.Fatalf("finding = %+v, want detail %s", decoded.Finding, tt.want)
			}
		})
	}
}

// TestNewErrorRecord preserves the internal message and stable code through
// ordinary wrapping so consumers can classify a failure without parsing text.
func TestNewErrorRecord(t *testing.T) {
	coded := errs.New().Code(errCodeFixture).Msg("fixture failure")
	for _, tt := range []struct {
		name string
		err  error
		code string
	}{
		{name: "plain", err: errors.New("plain failure")},
		{name: "coded", err: coded, code: errCodeFixture.String()},
		{name: "wrapped", err: fmt.Errorf("run fixture: %w", coded), code: errCodeFixture.String()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := findings.NewErrorRecord(tt.err, "fixture", "probe")
			if r.SchemaVersion != findings.SchemaVersion || r.Kind != findings.KindError {
				t.Fatalf("record header = (%d, %q), want (%d, %q)", r.SchemaVersion, r.Kind, findings.SchemaVersion, findings.KindError)
			}
			if r.Attack != "fixture" || r.Mode != "probe" {
				t.Errorf("record origin = (%q, %q), want (fixture, probe)", r.Attack, r.Mode)
			}
			if r.Error == nil {
				t.Fatal("missing error payload")
			}
			if r.Error.Code != tt.code || r.Error.Message != tt.err.Error() || r.Error.Attack != "fixture" {
				t.Errorf("error payload = %+v, want code %q, message %q, attack fixture", r.Error, tt.code, tt.err.Error())
			}
		})
	}
}
