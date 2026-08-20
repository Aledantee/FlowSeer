//go:build yang_integration_t4

package integration

import (
	"context"
	"testing"
	"time"

	ocplat "go.aledante.io/FlowSeer/generated/go/yang/ruckus-icx/openconfigplatform"
	ocsys "go.aledante.io/FlowSeer/generated/go/yang/ruckus-icx/openconfigsystem"
	"go.aledante.io/FlowSeer/src/common/restconf"
	"go.aledante.io/FlowSeer/src/common/yang"
)

// dialT4 opens one lab session. Every t4 write is a small reversible
// change with explicit cleanup; devices are never left modified, even
// on failure paths.
func dialT4(t *testing.T, target t4Target) *restconf.Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	s, err := restconf.Dial(ctx, target.BaseURL, restconf.Options{
		Username:              target.User,
		Password:              target.Password,
		InsecureSkipTLSVerify: true,
	})
	if err != nil {
		t.Fatalf("dial %s: %v", target.BaseURL, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestT4IdentityRead reads hostname, OS version, serial, and model as
// typed values via the generated openconfig bindings.
//
// Covers AE1 (ICX leg). Covers conformance matrix row: rc-t4-identity
func TestT4IdentityRead(t *testing.T) {
	for _, target := range t4Targets {
		t.Run(target.BaseURL, func(t *testing.T) {
			s := dialT4(t, target)
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			sysPayload, err := s.Get(ctx, ocsys.SystemDescriptor().Path, restconf.GetOptions{})
			if err != nil {
				t.Fatalf("get system: %v", err)
			}
			var system ocsys.System
			if err := yang.UnmarshalJSON7951Struct(ocsys.SystemSchema, sysPayload, &system); err != nil {
				t.Fatalf("decode system: %v", err)
			}
			if system.State == nil || system.State.Hostname == nil || *system.State.Hostname == "" {
				t.Error("hostname missing from /system/state")
			} else {
				t.Logf("hostname = %s", *system.State.Hostname)
			}

			serial, model, version := "", "", ""
			walker := restconf.Walk(ctx, s, ocplat.Components_ComponentDescriptor())
			for row := range walker.Iter() {
				if row.State == nil {
					continue
				}
				if row.State.SerialNo != nil && serial == "" {
					serial = *row.State.SerialNo
				}
				if row.State.PartNo != nil && model == "" {
					model = *row.State.PartNo
				}
				if row.State.SoftwareVersion != nil && version == "" {
					version = *row.State.SoftwareVersion
				}
			}
			if err := walker.Err(); err != nil {
				t.Fatalf("walk components: %v", err)
			}
			if serial == "" || model == "" {
				t.Errorf("serial/model missing: serial=%q model=%q version=%q", serial, model, version)
			} else {
				t.Logf("serial = %s, model = %s, version = %s", serial, model, version)
			}
		})
	}
}

// TestT4ReversibleEdit applies a reversible login-banner change and
// proves both the edit and the revert by read-back (R12's ICX leg).
//
// Covers conformance matrix row: rc-t4-reversible-edit
func TestT4ReversibleEdit(t *testing.T) {
	for _, target := range t4Targets {
		t.Run(target.BaseURL, func(t *testing.T) {
			s := dialT4(t, target)
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			banner := yang.Path{Segments: []yang.Segment{
				{Module: "openconfig-system", Name: "system"},
				{Name: "config"},
				{Name: "login-banner"},
			}}
			original, err := s.Get(ctx, banner, restconf.GetOptions{})
			if err != nil {
				t.Fatalf("capture original banner: %v", err)
			}
			restore := func() {
				cleanCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer cancel()
				if original == nil {
					_ = s.Delete(cleanCtx, banner)
					return
				}
				_, _ = s.Put(cleanCtx, banner, original)
			}
			t.Cleanup(restore)

			res, err := s.Put(ctx, banner, []byte(`{"openconfig-system:login-banner":"flowseer-t4"}`))
			if err != nil {
				t.Fatalf("put banner: %v", err)
			}
			t.Logf("conditional write used If-Match: %v", res.UsedIfMatch)
			readBack, err := s.Get(ctx, banner, restconf.GetOptions{})
			if err != nil {
				t.Fatalf("read back: %v", err)
			}
			if string(readBack) == string(original) {
				t.Fatal("read-back shows no change")
			}
			restore()
			reverted, err := s.Get(ctx, banner, restconf.GetOptions{})
			if err != nil {
				t.Fatalf("read back after revert: %v", err)
			}
			if string(reverted) != string(original) {
				t.Errorf("revert incomplete: original %s, now %s", original, reverted)
			}
		})
	}
}

// TestT4DepthFieldsSupport verifies the plan's ICX assumption: are
// the depth/fields query parameters honored? Either answer is
// recorded; an ignored parameter is the client-side-pruning fallback,
// not a failure.
//
// Covers conformance matrix row: rc-t4-depth-fields
func TestT4DepthFieldsSupport(t *testing.T) {
	for _, target := range t4Targets {
		t.Run(target.BaseURL, func(t *testing.T) {
			s := dialT4(t, target)
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			full, err := s.Get(ctx, ocsys.SystemDescriptor().Path, restconf.GetOptions{})
			if err != nil {
				t.Fatalf("full get: %v", err)
			}
			shallow, err := s.Get(ctx, ocsys.SystemDescriptor().Path, restconf.GetOptions{Depth: 2})
			if err != nil {
				t.Logf("depth parameter rejected: %v (client-side pruning fallback applies)", err)
				return
			}
			switch {
			case len(shallow) < len(full):
				t.Logf("depth honored: full %dB, depth=2 %dB", len(full), len(shallow))
			default:
				t.Logf("depth ignored by device: full %dB, depth=2 %dB (client-side pruning fallback applies)", len(full), len(shallow))
			}
		})
	}
}
