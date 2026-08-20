package restconf_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/restconf"
	"go.aledante.io/FlowSeer/src/common/yang"
	fixturemain "go.aledante.io/FlowSeer/src/common/yang/cmd/yanggen/testdata/golden/fixture/fixturemain"
)

func TestWatchOverRESTCONF(t *testing.T) {
	payloads := []string{
		`{"fixture-main:server":[{"name":"a","port":1}]}`,
		`{"fixture-main:server":[{"name":"a","port":2}]}`,
		"", // 404: subtree gone → Removed
	}
	var mu sync.Mutex
	idx := 0
	s := dialTest(t, hostMetaHandler("/restconf", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		i := idx
		if i >= len(payloads) {
			i = len(payloads) - 1
		}
		idx++
		body := payloads[i]
		mu.Unlock()
		if body == "" {
			http.Error(w, "{}", http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	})))

	desc := fixturemain.Servers_ServerDescriptor()
	w := restconf.Watch(context.Background(), s, desc, yang.WatchConfig{Interval: 20 * time.Millisecond})
	defer func() { _ = w.Close() }()

	ch := make(chan yang.WatchEvent[fixturemain.Servers_Server, fixturemain.Servers_ServerKey], 32)
	go func() {
		for ev := range w.Iter() {
			ch <- ev
		}
	}()
	var events []yang.WatchEvent[fixturemain.Servers_Server, fixturemain.Servers_ServerKey]
	deadline := time.After(10 * time.Second)
	for len(events) < 3 {
		select {
		case ev := <-ch:
			events = append(events, ev)
		case <-deadline:
			t.Fatalf("events = %d/3", len(events))
		}
	}
	kinds := []yang.ChangeKind{events[0].Kind, events[1].Kind, events[2].Kind}
	want := []yang.ChangeKind{yang.Added, yang.Modified, yang.Removed}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", kinds, want)
		}
	}
	if events[1].Key.Name != "a" || *events[1].Row.Port != 2 {
		t.Errorf("modified event = %+v", events[1])
	}
}
