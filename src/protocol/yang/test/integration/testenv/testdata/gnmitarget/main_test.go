package main

import (
	"context"
	"errors"
	"maps"
	"testing"
	"time"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func rowPath(name string, leaf bool) *gpb.Path {
	p := &gpb.Path{Elem: []*gpb.PathElem{{Name: "servers"}, {Name: "server", Key: map[string]string{"name": name}}}}
	if leaf {
		p.Elem = append(p.Elem, &gpb.PathElem{Name: "port"})
	}
	return p
}

func portUpdate(name, value string) *gpb.Update {
	return &gpb.Update{Path: rowPath(name, true), Val: &gpb.TypedValue{Value: &gpb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(value)}}}
}

func TestSetRejectsInvalidTransaction(t *testing.T) {
	cases := []struct {
		name   string
		update *gpb.Update
	}{
		{name: "bad_json", update: portUpdate("edge-1", "42 trailing")},
		{name: "zero_port", update: portUpdate("edge-1", "0")},
		{name: "overflow_port", update: portUpdate("edge-1", "65536")},
		{name: "unknown_path", update: &gpb.Update{Path: &gpb.Path{Elem: []*gpb.PathElem{{Name: "unknown"}}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newServer()
			before := maps.Clone(s.rows)
			_, err := s.Set(t.Context(), &gpb.SetRequest{Update: []*gpb.Update{portUpdate("new-row", "1234"), tc.update}})
			if status.Code(err) != codes.InvalidArgument {
				t.Errorf("got Set code %v, want InvalidArgument", status.Code(err))
			}
			if !maps.Equal(s.rows, before) {
				t.Errorf("failed Set changed rows: got %v, want %v", s.rows, before)
			}
		})
	}
	// A transaction applies delete, replace, then update, including with a prefix.
	s := newServer()
	path := rowPath("edge-1", true)
	replace := portUpdate("edge-1", "3333")
	update := portUpdate("edge-1", "4444")
	replace.Path.Elem = replace.Path.Elem[1:]
	update.Path.Elem = update.Path.Elem[1:]
	response, err := s.Set(t.Context(), &gpb.SetRequest{
		Prefix:  &gpb.Path{Elem: path.Elem[:1]},
		Delete:  []*gpb.Path{{Elem: path.Elem[1:2]}},
		Replace: []*gpb.Update{replace},
		Update:  []*gpb.Update{update},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.rows["edge-1"]; got != 4444 {
		t.Errorf("got final port %d, want 4444", got)
	}
	if len(response.GetResponse()) != 3 || response.GetResponse()[0].GetOp() != gpb.UpdateResult_DELETE {
		t.Error("Set results do not follow transaction order")
	}
}

type subscriptionStream struct {
	grpc.ServerStream
	ctx       context.Context
	cancel    context.CancelFunc
	request   *gpb.SubscribeRequest
	responses []*gpb.SubscribeResponse
}

func (s *subscriptionStream) Context() context.Context { return s.ctx }

func (s *subscriptionStream) Recv() (*gpb.SubscribeRequest, error) { return s.request, nil }

func (s *subscriptionStream) Send(response *gpb.SubscribeResponse) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	s.responses = append(s.responses, response)
	if response.GetSyncResponse() {
		s.cancel()
	}
	return nil
}

func checkReadUpdates(t *testing.T, updates []*gpb.Update, want int) {
	t.Helper()
	if len(updates) != want {
		t.Errorf("got %d updates, want %d", len(updates), want)
	}
	for _, update := range updates {
		elems := update.GetPath().GetElem()
		if len(elems) != 3 || elems[1].GetKey()["name"] != "edge-2" {
			t.Errorf("unexpected update path: %v", update.GetPath())
			continue
		}
		leaf := elems[2].GetName()
		if want == 1 && leaf != "port" {
			t.Errorf("got leaf %q, want port", leaf)
		}
		values := map[string]string{"name": `"edge-2"`, "port": "9090"}
		if got := string(update.GetVal().GetJsonIetfVal()); got != values[leaf] {
			t.Errorf("leaf %s got JSON %q, want %q", leaf, got, values[leaf])
		}
	}
}

func TestReadPaths(t *testing.T) {
	cases := []struct {
		name   string
		prefix *gpb.Path
		path   *gpb.Path
		want   int
	}{
		{name: "one_leaf", path: rowPath("edge-2", true), want: 1},
		{name: "one_row", prefix: &gpb.Path{Elem: []*gpb.PathElem{{Name: "servers"}}}, path: &gpb.Path{Elem: rowPath("edge-2", false).Elem[1:]}, want: 2},
		{name: "missing_row", path: rowPath("absent", false)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newServer()
			response, err := s.Get(t.Context(), &gpb.GetRequest{Prefix: tc.prefix, Path: []*gpb.Path{tc.path}})
			if err != nil {
				t.Fatal(err)
			}
			checkReadUpdates(t, response.GetNotification()[0].GetUpdate(), tc.want)
			for _, mode := range []gpb.SubscriptionList_Mode{gpb.SubscriptionList_ONCE, gpb.SubscriptionList_STREAM} {
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				stream := &subscriptionStream{ctx: ctx, cancel: cancel, request: &gpb.SubscribeRequest{
					Request: &gpb.SubscribeRequest_Subscribe{Subscribe: &gpb.SubscriptionList{
						Mode: mode, Prefix: tc.prefix, Subscription: []*gpb.Subscription{{Path: tc.path}},
					}},
				}}
				err := s.Subscribe(stream)
				cancel()
				if err != nil && !errors.Is(err, context.Canceled) {
					t.Fatalf("Subscribe: %v", err)
				}
				if len(stream.responses) != 2 || !stream.responses[1].GetSyncResponse() {
					t.Fatalf("got %d Subscribe responses, want snapshot and sync", len(stream.responses))
				}
				checkReadUpdates(t, stream.responses[0].GetUpdate().GetUpdate(), tc.want)
			}
		})
	}
}
