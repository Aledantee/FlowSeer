//go:build yang_integration_t1

package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/restconf"
	"go.aledante.io/FlowSeer/src/common/yang"
)

// paramPath addresses one clixon-example table parameter.
func paramPath(name string) yang.Path {
	return yang.Path{Segments: []yang.Segment{
		{Module: "clixon-example", Name: "table"},
		{Name: "parameter", Keys: []yang.KeyValue{{Name: "name", Value: name}}},
	}}
}

// TestT1RootDiscovery proves the host-meta path end to end.
//
// Covers conformance matrix row: rc-host-meta-discovery
func TestT1RootDiscovery(t *testing.T) {
	s := dialT1(t)
	if s.Root() != "/restconf" {
		t.Fatalf("Root() = %q, want /restconf", s.Root())
	}
}

// TestT1EditWithReadBack drives the conditional-write path: PUT, read-back
// carries the value, delete verified gone.
//
// Covers conformance matrix row: rc-edit-read-back
func TestT1EditWithReadBack(t *testing.T) {
	s := dialT1(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	p := paramPath("t1-param")
	body := []byte(`{"clixon-example:parameter":[{"name":"t1-param","value":"42"}]}`)
	res, err := s.Put(ctx, p, body)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	t.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = s.Delete(cleanCtx, p)
	})
	if !strings.Contains(string(res.ReadBack), `"value":"42"`) &&
		!strings.Contains(string(res.ReadBack), `"value": "42"`) {
		t.Fatalf("read-back = %s, want value 42", res.ReadBack)
	}

	if err := s.Delete(ctx, p); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	after, err := s.Get(ctx, p, restconf.GetOptions{})
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if after != nil {
		t.Fatalf("resource still present after delete: %s", after)
	}
}

// TestT1ReadWholeTable reads the parent container after an edit.
func TestT1ReadWholeTable(t *testing.T) {
	s := dialT1(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	p := paramPath("t1-read")
	if _, err := s.Put(ctx, p, []byte(`{"clixon-example:parameter":[{"name":"t1-read","value":"7"}]}`)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	t.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = s.Delete(cleanCtx, p)
	})

	table := yang.Path{Segments: []yang.Segment{{Module: "clixon-example", Name: "table"}}}
	body, err := s.Get(ctx, table, restconf.GetOptions{})
	if err != nil {
		t.Fatalf("Get table: %v", err)
	}
	if !strings.Contains(string(body), "t1-read") {
		t.Fatalf("table read = %s, want the created entry", body)
	}
}
