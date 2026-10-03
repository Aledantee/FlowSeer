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

	"buf.build/go/protovalidate"

	attachv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/attach/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	policyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/policy/v1"
	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	"go.aledante.io/FlowSeer/src/common/spawn"
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
	listings [][]*attachv1.ListedDevice
	calls    int
	err      error
}

func (l *listerFake) ListDevices(
	_ context.Context, _ *connect.Request[attachv1.ListDevicesRequest],
) (*connect.Response[attachv1.ListDevicesResponse], error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	if l.err != nil {
		return nil, l.err
	}
	batch := l.listings[min(l.calls-1, len(l.listings)-1)]
	return connect.NewResponse(attachv1.ListDevicesResponse_builder{Devices: batch}.Build()), nil
}

// registrarFake records every AddDevice, and fails for whichever devices a
// test names so one device's failure can be told from a whole listing's.
type registrarFake struct {
	mu       sync.Mutex
	added    []string
	sessions map[string]access.DeviceSession
	failing  map[string]bool
	stalling map[string]bool
	block    map[string]chan struct{}
	entered  chan string
}

func newRegistrar() *registrarFake {
	return &registrarFake{
		sessions: map[string]access.DeviceSession{},
		failing:  map[string]bool{},
		stalling: map[string]bool{},
		block:    map[string]chan struct{}{},
	}
}

func (r *registrarFake) AddDevice(ctx context.Context, deviceKey string, session access.DeviceSession) error {
	r.mu.Lock()
	stalling := r.stalling[deviceKey]
	block := r.block[deviceKey]
	entered := r.entered
	r.mu.Unlock()

	if entered != nil {
		select {
		case entered <- deviceKey:
		default:
		}
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
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

func (r *endpointRecorder) openSNMP(endpoint lanehost.Endpoint) func(context.Context, *attachv1.DeviceCredential) (access.SNMPSession, error) {
	r.mu.Lock()
	r.snmp = append(r.snmp, endpoint)
	r.mu.Unlock()
	return func(context.Context, *attachv1.DeviceCredential) (access.SNMPSession, error) {
		return access.SNMPSession{}, nil
	}
}

func (r *endpointRecorder) openShell(endpoint lanehost.Endpoint) func(context.Context, *attachv1.DeviceCredential, string) (access.ShellSession, error) {
	r.mu.Lock()
	r.shell = append(r.shell, endpoint)
	r.mu.Unlock()
	return func(context.Context, *attachv1.DeviceCredential, string) (access.ShellSession, error) {
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

func listedDevice(deviceID string, horizon time.Duration) *attachv1.ListedDevice {
	listed := attachv1.ListedDevice_builder{
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

func validateDevice(t *testing.T, dev *attachv1.ListedDevice) *attachv1.ListedDevice {
	t.Helper()
	if err := protovalidate.Validate(dev); err != nil {
		t.Fatalf("protovalidate: %v", err)
	}
	return dev
}

func edgeRef() *edgev1.EdgeGlobalRef {
	return edgev1.EdgeGlobalRef_builder{
		Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(edgeID)}.Build(),
	}.Build()
}

func newOnboarder(t *testing.T, lister *listerFake, registrar *registrarFake, logs slog.Handler, index ...*lanehost.DeviceIndex) *lanehost.Onboarder {
	t.Helper()
	return newOnboarderOver(t, lister, registrar, logs, nil, index...)
}

func newOnboarderOver(t *testing.T, lister *listerFake, registrar *registrarFake, logs slog.Handler, endpoints *endpointRecorder, index ...*lanehost.DeviceIndex) *lanehost.Onboarder {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	if logs != nil {
		logger = slog.New(logs)
	}
	var idx *lanehost.DeviceIndex
	if len(index) > 0 {
		idx = index[0]
	}
	cfg := lanehost.OnboardConfig{
		Client: lister, Lane: registrar, Edge: edgeRef(), Index: idx, Logger: logger,
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
	dev1 := validateDevice(t, listedDevice(deviceOne, 30*time.Second))
	dev2 := listedDevice(deviceTwo, time.Minute)
	dev2.SetIp(addrv1.IpAddress_builder{V4: addrv1.Ipv4Address_builder{Octets: []byte{172, 16, 0, 7}}.Build()}.Build())
	validateDevice(t, dev2)
	lister := &listerFake{listings: [][]*attachv1.ListedDevice{{
		dev1,
		dev2,
	}}}
	registrar := newRegistrar()
	idx := lanehost.NewDeviceIndex()

	if err := newOnboarder(t, lister, registrar, nil, idx).Sync(context.Background()); err != nil {
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

	entry, res := idx.Lookup("172.16.0.6")
	if res != lanehost.LookupFound {
		t.Fatalf("Lookup(\"172.16.0.6\") = (%v, %v), want LookupFound", entry, res)
	}
	if entry.DeviceID != deviceOne {
		t.Errorf("index DeviceID = %q, want %s", entry.DeviceID, deviceOne)
	}
	if entry.Binding == nil || entry.Binding.GetBinding().GetId() != bindingID {
		t.Errorf("index Binding = %v, want ID %s", entry.Binding, bindingID)
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
	lister := &listerFake{listings: [][]*attachv1.ListedDevice{{listedDevice(deviceOne, time.Minute), withPorts}}}
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
	lister := &listerFake{listings: [][]*attachv1.ListedDevice{{listedDevice(deviceOne, 0)}}}
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
	lister := &listerFake{listings: [][]*attachv1.ListedDevice{
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
	lister := &listerFake{listings: [][]*attachv1.ListedDevice{{listedDevice(deviceOne, 30*time.Second)}}}
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
	lister := &listerFake{listings: [][]*attachv1.ListedDevice{{
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
	lister := &listerFake{listings: [][]*attachv1.ListedDevice{{
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

func TestOnboard_UnusableAddressIsReportedAndNotOnboarded(t *testing.T) {
	t.Parallel()

	invalidDevice := listedDevice(deviceOne, 30*time.Second)
	invalidDevice.SetIp(addrv1.IpAddress_builder{
		V4: addrv1.Ipv4Address_builder{Octets: []byte{1, 2}}.Build(),
	}.Build())

	lister := &listerFake{listings: [][]*attachv1.ListedDevice{{invalidDevice}}}
	registrar := newRegistrar()
	logs := &recordingLogs{}
	idx := lanehost.NewDeviceIndex()

	onboarder := newOnboarder(t, lister, registrar, logs, idx)
	if err := onboarder.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if got := registrar.addedDevices(); len(got) != 0 {
		t.Fatalf("added devices = %v, want none", got)
	}

	attrs, ok := logs.event("flowseer.edge.device.onboarding_failed")
	if !ok {
		t.Fatal("onboarding_failed event not emitted")
	}
	if attrs["flowseer.device.id"] != deviceOne {
		t.Errorf("device id = %q, want %q", attrs["flowseer.device.id"], deviceOne)
	}
	if attrs["error.type"] != string(lanehost.ErrCodeOnboard) {
		t.Errorf("error.type = %q, want %q", attrs["error.type"], lanehost.ErrCodeOnboard)
	}
}

func TestSync_ListingDropsDeviceRemovesItsAddressFromIndex(t *testing.T) {
	t.Parallel()

	lister := &listerFake{listings: [][]*attachv1.ListedDevice{
		{listedDevice(deviceOne, 30*time.Second)},
		{},
	}}
	registrar := newRegistrar()
	idx := lanehost.NewDeviceIndex()
	onboarder := newOnboarder(t, lister, registrar, nil, idx)

	if err := onboarder.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 1: %v", err)
	}
	if entry, res := idx.Lookup("172.16.0.6"); res != lanehost.LookupFound || entry.DeviceID != deviceOne {
		t.Fatalf("Lookup(\"172.16.0.6\") after first Sync = (%v, %v), want (%s, LookupFound)", entry, res, deviceOne)
	}

	if err := onboarder.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 2: %v", err)
	}
	if entry, res := idx.Lookup("172.16.0.6"); res != lanehost.LookupUnknown {
		t.Errorf("Lookup(\"172.16.0.6\") after dropped listing = (%v, %v), want LookupUnknown", entry, res)
	}
}

func TestSync_FailedListDevicesLeavesIndexAsItWas(t *testing.T) {
	t.Parallel()

	lister := &listerFake{listings: [][]*attachv1.ListedDevice{
		{listedDevice(deviceOne, 30*time.Second)},
	}}
	registrar := newRegistrar()
	idx := lanehost.NewDeviceIndex()
	onboarder := newOnboarder(t, lister, registrar, nil, idx)

	if err := onboarder.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 1: %v", err)
	}
	if entry, res := idx.Lookup("172.16.0.6"); res != lanehost.LookupFound || entry.DeviceID != deviceOne {
		t.Fatalf("Lookup(\"172.16.0.6\") = (%v, %v), want (%s, LookupFound)", entry, res, deviceOne)
	}

	lister.mu.Lock()
	lister.err = connect.NewError(connect.CodeUnavailable, errors.New("central down"))
	lister.mu.Unlock()

	err := onboarder.Sync(context.Background())
	if err == nil {
		t.Fatal("Sync 2 error = nil, want failure")
	}

	if entry, res := idx.Lookup("172.16.0.6"); res != lanehost.LookupFound || entry.DeviceID != deviceOne {
		t.Errorf("Lookup(\"172.16.0.6\") after failed Sync = (%v, %v), want (%s, LookupFound)", entry, res, deviceOne)
	}
}

func TestSync_ListingDropsDeviceAndLaterRelistResolvesAgain(t *testing.T) {
	t.Parallel()

	lister := &listerFake{listings: [][]*attachv1.ListedDevice{
		{listedDevice(deviceOne, 30*time.Second)},
		{},
		{listedDevice(deviceOne, 30*time.Second)},
	}}
	registrar := newRegistrar()
	idx := lanehost.NewDeviceIndex()
	onboarder := newOnboarder(t, lister, registrar, nil, idx)

	if err := onboarder.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 1: %v", err)
	}
	if _, res := idx.Lookup("172.16.0.6"); res != lanehost.LookupFound {
		t.Fatalf("Lookup after Sync 1 = %v, want LookupFound", res)
	}

	if err := onboarder.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 2: %v", err)
	}
	if _, res := idx.Lookup("172.16.0.6"); res != lanehost.LookupUnknown {
		t.Fatalf("Lookup after Sync 2 = %v, want LookupUnknown", res)
	}

	if err := onboarder.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 3: %v", err)
	}
	if entry, res := idx.Lookup("172.16.0.6"); res != lanehost.LookupFound || entry.DeviceID != deviceOne {
		t.Fatalf("Lookup after Sync 3 = (%v, %v), want (%s, LookupFound)", entry, res, deviceOne)
	}
}

func TestSync_SharedAddressRelistTransitions(t *testing.T) {
	t.Parallel()

	dev1 := listedDevice(deviceOne, 30*time.Second)
	dev2 := listedDevice(deviceTwo, 30*time.Second)

	lister := &listerFake{listings: [][]*attachv1.ListedDevice{
		{dev1, dev2},
		{dev1},
		{dev1, dev2},
	}}
	registrar := newRegistrar()
	idx := lanehost.NewDeviceIndex()
	onboarder := newOnboarder(t, lister, registrar, nil, idx)

	if err := onboarder.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 1: %v", err)
	}
	if _, res := idx.Lookup("172.16.0.6"); res != lanehost.LookupAmbiguous {
		t.Fatalf("Lookup after Sync 1 = %v, want LookupAmbiguous", res)
	}

	if err := onboarder.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 2: %v", err)
	}
	if entry, res := idx.Lookup("172.16.0.6"); res != lanehost.LookupFound || entry.DeviceID != deviceOne {
		t.Fatalf("Lookup after Sync 2 = (%v, %v), want (%s, LookupFound)", entry, res, deviceOne)
	}

	if err := onboarder.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 3: %v", err)
	}
	if _, res := idx.Lookup("172.16.0.6"); res != lanehost.LookupAmbiguous {
		t.Fatalf("Lookup after Sync 3 = %v, want LookupAmbiguous", res)
	}
}

func TestSync_TwoListedDevicesAtOneAddressResolveToNeitherWhenOneFailsOnboarding(t *testing.T) {
	t.Parallel()

	dev1 := validateDevice(t, listedDevice(deviceOne, 30*time.Second))
	dev2 := validateDevice(t, listedDevice(deviceTwo, 30*time.Second))

	lister := &listerFake{listings: [][]*attachv1.ListedDevice{{dev1, dev2}}}
	registrar := newRegistrar()
	registrar.failing[deviceTwo] = true
	idx := lanehost.NewDeviceIndex()
	onboarder := newOnboarder(t, lister, registrar, nil, idx)

	if err := onboarder.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if _, res := idx.Lookup("172.16.0.6"); res != lanehost.LookupAmbiguous {
		t.Fatalf("Lookup = %v, want LookupAmbiguous for shared address even when one device failed onboarding", res)
	}
}

func TestSync_HeldDeviceRelistedAtNewAddressResolvesFromNewAddressAndNoLongerOld(t *testing.T) {
	t.Parallel()

	devOld := validateDevice(t, listedDevice(deviceOne, 30*time.Second))
	devNew := listedDevice(deviceOne, 30*time.Second)
	devNew.SetIp(addrv1.IpAddress_builder{V4: addrv1.Ipv4Address_builder{Octets: []byte{172, 16, 0, 99}}.Build()}.Build())
	validateDevice(t, devNew)

	lister := &listerFake{listings: [][]*attachv1.ListedDevice{
		{devOld},
		{devNew},
	}}
	registrar := newRegistrar()
	idx := lanehost.NewDeviceIndex()
	onboarder := newOnboarder(t, lister, registrar, nil, idx)

	if err := onboarder.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 1: %v", err)
	}
	if _, res := idx.Lookup("172.16.0.6"); res != lanehost.LookupFound {
		t.Fatalf("Lookup old after Sync 1 = %v, want LookupFound", res)
	}

	if err := onboarder.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 2: %v", err)
	}
	if _, res := idx.Lookup("172.16.0.6"); res != lanehost.LookupUnknown {
		t.Fatalf("Lookup old after Sync 2 = %v, want LookupUnknown", res)
	}
	if entry, res := idx.Lookup("172.16.0.99"); res != lanehost.LookupFound || entry.DeviceID != deviceOne {
		t.Fatalf("Lookup new after Sync 2 = (%v, %v), want (%s, LookupFound)", entry, res, deviceOne)
	}
}

func TestSync_DeviceRelistedAtNewAddressFailingOnboardingNoLongerResolvesFromOld(t *testing.T) {
	t.Parallel()

	devOld := validateDevice(t, listedDevice(deviceOne, 30*time.Second))
	devNew := listedDevice(deviceOne, 30*time.Second)
	devNew.SetIp(addrv1.IpAddress_builder{V4: addrv1.Ipv4Address_builder{Octets: []byte{172, 16, 0, 99}}.Build()}.Build())
	validateDevice(t, devNew)

	lister1 := &listerFake{listings: [][]*attachv1.ListedDevice{{devOld}}}
	reg1 := newRegistrar()
	idx := lanehost.NewDeviceIndex()
	onboarder1 := newOnboarder(t, lister1, reg1, nil, idx)
	if err := onboarder1.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 1: %v", err)
	}
	if _, res := idx.Lookup("172.16.0.6"); res != lanehost.LookupFound {
		t.Fatalf("Lookup old after Sync 1 = %v, want LookupFound", res)
	}

	lister2 := &listerFake{listings: [][]*attachv1.ListedDevice{{devNew}}}
	reg2 := newRegistrar()
	reg2.failing[deviceOne] = true
	onboarder2 := newOnboarder(t, lister2, reg2, nil, idx)
	if err := onboarder2.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 2: %v", err)
	}
	if _, res := idx.Lookup("172.16.0.6"); res != lanehost.LookupUnknown {
		t.Fatalf("Lookup old after Sync 2 = %v, want LookupUnknown", res)
	}
	if _, res := idx.Lookup("172.16.0.99"); res != lanehost.LookupUnknown {
		t.Fatalf("Lookup new after Sync 2 = %v, want LookupUnknown", res)
	}
}

func TestSync_DeviceRelistedWithUnusableAddressNoLongerResolvesFromOld(t *testing.T) {
	t.Parallel()

	devOld := validateDevice(t, listedDevice(deviceOne, 30*time.Second))
	// devBad is deliberately malformed with 3 octets to test an unusable management address.
	devBad := listedDevice(deviceOne, 30*time.Second)
	devBad.SetIp(addrv1.IpAddress_builder{V4: addrv1.Ipv4Address_builder{Octets: []byte{172, 16, 0}}.Build()}.Build())

	lister := &listerFake{listings: [][]*attachv1.ListedDevice{
		{devOld},
		{devBad},
	}}
	registrar := newRegistrar()
	idx := lanehost.NewDeviceIndex()
	onboarder := newOnboarder(t, lister, registrar, nil, idx)

	if err := onboarder.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 1: %v", err)
	}
	if _, res := idx.Lookup("172.16.0.6"); res != lanehost.LookupFound {
		t.Fatalf("Lookup old after Sync 1 = %v, want LookupFound", res)
	}

	if err := onboarder.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 2: %v", err)
	}
	if _, res := idx.Lookup("172.16.0.6"); res != lanehost.LookupUnknown {
		t.Fatalf("Lookup old after Sync 2 = %v, want LookupUnknown", res)
	}
}

func TestSync_PruneAndRecordClaimsBeforeOnboarding(t *testing.T) {
	t.Parallel()

	devDrop := validateDevice(t, listedDevice(deviceOne, 30*time.Second))
	devMove := listedDevice(deviceTwo, 30*time.Second)
	devMove.SetIp(addrv1.IpAddress_builder{V4: addrv1.Ipv4Address_builder{Octets: []byte{172, 16, 0, 7}}.Build()}.Build())
	validateDevice(t, devMove)

	devPruned := listedDevice("0192e6a0-0000-7000-8000-000000000003", 30*time.Second)
	devPruned.SetIp(addrv1.IpAddress_builder{V4: addrv1.Ipv4Address_builder{Octets: []byte{172, 16, 0, 8}}.Build()}.Build())
	validateDevice(t, devPruned)

	lister := &listerFake{listings: [][]*attachv1.ListedDevice{
		{devDrop, devMove, devPruned},
	}}
	registrar := newRegistrar()
	idx := lanehost.NewDeviceIndex()
	onboarder := newOnboarder(t, lister, registrar, nil, idx)

	if err := onboarder.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 1: %v", err)
	}

	if entry, res := idx.Lookup("172.16.0.6"); res != lanehost.LookupFound || entry.DeviceID != deviceOne {
		t.Fatalf("Lookup(devDrop) after Sync 1 = (%v, %v), want (%s, LookupFound)", entry, res, deviceOne)
	}
	if entry, res := idx.Lookup("172.16.0.7"); res != lanehost.LookupFound || entry.DeviceID != deviceTwo {
		t.Fatalf("Lookup(devMove) after Sync 1 = (%v, %v), want (%s, LookupFound)", entry, res, deviceTwo)
	}
	if entry, res := idx.Lookup("172.16.0.8"); res != lanehost.LookupFound || entry.DeviceID != "0192e6a0-0000-7000-8000-000000000003" {
		t.Fatalf("Lookup(devPruned) after Sync 1 = (%v, %v), want (devPruned, LookupFound)", entry, res)
	}

	// Sync 2 prunes devPruned from the index while keeping it in onboarder.held.
	lister.listings = append(lister.listings, []*attachv1.ListedDevice{devDrop, devMove})
	if err := onboarder.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 2: %v", err)
	}
	if _, res := idx.Lookup("172.16.0.8"); res != lanehost.LookupUnknown {
		t.Fatalf("Lookup(devPruned) after Sync 2 = %v, want LookupUnknown", res)
	}

	// Sync 3:
	// - devNew is an unheld device listed first so AddDevice is called on it first.
	// - devMove is listed at new address 172.16.0.99.
	// - devPruned is listed again at 172.16.0.8.
	// devDrop is omitted (dropped).
	devNew := listedDevice("0192e6a0-0000-7000-8000-000000000004", 30*time.Second)
	devNew.SetIp(addrv1.IpAddress_builder{V4: addrv1.Ipv4Address_builder{Octets: []byte{172, 16, 0, 10}}.Build()}.Build())
	validateDevice(t, devNew)

	devMoveNew := listedDevice(deviceTwo, 30*time.Second)
	devMoveNew.SetIp(addrv1.IpAddress_builder{V4: addrv1.Ipv4Address_builder{Octets: []byte{172, 16, 0, 99}}.Build()}.Build())
	validateDevice(t, devMoveNew)

	devPrunedAgain := listedDevice("0192e6a0-0000-7000-8000-000000000003", 30*time.Second)
	devPrunedAgain.SetIp(addrv1.IpAddress_builder{V4: addrv1.Ipv4Address_builder{Octets: []byte{172, 16, 0, 8}}.Build()}.Build())
	validateDevice(t, devPrunedAgain)

	lister.listings = append(lister.listings, []*attachv1.ListedDevice{devNew, devMoveNew, devPrunedAgain})

	release := make(chan struct{})
	entered := make(chan string, 1)
	registrar.block[devNew.GetDeviceId()] = release
	registrar.entered = entered

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	syncDone := make(chan error, 1)
	spawn.Go(ctx, "sync-runner", func() {
		syncDone <- onboarder.Sync(ctx)
	})

	inFlight := <-entered
	if inFlight != devNew.GetDeviceId() {
		t.Fatalf("entered AddDevice for %s, want %s", inFlight, devNew.GetDeviceId())
	}

	// While AddDevice is in flight:
	// 1. dropped device's address is unknown:
	if _, res := idx.Lookup("172.16.0.6"); res != lanehost.LookupUnknown {
		t.Errorf("Lookup(devDrop) during AddDevice = %v, want LookupUnknown", res)
	}

	// 2. held device moved to a new address: unknown at old, found at new:
	if _, res := idx.Lookup("172.16.0.7"); res != lanehost.LookupUnknown {
		t.Errorf("Lookup(devMove old) during AddDevice = %v, want LookupUnknown", res)
	}
	if entry, res := idx.Lookup("172.16.0.99"); res != lanehost.LookupFound || entry.DeviceID != deviceTwo {
		t.Errorf("Lookup(devMove new) during AddDevice = (%v, %v), want (%s, LookupFound)", entry, res, deviceTwo)
	}

	// 3. held device that an earlier Sync pruned and this one lists again is found:
	if entry, res := idx.Lookup("172.16.0.8"); res != lanehost.LookupFound || entry.DeviceID != "0192e6a0-0000-7000-8000-000000000003" {
		t.Errorf("Lookup(devPruned) during AddDevice = (%v, %v), want (devPruned, LookupFound)", entry, res)
	}

	close(release)

	if err := <-syncDone; err != nil {
		t.Fatalf("Sync 3: %v", err)
	}
}

func TestSync_HeldDeviceRelistedWithDifferentBindingReturnsListedBinding(t *testing.T) {
	t.Parallel()

	dev1 := validateDevice(t, listedDevice(deviceOne, 30*time.Second))

	const otherBinding = "0192e6a0-0000-7000-8000-0000000000b2"
	dev2 := listedDevice(deviceOne, 30*time.Second)
	dev2.SetBindingId(otherBinding)
	validateDevice(t, dev2)

	lister := &listerFake{listings: [][]*attachv1.ListedDevice{
		{dev1},
		{dev2},
	}}
	registrar := newRegistrar()
	idx := lanehost.NewDeviceIndex()
	onboarder := newOnboarder(t, lister, registrar, nil, idx)

	if err := onboarder.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 1: %v", err)
	}
	entry, res := idx.Lookup("172.16.0.6")
	if res != lanehost.LookupFound {
		t.Fatalf("Lookup after Sync 1 = %v, want LookupFound", res)
	}
	if entry.Binding.GetBinding().GetId() != bindingID {
		t.Fatalf("Binding after Sync 1 = %s, want %s", entry.Binding.GetBinding().GetId(), bindingID)
	}

	if err := onboarder.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 2: %v", err)
	}
	entry, res = idx.Lookup("172.16.0.6")
	if res != lanehost.LookupFound {
		t.Fatalf("Lookup after Sync 2 = %v, want LookupFound", res)
	}
	if entry.Binding.GetBinding().GetId() != otherBinding {
		t.Errorf("Binding after Sync 2 = %s, want %s", entry.Binding.GetBinding().GetId(), otherBinding)
	}
}

func TestSync_HeldDeviceAddressUnusableThenUsableAgain(t *testing.T) {
	t.Parallel()

	devValid := validateDevice(t, listedDevice(deviceOne, 30*time.Second))
	// devBad is deliberately malformed with 3 octets to test an unusable management address.
	devBad := listedDevice(deviceOne, 30*time.Second)
	devBad.SetIp(addrv1.IpAddress_builder{V4: addrv1.Ipv4Address_builder{Octets: []byte{172, 16, 0}}.Build()}.Build())

	devValidAgain := validateDevice(t, listedDevice(deviceOne, 30*time.Second))

	lister := &listerFake{listings: [][]*attachv1.ListedDevice{
		{devValid},
		{devBad},
		{devValidAgain},
	}}
	registrar := newRegistrar()
	idx := lanehost.NewDeviceIndex()
	onboarder := newOnboarder(t, lister, registrar, nil, idx)

	if err := onboarder.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 1: %v", err)
	}
	if _, res := idx.Lookup("172.16.0.6"); res != lanehost.LookupFound {
		t.Fatalf("Lookup after Sync 1 = %v, want LookupFound", res)
	}

	if err := onboarder.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 2: %v", err)
	}
	if _, res := idx.Lookup("172.16.0.6"); res != lanehost.LookupUnknown {
		t.Fatalf("Lookup after Sync 2 = %v, want LookupUnknown", res)
	}

	if err := onboarder.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 3: %v", err)
	}
	if entry, res := idx.Lookup("172.16.0.6"); res != lanehost.LookupFound || entry.DeviceID != deviceOne {
		t.Errorf("Lookup after Sync 3 = (%v, %v), want (%s, LookupFound)", entry, res, deviceOne)
	}
}

func TestSync_HeldDevicesSwappingAddressesInOneListing(t *testing.T) {
	t.Parallel()

	dev1At6 := validateDevice(t, listedDevice(deviceOne, 30*time.Second))
	dev2At7 := listedDevice(deviceTwo, 30*time.Second)
	dev2At7.SetIp(addrv1.IpAddress_builder{V4: addrv1.Ipv4Address_builder{Octets: []byte{172, 16, 0, 7}}.Build()}.Build())
	validateDevice(t, dev2At7)

	lister := &listerFake{listings: [][]*attachv1.ListedDevice{
		{dev1At6, dev2At7},
	}}
	registrar := newRegistrar()
	idx := lanehost.NewDeviceIndex()
	onboarder := newOnboarder(t, lister, registrar, nil, idx)

	if err := onboarder.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 1: %v", err)
	}
	if entry, res := idx.Lookup("172.16.0.6"); res != lanehost.LookupFound || entry.DeviceID != deviceOne {
		t.Fatalf("Lookup 172.16.0.6 after Sync 1 = (%v, %v), want (%s, LookupFound)", entry, res, deviceOne)
	}
	if entry, res := idx.Lookup("172.16.0.7"); res != lanehost.LookupFound || entry.DeviceID != deviceTwo {
		t.Fatalf("Lookup 172.16.0.7 after Sync 1 = (%v, %v), want (%s, LookupFound)", entry, res, deviceTwo)
	}

	// Swapped listing: dev1 at 172.16.0.7, dev2 at 172.16.0.6
	dev1At7 := listedDevice(deviceOne, 30*time.Second)
	dev1At7.SetIp(addrv1.IpAddress_builder{V4: addrv1.Ipv4Address_builder{Octets: []byte{172, 16, 0, 7}}.Build()}.Build())
	validateDevice(t, dev1At7)

	dev2At6 := validateDevice(t, listedDevice(deviceTwo, 30*time.Second))

	lister.listings = append(lister.listings, []*attachv1.ListedDevice{dev1At7, dev2At6})

	if err := onboarder.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 2: %v", err)
	}
	if entry, res := idx.Lookup("172.16.0.6"); res != lanehost.LookupFound || entry.DeviceID != deviceTwo {
		t.Errorf("Lookup 172.16.0.6 after swap = (%v, %v), want (%s, LookupFound)", entry, res, deviceTwo)
	}
	if entry, res := idx.Lookup("172.16.0.7"); res != lanehost.LookupFound || entry.DeviceID != deviceOne {
		t.Errorf("Lookup 172.16.0.7 after swap = (%v, %v), want (%s, LookupFound)", entry, res, deviceOne)
	}
}
