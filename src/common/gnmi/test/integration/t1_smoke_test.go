//go:build yang_integration_t1

package integration

import (
	"context"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/gnmi"
	"go.aledante.io/FlowSeer/src/common/yang"
	fixturemain "go.aledante.io/FlowSeer/src/common/yang/cmd/yanggen/testdata/golden/fixture/fixturemain"
)

func TestT1CapabilitiesAndEncoding(t *testing.T) {
	s := dialT1(t)
	caps := s.Capabilities()
	if len(caps.Models) == 0 || caps.Models[0].Name != "fixture-main" {
		t.Fatalf("models = %+v", caps.Models)
	}
	if s.Encoding() != "JSON_IETF" {
		t.Fatalf("encoding = %q", s.Encoding())
	}
}

func TestT1GetIdentityLeaves(t *testing.T) {
	s := dialT1(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	updates, err := s.Get(ctx, yang.Path{Segments: []yang.Segment{{Name: "servers"}}})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(updates) < 4 {
		t.Fatalf("updates = %d, want the 2×2 fixture leaves", len(updates))
	}
}

// TestT1SubscribeOnceWalk assembles rows from a real ONCE stream via
// the generated descriptor.
//
// Covers conformance matrix row: gn-subscribe-once-snapshot
func TestT1SubscribeOnceWalk(t *testing.T) {
	s := dialT1(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	walker := gnmi.Walk(ctx, s, fixturemain.Servers_ServerDescriptor(), gnmi.WatchOptions{})
	rows := map[string]uint16{}
	for row := range walker.Iter() {
		if row.Name != nil && row.Port != nil {
			rows[*row.Name] = *row.Port
		}
	}
	if err := walker.Err(); err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(rows) != 2 || rows["edge-2"] != 9090 {
		t.Fatalf("rows = %v", rows)
	}
}

// TestT1SubscribeStreamWatch observes the target's periodic port
// increment as Modified events after a sync-gated cold start.
//
// Covers conformance matrix row: gn-stream-sync-cold-start
func TestT1SubscribeStreamWatch(t *testing.T) {
	s := dialT1(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	w, err := gnmi.Watch(ctx, s, fixturemain.Servers_ServerDescriptor(), gnmi.WatchOptions{})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer func() { _ = w.Close() }()

	ch := make(chan yang.WatchEvent[fixturemain.Servers_Server, fixturemain.Servers_ServerKey], 64)
	go func() {
		defer close(ch)
		for ev := range w.Iter() {
			ch <- ev
		}
	}()

	next := func() yang.WatchEvent[fixturemain.Servers_Server, fixturemain.Servers_ServerKey] {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("watcher ended early: %v", w.Err())
			}
			return ev
		case <-time.After(30 * time.Second):
			t.Fatal("timed out waiting for event")
		}
		panic("unreachable")
	}

	added := map[string]bool{}
	for i := 0; i < 2; i++ {
		ev := next()
		if ev.Kind != yang.Added {
			t.Fatalf("cold-start event = %+v", ev)
		}
		added[ev.Key.Name] = true
	}
	if !added["edge-1"] || !added["edge-2"] {
		t.Fatalf("cold start rows = %v", added)
	}

	ev := next()
	if ev.Kind != yang.Modified || ev.Key.Name != "edge-1" {
		t.Fatalf("event = %+v, want Modified edge-1 (periodic increment)", ev)
	}
}

func TestT1SetRoundTrips(t *testing.T) {
	s := dialT1(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	target := yang.Path{Segments: []yang.Segment{
		{Name: "servers"},
		{Name: "server", Keys: []yang.KeyValue{{Name: "name", Value: "edge-2"}}},
		{Name: "port"},
	}}
	restore, err := snapshotRestore(ctx, s, target)
	if err != nil {
		t.Fatalf("capture fixture port: %v", err)
	}
	t.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.Set(cleanCtx, restore); err != nil {
			t.Errorf("restore fixture port: %v", err)
		}
	})

	if err := s.Set(ctx, gnmi.SetRequest{Updates: []gnmi.PathValue{{Path: target, JSON: []byte("7777")}}}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	walker := gnmi.Walk(ctx, s, fixturemain.Servers_ServerDescriptor(), gnmi.WatchOptions{})
	rows := map[string]uint16{}
	for row := range walker.Iter() {
		if row.Name != nil && row.Port != nil {
			rows[*row.Name] = *row.Port
		}
	}
	if err := walker.Err(); err != nil {
		t.Fatalf("walk: %v", err)
	}
	if rows["edge-2"] != 7777 {
		t.Fatalf("Set not visible in read-back: %v", rows)
	}
}
