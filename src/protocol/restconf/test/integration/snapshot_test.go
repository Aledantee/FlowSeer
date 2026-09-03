package integration

import (
	"encoding/json"
	"fmt"
	"testing"
)

type descriptionSnapshot struct {
	Description *string `json:"description"`
	Enabled     *bool   `json:"enabled"`
}

func decodeDescriptionSnapshot(payload []byte) (descriptionSnapshot, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return descriptionSnapshot{}, err
	}
	raw, ok := envelope["openconfig-interfaces:config"]
	if !ok {
		raw = envelope["config"]
	}
	var snapshot descriptionSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return descriptionSnapshot{}, err
	}
	if snapshot.Enabled == nil {
		return descriptionSnapshot{}, fmt.Errorf("interface enabled state is missing")
	}
	return snapshot, nil
}

func decodeDescription(payload []byte) (*string, error) {
	if payload == nil {
		return nil, nil
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, err
	}
	raw, ok := envelope["openconfig-interfaces:description"]
	if !ok {
		raw = envelope["description"]
	}
	var description *string
	if err := json.Unmarshal(raw, &description); err != nil {
		return nil, err
	}
	if description == nil {
		return nil, fmt.Errorf("description payload is null")
	}
	return description, nil
}

func TestDescriptionSnapshot(t *testing.T) {
	tests := []struct {
		name        string
		payload     string
		wantEnabled bool
		wantPresent bool
		wantErr     bool
	}{
		{name: "enabled and empty description", payload: `{"openconfig-interfaces:config":{"enabled":true,"description":""}}`, wantEnabled: true, wantPresent: true},
		{name: "disabled and absent description", payload: `{"openconfig-interfaces:config":{"enabled":false}}`},
		{name: "unqualified wrapper", payload: `{"config":{"enabled":true}}`, wantEnabled: true},
		{name: "missing enabled", payload: `{"openconfig-interfaces:config":{"description":"uplink"}}`, wantErr: true},
		{name: "null enabled", payload: `{"openconfig-interfaces:config":{"enabled":null}}`, wantErr: true},
		{name: "missing wrapper", payload: `{}`, wantErr: true},
		{name: "malformed payload", payload: `{`, wantErr: true},
		{name: "wrong enabled type", payload: `{"config":{"enabled":"true"}}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeDescriptionSnapshot([]byte(tt.payload))
			if (err != nil) != tt.wantErr {
				t.Fatalf("got error %v, want error=%t", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got.Enabled == nil || *got.Enabled != tt.wantEnabled {
				t.Errorf("got enabled %v, want %t", got.Enabled, tt.wantEnabled)
			}
			if (got.Description != nil) != tt.wantPresent {
				t.Errorf("got description presence %t, want %t", got.Description != nil, tt.wantPresent)
			}
		})
	}
}

func TestDescriptionReadBack(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		want    string
		absent  bool
		wantErr bool
	}{
		{name: "absent resource", absent: true},
		{name: "empty description", payload: []byte(`{"openconfig-interfaces:description":""}`)},
		{name: "unqualified description", payload: []byte(`{"description":"uplink"}`), want: "uplink"},
		{name: "missing leaf", payload: []byte(`{}`), wantErr: true},
		{name: "malformed payload", payload: []byte(`{`), wantErr: true},
		{name: "null description", payload: []byte(`{"description":null}`), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeDescription(tt.payload)
			if (err != nil) != tt.wantErr {
				t.Fatalf("got error %v, want error=%t", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if (got == nil) != tt.absent {
				t.Fatalf("got absent=%t, want %t", got == nil, tt.absent)
			}
			if got != nil && *got != tt.want {
				t.Errorf("got description %q, want %q", *got, tt.want)
			}
		})
	}
}
