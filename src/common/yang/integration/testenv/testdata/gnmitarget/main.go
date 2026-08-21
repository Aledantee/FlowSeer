// Command gnmitarget is FlowSeer's t1 gNMI reference target: a small
// standalone server exercising the client library's Capabilities,
// Get, Set, and Subscribe (ONCE and STREAM with sync_response) paths
// over real gRPC. It serves a fixture-main-shaped dataset
// (/servers/server[name]/port) and, in STREAM mode, emits a periodic
// port increment for one row so watchers observe modifies.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type server struct {
	gpb.UnimplementedGNMIServer

	mu   sync.Mutex
	rows map[string]int64 // server name -> port
}

func newServer() *server {
	return &server{rows: map[string]int64{"edge-1": 8080, "edge-2": 9090}}
}

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
	// Deterministic order.
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	var out []*gpb.Update
	for _, name := range names {
		base := []*gpb.PathElem{
			{Name: "servers"},
			{Name: "server", Key: map[string]string{"name": name}},
		}
		out = append(out,
			&gpb.Update{
				Path: &gpb.Path{Elem: append(append([]*gpb.PathElem{}, base...), &gpb.PathElem{Name: "name"})},
				Val:  &gpb.TypedValue{Value: &gpb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(fmt.Sprintf("%q", name))}},
			},
			&gpb.Update{
				Path: &gpb.Path{Elem: append(append([]*gpb.PathElem{}, base...), &gpb.PathElem{Name: "port"})},
				Val:  &gpb.TypedValue{Value: &gpb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(fmt.Sprintf("%d", s.rows[name]))}},
			},
		)
	}
	return out
}

func (s *server) Get(_ context.Context, _ *gpb.GetRequest) (*gpb.GetResponse, error) {
	return &gpb.GetResponse{Notification: []*gpb.Notification{{
		Timestamp: time.Now().UnixNano(),
		Update:    s.rowUpdates(),
	}}}, nil
}

func (s *server) Set(_ context.Context, req *gpb.SetRequest) (*gpb.SetResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var results []*gpb.UpdateResult
	apply := func(u *gpb.Update, op gpb.UpdateResult_Operation) {
		// Accept only /servers/server[name]/port updates.
		elems := u.GetPath().GetElem()
		if len(elems) == 3 && elems[1].GetName() == "server" && elems[2].GetName() == "port" {
			name := elems[1].GetKey()["name"]
			var port int64
			_, err := fmt.Sscanf(string(u.GetVal().GetJsonIetfVal()), "%d", &port)
			if err == nil && name != "" {
				s.rows[name] = port
			}
		}
		results = append(results, &gpb.UpdateResult{Path: u.GetPath(), Op: op})
	}
	for _, u := range req.GetUpdate() {
		apply(u, gpb.UpdateResult_UPDATE)
	}
	for _, u := range req.GetReplace() {
		apply(u, gpb.UpdateResult_REPLACE)
	}
	for _, d := range req.GetDelete() {
		elems := d.GetElem()
		if len(elems) == 2 && elems[1].GetName() == "server" {
			delete(s.rows, elems[1].GetKey()["name"])
		}
		results = append(results, &gpb.UpdateResult{Path: d, Op: gpb.UpdateResult_DELETE})
	}
	return &gpb.SetResponse{Response: results, Timestamp: time.Now().UnixNano()}, nil
}

func (s *server) Subscribe(srv gpb.GNMI_SubscribeServer) error {
	req, err := srv.Recv()
	if err != nil {
		return err
	}
	sub := req.GetSubscribe()
	if sub == nil {
		return status.Error(codes.InvalidArgument, "first message must be a SubscriptionList")
	}

	send := func(updates []*gpb.Update) error {
		return srv.Send(&gpb.SubscribeResponse{Response: &gpb.SubscribeResponse_Update{Update: &gpb.Notification{
			Timestamp: time.Now().UnixNano(),
			Update:    updates,
		}}})
	}
	if err := send(s.rowUpdates()); err != nil {
		return err
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
		s.rows["edge-1"]++
		port := s.rows["edge-1"]
		s.mu.Unlock()
		update := &gpb.Update{
			Path: &gpb.Path{Elem: []*gpb.PathElem{
				{Name: "servers"},
				{Name: "server", Key: map[string]string{"name": "edge-1"}},
				{Name: "port"},
			}},
			Val: &gpb.TypedValue{Value: &gpb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(fmt.Sprintf("%d", port))}},
		}
		if err := send([]*gpb.Update{update}); err != nil {
			return err
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
