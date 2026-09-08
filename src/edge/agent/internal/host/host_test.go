package host_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1/edgev1connect"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/host"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/identity"
)

const testEdgeID = "0192e6a0-0000-7000-8000-0000000000ed"

// orderedCentral records the procedure of every call and whether it arrived
// with an assertion, which is what the agent's startup order is visible as
// from the other end.
type orderedCentral struct {
	edgev1connect.UnimplementedEdgeServiceHandler

	mu       sync.Mutex
	calls    []call
	refuse   bool
	anchors  [][]byte
	attached bool
}

type call struct {
	procedure string
	signed    bool
}

func (c *orderedCentral) record(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.calls = append(c.calls, call{
			procedure: filepath.Base(r.URL.Path),
			signed:    r.Header.Get("Authorization") != "",
		})
		c.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

func (c *orderedCentral) seen() []call {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]call(nil), c.calls...)
}

func (c *orderedCentral) Enroll(
	_ context.Context, _ *connect.Request[edgev1.EnrollRequest],
) (*connect.Response[edgev1.EnrollResponse], error) {
	if c.refuse {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("the request was refused"))
	}
	anchors := c.anchors
	if anchors == nil {
		anchors = [][]byte{make([]byte, 32)}
	}
	return connect.NewResponse(edgev1.EnrollResponse_builder{
		Edge: edgev1.EdgeGlobalRef_builder{
			Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(testEdgeID)}.Build(),
		}.Build(),
		Audience:     proto.String("flowseer-central"),
		TrustAnchors: anchors,
	}.Build()), nil
}

// AttachBus fails, which ends the run before a leaf node or a receiver is
// started. What happens after a successful attachment needs a live hub and
// belongs to the end-to-end test; what this file covers is everything that
// has to be true before it.
func (c *orderedCentral) AttachBus(
	context.Context, *connect.Request[edgev1.AttachBusRequest],
) (*connect.Response[edgev1.AttachBusResponse], error) {
	c.mu.Lock()
	c.attached = true
	c.mu.Unlock()
	return nil, connect.NewError(connect.CodeUnavailable, errors.New("no bus here"))
}

// agentAgainst stands central up and writes the two files an agent is
// deployed with, returning its loaded configuration.
func agentAgainst(t *testing.T, central *orderedCentral) *host.Config {
	t.Helper()
	mux := http.NewServeMux()
	path, handler := edgev1connect.NewEdgeServiceHandler(central)
	mux.Handle(path, central.record(handler))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	dir := t.TempDir()
	provisioning := filepath.Join(dir, "provisioning.textproto")
	if err := os.WriteFile(provisioning, []byte(`central_url: "`+server.URL+`"
setup_key: "`+testSetupKey+`"
trust_anchors: "`+testAnchor+`"
`), 0o600); err != nil {
		t.Fatalf("write provisioning: %v", err)
	}
	configPath := filepath.Join(dir, "agent.textproto")
	if err := os.WriteFile(configPath, []byte(`state_dir: "`+filepath.Join(dir, "state")+`"
provisioning_path: "`+provisioning+`"
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := host.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return cfg
}

// TestTheAgentEnrollsFirstAndSignsEverythingAfterwards covers the first two
// links of the startup chain, asserted from central's side: the order the
// calls arrived in, and which of them carried an assertion.
//
// Both are invisible from the code that depends on them. Enroll is the only
// call an edge can make before it has an identity, and every later call is
// signed by the key that enrollment registered — so an agent that attached
// first would be making a signed call with nothing to sign it, and would fail
// in central's middleware rather than anywhere near the ordering that caused
// it.
func TestTheAgentEnrollsFirstAndSignsEverythingAfterwards(t *testing.T) {
	central := &orderedCentral{}
	cfg := agentAgainst(t, central)

	// The run ends at AttachBus, which this central refuses.
	if err := host.Run(context.Background(), cfg, "v0-test"); err == nil {
		t.Fatal("Run() error = nil, want the failed attachment surfaced")
	}

	seen := central.seen()
	procedures := make([]string, 0, len(seen))
	for _, c := range seen {
		procedures = append(procedures, c.procedure)
	}
	if !slices.Equal(procedures, []string{"Enroll", "AttachBus"}) {
		t.Fatalf("central saw %v, want Enroll then AttachBus", procedures)
	}
	if seen[0].signed {
		t.Error("Enroll carried an assertion; there is no identity to sign it with yet")
	}
	if !seen[1].signed {
		t.Error("AttachBus carried no assertion; every call but Enroll is signed")
	}
}

// TestAnAgentThatCannotEnrollStopsThere. Without an identity there is nothing
// to carry on with: every other call is signed by the key enrollment
// registers, so an agent that logged this and continued would make one failing
// call after another with no way to recover. It is the one failure that ends
// the process.
func TestAnAgentThatCannotEnrollStopsThere(t *testing.T) {
	central := &orderedCentral{refuse: true}
	cfg := agentAgainst(t, central)

	err := host.Run(context.Background(), cfg, "v0-test")

	// The code, not merely that an error came back. An agent that logged the
	// enrollment and carried on fails a moment later anyway — its empty
	// anchor set is refused by the attachment — and from outside that is the
	// same shape: an error, no AttachBus, one call to Enroll. Only the code
	// says which of the two happened, and stopping here is the behavior
	// under test.
	if code, _ := errs.CodeOf(err); code != identity.ErrCodeEnroll {
		t.Fatalf("Run() code = %v (err %v), want %v", code, err, identity.ErrCodeEnroll)
	}
	central.mu.Lock()
	attached := central.attached
	central.mu.Unlock()
	if attached {
		t.Error("the agent attached to the bus without an identity")
	}
	if procedures := central.seen(); len(procedures) != 1 || procedures[0].procedure != "Enroll" {
		t.Errorf("central saw %v, want the run to stop at Enroll", procedures)
	}
}

// TestTheAttachmentPinsWhatTheEnrollmentReturned covers the anchor swap.
// EnrollResponse replaces the set an edge shipped with, so a deployment can
// rotate its chain without re-provisioning every edge in the field — and an
// agent that kept pinning the provisioned anchors would refuse central after
// the first rotation, on a path that looks like a network fault.
//
// It observes the attachment and not the signing client, which is what this
// test can actually see: these calls go over plain HTTP, so a client's TLS
// configuration is never exercised and its anchors leave no trace. The two
// are built from the same value on adjacent lines, and this pins the half
// that has an observable — busattach refuses an empty anchor set before it
// dials, so reaching that refusal proves the enrollment's set was the one
// carried forward. The provisioned set is not empty.
func TestTheAttachmentPinsWhatTheEnrollmentReturned(t *testing.T) {
	central := &orderedCentral{anchors: [][]byte{}}
	cfg := agentAgainst(t, central)

	err := host.Run(context.Background(), cfg, "v0-test")
	if err == nil {
		t.Fatal("Run() error = nil, want the empty anchor set refused")
	}
	central.mu.Lock()
	attached := central.attached
	central.mu.Unlock()
	if attached {
		t.Error("AttachBus was called; the empty anchor set should have been refused before it")
	}
}
