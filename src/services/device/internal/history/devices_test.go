package history

import (
	"context"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
)

func TestMirrorDevicesRejectsABadIdentifierBeforeSendingAnything(t *testing.T) {
	cfg := testConfig(t, "")
	cfg.Endpoints = []string{"127.0.0.1:1"}
	store, err := Open(cfg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	const tenant = "018f0000-0000-7000-8000-0000000000a1"
	const device = "00000000-0000-7000-8000-0000000000d1"

	tests := []struct {
		name    string
		tenant  string
		devices []Device
		want    string
	}{
		{"tenant", "a1", []Device{{ID: device}}, "tenant"},
		{"device id", tenant, []Device{{ID: device}, {ID: "d2"}}, "invalid device id"},
		{"location id", tenant, []Device{{ID: device, LocationID: "rack-1"}}, "invalid location id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := store.MirrorDevices(context.Background(), tt.tenant, tt.devices)

			if code, _ := errs.CodeOf(err); code != ErrCodeInsert {
				t.Fatalf("code = %v (err %v), want %v", code, err, ErrCodeInsert)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not name %q, so it may be a connection failure instead", err, tt.want)
			}
		})
	}
}

func TestMirrorDevicesWithNoDevicesSendsNothing(t *testing.T) {
	cfg := testConfig(t, "")
	cfg.Endpoints = []string{"127.0.0.1:1"}
	store, err := Open(cfg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.MirrorDevices(context.Background(), "018f0000-0000-7000-8000-0000000000a1", nil); err != nil {
		t.Errorf("an empty mirror returned %v, want nil without touching the server", err)
	}
}
