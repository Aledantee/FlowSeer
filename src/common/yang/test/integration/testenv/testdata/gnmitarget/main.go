// Command gnmitarget is FlowSeer's t1 gNMI reference target: a small
// standalone server exercising the client library's Capabilities,
// Get, Set, and Subscribe (ONCE and STREAM with sync_response) paths
// over real gRPC. It serves a fixture-main-shaped dataset
// (/servers/server[name]/port) and, in STREAM mode, emits a periodic
// port increment for one row so watchers observe modifies.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"maps"
	"net"
	"slices"
	"sync"
	"time"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type server struct {
	gpb.UnimplementedGNMIServer

	mu   sync.Mutex       // guards rows
	rows map[string]int64 // server name -> port
}

func newServer() *server {
	return &server{rows: map[string]int64{"edge-1": 8080, "edge-2": 9090}}
}

// Capabilities advertises the fixture model and its JSON_IETF response encoding.
func (s *server) Capabilities(_ context.Context, _ *gpb.CapabilityRequest) (*gpb.CapabilityResponse, error) {
	return &gpb.CapabilityResponse{
		SupportedModels:    []*gpb.ModelData{{Name: "fixture-main", Organization: "FlowSeer", Version: "2026-01-02"}},
		SupportedEncodings: []gpb.Encoding{gpb.Encoding_JSON_IETF},
		GNMIVersion:        "0.10.0",
	}, nil
}

// rowUpdates renders the current rows as leaf updates.
func (s *server) rowUpdates() []*gpb.Update {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(s.rows))
	for name := range s.rows {
		names = append(names, name)
	}
	slices.Sort(names)
	var out []*gpb.Update
	for _, name := range names {
		nameJSON, _ := json.Marshal(name) // Marshaling a string cannot fail.
		base := []*gpb.PathElem{
			{Name: "servers"},
			{Name: "server", Key: map[string]string{"name": name}},
		}
		out = append(out,
			&gpb.Update{
				Path: &gpb.Path{Elem: append(append([]*gpb.PathElem{}, base...), &gpb.PathElem{Name: "name"})},
				Val:  &gpb.TypedValue{Value: &gpb.TypedValue_JsonIetfVal{JsonIetfVal: nameJSON}},
			},
			&gpb.Update{
				Path: &gpb.Path{Elem: append(append([]*gpb.PathElem{}, base...), &gpb.PathElem{Name: "port"})},
				Val:  &gpb.TypedValue{Value: &gpb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(fmt.Sprintf("%d", s.rows[name]))}},
			},
		)
	}
	return out
}

func pathElems(prefix, path *gpb.Path) []*gpb.PathElem {
	return append(slices.Clone(prefix.GetElem()), path.GetElem()...)
}

func selectUpdates(updates []*gpb.Update, prefix *gpb.Path, paths []*gpb.Path) []*gpb.Update {
	if len(paths) == 0 {
		paths = []*gpb.Path{nil}
	}
	var selected []*gpb.Update
	for _, update := range updates {
		for _, path := range paths {
			if containsPath(pathElems(prefix, path), update.GetPath().GetElem()) {
				selected = append(selected, update)
				break
			}
		}
	}
	return selected
}

func containsPath(query, path []*gpb.PathElem) bool {
	if len(query) > len(path) {
		return false
	}
	for i, elem := range query {
		if elem.GetName() != path[i].GetName() {
			return false
		}
		for key, value := range elem.GetKey() {
			if got, ok := path[i].GetKey()[key]; !ok || got != value {
				return false
			}
		}
	}
	return true
}

// Get returns a consistent snapshot of the requested fixture leaves or subtrees.
func (s *server) Get(_ context.Context, req *gpb.GetRequest) (*gpb.GetResponse, error) {
	return &gpb.GetResponse{Notification: []*gpb.Notification{{
		Timestamp: time.Now().UnixNano(),
		Update:    selectUpdates(s.rowUpdates(), req.GetPrefix(), req.GetPath()),
	}}}, nil
}

// Set applies supported port edits and row deletions atomically. Invalid paths or
// values return InvalidArgument without changing any row in the transaction.
func (s *server) Set(_ context.Context, req *gpb.SetRequest) (*gpb.SetResponse, error) {
	if len(req.GetUnionReplace()) > 0 {
		return nil, status.Error(codes.Unimplemented, "union-replace is not supported by the fixture")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := maps.Clone(s.rows)
	var results []*gpb.UpdateResult
	apply := func(u *gpb.Update, op gpb.UpdateResult_Operation) error {
		name, err := rowName(req.GetPrefix(), u.GetPath(), "port")
		if err != nil {
			return err
		}
		var port int64
		if err := json.Unmarshal(u.GetVal().GetJsonIetfVal(), &port); err != nil || port < 1 || port > 65535 {
			return status.Error(codes.InvalidArgument, "port must be a JSON integer between 1 and 65535")
		}
		rows[name] = port
		results = append(results, &gpb.UpdateResult{Path: u.GetPath(), Op: op})
		return nil
	}
	for _, path := range req.GetDelete() {
		name, err := rowName(req.GetPrefix(), path, "")
		if err != nil {
			return nil, err
		}
		delete(rows, name)
		results = append(results, &gpb.UpdateResult{Path: path, Op: gpb.UpdateResult_DELETE})
	}
	for _, u := range req.GetReplace() {
		if err := apply(u, gpb.UpdateResult_REPLACE); err != nil {
			return nil, err
		}
	}
	for _, u := range req.GetUpdate() {
		if err := apply(u, gpb.UpdateResult_UPDATE); err != nil {
			return nil, err
		}
	}
	s.rows = rows
	return &gpb.SetResponse{Prefix: req.GetPrefix(), Response: results, Timestamp: time.Now().UnixNano()}, nil
}

func rowName(prefix, path *gpb.Path, leaf string) (string, error) {
	elems := pathElems(prefix, path)
	if leaf != "" {
		if len(elems) != 3 || elems[2].GetName() != leaf || len(elems[2].GetKey()) != 0 {
			return "", status.Error(codes.InvalidArgument, "only /servers/server[name]/port edits are supported")
		}
		elems = elems[:2]
	}
	if len(elems) != 2 || elems[0].GetName() != "servers" || len(elems[0].GetKey()) != 0 ||
		elems[1].GetName() != "server" || len(elems[1].GetKey()) != 1 || elems[1].GetKey()["name"] == "" {
		return "", status.Error(codes.InvalidArgument, "expected a /servers/server[name] row")
	}
	return elems[1].GetKey()["name"], nil
}

// Subscribe sends matching leaves and a sync marker for ONCE or STREAM. STREAM
// changes edge-1 once per second while it exists and stops on cancellation.
func (s *server) Subscribe(srv gpb.GNMI_SubscribeServer) error {
	req, err := srv.Recv()
	if err != nil {
		return err
	}
	sub := req.GetSubscribe()
	if sub == nil {
		return status.Error(codes.InvalidArgument, "first message must be a SubscriptionList")
	}
	if sub.GetMode() != gpb.SubscriptionList_ONCE && sub.GetMode() != gpb.SubscriptionList_STREAM {
		return status.Error(codes.Unimplemented, "only ONCE and STREAM are supported by the fixture")
	}
	paths := make([]*gpb.Path, 0, len(sub.GetSubscription()))
	for _, subscription := range sub.GetSubscription() {
		paths = append(paths, subscription.GetPath())
	}

	send := func(updates []*gpb.Update) error {
		return srv.Send(&gpb.SubscribeResponse{Response: &gpb.SubscribeResponse_Update{Update: &gpb.Notification{
			Timestamp: time.Now().UnixNano(),
			Update:    updates,
		}}})
	}
	if !sub.GetUpdatesOnly() {
		if err := send(selectUpdates(s.rowUpdates(), sub.GetPrefix(), paths)); err != nil {
			return err
		}
	}
	if err := srv.Send(&gpb.SubscribeResponse{Response: &gpb.SubscribeResponse_SyncResponse{SyncResponse: true}}); err != nil {
		return err
	}
	if sub.GetMode() == gpb.SubscriptionList_ONCE {
		return nil
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-srv.Context().Done():
			return srv.Context().Err()
		case <-ticker.C:
		}
		s.mu.Lock()
		port, exists := s.rows["edge-1"]
		if !exists {
			s.mu.Unlock()
			continue
		}
		port = port%65535 + 1
		s.rows["edge-1"] = port
		s.mu.Unlock()
		update := &gpb.Update{
			Path: &gpb.Path{Elem: []*gpb.PathElem{
				{Name: "servers"},
				{Name: "server", Key: map[string]string{"name": "edge-1"}},
				{Name: "port"},
			}},
			Val: &gpb.TypedValue{Value: &gpb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(fmt.Sprintf("%d", port))}},
		}
		if updates := selectUpdates([]*gpb.Update{update}, sub.GetPrefix(), paths); len(updates) > 0 {
			if err := send(updates); err != nil {
				return err
			}
		}
	}
}

func main() {
	addr := flag.String("addr", ":9339", "listen address")
	flag.Parse()
	lis, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	grpcSrv := grpc.NewServer()
	gpb.RegisterGNMIServer(grpcSrv, newServer())
	log.Printf("gnmitarget listening on %s", *addr)
	if err := grpcSrv.Serve(lis); err != nil {
		log.Fatal(err)
	}
}
