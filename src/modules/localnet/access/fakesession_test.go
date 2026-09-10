package access_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	credentialv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/credential/v1"
	policyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/policy/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	"go.aledante.io/FlowSeer/src/modules/localnet/access"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/epoch"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// probeFactory is the OpenSNMP every fixture device uses: it hands out the
// identity-probe session and reports when it was closed. A test needing a
// different firmware epoch builds its own factory.
func probeFactory() func(context.Context, *edgev1.DeviceCredential) (access.SNMPSession, error) {
	return func(context.Context, *edgev1.DeviceCredential) (access.SNMPSession, error) {
		return access.SNMPSession{
			Session: fakeIdentitySession{onGet: func() {}},
			Close:   func() error { return nil },
		}, nil
	}
}

// countingProbeFactory is probeFactory that counts how many sessions were
// opened and how many were closed, so a test can prove the lane does not
// leak a session per operation.
func countingProbeFactory(opened, closed *atomic.Int64) func(context.Context, *edgev1.DeviceCredential) (access.SNMPSession, error) {
	return func(context.Context, *edgev1.DeviceCredential) (access.SNMPSession, error) {
		opened.Add(1)
		return access.SNMPSession{
			Session: fakeIdentitySession{onGet: func() {}},
			Close:   func() error { closed.Add(1); return nil },
		}, nil
	}
}

// probedFingerprint is what every fixture device's identity probe returns.
// Asked of epoch.Probe rather than hard-coded: the digest is Probe's own
// concern, and a copy of it here would go stale the first time its inputs
// or its hashing change, in a way that looks like a lane bug.
var probedFingerprint = sync.OnceValue(func() string {
	fingerprint, err := epoch.Probe(context.Background(), fakeIdentitySession{onGet: func() {}})
	if err != nil {
		panic("the identity probe fixture cannot fail: " + err.Error())
	}
	return fingerprint
})

// recordingCredentials records every acquisition, so a test can prove a
// credential was taken once per read and once per onboarding, and under
// which handle.
type recordingCredentials struct {
	mu       sync.Mutex
	handles  []string
	versions []uint64
	devices  []string
	bindings []string
	hostKey  string
	material *credentialv1.CredentialMaterial
	err      error
}

func (c *recordingCredentials) AcquireReadCredential(
	_ context.Context, deviceID, bindingID string, handle *policyv1.AccessPolicyHandle,
) (*edgev1.AcquireReadCredentialResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return nil, c.err
	}
	c.devices = append(c.devices, deviceID)
	c.bindings = append(c.bindings, bindingID)
	c.handles = append(c.handles, handle.GetKey())
	c.versions = append(c.versions, handle.GetVersion())

	credential := &edgev1.DeviceCredential{}
	if c.material != nil {
		credential.SetTypedMaterial(c.material)
	}
	response := &edgev1.AcquireReadCredentialResponse{}
	response.SetCredential(credential)
	if c.hostKey != "" {
		response.SetSshHostKeySha256(c.hostKey)
	}
	return response, nil
}

// handleVersions is the version of each acquisition's handle, in the same
// order as acquisitions. Separate from the keys because the two can differ
// independently: a deployment reuses one policy key and bumps its version, so
// a handle taken from the wrong place has the right key and the wrong version.
func (c *recordingCredentials) handleVersions() []uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]uint64(nil), c.versions...)
}

func (c *recordingCredentials) acquisitions() (handles, devices []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.handles...), append([]string(nil), c.devices...)
}

// fakeShell records the credential material it was opened with and the
// commands it was asked to send.
type fakeShell struct {
	mu       sync.Mutex
	openedAs []*credentialv1.CredentialMaterial
	hostKeys []string
	commands []string
	closes   int
}

func (f *fakeShell) open(_ context.Context, credential *edgev1.DeviceCredential, hostKey string) (access.ShellSession, error) {
	f.mu.Lock()
	f.openedAs = append(f.openedAs, credential.GetTypedMaterial())
	f.hostKeys = append(f.hostKeys, hostKey)
	f.mu.Unlock()
	return access.ShellSession{
		Adapter: shellAdapter{owner: f},
		Close: func() error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.closes++
			return nil
		},
	}, nil
}

func (f *fakeShell) snapshot() (materials []*credentialv1.CredentialMaterial, hostKeys, commands []string, closes int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*credentialv1.CredentialMaterial(nil), f.openedAs...),
		append([]string(nil), f.hostKeys...),
		append([]string(nil), f.commands...),
		f.closes
}

type shellAdapter struct{ owner *fakeShell }

func (a shellAdapter) ReadInterface(context.Context, string) (string, interfacev1.AdminStatus, interfacev1.OperStatus, error) {
	return "uplink to core", interfacev1.AdminStatus_ADMIN_STATUS_UP, interfacev1.OperStatus_OPER_STATUS_UP, nil
}

func (a shellAdapter) SetPortName(_ context.Context, name, text string) error {
	a.owner.mu.Lock()
	defer a.owner.mu.Unlock()
	a.owner.commands = append(a.owner.commands, name+"="+text)
	return nil
}

// identityAnswering builds a probe session reporting descr as the device's
// sysDescr, which is what its firmware fingerprint is derived from.
func identityAnswering(descr string) snmp.Session {
	return fakeIdentitySession{onGet: func() {}, descr: descr}
}

// fingerprintOf is the fingerprint epoch.Probe derives from descr. Asked of
// Probe rather than hard-coded, for the same reason probedFingerprint is.
func fingerprintOf(t *testing.T, descr string) string {
	t.Helper()
	fingerprint, err := epoch.Probe(context.Background(), identityAnswering(descr))
	if err != nil {
		t.Fatalf("epoch.Probe(%q) error: %v", descr, err)
	}
	return fingerprint
}
