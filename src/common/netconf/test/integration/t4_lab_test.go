//go:build yang_integration_t4

package integration

import (
	"context"
	"os"
	"testing"
	"time"

	devhw "go.aledante.io/FlowSeer/generated/go/yang/cisco-iosxe/ciscoiosxedevicehardwareoper"
	native "go.aledante.io/FlowSeer/generated/go/yang/cisco-iosxe/ciscoiosxenative"
	ietfif "go.aledante.io/FlowSeer/generated/go/yang/cisco-iosxe/ietfinterfaces"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/netconf"
	"go.aledante.io/FlowSeer/src/common/yang"
)

// dialT4 opens a lab session and closes it after fixture cleanup completes.
func dialT4(t *testing.T, target t4Target) *netconf.Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	s, err := netconf.Dial(ctx, target.Addr, netconf.Options{
		Username:              target.User,
		Password:              target.Password,
		InsecureIgnoreHostKey: true,
	})
	if err != nil {
		t.Fatalf("dial %s: %v", target.Addr, err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.Close(ctx); err != nil {
			t.Errorf("close session: %v", err)
		}
	})
	return s
}

func checkNativeUser(t *testing.T, config []byte, name string, want bool) {
	t.Helper()
	exists, err := nativeUserExists(config, name)
	if err != nil {
		t.Fatalf("decode native usernames: %v", err)
	}
	if exists != want {
		t.Fatalf("username %q exists = %t, want %t", name, exists, want)
	}
}

func cleanupNativeUser(t *testing.T, s *netconf.Session, name string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		remove := []byte(`<native xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-native">` +
			`<username xmlns:nc="urn:ietf:params:xml:ns:netconf:base:1.0" nc:operation="remove">` +
			`<name>` + name + `</name></username></native>`)
		if err := s.Apply(ctx, remove); err != nil {
			t.Errorf("remove fixture username %q: %v", name, err)
		}
	})
}

// TestT4IdentityRead reads hostname, OS version, and — via the
// device-hardware oper model — serial and model as typed values.
//
// Covers the typed identity read (IOS-XE leg). Covers conformance matrix row: nc-t4-identity
func TestT4IdentityRead(t *testing.T) {
	for _, target := range t4Targets {
		t.Run(target.Addr, func(t *testing.T) {
			s := dialT4(t, target)
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			payload, err := s.Get(ctx, native.NativeDescriptor().Path)
			if err != nil {
				t.Fatalf("get native: %v", err)
			}
			var nat native.Native
			if err := yang.UnmarshalXMLStruct(native.NativeSchema, payload, &nat); err != nil {
				t.Fatalf("decode native: %v", err)
			}
			if nat.Hostname == nil || *nat.Hostname == "" {
				t.Error("hostname missing")
			} else {
				t.Logf("hostname = %s", *nat.Hostname)
			}
			if nat.Version == nil || *nat.Version == "" {
				t.Error("OS version missing")
			} else {
				t.Logf("version = %s", *nat.Version)
			}

			hwPayload, err := s.Get(ctx, devhw.DeviceHardwareDataDescriptor().Path)
			if err != nil {
				t.Fatalf("get device-hardware: %v", err)
			}
			var hw devhw.DeviceHardwareData
			if err := yang.UnmarshalXMLStruct(devhw.DeviceHardwareDataSchema, hwPayload, &hw); err != nil {
				t.Fatalf("decode device-hardware: %v", err)
			}
			serial, model := "", ""
			if hw.DeviceHardware != nil {
				for _, inv := range hw.DeviceHardware.DeviceInventory {
					if inv.SerialNumber != nil && serial == "" {
						serial = *inv.SerialNumber
					}
					if inv.PartNumber != nil && model == "" {
						model = *inv.PartNumber
					}
				}
			}
			if serial == "" || model == "" {
				t.Errorf("serial/model missing: serial=%q model=%q", serial, model)
			} else {
				t.Logf("serial = %s, model = %s", serial, model)
			}
		})
	}
}

// TestT4InvalidEditRollback stages an invalid change to the candidate
// datastore, expects the device's rejection, and proves by read-back
// diff that running is unchanged.
//
// Covers the invalid-edit rollback proof. Covers conformance matrix row: nc-t4-invalid-rollback
func TestT4InvalidEditRollback(t *testing.T) {
	for _, target := range t4Targets {
		t.Run(target.Addr, func(t *testing.T) {
			s := dialT4(t, target)
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			before, err := s.GetConfig(ctx, netconf.Running, native.NativeDescriptor().Path)
			if err != nil {
				t.Fatalf("get-config before: %v", err)
			}
			checkNativeUser(t, before, "flowseer-t4-invalid", false)
			cleanupNativeUser(t, s, "flowseer-t4-invalid")

			// privilege is uint8 range 0..15; 99 must fail validation.
			bad := []byte(`<native xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-native">` +
				`<username><name>flowseer-t4-invalid</name><privilege>99</privilege></username></native>`)
			err = s.Apply(ctx, bad)
			if err == nil {
				t.Fatal("device accepted privilege 99; expected a validation rejection")
			}
			if code, ok := errs.CodeOf(err); !ok || code != netconf.ErrCodeRPC {
				t.Errorf("error code = %v, want %v (device rpc-error)", code, netconf.ErrCodeRPC)
			}
			t.Logf("device rejection: %v", err)

			after, err := s.GetConfig(ctx, netconf.Running, native.NativeDescriptor().Path)
			if err != nil {
				t.Fatalf("get-config after: %v", err)
			}
			if string(before) != string(after) {
				t.Error("running config changed after the rejected edit (read-back diff)")
			}
		})
	}
}

// TestT4ReversibleEditCycle applies a small change, proves it by
// read-back, reverts it, and proves the fixture username is absent again.
//
// Covers conformance matrix row: nc-t4-reversible-edit
func TestT4ReversibleEditCycle(t *testing.T) {
	for _, target := range t4Targets {
		t.Run(target.Addr, func(t *testing.T) {
			s := dialT4(t, target)
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			before, err := s.GetConfig(ctx, netconf.Running, native.NativeDescriptor().Path)
			if err != nil {
				t.Fatalf("get-config before: %v", err)
			}
			checkNativeUser(t, before, "flowseer-t4", false)
			cleanupNativeUser(t, s, "flowseer-t4")

			create := []byte(`<native xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-native">` +
				`<username><name>flowseer-t4</name><privilege>1</privilege></username></native>`)
			remove := []byte(`<native xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-native">` +
				`<username xmlns:nc="urn:ietf:params:xml:ns:netconf:base:1.0" nc:operation="delete">` +
				`<name>flowseer-t4</name></username></native>`)

			if err := s.Apply(ctx, create); err != nil {
				t.Fatalf("apply create: %v", err)
			}
			cfg, err := s.GetConfig(ctx, netconf.Running, native.NativeDescriptor().Path)
			if err != nil {
				t.Fatalf("read back: %v", err)
			}
			checkNativeUser(t, cfg, "flowseer-t4", true)

			if err := s.Apply(ctx, remove); err != nil {
				t.Fatalf("apply delete: %v", err)
			}
			cfg, err = s.GetConfig(ctx, netconf.Running, native.NativeDescriptor().Path)
			if err != nil {
				t.Fatalf("read back after delete: %v", err)
			}
			checkNativeUser(t, cfg, "flowseer-t4", false)
		})
	}
}

// TestT4InterfaceWalk runs the metrics-read Walker over the interface
// state subtree.
//
// Covers conformance matrix row: nc-t4-interface-walk
func TestT4InterfaceWalk(t *testing.T) {
	for _, target := range t4Targets {
		t.Run(target.Addr, func(t *testing.T) {
			s := dialT4(t, target)
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			walker := netconf.Walk(ctx, s, ietfif.Interfaces_InterfaceDescriptor())
			count := 0
			for row := range walker.Iter() {
				if row.Name != nil {
					count++
				}
			}
			if err := walker.Err(); err != nil {
				t.Fatalf("walk: %v", err)
			}
			if count == 0 {
				t.Error("no interfaces decoded")
			}
			t.Logf("decoded %d interfaces", count)
		})
	}
}

// TestT4RevisionDrift exercises the runtime revision-drift check:
// device-advertised module revisions are compared against the
// committed lockfile; drift is a warning, not a failure.
//
// Covers conformance matrix row: nc-t4-revision-drift
func TestT4RevisionDrift(t *testing.T) {
	lock, err := os.ReadFile("../../../../../generated/go/yang/yanggen.lock.json")
	if err != nil {
		t.Fatalf("read lockfile: %v", err)
	}
	vendored, err := yang.ParseLockfileRevisions(lock, "cisco-iosxe")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range t4Targets {
		t.Run(target.Addr, func(t *testing.T) {
			s := dialT4(t, target)
			drift := yang.DiffRevisions(vendored, s.ModuleRevisions())
			for _, d := range drift {
				t.Logf("revision drift: %s vendored %s, device %s", d.Module, d.Vendored, d.Advertised)
			}
			t.Logf("%d module(s) drifted (warn-and-proceed)", len(drift))
		})
	}
}
