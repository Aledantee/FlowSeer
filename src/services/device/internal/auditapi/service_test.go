package auditapi_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	connect "connectrpc.com/connect"

	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/inventory/v1"
	eventv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/device/v1"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/auditapi"
)

const deviceID = "0192e6a0-0000-7000-8000-0000000000d1"

// tenant is the tenant the hub's audit stream is scoped to.
var tenant = edgebus.DefaultTenant

func event(id string) *eventv1.DeviceOperationEvent {
	device := &inventoryv1.DeviceGlobalRef{}
	local := &inventoryv1.DeviceLocalRef{}
	local.SetId(deviceID)
	device.SetDevice(local)
	ev := &eventv1.DeviceOperationEvent{}
	ev.SetDevice(device)
	ev.SetEventId(id)
	ev.SetLaneReleased(&eventv1.LaneReleased{})
	return ev
}

func deliver(t *testing.T, svc *auditapi.Service, id string) error {
	t.Helper()
	req := &eventv1.DeliverRequest{}
	req.SetEvent(event(id))
	_, err := svc.Deliver(context.Background(), connect.NewRequest(req))
	return err
}

type refusing struct{}

func (refusing) Publish(context.Context, string, []byte, string) error {
	return errors.New("stream refused the publish")
}

// binding authorizes a delivery; hosts controls whether the calling edge is
// said to host the device.
type binding struct {
	hosts bool
}

func (binding) EdgeID(context.Context) (string, error) { return "edge-1", nil }
func (b binding) Hosts(context.Context, string, string) (bool, error) {
	return b.hosts, nil
}

func TestDeliverFailsWhenTheStreamRefuses(t *testing.T) {
	svc := auditapi.New(refusing{}, binding{hosts: true}, tenant)
	if err := deliver(t, svc, "0192e6a0-0000-7000-8000-00000000e001"); err == nil {
		t.Fatal("Deliver answered success though the stream refused the publish")
	}
}

func TestDeliverRefusesADeviceTheEdgeDoesNotHost(t *testing.T) {
	// The publisher would succeed; the binding must stop the delivery first,
	// so a forged device id never reaches the central-owned stream.
	svc := auditapi.New(refusing{}, binding{hosts: false}, tenant)
	err := deliver(t, svc, "0192e6a0-0000-7000-8000-00000000e0ff")
	if err == nil {
		t.Fatal("Deliver accepted an event for a device the edge does not host")
	}
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want permission denied", connect.CodeOf(err))
	}
}

func TestDeliverIsDurableAndDeduplicates(t *testing.T) {
	ctx := context.Background()
	hub, err := edgebus.StartHub(ctx, edgebus.HubConfig{
		StateDir:    t.TempDir(),
		FsyncPolicy: service.BusFsyncPeriodic,
		ListenPort:  0,
	})
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	t.Cleanup(hub.Close)

	svc := auditapi.New(auditapi.JetStreamPublisher{JS: hub.JetStream()}, binding{hosts: true}, tenant)

	const id = "0192e6a0-0000-7000-8000-00000000e002"
	if err := deliver(t, svc, id); err != nil {
		t.Fatalf("first deliver: %v", err)
	}
	// A resubmission of the same event id is stored once.
	if err := deliver(t, svc, id); err != nil {
		t.Fatalf("duplicate deliver: %v", err)
	}
	if got := streamMsgs(ctx, t, hub); got != 1 {
		t.Fatalf("audit stream holds %d messages after a duplicate, want 1", got)
	}
	// A distinct event is a new record.
	if err := deliver(t, svc, "0192e6a0-0000-7000-8000-00000000e003"); err != nil {
		t.Fatalf("second deliver: %v", err)
	}
	if got := streamMsgs(ctx, t, hub); got != 2 {
		t.Fatalf("audit stream holds %d messages, want 2", got)
	}
}

func streamMsgs(ctx context.Context, t *testing.T, hub *edgebus.Hub) uint64 {
	t.Helper()
	stream, err := hub.JetStream().Stream(ctx, edgebus.AuditStream)
	if err != nil {
		t.Fatalf("audit stream: %v", err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	return info.State.Msgs
}

// The audit stream is central's, and what refused a publish on it is central's
// business: the edge learns to deliver again, not what the stream said.
func TestDeliverSendsNoStreamDetailToTheEdge(t *testing.T) {
	svc := auditapi.New(refusing{}, binding{hosts: true}, tenant)

	err := deliver(t, svc, "0192e6a0-0000-7000-8000-00000000e002")
	if err == nil {
		t.Fatal("Deliver answered success though the stream refused the publish")
	}
	if got := connect.CodeOf(err); got != connect.CodeUnavailable {
		t.Errorf("code = %v, want unavailable", got)
	}
	if strings.Contains(err.Error(), "stream refused the publish") {
		t.Errorf("the edge was sent the stream's own refusal: %q", err.Error())
	}
}
