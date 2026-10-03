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

	entry, res := idx.Lookup("192.0.2.1")
	if res != lanehost.LookupFound {
		t.Fatalf("Lookup(\"192.0.2.1\") = %v, want LookupFound", res)
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

	if _, res := idx.Lookup("192.0.2.99"); res != lanehost.LookupUnknown {
		t.Errorf("Lookup(\"192.0.2.99\") = %v, want LookupUnknown", res)
	}
}

func TestDeviceIndex_Replace(t *testing.T) {
	t.Parallel()

	idx := lanehost.NewDeviceIndex()
	idx.Add("192.0.2.1", deviceOne, bindingRefFor(bindingOne))
	idx.Add("192.0.2.1", deviceOne, bindingRefFor(bindingTwo))

	entry, res := idx.Lookup("192.0.2.1")
	if res != lanehost.LookupFound {
		t.Fatalf("Lookup(\"192.0.2.1\") = %v, want LookupFound", res)
	}
	if entry.DeviceID != deviceOne {
		t.Errorf("DeviceID = %q, want %s", entry.DeviceID, deviceOne)
	}
	if entry.Binding.GetBinding().GetId() != bindingTwo {
		t.Errorf("Binding ID = %q, want %s", entry.Binding.GetBinding().GetId(), bindingTwo)
	}
}

func TestDeviceIndex_DeviceAddedAtSecondAddressRemovesFirstAddress(t *testing.T) {
	t.Parallel()

	idx := lanehost.NewDeviceIndex()
	idx.Add("192.0.2.1", deviceOne, bindingRefFor(bindingOne))

	if entry, res := idx.Lookup("192.0.2.1"); res != lanehost.LookupFound || entry.DeviceID != deviceOne {
		t.Fatalf("Lookup(\"192.0.2.1\") = (%v, %v), want (%s, LookupFound)", entry, res, deviceOne)
	}

	idx.Add("192.0.2.2", deviceOne, bindingRefFor(bindingOne))

	if entry, res := idx.Lookup("192.0.2.1"); res != lanehost.LookupUnknown {
		t.Errorf("Lookup(\"192.0.2.1\") after move = (%v, %v), want LookupUnknown", entry, res)
	}
	if entry, res := idx.Lookup("192.0.2.2"); res != lanehost.LookupFound || entry.DeviceID != deviceOne {
		t.Errorf("Lookup(\"192.0.2.2\") = (%v, %v), want (%s, LookupFound)", entry, res, deviceOne)
	}
}

func TestDeviceIndex_PruneKeepsListedIDsAndDropsTheRest(t *testing.T) {
	t.Parallel()

	idx := lanehost.NewDeviceIndex()
	idx.Add("192.0.2.1", deviceOne, bindingRefFor(bindingOne))
	idx.Add("192.0.2.2", deviceTwo, bindingRefFor(bindingTwo))

	idx.Prune([]string{deviceOne})

	if entry, res := idx.Lookup("192.0.2.1"); res != lanehost.LookupFound || entry.DeviceID != deviceOne {
		t.Errorf("Lookup(\"192.0.2.1\") = (%v, %v), want (%s, LookupFound)", entry, res, deviceOne)
	}
	if entry, res := idx.Lookup("192.0.2.2"); res != lanehost.LookupUnknown {
		t.Errorf("Lookup(\"192.0.2.2\") = (%v, %v), want LookupUnknown", entry, res)
	}
}

func TestDeviceIndex_SharedAddressResolvesToNoDeviceUntilOneLeaves(t *testing.T) {
	t.Parallel()

	idx := lanehost.NewDeviceIndex()
	idx.Add("192.0.2.1", deviceOne, bindingRefFor(bindingOne))
	idx.Add("192.0.2.1", deviceTwo, bindingRefFor(bindingTwo))

	entry, res := idx.Lookup("192.0.2.1")
	if res != lanehost.LookupAmbiguous {
		t.Fatalf("Lookup(\"192.0.2.1\") = (%v, %v), want LookupAmbiguous", entry, res)
	}
	if entry.DeviceID != "" {
		t.Errorf("entry.DeviceID = %q, want empty for ambiguous address", entry.DeviceID)
	}

	idx.Add("192.0.2.2", deviceTwo, bindingRefFor(bindingTwo))

	entry, res = idx.Lookup("192.0.2.1")
	if res != lanehost.LookupFound || entry.DeviceID != deviceOne {
		t.Errorf("Lookup(\"192.0.2.1\") after deviceTwo moved = (%v, %v), want (%s, LookupFound)", entry, res, deviceOne)
	}
	if entry, res := idx.Lookup("192.0.2.2"); res != lanehost.LookupFound || entry.DeviceID != deviceTwo {
		t.Errorf("Lookup(\"192.0.2.2\") = (%v, %v), want (%s, LookupFound)", entry, res, deviceTwo)
	}

	idx.Add("192.0.2.1", deviceTwo, bindingRefFor(bindingTwo))
	if _, res := idx.Lookup("192.0.2.1"); res != lanehost.LookupAmbiguous {
		t.Fatalf("Lookup(\"192.0.2.1\") after re-sharing = %v, want LookupAmbiguous", res)
	}

	idx.Prune([]string{deviceOne})

	entry, res = idx.Lookup("192.0.2.1")
	if res != lanehost.LookupFound || entry.DeviceID != deviceOne {
		t.Errorf("Lookup(\"192.0.2.1\") after pruning deviceTwo = (%v, %v), want (%s, LookupFound)", entry, res, deviceOne)
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

	if _, res := idx.Lookup("192.0.2.1"); res != lanehost.LookupFound {
		t.Fatalf("Lookup(\"192.0.2.1\") after first onboarder = %v, want LookupFound", res)
	}

	lister2 := &listerFake{listings: [][]*attachv1.ListedDevice{{
		listedDeviceWithAddr(deviceOne, []byte{192, 0, 2, 1}),
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

	if entry, res := idx.Lookup("192.0.2.1"); res != lanehost.LookupFound || entry.DeviceID != deviceOne {
		t.Errorf("deviceOne in index = (%v, %v), want (%s, LookupFound)", entry, res, deviceOne)
	}
	if entry, res := idx.Lookup("192.0.2.2"); res != lanehost.LookupFound || entry.DeviceID != deviceTwo {
		t.Errorf("deviceTwo in index = (%v, %v), want (%s, LookupFound)", entry, res, deviceTwo)
	}
}

func TestDeviceIndex_MappedIPv4MatchesPlainIPv4(t *testing.T) {
	t.Parallel()

	idx := lanehost.NewDeviceIndex()
	idx.Add("::ffff:192.0.2.1", deviceOne, bindingRefFor(bindingOne))

	if _, res := idx.Lookup("192.0.2.1"); res != lanehost.LookupFound {
		t.Errorf("Lookup of the plain form = %v, want LookupFound for a device added in the mapped form", res)
	}
	if _, res := idx.Lookup("::ffff:192.0.2.1"); res != lanehost.LookupFound {
		t.Errorf("Lookup of the mapped form = %v, want LookupFound", res)
	}
}

func TestDeviceIndex_LinkLocalIPv6WithZone(t *testing.T) {
	t.Parallel()

	idx := lanehost.NewDeviceIndex()
	idx.Add("fe80::1", deviceOne, bindingRefFor(bindingOne))

	if entry, res := idx.Lookup("fe80::1%en0"); res != lanehost.LookupFound || entry.DeviceID != deviceOne {
		t.Errorf("Lookup(\"fe80::1%%en0\") = (%v, %v), want (%s, LookupFound)", entry, res, deviceOne)
	}
}
