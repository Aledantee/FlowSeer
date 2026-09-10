package lanehost_test

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	policyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/policy/v1"
	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/lanehost"
	"go.aledante.io/FlowSeer/src/modules/localnet/access"
)

const (
	deviceOne = "0192e6a0-0000-7000-8000-0000000000d1"
	deviceTwo = "0192e6a0-0000-7000-8000-0000000000d2"
	bindingID = "0192e6a0-0000-7000-8000-0000000000b1"
	edgeID    = "0192e6a0-0000-7000-8000-0000000000ed"
)

// listerFake answers ListDevices with what a test queued: the last listing
// stands once the queue is exhausted, so a test that lists twice does not
// have to say the same thing twice.
type listerFake struct {
	mu       sync.Mutex
	listings [][]*edgev1.ListedDevice
	calls    int
	err      error
}

func (l *listerFake) ListDevices(
	_ context.Context, _ *connect.Request[edgev1.ListDevicesRequest],
) (*connect.Response[edgev1.ListDevicesResponse], error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	if l.err != nil {
		return nil, l.err
	}
	batch := l.listings[min(l.calls-1, len(l.listings)-1)]
	return connect.NewResponse(edgev1.ListDevicesResponse_builder{Devices: batch}.Build()), nil
}

// registrarFake records every AddDevice, and fails for whichever devices a
// test names so one device's failure can be told from a whole listing's.
type registrarFake struct {
	mu       sync.Mutex
	added    []string
	sessions map[string]access.DeviceSession
	failing  map[string]bool
	stalling map[string]bool
}

func newRegistrar() *registrarFake {
	return &registrarFake{
		sessions: map[string]access.DeviceSession{},
		failing:  map[string]bool{},
		stalling: map[string]bool{},
	}
}

func (r *registrarFake) AddDevice(ctx context.Context, deviceKey string, session access.DeviceSession) error {
	r.mu.Lock()
	stalling := r.stalling[deviceKey]
	r.mu.Unlock()
	if stalling {
		// What a powered-off device does: it does not refuse, it says
		// nothing, until whoever is asking gives up.
		<-ctx.Done()
		return ctx.Err()
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failing[deviceKey] {
		return errors.New("the device did not answer the identity probe")
	}
	r.added = append(r.added, deviceKey)
	r.sessions[deviceKey] = session
	return nil
}

func (r *registrarFake) addedDevices() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.added...)
}

func (r *registrarFake) session(deviceKey string) (access.DeviceSession, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, ok := r.sessions[deviceKey]
	return session, ok
}

// endpointRecorder stands in for the real session factories and keeps the
// endpoint each was built with, in the order they were built. It is what
// makes the endpoint observable: once a factory is built, the endpoint it
// captured cannot be read back out of a DeviceSession.
type endpointRecorder struct {
	mu    sync.Mutex
	snmp  []lanehost.Endpoint
	shell []lanehost.Endpoint
}

func (r *endpointRecorder) openSNMP(endpoint lanehost.Endpoint) func(context.Context, *edgev1.DeviceCredential) (access.SNMPSession, error) {
	r.mu.Lock()
	r.snmp = append(r.snmp, endpoint)
	r.mu.Unlock()
	return func(context.Context, *edgev1.DeviceCredential) (access.SNMPSession, error) {
		return access.SNMPSession{}, nil
	}
}

func (r *endpointRecorder) openShell(endpoint lanehost.Endpoint) func(context.Context, *edgev1.DeviceCredential, string) (access.ShellSession, error) {
	r.mu.Lock()
	r.shell = append(r.shell, endpoint)
	r.mu.Unlock()
	return func(context.Context, *edgev1.DeviceCredential, string) (access.ShellSession, error) {
		return access.ShellSession{}, nil
	}
}

func (r *endpointRecorder) built() ([]lanehost.Endpoint, []lanehost.Endpoint) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]lanehost.Endpoint(nil), r.snmp...), append([]lanehost.Endpoint(nil), r.shell...)
}

// recordingLogs keeps every record, so a test can assert on a signal the
// agent emits and on nothing else about it.
type recordingLogs struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordingLogs) Enabled(context.Context, slog.Level) bool { return true }
func (h *recordingLogs) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *recordingLogs) WithGroup(string) slog.Handler            { return h }

func (h *recordingLogs) Handle(_ context.Context, record slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, record)
	return nil
}

// event returns the attributes of the one record carrying an event name, or
// reports that no record did.
func (h *recordingLogs) event(name string) (map[string]string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, record := range h.records {
		attrs := map[string]string{}
		record.Attrs(func(a slog.Attr) bool {
			attrs[a.Key] = a.Value.String()
			return true
		})
		if attrs["otel.event.name"] == name {
			return attrs, true
		}
	}
	return nil, false
}

func listedDevice(deviceID string, horizon time.Duration) *edgev1.ListedDevice {
	listed := edgev1.ListedDevice_builder{
		DeviceId:  proto.String(deviceID),
		BindingId: proto.String(bindingID),
		Ip:        addrv1.IpAddress_builder{V4: addrv1.Ipv4Address_builder{Octets: []byte{172, 16, 0, 6}}.Build()}.Build(),
		AccessPolicy: policyv1.AccessPolicyHandle_builder{
			Key: proto.String("icx7150-lab"), Version: proto.Uint64(3),
		}.Build(),
	}.Build()
	if horizon > 0 {
		listed.SetDelayedApplyHorizon(durationpb.New(horizon))
	}
	return listed
}

func edgeRef() *edgev1.EdgeGlobalRef {
	return edgev1.EdgeGlobalRef_builder{
		Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(edgeID)}.Build(),
	}.Build()
}

func newOnboarder(t *testing.T, lister *listerFake, registrar *registrarFake, logs slog.Handler) *lanehost.Onboarder {
	t.Helper()
	return newOnboarderOver(t, lister, registrar, logs, nil)
}

func newOnboarderOver(t *testing.T, lister *listerFake, registrar *registrarFake, logs slog.Handler, endpoints *endpointRecorder) *lanehost.Onboarder {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	if logs != nil {
		logger = slog.New(logs)
	}
	cfg := lanehost.OnboardConfig{
		Client: lister, Lane: registrar, Edge: edgeRef(), Logger: logger,
		PerDeviceTimeout: 50 * time.Millisecond,
	}
	if endpoints != nil {
		cfg.OpenSNMP, cfg.OpenShell = endpoints.openSNMP, endpoints.openShell
	}
	onboarder, err := lanehost.NewOnboarder(cfg)
	if err != nil {
		t.Fatalf("NewOnboarder: %v", err)
	}
	return onboarder
}

// TestTheAgentOnboardsWhatItIsToldToServe is the whole point of the listing:
// before it, an edge held credentials for devices it could not locate. The
// session's binding, policy and horizon are checked because each has exactly
// one source — the listing — and each is what a later call needs to be able
// to name.
func TestTheAgentOnboardsWhatItIsToldToServe(t *testing.T) {
	lister := &listerFake{listings: [][]*edgev1.ListedDevice{{
		listedDevice(deviceOne, 30*time.Second),
		listedDevice(deviceTwo, time.Minute),
	}}}
	registrar := newRegistrar()

	if err := newOnboarder(t, lister, registrar, nil).Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	added := registrar.addedDevices()
	slices.Sort(added)
	if !slices.Equal(added, []string{deviceOne, deviceTwo}) {
		t.Fatalf("onboarded %v, want both listed devices", added)
	}

	session, ok := registrar.session(deviceOne)
	if !ok {
		t.Fatal("no session was registered for the first device")
	}
	if session.BindingID != bindingID {
		t.Errorf("BindingID = %q, want the listing's binding", session.BindingID)
	}
	if got := session.Prov.Binding.GetBinding().GetId(); got != bindingID {
		t.Errorf("provenance binding = %q, want the listing's binding", got)
	}
	if got := session.Prov.Edge.GetEdge().GetId(); got != edgeID {
		t.Errorf("provenance edge = %q, want this edge", got)
	}
	if got := session.AccessPolicy.GetVersion(); got != 3 {
		t.Errorf("AccessPolicy version = %d, want the listing's 3", got)
	}
	if session.DelayedEffect.Horizon != 30*time.Second {
		t.Errorf("DelayedEffect.Horizon = %v, want the listing's 30s", session.DelayedEffect.Horizon)
	}
	if session.OpenSNMP == nil || session.OpenShell == nil {
		t.Error("the session carries no factories; the device cannot be reached")
	}
}

// TestTheSessionFactoriesAreBuiltForWhereTheDeviceAnswers is the address
// gap's own test, and it asserts the endpoint the dialers were built with
// rather than the one the mapping computed. Nothing can read an endpoint
// back out of a DeviceSession, so a test of the mapping alone passes for an
// agent that resolves the right address and hands the dialers an empty one —
// an agent that dials nothing.
//
// An unset port stays zero here rather than becoming 161 or 22, so the
// default lives in Endpoint alone instead of being applied in two places.
func TestTheSessionFactoriesAreBuiltForWhereTheDeviceAnswers(t *testing.T) {
	withPorts := listedDevice(deviceTwo, time.Minute)
	withPorts.SetIp(addrv1.IpAddress_builder{V4: addrv1.Ipv4Address_builder{Octets: []byte{172, 16, 0, 7}}.Build()}.Build())
	withPorts.SetSnmpPort(1161)
	withPorts.SetSshPort(2222)
	lister := &listerFake{listings: [][]*edgev1.ListedDevice{{listedDevice(deviceOne, time.Minute), withPorts}}}
	endpoints := &endpointRecorder{}

	if err := newOnboarderOver(t, lister, newRegistrar(), nil, endpoints).Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	want := []lanehost.Endpoint{
		{Address: "172.16.0.6"},
		{Address: "172.16.0.7", SNMPPort: 1161, SSHPort: 2222},
	}
	snmp, shell := endpoints.built()
	if !slices.Equal(snmp, want) {
		t.Errorf("SNMP factories built for %+v, want %+v", snmp, want)
	}
	if !slices.Equal(shell, want) {
		t.Errorf("shell factories built for %+v, want %+v", shell, want)
	}
}

// TestADeviceListedWithNoHorizonIsStillOnboarded: the horizon gates
// mutations, not the device. It is onboarded with a zero horizon, which is
// what the lane refuses a mutation on, and the agent says so at onboarding
// rather than leaving the first mutation to discover it.
func TestADeviceListedWithNoHorizonIsStillOnboarded(t *testing.T) {
	lister := &listerFake{listings: [][]*edgev1.ListedDevice{{listedDevice(deviceOne, 0)}}}
	registrar := newRegistrar()
	logs := &recordingLogs{}

	if err := newOnboarder(t, lister, registrar, logs).Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if got := registrar.addedDevices(); !slices.Equal(got, []string{deviceOne}) {
		t.Fatalf("onboarded %v, want the unmeasured device onboarded", got)
	}
	session, _ := registrar.session(deviceOne)
	if session.DelayedEffect.Horizon != 0 {
		t.Errorf("DelayedEffect.Horizon = %v, want zero for an unmeasured device", session.DelayedEffect.Horizon)
	}
	if _, ok := logs.event("flowseer.edge.device.horizon_unmeasured"); !ok {
		t.Error("nothing recorded that the device was onboarded without a horizon")
	}
}

// TestARelistDoesNotReAddADeviceAlreadyOnboarded is the constraint the
// re-listing runs into: a second AddDevice for a registered device replaces
// its whole lane state, orphaning its queue and any drainer working through
// it. So the second listing must not reach the lane — and because it does
// not, a device whose listing changed keeps its first values, which is why
// the divergence is recorded rather than passed over.
func TestARelistDoesNotReAddADeviceAlreadyOnboarded(t *testing.T) {
	lister := &listerFake{listings: [][]*edgev1.ListedDevice{
		{listedDevice(deviceOne, 0)},
		{listedDevice(deviceOne, 30*time.Second)},
	}}
	registrar := newRegistrar()
	logs := &recordingLogs{}
	onboarder := newOnboarder(t, lister, registrar, logs)

	for range 2 {
		if err := onboarder.Sync(context.Background()); err != nil {
			t.Fatalf("Sync: %v", err)
		}
	}

	// The second listing was served: without this, one AddDevice would also
	// be what a second Sync that never called central looks like.
	if lister.calls != 2 {
		t.Fatalf("ListDevices calls = %d, want the re-list to have happened", lister.calls)
	}
	if got := registrar.addedDevices(); !slices.Equal(got, []string{deviceOne}) {
		t.Fatalf("onboarded %v, want the device added exactly once", got)
	}
	session, _ := registrar.session(deviceOne)
	if session.DelayedEffect.Horizon != 0 {
		t.Errorf("DelayedEffect.Horizon = %v, want the first listing's zero to have stood", session.DelayedEffect.Horizon)
	}

	attrs, ok := logs.event("flowseer.edge.device.listing_diverged")
	if !ok {
		t.Fatal("the changed listing was passed over silently")
	}
	if attrs["flowseer.device.id"] != deviceOne {
		t.Errorf("diverged event names device %q, want %q", attrs["flowseer.device.id"], deviceOne)
	}
	if !strings.Contains(attrs["flowseer.edge.device.diverged"], "delayed_apply_horizon: 0s → 30s") {
		t.Errorf("diverged = %q, want it to name the horizon and both values", attrs["flowseer.edge.device.diverged"])
	}
}

// TestARelistIsSilentAboutADeviceThatDidNotChange keeps the signal worth
// reading. It is emitted on every reconnection, so one that fired for an
// unchanged device would be one an operator learns to ignore.
func TestARelistIsSilentAboutADeviceThatDidNotChange(t *testing.T) {
	lister := &listerFake{listings: [][]*edgev1.ListedDevice{{listedDevice(deviceOne, 30*time.Second)}}}
	logs := &recordingLogs{}
	onboarder := newOnboarder(t, lister, newRegistrar(), logs)

	for range 2 {
		if err := onboarder.Sync(context.Background()); err != nil {
			t.Fatalf("Sync: %v", err)
		}
	}

	if _, ok := logs.event("flowseer.edge.device.listing_diverged"); ok {
		t.Error("an unchanged re-listing reported a divergence")
	}
}

// TestOneDeviceFailingToOnboardLeavesTheRestServed, and leaves that device
// to be tried again. A device unreachable at this moment is the ordinary
// case; costing every other device on the edge for it is not.
func TestOneDeviceFailingToOnboardLeavesTheRestServed(t *testing.T) {
	lister := &listerFake{listings: [][]*edgev1.ListedDevice{{
		listedDevice(deviceOne, 30*time.Second),
		listedDevice(deviceTwo, time.Minute),
	}}}
	registrar := newRegistrar()
	registrar.failing[deviceOne] = true
	onboarder := newOnboarder(t, lister, registrar, nil)

	if err := onboarder.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if got := registrar.addedDevices(); !slices.Equal(got, []string{deviceTwo}) {
		t.Fatalf("onboarded %v, want the reachable device served", got)
	}

	// Not recorded as held, so the next listing tries it again.
	registrar.mu.Lock()
	registrar.failing[deviceOne] = false
	registrar.mu.Unlock()
	if err := onboarder.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	added := registrar.addedDevices()
	slices.Sort(added)
	if !slices.Equal(added, []string{deviceOne, deviceTwo}) {
		t.Errorf("onboarded %v after the retry, want both devices and the second added once", added)
	}
}

// TestAFailedListingOnboardsNothingAndSaysSo. The caller is the dispatch
// loop, which logs it and opens the stream anyway; what it must not get is a
// nil error for a listing that never arrived.
func TestAFailedListingOnboardsNothingAndSaysSo(t *testing.T) {
	lister := &listerFake{err: connect.NewError(connect.CodeUnavailable, errors.New("central is down"))}
	registrar := newRegistrar()

	err := newOnboarder(t, lister, registrar, nil).Sync(context.Background())
	if err == nil {
		t.Fatal("Sync() error = nil, want the listing failure surfaced")
	}
	if got := registrar.addedDevices(); len(got) != 0 {
		t.Errorf("onboarded %v, want nothing from a listing that failed", got)
	}
}

// TestDivergedFieldsNamesEachChangeAndItsValues covers the signal's content
// directly, since an operator reading "the listing differs" is left to
// compare two things by hand.
func TestDivergedFieldsNamesEachChangeAndItsValues(t *testing.T) {
	previous := listedDevice(deviceOne, 30*time.Second)
	current := listedDevice(deviceOne, time.Minute)
	current.SetIp(addrv1.IpAddress_builder{V4: addrv1.Ipv4Address_builder{Octets: []byte{172, 16, 0, 7}}.Build()}.Build())
	current.SetSshPort(2222)

	changed := lanehost.DivergedFields(previous, current)
	want := []string{
		"address: 172.16.0.6 → 172.16.0.7",
		"ssh_port: 0 → 2222",
		"delayed_apply_horizon: 30s → 1m0s",
	}
	if !slices.Equal(changed, want) {
		t.Errorf("DivergedFields() = %v, want %v", changed, want)
	}
	if got := lanehost.DivergedFields(previous, previous); len(got) != 0 {
		t.Errorf("DivergedFields() over an unchanged listing = %v, want none", got)
	}
}

// TestADeviceThatNeverAnswersDoesNotHoldTheListingUp is why onboarding is
// bounded per device.
//
// Sync runs before every attempt to open the dispatch stream, and adding a
// device probes it over SNMP. A device that is powered off does not refuse;
// it says nothing for the backend's whole retransmit horizon. Unbounded and
// serial, a few of those delay every reconnection by minutes — with the
// heartbeat still succeeding and the listing not having failed, so from
// central the edge looks enrolled, healthy, and permanently unsubscribed.
func TestADeviceThatNeverAnswersDoesNotHoldTheListingUp(t *testing.T) {
	registrar := newRegistrar()
	registrar.stalling[deviceOne] = true
	lister := &listerFake{listings: [][]*edgev1.ListedDevice{{
		listedDevice(deviceOne, time.Minute), listedDevice(deviceTwo, time.Minute),
	}}}
	onboarder := newOnboarder(t, lister, registrar, nil)

	start := time.Now()
	if err := onboarder.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Sync took %v; a silent device held the whole listing", elapsed)
	}
	if got := registrar.addedDevices(); !slices.Equal(got, []string{deviceTwo}) {
		t.Errorf("added = %v, want only %q: the silent device is not held and is tried again", got, deviceTwo)
	}
}
