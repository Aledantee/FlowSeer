//go:build yang_integration_t4

package integration

import (
	"context"
	"encoding/json"
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
			// A RESTCONF GET on a container returns it wrapped in its
			// module-qualified name (RFC 8040), e.g.
			// {"openconfig-system:system": {...}}; the typed decoder
			// expects the bare container object, so unwrap first.
			sysBody := unwrapContainer(t, sysPayload, "openconfig-system:system", "system")
			var system ocsys.System
			if err := yang.UnmarshalJSON7951Struct(ocsys.SystemSchema, sysBody, &system); err != nil {
				t.Fatalf("decode system: %v", err)
			}
			// FastIron 10.0.10g mirrors hostname into config, not state
			// (state is returned empty), so accept either. This is the
			// concrete typed-value proof for AE1 end to end:
			// dial -> host-meta discovery -> GET -> JSON7951 decode.
			hostname := ""
			if system.State != nil && system.State.Hostname != nil {
				hostname = *system.State.Hostname
			}
			if hostname == "" && system.Config != nil && system.Config.Hostname != nil {
				hostname = *system.Config.Hostname
			}
			if hostname == "" {
				t.Error("hostname missing from both /system/state and /system/config")
			} else {
				t.Logf("hostname = %s", hostname)
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
			// Conformance observation, not a failure. FastIron 10.0.10g
			// does not populate openconfig serial-no/part-no/
			// software-version over RESTCONF: /system/state is empty and
			// the single platform component carries no standard identity
			// leaves. Model is exposed only as
			// icx-openconfig-platform-aug:switch-model, an augmentation
			// absent from the vendored 9.0.x YANG corpus (device/corpus
			// version skew), so the typed bindings cannot surface it;
			// serial and software-version are CLI-only on this device.
			// The official ICX system deviation removes only dns/server
			// port, so these are unimplemented runtime state, not a
			// modeled deviation. Tracked in the conformance corpus row
			// rc-t4-identity.
			t.Logf("platform identity via RESTCONF: serial=%q model=%q version=%q (documented FastIron surface gap; see rc-t4-identity)", serial, model, version)
		})
	}
}

// TestT4ReversibleEdit applies a reversible interface-description
// change and proves both the edit and the revert by read-back
// (R12's ICX leg).
//
// FastIron 10.0.10g rejects writes to openconfig-system config leaves
// (login-banner/hostname return "invalid internal value"); the
// interface description is the device's documented, non-disruptive
// writable leaf (purely cosmetic, no forwarding impact). The port's
// enabled state is captured and restored so the port is never toggled.
//
// Covers conformance matrix row: rc-t4-reversible-edit
func TestT4ReversibleEdit(t *testing.T) {
	const port = "ethernet 1/1/1"
	for _, target := range t4Targets {
		t.Run(target.BaseURL, func(t *testing.T) {
			s := dialT4(t, target)
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			cfg := yang.Path{Segments: []yang.Segment{
				{Module: "openconfig-interfaces", Name: "interfaces"},
				{Name: "interface", Keys: []yang.KeyValue{{Name: "name", Value: port}}},
				{Name: "config"},
			}}
			descPath := yang.Path{Segments: append(append([]yang.Segment{}, cfg.Segments...), yang.Segment{Name: "description"})}

			// Capture original description + enabled so the revert is exact
			// and the port's up/down state is never altered.
			origCfgRaw, err := s.Get(ctx, cfg, restconf.GetOptions{})
			if err != nil {
				t.Fatalf("capture original config: %v", err)
			}
			var origCfg struct {
				Config struct {
					Description string `json:"description"`
					Enabled     bool   `json:"enabled"`
				} `json:"openconfig-interfaces:config"`
			}
			if err := json.Unmarshal(origCfgRaw, &origCfg); err != nil {
				t.Fatalf("parse original config: %v", err)
			}
			origDesc := origCfg.Config.Description

			writeDesc := func(c context.Context, desc string) (restconf.WriteResult, error) {
				body := map[string]any{"openconfig-interfaces:config": map[string]any{
					"name":        port,
					"type":        "iana-if-type:ethernetCsmacd",
					"description": desc,
					"enabled":     origCfg.Config.Enabled,
				}}
				b, _ := json.Marshal(body)
				return s.Patch(c, cfg, b)
			}
			readDesc := func(c context.Context) string {
				raw, err := s.Get(c, descPath, restconf.GetOptions{})
				if err != nil {
					return ""
				}
				var d struct {
					Description string `json:"openconfig-interfaces:description"`
				}
				_ = json.Unmarshal(raw, &d)
				return d.Description
			}

			// Always restore the original description, even on failure.
			t.Cleanup(func() {
				cleanCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer cancel()
				_, _ = writeDesc(cleanCtx, origDesc)
			})

			const testDesc = "flowseer-t4"
			res, err := writeDesc(ctx, testDesc)
			if err != nil {
				t.Fatalf("patch description: %v", err)
			}
			t.Logf("conditional write used If-Match: %v", res.UsedIfMatch)
			if got := readDesc(ctx); got != testDesc {
				t.Fatalf("read-back after edit = %q, want %q", got, testDesc)
			}

			if _, err := writeDesc(ctx, origDesc); err != nil {
				t.Fatalf("revert description: %v", err)
			}
			if got := readDesc(ctx); got != origDesc {
				t.Errorf("revert incomplete: original %q, now %q", origDesc, got)
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

// unwrapContainer strips the RFC 8040 module-qualified envelope a
// RESTCONF GET puts around a container, returning the bare object.
func unwrapContainer(t *testing.T, payload []byte, keys ...string) []byte {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(payload, &obj); err != nil {
		t.Fatalf("payload is not a JSON object: %v", err)
	}
	for _, k := range keys {
		if raw, ok := obj[k]; ok {
			return raw
		}
	}
	return payload
}
