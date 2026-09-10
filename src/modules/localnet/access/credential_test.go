package access_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	credentialv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/credential/v1"
	policyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/policy/v1"
	"go.aledante.io/FlowSeer/src/modules/localnet/access"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/credential"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/telemetry"
)

func policyHandle(key string, version uint64) *policyv1.AccessPolicyHandle {
	handle := &policyv1.AccessPolicyHandle{}
	handle.SetKey(key)
	handle.SetVersion(version)
	return handle
}

func laneWithCredentials(t *testing.T, source *recordingCredentials) *access.Lane {
	t.Helper()
	view, err := telemetry.NewView(telemetry.ViewConfig{})
	if err != nil {
		t.Fatalf("NewView() error: %v", err)
	}
	return access.NewLane(access.Config{
		QueueCapacity:    4,
		Audit:            noopDeliverer{},
		Telemetry:        view,
		Clock:            time.Now,
		ReadCredentials:  source,
		OperationTimeout: 2 * time.Second,
	})
}

// TestEveryReadAndTheOnboardingProbeAcquireTheirOwnCredential is the
// central claim of the per-operation session design: there is no standing
// authority. Onboarding acquires under the device's own handle, each read
// acquires under the handle central put on that read, and the count grows
// with the number of operations rather than staying at one.
//
// It asserts the acquisitions that happened, not the absence of a cached
// one. A test that checked "the lane holds no session" would pass for a
// lane that never contacted the device at all.
func TestEveryReadAndTheOnboardingProbeAcquireTheirOwnCredential(t *testing.T) {
	source := &recordingCredentials{}
	l := laneWithCredentials(t, source)

	err := l.AddDevice(context.Background(), "dev-1", access.DeviceSession{
		DelayedEffect: interfaces.DelayedEffect{Horizon: time.Minute},
		OpenSNMP:      probeFactory(),
		AccessPolicy:  policyHandle("onboarding-policy", 3),
		BindingID:     "binding-1",
		ReadOverride: func(context.Context, string) (*accessv1.InterfaceObservation, error) {
			return completeObservation("uplink to core"), nil
		},
	})
	if err != nil {
		t.Fatalf("AddDevice() error: %v", err)
	}

	handles, devices := source.acquisitions()
	if len(handles) != 1 || handles[0] != "onboarding-policy" {
		t.Fatalf("after onboarding, handles = %v, want one acquisition under onboarding-policy", handles)
	}
	if devices[0] != "dev-1" {
		t.Errorf("acquired for device %q, want dev-1", devices[0])
	}

	// Two reads, each carrying its own access policy.
	for _, key := range []string{"read-policy-a", "read-policy-b"} {
		req := readRequest()
		req.GetRead().SetAccessPolicy(policyHandle(key, 7))
		req.SetSequence(1)
		if _, err := l.Submit(context.Background(), access.SubmitOptions{
			DeviceKey: "dev-1",
			Request:   req,
		}); err != nil {
			t.Fatalf("Submit(%s) error: %v", key, err)
		}
	}

	// Each read acquires under the handle it carries, in order. The
	// identity probe also acquires, under the device's own handle, and it
	// runs at onboarding and again after every observation to check the
	// firmware epoch — so the sequence interleaves. What matters is that
	// the read handles are exactly the ones central sent, once each: a
	// probe acquisition can never stand in for a read's.
	handles, _ = source.acquisitions()
	var readHandles []string
	for _, handle := range handles {
		if handle != "onboarding-policy" {
			readHandles = append(readHandles, handle)
		}
	}
	want := []string{"read-policy-a", "read-policy-b"}
	if len(readHandles) != len(want) {
		t.Fatalf("read handles = %v (all acquisitions %v), want %v", readHandles, handles, want)
	}
	for i := range want {
		if readHandles[i] != want[i] {
			t.Fatalf("read handles = %v, want %v: each read must acquire under the handle it carries", readHandles, want)
		}
	}
}

// TestReadOverrideStillAcquiresACredential pins the property that makes the
// test hook safe to have: replacing the device call does not replace the
// acquisition. A hook that could skip it would let every other test in this
// package pass with the credential path switched off.
func TestReadOverrideStillAcquiresACredential(t *testing.T) {
	source := &recordingCredentials{}
	l := laneWithCredentials(t, source)

	err := l.AddDevice(context.Background(), "dev-1", access.DeviceSession{
		DelayedEffect: interfaces.DelayedEffect{Horizon: time.Minute},
		OpenSNMP:      probeFactory(),
		AccessPolicy:  policyHandle("onboarding-policy", 1),
		ReadOverride: func(context.Context, string) (*accessv1.InterfaceObservation, error) {
			return completeObservation("uplink to core"), nil
		},
	})
	if err != nil {
		t.Fatalf("AddDevice() error: %v", err)
	}

	req := readRequest()
	req.GetRead().SetAccessPolicy(policyHandle("read-policy", 1))
	if _, err := l.Submit(context.Background(), access.SubmitOptions{
		DeviceKey: "dev-1",
		Request:   req,
	}); err != nil {
		t.Fatalf("Submit() error: %v", err)
	}

	handles, _ := source.acquisitions()
	var sawReadPolicy bool
	for _, handle := range handles {
		if handle == "read-policy" {
			sawReadPolicy = true
		}
	}
	if !sawReadPolicy {
		t.Fatalf("handles = %v, want the overridden read to have acquired under read-policy", handles)
	}
}

// TestAFailedAcquisitionFailsTheOperation proves the acquisition is a gate
// rather than a formality: a lane that could not get a credential must not
// fall through to the device on whatever it used last.
func TestAFailedAcquisitionFailsTheOperation(t *testing.T) {
	source := &recordingCredentials{}
	l := laneWithCredentials(t, source)

	err := l.AddDevice(context.Background(), "dev-1", access.DeviceSession{
		DelayedEffect: interfaces.DelayedEffect{Horizon: time.Minute},
		OpenSNMP:      probeFactory(),
		AccessPolicy:  policyHandle("onboarding-policy", 1),
	})
	if err != nil {
		t.Fatalf("AddDevice() error: %v", err)
	}

	var opened atomic.Int64
	source.err = errors.New("central refused the acquisition")

	// A second device cannot even onboard.
	err = l.AddDevice(context.Background(), "dev-2", access.DeviceSession{
		DelayedEffect: interfaces.DelayedEffect{Horizon: time.Minute},
		OpenSNMP:      countingProbeFactory(&opened, &atomic.Int64{}),
		AccessPolicy:  policyHandle("onboarding-policy", 1),
	})
	if err == nil {
		t.Fatal("AddDevice() error = nil, want the failed acquisition surfaced")
	}
	if n := opened.Load(); n != 0 {
		t.Errorf("opened %d sessions after a failed acquisition, want 0", n)
	}
}

// TestSessionsAreOpenedAndClosedPerOperation proves the sessions do not
// accumulate. Counting both halves matters: opening per operation without
// closing per operation is a file-descriptor leak that no test asserting
// only "a session was opened" would ever notice.
func TestSessionsAreOpenedAndClosedPerOperation(t *testing.T) {
	source := &recordingCredentials{}
	l := laneWithCredentials(t, source)

	var opened, closed atomic.Int64
	err := l.AddDevice(context.Background(), "dev-1", access.DeviceSession{
		DelayedEffect: interfaces.DelayedEffect{Horizon: time.Minute},
		OpenSNMP:      countingProbeFactory(&opened, &closed),
		AccessPolicy:  policyHandle("onboarding-policy", 1),
		ReadOverride: func(context.Context, string) (*accessv1.InterfaceObservation, error) {
			return completeObservation("uplink to core"), nil
		},
	})
	if err != nil {
		t.Fatalf("AddDevice() error: %v", err)
	}
	if opened.Load() != 1 || closed.Load() != 1 {
		t.Fatalf("after onboarding opened=%d closed=%d, want 1 and 1", opened.Load(), closed.Load())
	}

	// Every session opened is closed, however many operations run: the
	// count is what would show a leak, and a test asserting only that a
	// session was opened would never see one.
	req := readRequest()
	req.GetRead().SetAccessPolicy(policyHandle("read-policy", 1))
	if _, err := l.Submit(context.Background(), access.SubmitOptions{DeviceKey: "dev-1", Request: req}); err != nil {
		t.Fatalf("Submit() error: %v", err)
	}
	if opened.Load() != closed.Load() {
		t.Errorf("opened=%d closed=%d: every session opened must be closed", opened.Load(), closed.Load())
	}
}

// TestTheCommandGoesOverASessionOpenedFromTheGrant is the
// example for the mutation half: the shell the command travels over is
// opened from the grant's own material and pinned to the host key the grant
// names, not from anything the device was registered with.
func TestTheCommandGoesOverASessionOpenedFromTheGrant(t *testing.T) {
	material := &credentialv1.CredentialMaterial{}
	shellCredential := &credentialv1.ShellCredential{}
	shellCredential.SetUsername("grant-user")
	material.SetShell(shellCredential)

	shell := &fakeShell{}
	reporter := &recordingReporter{}

	view, err := telemetry.NewView(telemetry.ViewConfig{})
	if err != nil {
		t.Fatalf("NewView() error: %v", err)
	}
	l := access.NewLane(access.Config{
		QueueCapacity:         4,
		Audit:                 noopDeliverer{},
		Telemetry:             view,
		Clock:                 time.Now,
		Reporter:              reporter,
		OperationTimeout:      2 * time.Second,
		SubmissionCredentials: grantingSubmission{material: material, hostKey: "grant-host-key"},
	})

	err = l.AddDevice(context.Background(), "dev-1", access.DeviceSession{
		DelayedEffect: interfaces.DelayedEffect{Horizon: time.Minute},
		OpenSNMP:      probeFactory(),
		OpenShell:     shell.open,
		AccessPolicy:  policyHandle("onboarding-policy", 1),
		ReadOverride: func(context.Context, string) (*accessv1.InterfaceObservation, error) {
			return completeObservation("uplink to core"), nil
		},
	})
	if err != nil {
		t.Fatalf("AddDevice() error: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := l.Submit(context.Background(), access.SubmitOptions{
			DeviceKey: "dev-1",
			Request:   mutationRequest(1),
		})
		done <- err
	}()

	deliverCheckpoint(t, l, 1)
	waitForPhase(t, reporter, accessv1.OperationPhase_OPERATION_PHASE_VERIFIED)
	if err := deliverAck(t, l, terminalAck(1, accessv1.Disposition_DISPOSITION_VERIFIED)); err != nil {
		t.Fatalf("HandleTerminalAck() error: %v", err)
	}
	if err := awaitSubmit(t, done); err != nil {
		t.Fatalf("Submit() error: %v", err)
	}

	materials, hostKeys, commands, closes := shell.snapshot()
	if len(commands) != 1 || commands[0] != "ethernet 1/1/1=uplink to core" {
		t.Fatalf("commands = %v, want the one interface description", commands)
	}
	if len(materials) != 1 || materials[0].GetShell().GetUsername() != "grant-user" {
		t.Fatalf("the shell was opened with %v, want the grant's own material", materials)
	}
	if len(hostKeys) != 1 || hostKeys[0] != "grant-host-key" {
		t.Fatalf("host keys = %v, want the grant's own pin", hostKeys)
	}
	if closes != 1 {
		t.Errorf("the command's shell session was closed %d times, want 1", closes)
	}
}

// grantingSubmission hands out a grant carrying the material and host key a
// test names, so the shell factory can be checked against them.
type grantingSubmission struct {
	material *credentialv1.CredentialMaterial
	hostKey  string
}

func (s grantingSubmission) Open(context.Context, string, string, uint64) (credential.SubmissionHandle, error) {
	credential := &edgev1.DeviceCredential{}
	credential.SetTypedMaterial(s.material)
	grant := &edgev1.SubmissionGrant{}
	grant.SetCredential(credential)
	grant.SetSshHostKeySha256(s.hostKey)
	return grantedHandle{grant: grant}, nil
}

type grantedHandle struct{ grant *edgev1.SubmissionGrant }

func (h grantedHandle) Grant() *edgev1.SubmissionGrant { return h.grant }
func (grantedHandle) Authority() edgev1.SubmissionAuthority {
	return edgev1.SubmissionAuthority_SUBMISSION_AUTHORITY_AUTHORIZED
}
func (grantedHandle) Err() error   { return nil }
func (grantedHandle) Close() error { return nil }
