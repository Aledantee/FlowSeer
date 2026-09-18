//go:build yang_integration_t4

package integration

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/protocol/gnmi"
	"go.aledante.io/FlowSeer/src/protocol/yang"
)

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

// TestT4ArubaSetCapability restores the login banner after checking Set.
// A rejected write is recorded as unsupported; a failed restoration fails
// the test because the device may still carry the test banner.
//
// Covers conformance matrix row: gn-t4-set-verdict
// Covers conformance matrix row: gn-banner-newline-normalization
func TestT4ArubaSetCapability(t *testing.T) {
	for _, target := range t4Targets {
		t.Run(target.Addr, func(t *testing.T) {
			s := dialT4(t, target)
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			banner := yang.Path{Segments: []yang.Segment{
				{Name: "system"}, {Name: "config"}, {Name: "login-banner"},
			}}

			restore, err := snapshotRestore(ctx, s, banner)
			if err != nil {
				t.Fatalf("capture login banner: %v", err)
			}
			t.Cleanup(func() {
				cleanCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer cancel()
				if err := s.Set(cleanCtx, restore); err != nil {
					t.Errorf("restore login banner: %v", err)
				}
			})

			err = s.Set(ctx, gnmi.SetRequest{Updates: []gnmi.PathValue{{Path: banner, JSON: []byte(`"flowseer-t4"`)}}})
			if err != nil {
				t.Logf("gNMI Set rejected on %s: %v", target.Addr, err)
				t.Skip("Set unsupported")
			}

			updates, err := s.Get(ctx, banner)
			if err != nil {
				t.Fatalf("read back: %v", err)
			}
			// Some devices normalize a banner by appending a trailing
			// newline (EOS does; see conformance row
			// gn-banner-newline-normalization), so a single trailing
			// newline is the value round-tripping rather than a
			// different value coming back.
			const want = "flowseer-t4"
			matches := func(got string) bool {
				return got == want || got == want+"\n"
			}
			verified := false
			for _, u := range updates {
				if u.Path.String() != banner.String() {
					continue
				}
				switch {
				case u.Value != nil && u.Value.Type.Kind == yang.TypeString && matches(u.Value.String):
					verified = true
					if u.Value.String != want {
						t.Logf("device normalized the banner: set %q, read back %q", want, u.Value.String)
					}
				case len(u.JSON) > 0:
					var got string
					if err := json.Unmarshal(u.JSON, &got); err == nil && matches(got) {
						verified = true
						if got != want {
							t.Logf("device normalized the banner: set %q, read back %q", want, got)
						}
					}
				}
			}
			if !verified {
				for _, u := range updates {
					switch {
					case u.Value != nil:
						t.Logf("read-back %s: kind %v, value %q", u.Path, u.Value.Type.Kind, u.Value.String)
					case u.JSON != nil:
						t.Logf("read-back %s: JSON %q", u.Path, u.JSON)
					default:
						t.Logf("read-back %s: no payload", u.Path)
					}
				}
				t.Errorf("Set reported success but read-back does not show the change")
			} else {
				t.Log("gNMI Set round-trips on this device")
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
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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

			// Counting updates only proves the stream did not error.
			// A payload is asserted as well, since a decoder that
			// dropped every value would still deliver updates.
			sawSync, updates, withPayload, leafLists := false, 0, 0, 0
			for ev := range stream.Iter() {
				if ev.Sync {
					sawSync = true
					continue
				}
				if !sawSync {
					continue
				}
				updates += len(ev.Updates)
				for _, u := range ev.Updates {
					switch {
					case u.Values != nil:
						leafLists++
						withPayload++
					case len(u.JSON) > 0 || u.Value != nil:
						withPayload++
					}
				}
				if withPayload > 0 {
					break
				}
			}
			if !sawSync {
				t.Errorf("no sync_response observed (stream err: %v)", stream.Err())
			}
			if updates == 0 {
				t.Errorf("no updates after sync_response (stream err: %v)", stream.Err())
			}
			if updates > 0 && withPayload == 0 {
				t.Errorf("%d update(s) after sync and none carried a payload (stream err: %v)", updates, stream.Err())
			}
			t.Logf("observed sync=%v with %d update(s), %d carrying a payload, %d a leaf-list",
				sawSync, updates, withPayload, leafLists)
		})
	}
}

// TestT4RevisionDrift compares the peer's advertised model versions
// against the committed lockfile; drift is a warning.
//
// Covers conformance matrix row: gn-t4-revision-drift
func TestT4RevisionDrift(t *testing.T) {
	lock, err := os.ReadFile("../../../../../generated/go/yang/yanggen.lock.json")
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
			drift, incomparable := yang.DiffRevisions(vendored, s.Capabilities().ModelRevisions())
			for _, d := range drift {
				t.Logf("revision drift: %s vendored %s, device %s", d.Module, d.Vendored, d.Advertised)
			}
			// OpenConfig models report their openconfig-version semver
			// over gNMI while the lockfile holds revision dates, so
			// these are unknown rather than drifted.
			for _, d := range incomparable {
				t.Logf("revision incomparable: %s vendored %s, device reports %s", d.Module, d.Vendored, d.Advertised)
			}
			t.Logf("%d model(s) drifted, %d incomparable (warn-and-proceed)", len(drift), len(incomparable))
		})
	}
}
