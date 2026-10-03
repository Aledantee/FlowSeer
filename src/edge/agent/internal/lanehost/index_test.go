package lanehost_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"buf.build/go/protovalidate"

	attachv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/attach/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/lanehost"
)

const (
	bindingOne = "0192e6a0-0000-7000-8000-0000000000b1"
	bindingTwo = "0192e6a0-0000-7000-8000-0000000000b2"
)

func bindingRefFor(id string) *inventoryv1.BindingGlobalRef {
	b := inventoryv1.BindingGlobalRef_builder{
		Binding: inventoryv1.BindingLocalRef_builder{Id: proto.String(id)}.Build(),
	}.Build()
	if err := protovalidate.Validate(b); err != nil {
		panic(err)
	}
	return b
}

func TestDeviceIndex_AddAndLookup(t *testing.T) {
	t.Parallel()

	idx := lanehost.NewDeviceIndex()
	binding := bindingRefFor(bindingOne)
	idx.Add("192.0.2.1", deviceOne, binding)

	entry, ok := idx.Lookup("192.0.2.1")
	if !ok {
		t.Fatal("Lookup(\"192.0.2.1\") = false, want true")
	}
	if entry.DeviceID != deviceOne {
		t.Errorf("DeviceID = %q, want %s", entry.DeviceID, deviceOne)
	}
	if entry.Device == nil || entry.Device.GetDevice().GetId() != deviceOne {
		t.Errorf("Device ref = %v, want ID %s", entry.Device, deviceOne)
	}
	if entry.Binding == nil || entry.Binding.GetBinding().GetId() != bindingOne {
		t.Errorf("Binding ref = %v, want ID %s", entry.Binding, bindingOne)
	}

	if _, ok := idx.Lookup("192.0.2.99"); ok {
		t.Error("Lookup(\"192.0.2.99\") = true, want false")
	}
}

func TestDeviceIndex_Replace(t *testing.T) {
	t.Parallel()

	idx := lanehost.NewDeviceIndex()
	idx.Add("192.0.2.1", deviceOne, bindingRefFor(bindingOne))
	idx.Add("192.0.2.1", deviceTwo, bindingRefFor(bindingTwo))

	entry, ok := idx.Lookup("192.0.2.1")
	if !ok {
		t.Fatal("Lookup(\"192.0.2.1\") = false, want true")
	}
	if entry.DeviceID != deviceTwo {
		t.Errorf("DeviceID = %q, want %s", entry.DeviceID, deviceTwo)
	}
	if entry.Binding.GetBinding().GetId() != bindingTwo {
		t.Errorf("Binding ID = %q, want %s", entry.Binding.GetBinding().GetId(), bindingTwo)
	}
}

func listedDeviceWithAddr(deviceID string, octets []byte) *attachv1.ListedDevice {
	listed := listedDevice(deviceID, 30*time.Second)
	listed.SetIp(addrv1.IpAddress_builder{
		V4: addrv1.Ipv4Address_builder{Octets: octets}.Build(),
	}.Build())
	return listed
}

func TestDeviceIndex_LookupSurvivesSecondOnboarder(t *testing.T) {
	t.Parallel()

	idx := lanehost.NewDeviceIndex()

	lister1 := &listerFake{listings: [][]*attachv1.ListedDevice{{
		listedDeviceWithAddr(deviceOne, []byte{192, 0, 2, 1}),
	}}}
	reg1 := newRegistrar()
	cfg1 := lanehost.OnboardConfig{
		Client:           lister1,
		Lane:             reg1,
		Edge:             edgeRef(),
		Index:            idx,
		PerDeviceTimeout: 50 * time.Millisecond,
	}
	onboarder1, err := lanehost.NewOnboarder(cfg1)
	if err != nil {
		t.Fatalf("NewOnboarder 1: %v", err)
	}
	if err := onboarder1.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 1: %v", err)
	}

	if _, ok := idx.Lookup("192.0.2.1"); !ok {
		t.Fatal("Lookup(\"192.0.2.1\") after first onboarder = false, want true")
	}

	lister2 := &listerFake{listings: [][]*attachv1.ListedDevice{{
		listedDeviceWithAddr(deviceTwo, []byte{192, 0, 2, 2}),
	}}}
	reg2 := newRegistrar()
	cfg2 := lanehost.OnboardConfig{
		Client:           lister2,
		Lane:             reg2,
		Edge:             edgeRef(),
		Index:            idx,
		PerDeviceTimeout: 50 * time.Millisecond,
	}
	onboarder2, err := lanehost.NewOnboarder(cfg2)
	if err != nil {
		t.Fatalf("NewOnboarder 2: %v", err)
	}
	if err := onboarder2.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 2: %v", err)
	}

	if entry, ok := idx.Lookup("192.0.2.1"); !ok || entry.DeviceID != deviceOne {
		t.Errorf("deviceOne in index = %v (%v), want true / %s", ok, entry.DeviceID, deviceOne)
	}
	if entry, ok := idx.Lookup("192.0.2.2"); !ok || entry.DeviceID != deviceTwo {
		t.Errorf("deviceTwo in index = %v (%v), want true / %s", ok, entry.DeviceID, deviceTwo)
	}
}

func TestDeviceIndex_MappedIPv4MatchesPlainIPv4(t *testing.T) {
	t.Parallel()

	idx := lanehost.NewDeviceIndex()
	idx.Add("::ffff:192.0.2.1", deviceOne, bindingRefFor(bindingOne))

	if _, ok := idx.Lookup("192.0.2.1"); !ok {
		t.Error("Lookup of the plain form = false, want true for a device added in the mapped form")
	}
	if _, ok := idx.Lookup("::ffff:192.0.2.1"); !ok {
		t.Error("Lookup of the mapped form = false, want true")
	}
}

func TestDeviceIndex_LinkLocalIPv6WithZone(t *testing.T) {
	t.Parallel()

	idx := lanehost.NewDeviceIndex()
	idx.Add("fe80::1", deviceOne, bindingRefFor(bindingOne))

	if entry, ok := idx.Lookup("fe80::1%en0"); !ok || entry.DeviceID != deviceOne {
		t.Errorf("Lookup(\"fe80::1%%en0\") = %v (%q), want true / %s", ok, entry.DeviceID, deviceOne)
	}
}
