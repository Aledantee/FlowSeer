package syslog_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/syslog"
)

func TestCloneAndRawPresence(t *testing.T) {
	raw := []byte{0xff, 0, 'x'}
	r := syslog.Record{Content: raw, Raw: &raw, StructuredData: []syslog.Element{{ID: "example", Parameters: []syslog.Parameter{{Name: "x", Value: raw}}}}}
	clone, err := r.Clone(syslog.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	raw[0] = 'y'
	if clone.Content[0] != 0xff || (*clone.Raw)[0] != 0xff || clone.StructuredData[0].Parameters[0].Value[0] != 0xff {
		t.Fatal("clone aliases original")
	}
	for _, enabled := range []bool{false, true} {
		r = syslog.Record{}
		if enabled {
			empty := []byte{}
			r.Raw = &empty
		}
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(b, []byte(`"_raw"`)) != enabled {
			t.Fatalf("raw presence: %s", b)
		}
	}
}

func TestCloneCollectionBudget(t *testing.T) {
	for _, count := range []int{1, 500, 50000} {
		params := make([]syslog.Parameter, count)
		for i := range params {
			params[i].Name = "a"
		}
		r := syslog.Record{StructuredData: []syslog.Element{{ID: "x", Parameters: params}}}
		got, err := r.Clone(syslog.Limits{MaxBytes: 512 << 10, MaxParameters: 50000, MetadataBytes: 1024})
		if count == 1 {
			if err != nil || len(got.StructuredData) != 1 {
				t.Fatal(err)
			}
		} else if !errors.Is(err, syslog.ErrLimit) {
			t.Fatalf("count %d: got %v", count, err)
		}
	}
}
