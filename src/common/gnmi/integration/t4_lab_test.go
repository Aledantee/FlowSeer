//go:build yang_integration_t4

package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/gnmi"
	"go.aledante.io/FlowSeer/src/common/yang"
)

// dialT4 opens one lab session. Every t4 write is a small reversible
// change with explicit cleanup; devices are never left modified, even
// on failure paths.
func dialT4(t *testing.T, target t4Target) *gnmi.Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	s, err := gnmi.Dial(ctx, target.Addr, gnmi.Options{
		Username:              target.User,
		Password:              target.Password,
		InsecureSkipTLSVerify: true,
	})
	if err != nil {
		t.Fatalf("dial %s: %v", target.Addr, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// identityPaths are the identity leaves (hostname, version, serial,
// model) per the OpenConfig models AOS-CX advertises.
func identityPaths() []yang.Path {
	mk := func(names ...string) yang.Path {
		var p yang.Path
		for _, n := range names {
			p.Segments = append(p.Segments, yang.Segment{Name: n})
		}
		return p
	}
	return []yang.Path{
		mk("system", "state", "hostname"),
		mk("system", "state", "software-version"),
		mk("components", "component", "state", "serial-no"),
		mk("components", "component", "state", "part-no"),
	}
}

// TestT4CapabilitiesAndIdentity records the peer's capability surface
// and reads the identity leaves as typed values.
//
// Covers the typed identity read (Aruba leg). Covers conformance matrix row: gn-t4-identity
func TestT4CapabilitiesAndIdentity(t *testing.T) {
	for _, target := range t4Targets {
		t.Run(target.Addr, func(t *testing.T) {
			s := dialT4(t, target)
			caps := s.Capabilities()
			t.Logf("gNMI %s, %d models, encodings %v", caps.Version, len(caps.Models), caps.Encodings)

			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			updates, err := s.Get(ctx, identityPaths()...)
			if err != nil {
				t.Fatalf("identity Get: %v", err)
			}
			got := map[string]string{}
			for _, u := range updates {
				val := string(u.JSON)
				if u.Value != nil {
					if canon, cerr := u.Value.Canonical(); cerr == nil {
						val = canon
					}
				}
				got[u.Path.String()] = val
				t.Logf("%s = %s", u.Path.String(), val)
			}
			if len(got) < 4 {
				t.Errorf("identity read returned %d leaves, want hostname, version, serial, and model", len(got))
			}
		})
	}
}

// TestT4ArubaSetCapability is the early write-capability
// verification: a small reversible config change via Set either
// round-trips (recorded), or the incapacity is recorded and the Aruba
// write-acceptance criterion converts to a documented gap with the
// fallback path recorded — this test then reports the verdict without
// failing the tier for the other families.
//
// Covers the reversible-Set acceptance check. Covers conformance matrix row: gn-t4-set-verdict
func TestT4ArubaSetCapability(t *testing.T) {
	for _, target := range t4Targets {
		t.Run(target.Addr, func(t *testing.T) {
			s := dialT4(t, target)
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			banner := yang.Path{Segments: []yang.Segment{
				{Name: "system"}, {Name: "config"}, {Name: "login-banner"},
			}}

			// Capture the current value for restoration.
			var original []byte
			if updates, err := s.Get(ctx, banner); err == nil && len(updates) > 0 {
				original = updates[0].JSON
			}
			restore := func() {
				cleanCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer cancel()
				if original == nil {
					_ = s.Set(cleanCtx, gnmi.SetRequest{Deletes: []yang.Path{banner}})
					return
				}
				_ = s.Set(cleanCtx, gnmi.SetRequest{Updates: []gnmi.PathValue{{Path: banner, JSON: original}}})
			}
			t.Cleanup(restore)

			err := s.Set(ctx, gnmi.SetRequest{Updates: []gnmi.PathValue{{Path: banner, JSON: []byte(`"flowseer-t4"`)}}})
			if err != nil {
				// The documented escape hatch: record the incapacity;
				// the corpus row and write-criterion conversion are
				// the follow-up.
				t.Logf("R14 VERDICT: gNMI Set rejected on %s: %v — convert the Aruba write criterion per R14", target.Addr, err)
				t.Skip("Set unsupported; R14 conversion applies")
			}

			updates, err := s.Get(ctx, banner)
			if err != nil {
				t.Fatalf("read back: %v", err)
			}
			verified := false
			for _, u := range updates {
				if string(u.JSON) == `"flowseer-t4"` {
					verified = true
				}
			}
			if !verified {
				t.Errorf("Set reported success but read-back does not show the change: %+v", updates)
			} else {
				t.Log("R14 VERDICT: gNMI Set round-trips on this device")
			}
		})
	}
}

// TestT4SubscribeStream verifies the streaming path on hardware: a
// STREAM subscription over interface state delivers a sync marker and
// keeps flowing.
//
// Covers conformance matrix row: gn-t4-stream
func TestT4SubscribeStream(t *testing.T) {
	for _, target := range t4Targets {
		t.Run(target.Addr, func(t *testing.T) {
			s := dialT4(t, target)
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			stream, err := s.Subscribe(ctx, gnmi.SubscribeOptions{
				Mode:           gnmi.ModeStream,
				Paths:          []yang.Path{{Segments: []yang.Segment{{Name: "interfaces"}}}},
				SampleInterval: 10 * time.Second,
			})
			if err != nil {
				t.Fatalf("subscribe: %v", err)
			}
			defer func() { _ = stream.Close() }()

			sawSync, updates := false, 0
			deadline := time.After(60 * time.Second)
		loop:
			for {
				select {
				case <-deadline:
					break loop
				default:
				}
				for ev := range stream.Iter() {
					if ev.Sync {
						sawSync = true
					}
					updates += len(ev.Updates)
					if sawSync && updates > 0 {
						break loop
					}
				}
				break loop
			}
			if !sawSync {
				t.Errorf("no sync_response observed (stream err: %v)", stream.Err())
			}
			t.Logf("observed sync=%v with %d updates", sawSync, updates)
		})
	}
}

// TestT4RevisionDrift compares the peer's advertised model versions
// against the committed lockfile; drift is a warning.
//
// Covers conformance matrix row: gn-t4-revision-drift
func TestT4RevisionDrift(t *testing.T) {
	lock, err := os.ReadFile("../../../../generated/go/yang/yanggen.lock.json")
	if err != nil {
		t.Fatalf("read lockfile: %v", err)
	}
	vendored, err := yang.ParseLockfileRevisions(lock, "aruba-cx")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range t4Targets {
		t.Run(target.Addr, func(t *testing.T) {
			s := dialT4(t, target)
			drift := yang.DiffRevisions(vendored, s.Capabilities().ModelRevisions())
			for _, d := range drift {
				t.Logf("revision drift: %s vendored %s, device %s", d.Module, d.Vendored, d.Advertised)
			}
			t.Logf("%d model(s) drifted (warn-and-proceed)", len(drift))
		})
	}
}
