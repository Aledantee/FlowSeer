package integration

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/gnmi"
	"go.aledante.io/FlowSeer/src/protocol/yang"
)

type snapshotReader interface {
	Get(context.Context, ...yang.Path) ([]gnmi.Update, error)
}

func snapshotRestore(ctx context.Context, s snapshotReader, path yang.Path) (gnmi.SetRequest, error) {
	updates, err := s.Get(ctx, path)
	if err != nil {
		return gnmi.SetRequest{}, fmt.Errorf("read original value: %w", err)
	}
	if len(updates) == 0 {
		return gnmi.SetRequest{Deletes: []yang.Path{path}}, nil
	}

	var original *gnmi.Update
	for i := range updates {
		if updates[i].Path.String() != path.String() {
			continue
		}
		if original != nil {
			return gnmi.SetRequest{}, fmt.Errorf("multiple values returned for original path")
		}
		original = &updates[i]
	}
	if original == nil {
		return gnmi.SetRequest{}, fmt.Errorf("original path missing from nonempty response")
	}
	payloads := 0
	for _, carried := range []bool{original.JSON != nil, original.Value != nil, original.Values != nil} {
		if carried {
			payloads++
		}
	}
	if payloads != 1 {
		return gnmi.SetRequest{}, fmt.Errorf("original value must have exactly one payload, got %d", payloads)
	}

	return gnmi.SetRequest{Updates: []gnmi.PathValue{{
		Path: path, JSON: original.JSON, Value: original.Value, Values: original.Values,
	}}}, nil
}

type snapshotStub struct {
	updates []gnmi.Update
	err     error
}

func (s snapshotStub) Get(context.Context, ...yang.Path) ([]gnmi.Update, error) {
	return s.updates, s.err
}

func TestSnapshotRestore(t *testing.T) {
	path := yang.Path{Segments: []yang.Segment{{Name: "login-banner"}}}
	other := yang.Path{Segments: []yang.Segment{{Name: "hostname"}}}
	typed := &yang.Value{Type: yang.Type{Kind: yang.TypeString}, String: "original"}
	list := []yang.Value{{Type: yang.Type{Kind: yang.TypeString}, String: "a"}}
	json := []byte(`"original"`)
	readErr := errors.New("snapshot unavailable")

	tests := []struct {
		name    string
		reader  snapshotStub
		want    gnmi.SetRequest
		wantErr bool
	}{
		{name: "read failure", reader: snapshotStub{err: readErr}, wantErr: true},
		{name: "confirmed absent", want: gnmi.SetRequest{Deletes: []yang.Path{path}}},
		{
			name:   "JSON value",
			reader: snapshotStub{updates: []gnmi.Update{{Path: path, JSON: json}}},
			want:   gnmi.SetRequest{Updates: []gnmi.PathValue{{Path: path, JSON: json}}},
		},
		{
			name:   "typed scalar",
			reader: snapshotStub{updates: []gnmi.Update{{Path: path, Value: typed}}},
			want:   gnmi.SetRequest{Updates: []gnmi.PathValue{{Path: path, Value: typed}}},
		},
		{
			name:   "matching leaf after unrelated leaves",
			reader: snapshotStub{updates: []gnmi.Update{{Path: other, JSON: []byte(`"host"`)}, {Path: path, JSON: json}}},
			want:   gnmi.SetRequest{Updates: []gnmi.PathValue{{Path: path, JSON: json}}},
		},
		{name: "unrelated response", reader: snapshotStub{updates: []gnmi.Update{{Path: other, JSON: json}}}, wantErr: true},
		{name: "missing payload", reader: snapshotStub{updates: []gnmi.Update{{Path: path}}}, wantErr: true},
		{
			name:   "leaf-list value",
			reader: snapshotStub{updates: []gnmi.Update{{Path: path, Values: list}}},
			want:   gnmi.SetRequest{Updates: []gnmi.PathValue{{Path: path, Values: list}}},
		},
		{
			name:   "empty leaf-list is a payload, not an absent one",
			reader: snapshotStub{updates: []gnmi.Update{{Path: path, Values: []yang.Value{}}}},
			want:   gnmi.SetRequest{Updates: []gnmi.PathValue{{Path: path, Values: []yang.Value{}}}},
		},
		{name: "ambiguous payload", reader: snapshotStub{updates: []gnmi.Update{{Path: path, JSON: json, Value: typed}}}, wantErr: true},
		{name: "ambiguous payload with a leaf-list", reader: snapshotStub{updates: []gnmi.Update{{Path: path, JSON: json, Values: list}}}, wantErr: true},
		{name: "duplicate path", reader: snapshotStub{updates: []gnmi.Update{{Path: path, JSON: json}, {Path: path, JSON: json}}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := snapshotRestore(t.Context(), tt.reader, path)
			if (err != nil) != tt.wantErr {
				t.Fatalf("got error %v, want error=%t", err, tt.wantErr)
			}
			if tt.reader.err != nil && !errors.Is(err, tt.reader.err) {
				t.Errorf("got error %v, want wrapped %v", err, tt.reader.err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got request %#v, want %#v", got, tt.want)
			}
		})
	}
}
