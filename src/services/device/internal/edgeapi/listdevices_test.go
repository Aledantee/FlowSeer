package edgeapi_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
)

// enrolledHarnessWith is enrolledHarness over a registry a test has changed.
func enrolledHarnessWith(t *testing.T, device func(*storev1.RegistryDevice)) (*harness, string) {
	t.Helper()
	h := newHarnessWith(t, device)
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	if _, err := h.edge.Enroll(context.Background(), connect.NewRequest(edgev1.EnrollRequest_builder{
		SetupKey: proto.String(h.setupKey),
		Proof:    enrollProof(t, private, public, h.setupKey),
	}.Build())); err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	return h, h.edgeID
}

func listDevices(t *testing.T, h *harness, edgeID string) *edgev1.ListDevicesResponse {
	t.Helper()
	resp, err := h.edge.ListDevices(enrollCtx(edgeID, nil), connect.NewRequest(&edgev1.ListDevicesRequest{}))
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if err := protovalidate.Validate(resp.Msg); err != nil {
		t.Fatalf("response fails its schema rules: %v", err)
	}
	return resp.Msg
}

// TestListDevicesCarriesEverythingTheEdgeCannotOtherwiseLearn covers the
// whole of what a listing is for. Each field here has exactly one source —
// the registry the edge cannot see — and an edge missing any one of them
// cannot open a session, name a binding on an observation, or acquire the
// credential for its onboarding probe.
func TestListDevicesCarriesEverythingTheEdgeCannotOtherwiseLearn(t *testing.T) {
	h, edgeID := enrolledHarnessWith(t, func(d *storev1.RegistryDevice) {
		d.SetSnmpPort(1161)
		d.SetSshPort(2222)
	})

	devices := listDevices(t, h, edgeID).GetDevices()
	if len(devices) != 1 {
		t.Fatalf("devices = %d, want the one device the registry lists for this edge", len(devices))
	}
	got := devices[0]

	if got.GetDeviceId() != testDeviceID {
		t.Errorf("device_id = %q, want %q", got.GetDeviceId(), testDeviceID)
	}
	if got.GetBindingId() != testBindingID {
		t.Errorf("binding_id = %q, want %q", got.GetBindingId(), testBindingID)
	}
	if octets := got.GetIp().GetV4().GetOctets(); !bytes.Equal(octets, []byte{172, 16, 0, 6}) {
		t.Errorf("ip = %v, want the registry's management address", octets)
	}
	if got.GetSnmpPort() != 1161 || got.GetSshPort() != 2222 {
		t.Errorf("ports = %d/%d, want the registry's 1161/2222", got.GetSnmpPort(), got.GetSshPort())
	}
	if key, version := got.GetAccessPolicy().GetKey(), got.GetAccessPolicy().GetVersion(); key != testPolicyKey || version != 3 {
		t.Errorf("access_policy = %s/%d, want %s/3", key, version, testPolicyKey)
	}
	if horizon := got.GetDelayedApplyHorizon().AsDuration(); horizon != 30*time.Second {
		t.Errorf("delayed_apply_horizon = %v, want the registry's 30s", horizon)
	}
}

// TestListDevicesLeavesTheDefaultPortsUnset proves the ports above traveled
// rather than being whatever the wire happened to hold. A handler that wrote
// 161 and 22 into every listing would pass the test above and would rob the
// edge of the ability to tell "the registry says 161" from "the registry says
// nothing", which is the difference the schema's own default rests on.
func TestListDevicesLeavesTheDefaultPortsUnset(t *testing.T) {
	h, edgeID := enrolledHarnessWith(t, nil)

	got := listDevices(t, h, edgeID).GetDevices()[0]
	if got.HasSnmpPort() || got.HasSshPort() {
		t.Errorf("ports present = %v/%v, want both absent for a registry that names none", got.HasSnmpPort(), got.HasSshPort())
	}
}

// TestListDevicesCarriesNoHorizonForAnUnmeasuredDevice keeps the device in
// the listing. The edge serves it and refuses a mutation on it, which is
// what central does with the same fact; dropping it from the listing would
// leave the edge unable to tell a device it must not mutate from one central
// has never heard of.
func TestListDevicesCarriesNoHorizonForAnUnmeasuredDevice(t *testing.T) {
	h, edgeID := enrolledHarnessWith(t, func(d *storev1.RegistryDevice) { d.ClearDelayedApplyHorizon() })

	devices := listDevices(t, h, edgeID).GetDevices()
	if len(devices) != 1 {
		t.Fatalf("devices = %d, want the unmeasured device listed all the same", len(devices))
	}
	if devices[0].HasDelayedApplyHorizon() {
		t.Errorf("delayed_apply_horizon = %v, want it absent", devices[0].GetDelayedApplyHorizon())
	}
}

// TestListDevicesAnswersOnlyTheEdgeTheAssertionNames checks the listing
// against the edge the call is authorized as, not the one the request names
// — there is nothing in the request to name one. The registry describes a
// single edge and refuses a question about another rather than answering it
// empty, because an edge told it hosts nothing onboards nothing and waits
// forever.
func TestListDevicesAnswersOnlyTheEdgeTheAssertionNames(t *testing.T) {
	h, edgeID := enrolledHarnessWith(t, nil)

	// The device is listed for the edge the registry describes: without
	// this, the refusal below would also be what an empty registry looks
	// like.
	if got := len(listDevices(t, h, edgeID).GetDevices()); got != 1 {
		t.Fatalf("devices for the registry's own edge = %d, want 1", got)
	}

	const otherEdge = "0192e6a0-0000-7000-8000-0000000000ee"
	_, err := h.edge.ListDevices(enrollCtx(otherEdge, nil), connect.NewRequest(&edgev1.ListDevicesRequest{}))
	wantConnectCode(t, err, connect.CodePermissionDenied)
}
