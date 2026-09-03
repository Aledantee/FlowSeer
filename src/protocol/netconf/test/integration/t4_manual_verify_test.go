//go:build yang_integration_t4

package integration

import (
	"context"
	"os"
	"testing"
	"time"

	ietfif "go.aledante.io/FlowSeer/generated/go/yang/cisco-iosxe/ietfinterfaces"
	"go.aledante.io/FlowSeer/src/protocol/netconf"
	"go.aledante.io/FlowSeer/src/protocol/yang"
)

// TestT4WatcherObservesInducedChange is the one-change-per-row
// watcher contract on hardware: a Watcher
// over the interface subtree must emit exactly one Modified for an
// interface whose state the operator changes during the window.
//
// The change is operator-induced (shut/no-shut an interface on the
// lab device while the test runs), so the test only runs when
// YANG_T4_INDUCE=1 accompanies the target env — the rest of the t4
// tier stays hands-off.
//
// Covers the induced-change watch (hardware leg). Covers conformance matrix row: nc-t4-watch-induced
func TestT4WatcherObservesInducedChange(t *testing.T) {
	if os.Getenv("YANG_T4_INDUCE") != "1" {
		t.Skip("set YANG_T4_INDUCE=1 and change one interface during the observation window")
	}
	for _, target := range t4Targets {
		t.Run(target.Addr, func(t *testing.T) {
			s := dialT4(t, target)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			w := netconf.Watch(ctx, s, ietfif.Interfaces_InterfaceDescriptor(), yang.WatchConfig{Interval: 10 * time.Second})

			t.Log("watching /interfaces; change one interface once during the 3 minute window")
			deadline := time.After(3 * time.Minute)
			modified := map[string]int{}
			ch := make(chan yang.WatchEvent[ietfif.Interfaces_Interface, ietfif.Interfaces_InterfaceKey], 128)
			done := make(chan struct{})
			defer func() {
				cancel()
				_ = w.Close()
				<-done
			}()
			go func() {
				defer close(done)
				defer close(ch)
				for ev := range w.Iter() {
					select {
					case ch <- ev:
					case <-ctx.Done():
						return
					}
				}
			}()
			for {
				select {
				case ev, ok := <-ch:
					if !ok {
						t.Fatalf("watcher ended: %v", w.Err())
					}
					if ev.Kind == yang.Modified {
						modified[ev.Key.Name]++
						t.Logf("Modified %s (count %d)", ev.Key.Name, modified[ev.Key.Name])
					}
				case <-deadline:
					if len(modified) == 0 {
						t.Fatal("no Modified events observed — was an interface toggled?")
					}
					for name, count := range modified {
						if count != 1 {
							t.Errorf("interface %s emitted %d Modified events, want 1", name, count)
						}
					}
					return
				}
			}
		})
	}
}
