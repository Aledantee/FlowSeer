package gnmi_test

import (
	"context"
	"testing"
	"time"

	gpb "github.com/openconfig/gnmi/proto/gnmi"

	ocif "go.aledante.io/FlowSeer/generated/go/yang/cisco-iosxe/openconfiginterfaces"
	"go.aledante.io/FlowSeer/src/protocol/gnmi"
	"go.aledante.io/FlowSeer/src/protocol/yang"
	fixturemain "go.aledante.io/FlowSeer/src/protocol/yang/cmd/yanggen/testdata/golden/fixture/fixturemain"
)

// leafUpdate builds one JSON_IETF leaf update under /servers/server.
func leafUpdate(rowKey, leaf, jsonVal string) *gpb.Update {
	return &gpb.Update{
		Path: &gpb.Path{Elem: []*gpb.PathElem{
			{Name: "servers"},
			{Name: "server", Key: map[string]string{"name": rowKey}},
			{Name: leaf},
		}},
		Val: &gpb.TypedValue{Value: &gpb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(jsonVal)}},
	}
}

// notif wraps updates into one SubscribeResponse batch.
func notif(updates ...*gpb.Update) *gpb.SubscribeResponse {
	return &gpb.SubscribeResponse{Response: &gpb.SubscribeResponse_Update{Update: &gpb.Notification{Update: updates}}}
}

func syncResp() *gpb.SubscribeResponse {
	return &gpb.SubscribeResponse{Response: &gpb.SubscribeResponse_SyncResponse{SyncResponse: true}}
}

// watchEvents pipes a watcher's events.
func watchEvents(w *gnmi.Watcher[fixturemain.Servers_Server, fixturemain.Servers_ServerKey]) <-chan yang.WatchEvent[fixturemain.Servers_Server, fixturemain.Servers_ServerKey] {
	ch := make(chan yang.WatchEvent[fixturemain.Servers_Server, fixturemain.Servers_ServerKey], 64)
	go func() {
		defer close(ch)
		for ev := range w.Iter() {
			ch <- ev
		}
	}()
	return ch
}

func nextEvent[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("watcher ended early")
		}
		return ev
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for event")
	}
	panic("unreachable")
}

// Covers conformance matrix row: gn-presync-buffering
func TestStreamWatcherColdStartAndBatches(t *testing.T) {
	release := make(chan struct{})
	f := &fakeServer{
		encodings: []gpb.Encoding{gpb.Encoding_JSON_IETF},
		subscribe: func(srv gpb.GNMI_SubscribeServer) error {
			if _, err := srv.Recv(); err != nil {
				return err
			}
			// Pre-sync: row a arrives as two separate leaf batches —
			// they must buffer, not emit.
			_ = srv.Send(notif(leafUpdate("a", "name", `"a"`)))
			_ = srv.Send(notif(leafUpdate("a", "port", `1`)))
			_ = srv.Send(syncResp())
			// One post-sync batch touching two leaves of row a: one
			// Modified expected.
			_ = srv.Send(notif(leafUpdate("a", "port", `9`), leafUpdate("a", "owner", `"ops"`)))
			// Row b appears, then row a is deleted at row level.
			_ = srv.Send(notif(leafUpdate("b", "name", `"b"`)))
			_ = srv.Send(&gpb.SubscribeResponse{Response: &gpb.SubscribeResponse_Update{Update: &gpb.Notification{
				Delete: []*gpb.Path{{Elem: []*gpb.PathElem{
					{Name: "servers"},
					{Name: "server", Key: map[string]string{"name": "a"}},
				}}},
			}}})
			<-release
			return nil
		},
	}
	s := dialFake(t, f)
	defer close(release)

	w, err := gnmi.Watch(context.Background(), s, fixturemain.Servers_ServerDescriptor(), gnmi.WatchOptions{})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer func() { _ = w.Close() }()
	ch := watchEvents(w)

	ev := nextEvent(t, ch)
	if ev.Kind != yang.Added || ev.Key.Name != "a" || *ev.Row.Port != 1 {
		t.Fatalf("cold start event = %+v, want Added a with port 1", ev)
	}

	ev = nextEvent(t, ch)
	if ev.Kind != yang.Modified || ev.Key.Name != "a" {
		t.Fatalf("event = %+v, want one Modified for a", ev)
	}
	if *ev.Row.Port != 9 || ev.Row.Owner == nil || *ev.Row.Owner != "ops" {
		t.Errorf("modified row = %+v", ev.Row)
	}

	ev = nextEvent(t, ch)
	if ev.Kind != yang.Added || ev.Key.Name != "b" {
		t.Fatalf("event = %+v, want Added b", ev)
	}

	ev = nextEvent(t, ch)
	if ev.Kind != yang.Removed || ev.Key.Name != "a" {
		t.Fatalf("event = %+v, want Removed a", ev)
	}
}

// TestStreamWatcherNestedFlatRows: a change in an inner-list entry
// emits one Modified for the flattened row, whose identity carries
// the ancestor key.
func TestStreamWatcherNestedFlatRows(t *testing.T) {
	epUpdate := func(server, addr, port, leaf, jsonVal string) *gpb.Update {
		return &gpb.Update{
			Path: &gpb.Path{Elem: []*gpb.PathElem{
				{Name: "servers"},
				{Name: "server", Key: map[string]string{"name": server}},
				{Name: "endpoint", Key: map[string]string{"address": addr, "port": port}},
				{Name: leaf},
			}},
			Val: &gpb.TypedValue{Value: &gpb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(jsonVal)}},
		}
	}
	release := make(chan struct{})
	f := &fakeServer{
		encodings: []gpb.Encoding{gpb.Encoding_JSON_IETF},
		subscribe: func(srv gpb.GNMI_SubscribeServer) error {
			if _, err := srv.Recv(); err != nil {
				return err
			}
			_ = srv.Send(notif(epUpdate("a", "10.0.0.1", "443", "enabled", `true`)))
			_ = srv.Send(syncResp())
			_ = srv.Send(notif(epUpdate("a", "10.0.0.1", "443", "enabled", `false`)))
			<-release
			return nil
		},
	}
	s := dialFake(t, f)
	defer close(release)

	desc := fixturemain.Servers_Server_EndpointDescriptor()
	w, err := gnmi.Watch(context.Background(), s, desc, gnmi.WatchOptions{})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer func() { _ = w.Close() }()

	ch := make(chan yang.WatchEvent[fixturemain.Servers_Server_EndpointFlatRow, fixturemain.Servers_Server_EndpointKey], 16)
	go func() {
		defer close(ch)
		for ev := range w.Iter() {
			ch <- ev
		}
	}()

	added := nextEvent(t, ch)
	wantKey := fixturemain.Servers_Server_EndpointKey{Server_Name: "a", Address: "10.0.0.1", Port: 443}
	if added.Kind != yang.Added || added.Key != wantKey {
		t.Fatalf("event = %+v, want Added with ancestor-keyed identity %+v", added, wantKey)
	}
	if added.Row.Entry.Enabled == nil || !*added.Row.Entry.Enabled {
		t.Errorf("added row = %+v", added.Row)
	}

	mod := nextEvent(t, ch)
	if mod.Kind != yang.Modified || mod.Key != wantKey {
		t.Fatalf("event = %+v, want one Modified for the flattened row", mod)
	}
	if *mod.Row.Entry.Enabled {
		t.Error("modified row kept enabled=true")
	}
}

// TestStreamWalkerOnce: Subscribe ONCE assembles the snapshot rows.
func TestStreamWalkerOnce(t *testing.T) {
	f := &fakeServer{
		encodings: []gpb.Encoding{gpb.Encoding_JSON_IETF},
		subscribe: func(srv gpb.GNMI_SubscribeServer) error {
			if _, err := srv.Recv(); err != nil {
				return err
			}
			_ = srv.Send(notif(leafUpdate("a", "name", `"a"`), leafUpdate("a", "port", `1`)))
			_ = srv.Send(notif(leafUpdate("b", "name", `"b"`)))
			_ = srv.Send(syncResp())
			return nil
		},
	}
	s := dialFake(t, f)

	walker := gnmi.Walk(context.Background(), s, fixturemain.Servers_ServerDescriptor(), gnmi.WatchOptions{})
	var names []string
	for row := range walker.Iter() {
		names = append(names, *row.Name)
	}
	if err := walker.Err(); err != nil {
		t.Fatalf("walker: %v", err)
	}
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Errorf("rows = %v", names)
	}
}

// TestRecreatedWatcherColdStarts: a second Watch over the same data
// emits Added again — never a resumed Modified.
func TestRecreatedWatcherColdStarts(t *testing.T) {
	release := make(chan struct{})
	f := &fakeServer{
		encodings: []gpb.Encoding{gpb.Encoding_JSON_IETF},
		subscribe: func(srv gpb.GNMI_SubscribeServer) error {
			if _, err := srv.Recv(); err != nil {
				return err
			}
			_ = srv.Send(notif(leafUpdate("a", "name", `"a"`)))
			_ = srv.Send(syncResp())
			<-release
			return nil
		},
	}
	s := dialFake(t, f)
	defer close(release)

	for i := 0; i < 2; i++ {
		w, err := gnmi.Watch(context.Background(), s, fixturemain.Servers_ServerDescriptor(), gnmi.WatchOptions{})
		if err != nil {
			t.Fatalf("Watch #%d: %v", i, err)
		}
		ch := watchEvents(w)
		ev := nextEvent(t, ch)
		if ev.Kind != yang.Added || ev.Key.Name != "a" {
			t.Fatalf("watch #%d first event = %+v, want Added", i, ev)
		}
		_ = w.Close()
	}
}

func TestWatcherStopsQuietSubscription(t *testing.T) {
	for _, closeExplicitly := range []bool{false, true} {
		name := "iterator break"
		if closeExplicitly {
			name = "Close"
		}
		t.Run(name, func(t *testing.T) {
			stopped := make(chan struct{})
			f := &fakeServer{
				encodings: []gpb.Encoding{gpb.Encoding_JSON_IETF},
				subscribe: func(srv gpb.GNMI_SubscribeServer) error {
					defer close(stopped)
					if _, err := srv.Recv(); err != nil {
						return err
					}
					if err := srv.Send(notif(leafUpdate("a", "name", `"a"`))); err != nil {
						return err
					}
					if err := srv.Send(syncResp()); err != nil {
						return err
					}
					<-srv.Context().Done()
					return srv.Context().Err()
				},
			}
			s := dialFake(t, f)
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			w, err := gnmi.Watch(ctx, s, fixturemain.Servers_ServerDescriptor(), gnmi.WatchOptions{})
			if err != nil {
				t.Fatalf("Watch: %v", err)
			}
			t.Cleanup(func() { _ = w.Close() })
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				for range w.Iter() {
					if closeExplicitly {
						_ = w.Close()
						continue
					}
					break
				}
			}()

			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("watcher iteration did not finish after consumer stopped")
			}
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("watcher left its quiet subscription running")
			}
			if err := w.Err(); err != nil {
				t.Errorf("watcher error = %v, want nil after consumer stopped", err)
			}
		})
	}
}

// leafListUpdate builds one leaflist_val update under /interfaces/interface.
func leafListUpdate(name string, leaf []string, segs ...string) *gpb.Update {
	elems := []*gpb.PathElem{
		{Name: "interfaces"},
		{Name: "interface", Key: map[string]string{"name": name}},
	}
	for _, s := range segs {
		elems = append(elems, &gpb.PathElem{Name: s})
	}
	arr := &gpb.ScalarArray{}
	for _, v := range leaf {
		arr.Element = append(arr.Element, &gpb.TypedValue{Value: &gpb.TypedValue_StringVal{StringVal: v}})
	}
	return &gpb.Update{
		Path: &gpb.Path{Elem: elems},
		Val:  &gpb.TypedValue{Value: &gpb.TypedValue_LeaflistVal{LeaflistVal: arr}},
	}
}

// Covers conformance matrix row: gn-leaf-list-typed-value
func TestWatchDecodesLeafListIntoRow(t *testing.T) {
	f := &fakeServer{
		encodings: []gpb.Encoding{gpb.Encoding_PROTO},
		subscribe: func(srv gpb.GNMI_SubscribeServer) error {
			if _, err := srv.Recv(); err != nil {
				return err
			}
			_ = srv.Send(notif(leafListUpdate("Port-channel1", []string{"GigabitEthernet1", "GigabitEthernet2"}, "aggregation", "state", "member")))
			_ = srv.Send(syncResp())
			<-srv.Context().Done()
			return nil
		},
	}
	s := dialFake(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	w, err := gnmi.Watch(ctx, s, ocif.Interfaces_InterfaceDescriptor(), gnmi.WatchOptions{})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer func() { _ = w.Close() }()

	for ev := range w.Iter() {
		if ev.Kind != yang.Added {
			continue
		}
		got := ev.Row.Aggregation.State.Member
		if len(got) != 2 || got[0] != "GigabitEthernet1" || got[1] != "GigabitEthernet2" {
			t.Fatalf("Member = %+v, want the two leaf-list elements in wire order", got)
		}
		return
	}
	t.Fatalf("watcher produced no Added row: %v", w.Err())
}

// Covers conformance matrix row: gn-leaf-list-typed-value
func TestWatchDecodesEmptyLeafListIntoRow(t *testing.T) {
	f := &fakeServer{
		encodings: []gpb.Encoding{gpb.Encoding_PROTO},
		subscribe: func(srv gpb.GNMI_SubscribeServer) error {
			if _, err := srv.Recv(); err != nil {
				return err
			}
			_ = srv.Send(notif(leafListUpdate("Port-channel1", nil, "aggregation", "state", "member")))
			_ = srv.Send(syncResp())
			<-srv.Context().Done()
			return nil
		},
	}
	s := dialFake(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	w, err := gnmi.Watch(ctx, s, ocif.Interfaces_InterfaceDescriptor(), gnmi.WatchOptions{})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer func() { _ = w.Close() }()

	for ev := range w.Iter() {
		if ev.Kind != yang.Added {
			continue
		}
		// An empty leaf-list must render as [] and decode to an empty
		// slice; rendering it as null instead fails the row decode
		// outright.
		if got := ev.Row.Aggregation.State.Member; len(got) != 0 {
			t.Fatalf("Member = %+v, want empty", got)
		}
		return
	}
	t.Fatalf("watcher produced no Added row: %v", w.Err())
}
