package netconf_test

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	nclib "nemith.io/netconf"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/netconf"
	"go.aledante.io/FlowSeer/src/common/yang"
)

// fakeTransport scripts the Transport seam: per-operation handlers
// keyed by the op's XML element name, a call log, and optional
// whole-transport hangs for the keepalive tests.
type fakeTransport struct {
	caps []string

	mu     sync.Mutex
	calls  []string
	fail   map[string]error // op name -> injected error
	data   map[string][]byte
	hang   bool
	closed int
}

func newFake(caps ...string) *fakeTransport {
	return &fakeTransport{
		caps: caps,
		fail: make(map[string]error),
		data: make(map[string][]byte),
	}
}

// opName extracts the operation element name via a marshal probe.
func opName(op any) string {
	raw, err := xml.Marshal(op)
	if err != nil {
		return fmt.Sprintf("unmarshalable:%T", op)
	}
	s := string(raw)
	end := strings.IndexAny(s[1:], " >")
	return s[1 : 1+end]
}

func (f *fakeTransport) Exec(ctx context.Context, op, reply any) error {
	name := opName(op)
	f.mu.Lock()
	f.calls = append(f.calls, name)
	failErr := f.fail[name]
	payload := f.data[name]
	hang := f.hang
	f.mu.Unlock()

	if hang {
		<-ctx.Done()
		return ctx.Err()
	}
	if failErr != nil {
		return failErr
	}
	if payload != nil {
		if dr, ok := reply.(interface{ setData([]byte) }); ok {
			dr.setData(payload)
		} else {
			// Decode through XML so the production reply struct is
			// exercised.
			wrapped := "<rpc-reply><data>" + string(payload) + "</data></rpc-reply>"
			if err := xml.Unmarshal([]byte(wrapped), reply); err != nil {
				return err
			}
		}
	}
	return nil
}

func (f *fakeTransport) Capabilities() []string { return f.caps }

func (f *fakeTransport) Close(context.Context) error {
	f.mu.Lock()
	f.closed++
	f.mu.Unlock()
	return nil
}

func (f *fakeTransport) callLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

const (
	capCandidate       = "urn:ietf:params:netconf:capability:candidate:1.0"
	capWritableRunning = "urn:ietf:params:netconf:capability:writable-running:1.0"
	capValidate        = "urn:ietf:params:netconf:capability:validate:1.1"
)

func rpcError(tag nclib.ErrTag, msg string) error {
	return nclib.RPCErrors{{Tag: tag, Severity: nclib.SevError, Message: msg}}
}

// Covers conformance matrix row: nc-candidate-running-readonly
func TestCapabilitySelectsEditTarget(t *testing.T) {
	tests := []struct {
		name     string
		caps     []string
		want     netconf.Datastore
		editable bool
	}{
		{name: "candidate mode", caps: []string{capCandidate, capValidate}, want: netconf.Candidate, editable: true},
		{name: "candidate wins over writable-running", caps: []string{capWritableRunning, capCandidate}, want: netconf.Candidate, editable: true},
		{name: "writable running", caps: []string{capWritableRunning}, want: netconf.Running, editable: true},
		{name: "neither", caps: []string{"urn:ietf:params:netconf:base:1.1"}, editable: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := netconf.NewSession(newFake(tc.caps...), netconf.Options{})
			defer func() { _ = s.Close(context.Background()) }()
			got, ok := s.EditTarget()
			if ok != tc.editable || (ok && got != tc.want) {
				t.Errorf("EditTarget() = (%v, %v), want (%v, %v)", got, ok, tc.want, tc.editable)
			}
		})
	}
}

func TestApplyUnsupportedWithoutWritableDatastore(t *testing.T) {
	s := netconf.NewSession(newFake("urn:ietf:params:netconf:base:1.1"), netconf.Options{})
	defer func() { _ = s.Close(context.Background()) }()
	err := s.Apply(context.Background(), []byte("<x/>"))
	if code, ok := errs.CodeOf(err); !ok || code != netconf.ErrCodeUnsupported {
		t.Fatalf("Apply on read-only peer = %v, want %v", err, netconf.ErrCodeUnsupported)
	}
}

func TestApplyCandidateHappyPath(t *testing.T) {
	f := newFake(capCandidate, capValidate)
	s := netconf.NewSession(f, netconf.Options{})
	defer func() { _ = s.Close(context.Background()) }()

	if err := s.Apply(context.Background(), []byte("<hostname>edge</hostname>")); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := []string{"lock", "edit-config", "validate", "commit", "unlock"}
	got := f.callLog()
	if len(got) != len(want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("calls = %v, want %v", got, want)
		}
	}
}

// TestApplyValidateFailureDiscardsAndUnlocks is the unit-level analog
// of the invalid-edit rollback proof: a validate rejection triggers
// discard-changes plus unlock,
// and the device's error surfaces to the caller.
// Covers conformance matrix row: nc-validate-fail-discard-unlock
func TestApplyValidateFailureDiscardsAndUnlocks(t *testing.T) {
	f := newFake(capCandidate, capValidate)
	f.fail["validate"] = rpcError(nclib.ErrOperationFailed, "ip address overlaps")
	s := netconf.NewSession(f, netconf.Options{})
	defer func() { _ = s.Close(context.Background()) }()

	err := s.Apply(context.Background(), []byte("<bad/>"))
	if err == nil {
		t.Fatal("Apply succeeded despite validate failure")
	}
	if code, ok := errs.CodeOf(err); !ok || code != netconf.ErrCodeRPC {
		t.Errorf("error code = %v, want %v", code, netconf.ErrCodeRPC)
	}
	if !strings.Contains(err.Error(), "ip address overlaps") {
		t.Errorf("device message lost: %v", err)
	}
	want := []string{"lock", "edit-config", "validate", "discard-changes", "unlock"}
	got := f.callLog()
	if len(got) != len(want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("calls = %v, want %v", got, want)
		}
	}
	// Commit never ran.
	for _, c := range got {
		if c == "commit" {
			t.Error("commit ran despite validate failure")
		}
	}
}

// Covers conformance matrix row: nc-lock-denied-retryable
func TestLockContentionIsRetryable(t *testing.T) {
	f := newFake(capCandidate)
	f.fail["lock"] = rpcError(nclib.ErrLockDenied, "lock held by session 7")
	s := netconf.NewSession(f, netconf.Options{})
	defer func() { _ = s.Close(context.Background()) }()

	err := s.Lock(context.Background(), netconf.Candidate)
	if code, ok := errs.CodeOf(err); !ok || code != netconf.ErrCodeLockDenied {
		t.Fatalf("lock error code = %v, want %v", code, netconf.ErrCodeLockDenied)
	}
	if !errs.Retryable(err) {
		t.Error("lock-denied not marked retryable")
	}
}

func TestGetReturnsDataPayload(t *testing.T) {
	f := newFake(capCandidate)
	f.data["get"] = []byte(`<interfaces xmlns="urn:x"><interface><name>eth0</name></interface></interfaces>`)
	s := netconf.NewSession(f, netconf.Options{})
	defer func() { _ = s.Close(context.Background()) }()

	payload, err := s.Get(context.Background(), yang.Path{Segments: []yang.Segment{{Module: "m", Namespace: "urn:x", Name: "interfaces"}}})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !strings.Contains(string(payload), "<name>eth0</name>") {
		t.Errorf("payload = %s", payload)
	}
}

// TestKeepaliveLatchesDeadTransport: a peer that stops responding
// trips the keepalive guard within the configured deadline and
// latches Err.
// Covers conformance matrix row: nc-dead-transport-latch
func TestKeepaliveLatchesDeadTransport(t *testing.T) {
	f := newFake(capCandidate)
	f.mu.Lock()
	f.hang = true
	f.mu.Unlock()

	s := netconf.NewSession(f, netconf.Options{KeepaliveInterval: 30 * time.Millisecond})
	defer func() { _ = s.Close(context.Background()) }()

	deadline := time.After(5 * time.Second)
	for s.Err() == nil {
		select {
		case <-deadline:
			t.Fatal("keepalive never latched a dead transport")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if code, ok := errs.CodeOf(s.Err()); !ok || code != netconf.ErrCodeTransport {
		t.Errorf("latched code = %v, want %v", code, netconf.ErrCodeTransport)
	}
	// Subsequent RPCs fail fast with the latched error.
	if _, err := s.Get(context.Background(), yang.Path{}); err == nil {
		t.Error("Get succeeded on a latched session")
	}
}

func TestCloseIdempotentAndMidRPC(t *testing.T) {
	f := newFake(capCandidate)
	f.mu.Lock()
	f.hang = true
	f.mu.Unlock()
	s := netconf.NewSession(f, netconf.Options{RPCTimeout: 100 * time.Millisecond})

	done := make(chan error, 1)
	go func() {
		_, err := s.Get(context.Background(), yang.Path{})
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)

	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Error("mid-RPC Get returned nil after Close")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight RPC never returned")
	}
	if err := s.Lock(context.Background(), netconf.Candidate); !errors.Is(err, netconf.ErrSessionClosed) {
		t.Errorf("post-Close Lock = %v, want ErrSessionClosed", err)
	}
	f.mu.Lock()
	closed := f.closed
	f.mu.Unlock()
	if closed != 1 {
		t.Errorf("transport closed %d times, want once", closed)
	}
}

func TestContextCancellationStaysUnwrapped(t *testing.T) {
	f := newFake(capCandidate)
	f.mu.Lock()
	f.hang = true
	f.mu.Unlock()
	s := netconf.NewSession(f, netconf.Options{})
	defer func() { _ = s.Close(context.Background()) }()

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	_, err := s.Get(ctx, yang.Path{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled end to end", err)
	}
	// Cancellation is the caller's doing — never latched as terminal.
	if s.Err() != nil {
		t.Errorf("caller cancellation latched: %v", s.Err())
	}
}

func TestModuleRevisionsFromHello(t *testing.T) {
	f := newFake(
		capCandidate,
		"urn:ietf:params:xml:ns:yang:ietf-interfaces?module=ietf-interfaces&revision=2014-05-08",
		"http://cisco.com/ns/yang/Cisco-IOS-XE-native?module=Cisco-IOS-XE-native&revision=2023-11-01&features=x",
		"urn:no-query-here",
	)
	s := netconf.NewSession(f, netconf.Options{})
	defer func() { _ = s.Close(context.Background()) }()

	revs := s.ModuleRevisions()
	if len(revs) != 2 || revs["ietf-interfaces"] != "2014-05-08" || revs["Cisco-IOS-XE-native"] != "2023-11-01" {
		t.Errorf("ModuleRevisions() = %v", revs)
	}
}
