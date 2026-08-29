//go:build yang_integration_t1

package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/netconf"
	"go.aledante.io/FlowSeer/src/common/yang"
	fixturemain "go.aledante.io/FlowSeer/src/common/yang/cmd/yanggen/testdata/golden/fixture/fixturemain"
)

// serverConfigXML renders one fixture server entry as edit-config
// payload via the generated codec.
func serverConfigXML(t *testing.T, name string, port uint16) []byte {
	t.Helper()
	servers := fixturemain.Servers{Server: []fixturemain.Servers_Server{{Name: &name, Port: &port}}}
	xmlBytes, err := yang.MarshalXMLStruct(fixturemain.ServersSchema, servers)
	if err != nil {
		t.Fatal(err)
	}
	return xmlBytes
}

// deleteServerXML renders a delete edit for one server entry.
func deleteServerXML(name string) []byte {
	return []byte(`<servers xmlns="urn:flowseer:fixture-main">` +
		`<server xmlns:nc="urn:ietf:params:xml:ns:netconf:base:1.0" nc:operation="delete">` +
		`<name>` + name + `</name></server></servers>`)
}

// readServers walks the current server rows through the generated
// descriptor.
func readServers(t *testing.T, s *netconf.Session) map[string]uint16 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	walker := netconf.Walk(ctx, s, fixturemain.Servers_ServerDescriptor())
	out := make(map[string]uint16)
	for row := range walker.Iter() {
		if row.Name == nil {
			continue
		}
		var port uint16
		if row.Port != nil {
			port = *row.Port
		}
		out[*row.Name] = port
	}
	if err := walker.Err(); err != nil {
		t.Fatalf("walk servers: %v", err)
	}
	return out
}

func TestT1CapabilitiesSelectCandidate(t *testing.T) {
	s := dialT1(t)
	if len(s.Capabilities()) == 0 {
		t.Fatal("no capabilities advertised")
	}
	target, ok := s.EditTarget()
	if !ok || target != netconf.Candidate {
		t.Fatalf("EditTarget = (%v, %v), want candidate", target, ok)
	}
}

func TestT1GetConfig(t *testing.T) {
	s := dialT1(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Unfiltered running config must decode as XML and include the
	// fixture module's namespace once data exists; an empty datastore
	// is also fine — the RPC path is what's under test.
	if _, err := s.GetConfig(ctx, netconf.Running, yang.Path{}); err != nil {
		t.Fatalf("get-config: %v", err)
	}
}

// TestT1CandidateEditCommitCycle drives the full F2 write path against
// the real candidate datastore and proves it by read-back.
//
// Covers conformance matrix row: nc-candidate-edit-cycle
func TestT1CandidateEditCommitCycle(t *testing.T) {
	s := dialT1(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := s.Apply(ctx, serverConfigXML(t, "t1-edge", 4242)); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	t.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = s.Apply(cleanCtx, deleteServerXML("t1-edge"))
	})

	rows := readServers(t, s)
	if rows["t1-edge"] != 4242 {
		t.Fatalf("read-back rows = %v, want t1-edge:4242", rows)
	}

	// Delete and prove removal by read-back too.
	if err := s.Apply(ctx, deleteServerXML("t1-edge")); err != nil {
		t.Fatalf("Apply delete: %v", err)
	}
	if rows := readServers(t, s); rows["t1-edge"] != 0 {
		t.Fatalf("row survived delete: %v", rows)
	}
}

// TestT1InvalidEditIsRejectedAndDiscarded is the t1 analog of the
// invalid-edit rollback proof: an
// out-of-range key value fails the edit, the library discards and
// unlocks, and read-back shows running unchanged.
//
// Covers conformance matrix row: nc-invalid-edit-discard
func TestT1InvalidEditIsRejectedAndDiscarded(t *testing.T) {
	s := dialT1(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	before := readServers(t, s)

	// port 0 violates fixture-types port-number's range 1..65535.
	bad := []byte(`<servers xmlns="urn:flowseer:fixture-main">` +
		`<server><name>bad</name><port>0</port></server></servers>`)
	err := s.Apply(ctx, bad)
	if err == nil {
		t.Fatal("out-of-range edit was accepted")
	}
	if code, ok := errs.CodeOf(err); !ok || code != netconf.ErrCodeRPC {
		t.Errorf("error code = %v, want %v (device rpc-error)", code, netconf.ErrCodeRPC)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "port") &&
		!strings.Contains(strings.ToLower(err.Error()), "range") &&
		!strings.Contains(strings.ToLower(err.Error()), "invalid") {
		t.Logf("device error text: %v", err)
	}

	after := readServers(t, s)
	if len(after) != len(before) {
		t.Fatalf("running config changed after rejected edit: before %v, after %v", before, after)
	}
	// A fresh session must be able to lock: the failed Apply released
	// the candidate lock.
	s2 := dialT1(t)
	if err := s2.Lock(ctx, netconf.Candidate); err != nil {
		t.Fatalf("candidate still locked after failed Apply: %v", err)
	}
	_ = s2.Unlock(ctx, netconf.Candidate)
}
