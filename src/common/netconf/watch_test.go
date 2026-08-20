package netconf_test

import (
	"context"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/netconf"
	"go.aledante.io/FlowSeer/src/common/yang"
	fixturemain "go.aledante.io/FlowSeer/src/common/yang/cmd/yanggen/testdata/golden/fixture/fixturemain"
)

// sequencedFake serves successive <get> payloads.
type sequencedFake struct {
	*fakeTransport
	payloads [][]byte
	idx      int
}

func (f *sequencedFake) Exec(ctx context.Context, op, reply any) error {
	if opName(op) == "get" {
		f.mu.Lock()
		i := f.idx
		if i >= len(f.payloads) {
			i = len(f.payloads) - 1
		}
		f.idx++
		f.data["get"] = f.payloads[i]
		f.mu.Unlock()
	}
	return f.fakeTransport.Exec(ctx, op, reply)
}

func TestWalkAndWatchOverSession(t *testing.T) {
	tick1 := []byte(`<servers xmlns="urn:flowseer:fixture-main">` +
		`<server><name>a</name><port>1</port></server>` +
		`<server><name>b</name><port>2</port></server></servers>`)
	tick2 := []byte(`<servers xmlns="urn:flowseer:fixture-main">` +
		`<server><name>a</name><port>9</port></server>` +
		`<server><name>b</name><port>2</port></server></servers>`)

	f := &sequencedFake{fakeTransport: newFake(capCandidate), payloads: [][]byte{tick1, tick1, tick2}}
	s := netconf.NewSession(f, netconf.Options{})
	defer func() { _ = s.Close(context.Background()) }()

	desc := fixturemain.Servers_ServerDescriptor()

	// Walker: bounded traversal yields both rows.
	walker := netconf.Walk(context.Background(), s, desc)
	var names []string
	for row := range walker.Iter() {
		names = append(names, *row.Name)
	}
	if err := walker.Err(); err != nil {
		t.Fatalf("walker: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("walker rows = %v", names)
	}

	// Watcher: cold start Added×2, then one Modified for a.
	w := netconf.Watch(context.Background(), s, desc, yang.WatchConfig{Interval: 20 * time.Millisecond})
	defer func() { _ = w.Close() }()

	var events []yang.WatchEvent[fixturemain.Servers_Server, fixturemain.Servers_ServerKey]
	deadline := time.After(10 * time.Second)
	ch := make(chan yang.WatchEvent[fixturemain.Servers_Server, fixturemain.Servers_ServerKey], 32)
	go func() {
		for ev := range w.Iter() {
			ch <- ev
		}
	}()
	for len(events) < 3 {
		select {
		case ev := <-ch:
			events = append(events, ev)
		case <-deadline:
			t.Fatalf("events = %d/3", len(events))
		}
	}
	if events[0].Kind != yang.Added || events[1].Kind != yang.Added {
		t.Fatalf("cold start kinds = %v %v", events[0].Kind, events[1].Kind)
	}
	if events[2].Kind != yang.Modified || events[2].Key.Name != "a" || *events[2].Row.Port != 9 {
		t.Fatalf("event = %+v, want Modified a port 9", events[2])
	}
}
