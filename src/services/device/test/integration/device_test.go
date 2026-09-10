package integration_test

import (
	"context"
	"errors"
	"sync"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	"go.aledante.io/FlowSeer/src/modules/localnet/access"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// errProbeOnly is what this device answers to any SNMP call that is not the
// identity probe. The fixture's read and mutation routes are the shell, and a
// fake that answered plausible nonsense to an interface read would be
// answering it wrongly rather than not at all.
var errProbeOnly = errors.New("this fixture device answers only the identity probe over SNMP")

// fakeDevice stands in for the switch: it holds the one interface description
// this fixture reads and writes, and remembers what it was asked to do.
//
// It substitutes the transport and nothing above it. The lane still computes
// the endpoint, still acquires a credential for every operation, and still
// pins the host key — this device records what it was handed so the test can
// assert that those happened rather than assuming them.
type fakeDevice struct {
	mu sync.Mutex
	// description is the interface's current description, which a read
	// returns and a mutation replaces.
	description string
	// firmware is what the identity probe reports as sysDescr, and therefore
	// what the firmware fingerprint is derived from.
	firmware string

	// endpoints, hostKeys and credentials record what the agent built each
	// session with.
	endpoints   []string
	hostKeys    []string
	credentials int
	// commands is one entry per description written, in order.
	commands []string
	// pinned, when non-empty, is what every read returns regardless of what
	// the device now holds.
	//
	// This is the delayed-apply case the whole design is built around: the
	// command lands, and a read taken afterwards still shows the old value
	// because the change has not become visible yet. It is the state in which
	// nobody can say whether a mutation took effect, which is the only state
	// an operator has a reason to abandon one in.
	//
	// Blocking the read instead would not do: the lane takes a baseline read
	// before it writes, so a held read stops the mutation before the command
	// goes out and leaves the device untouched — a scenario in which a
	// restore restores nothing and every assertion passes without exercising
	// anything.
	pinned string
}

// pinReads makes every read return description until unpinReads is called,
// whatever the device actually holds.
func (d *fakeDevice) pinReads(description string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pinned = description
}

func (d *fakeDevice) unpinReads() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pinned = ""
}

func newFakeDevice(description string) *fakeDevice {
	return &fakeDevice{description: description, firmware: "ICX7150 fixture firmware A"}
}

func (d *fakeDevice) snapshot() (description string, endpoints, hostKeys, commands []string, credentials int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.description, append([]string(nil), d.endpoints...), append([]string(nil), d.hostKeys...),
		append([]string(nil), d.commands...), d.credentials
}

func (d *fakeDevice) snmpFactory(endpoint string) func(context.Context, *edgev1.DeviceCredential) (access.SNMPSession, error) {
	d.mu.Lock()
	d.endpoints = append(d.endpoints, endpoint)
	d.mu.Unlock()
	return func(context.Context, *edgev1.DeviceCredential) (access.SNMPSession, error) {
		d.mu.Lock()
		d.credentials++
		d.mu.Unlock()
		return access.SNMPSession{Session: identitySession{device: d}, Close: func() error { return nil }}, nil
	}
}

func (d *fakeDevice) shellFactory(endpoint string) func(context.Context, *edgev1.DeviceCredential, string) (access.ShellSession, error) {
	d.mu.Lock()
	d.endpoints = append(d.endpoints, endpoint)
	d.mu.Unlock()
	return func(_ context.Context, _ *edgev1.DeviceCredential, hostKey string) (access.ShellSession, error) {
		d.mu.Lock()
		d.hostKeys = append(d.hostKeys, hostKey)
		d.mu.Unlock()
		return access.ShellSession{Adapter: shellAdapter{device: d}, Close: func() error { return nil }}, nil
	}
}

// shellAdapter is the two calls the interface capability makes over a shell.
type shellAdapter struct{ device *fakeDevice }

func (a shellAdapter) ReadInterface(_ context.Context, _ string) (string, interfacev1.AdminStatus, interfacev1.OperStatus, error) {
	a.device.mu.Lock()
	defer a.device.mu.Unlock()
	description := a.device.description
	if a.device.pinned != "" {
		description = a.device.pinned
	}
	return description,
		interfacev1.AdminStatus_ADMIN_STATUS_UP,
		interfacev1.OperStatus_OPER_STATUS_UP,
		nil
}

func (a shellAdapter) SetPortName(_ context.Context, name, text string) error {
	a.device.mu.Lock()
	defer a.device.mu.Unlock()
	a.device.description = text
	a.device.commands = append(a.device.commands, name+"="+text)
	return nil
}

// identitySession answers the identity probe's sysDescr Get and nothing else.
// The probe is the only SNMP this fixture's route needs: the interface read
// and the mutation both go over the shell adapter.
type identitySession struct{ device *fakeDevice }

func (s identitySession) Get(_ context.Context, oids []snmp.OID, _ ...snmp.CallOption) ([]snmp.VarBind, error) {
	if len(oids) != 2 {
		return nil, errProbeOnly
	}
	s.device.mu.Lock()
	firmware := s.device.firmware
	s.device.mu.Unlock()

	vbs := make([]snmp.VarBind, len(oids))
	for i, oid := range oids {
		// The probe asks for sysDescr and sysObjectID together; the second
		// is an object identifier and the first a string.
		if i == 1 {
			vbs[i] = snmp.ObjectIDVar{Header: snmp.Header{OID: oid, Kind: snmp.KindObjectID}, Value: snmp.MustOID(1, 3, 6, 1, 4, 1, 1991)}
			continue
		}
		vbs[i] = snmp.OctetStringVar{Header: snmp.Header{OID: oid, Kind: snmp.KindOctetString}, Value: []byte(firmware)}
	}
	return vbs, nil
}

func (identitySession) GetNext(context.Context, []snmp.OID, ...snmp.CallOption) ([]snmp.VarBind, error) {
	return nil, nil
}

func (identitySession) GetBulk(context.Context, uint8, uint8, []snmp.OID, ...snmp.CallOption) ([]snmp.VarBind, error) {
	return nil, nil
}

func (identitySession) Set(context.Context, []snmp.VarBind, ...snmp.CallOption) ([]snmp.VarBind, error) {
	return nil, nil
}

func (identitySession) Walk(context.Context, snmp.OID, ...snmp.CallOption) *snmp.Walker { return nil }

func (identitySession) BulkWalk(context.Context, snmp.OID, ...snmp.CallOption) *snmp.Walker {
	return nil
}

func (identitySession) BulkWalkRaw(context.Context, snmp.OID, ...snmp.CallOption) *snmp.RawWalker {
	return nil
}

func (identitySession) Close() error { return nil }
