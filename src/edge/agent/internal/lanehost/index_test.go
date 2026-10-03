package lanehost_test

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"buf.build/go/protovalidate"

	attachv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/attach/v1"
	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	"go.aledante.io/FlowSeer/src/common/spawn"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/lanehost"
)

const (
	bindingOne = "0192e6a0-0000-7000-8000-0000000000b1"
	bindingTwo = "0192e6a0-0000-7000-8000-0000000000b2"
)

func TestDeviceIndex_PruneKeepsListedIDsAndDropsTheRest(t *testing.T) {
	t.Parallel()

	idx := lanehost.NewDeviceIndex()
	dev1 := listedDeviceWithAddr(deviceOne, []byte{192, 0, 2, 1})
	dev2 := listedDeviceWithAddr(deviceTwo, []byte{192, 0, 2, 2})
	idx.ApplyListing([]*attachv1.ListedDevice{dev1, dev2})

	idx.ApplyListing([]*attachv1.ListedDevice{dev1})

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
	dev1 := listedDeviceWithAddr(deviceOne, []byte{192, 0, 2, 1})
	dev2 := listedDeviceWithAddr(deviceTwo, []byte{192, 0, 2, 1})
	idx.ApplyListing([]*attachv1.ListedDevice{dev1, dev2})

	entry, res := idx.Lookup("192.0.2.1")
	if res != lanehost.LookupAmbiguous {
		t.Fatalf("Lookup(\"192.0.2.1\") = (%v, %v), want LookupAmbiguous", entry, res)
	}
	if entry.DeviceID != "" {
		t.Errorf("entry.DeviceID = %q, want empty for ambiguous address", entry.DeviceID)
	}

	dev2Moved := listedDeviceWithAddr(deviceTwo, []byte{192, 0, 2, 2})
	idx.ApplyListing([]*attachv1.ListedDevice{dev1, dev2Moved})

	entry, res = idx.Lookup("192.0.2.1")
	if res != lanehost.LookupFound || entry.DeviceID != deviceOne {
		t.Errorf("Lookup(\"192.0.2.1\") after deviceTwo moved = (%v, %v), want (%s, LookupFound)", entry, res, deviceOne)
	}
	if entry, res := idx.Lookup("192.0.2.2"); res != lanehost.LookupFound || entry.DeviceID != deviceTwo {
		t.Errorf("Lookup(\"192.0.2.2\") = (%v, %v), want (%s, LookupFound)", entry, res, deviceTwo)
	}

	idx.ApplyListing([]*attachv1.ListedDevice{dev1, dev2})
	if _, res := idx.Lookup("192.0.2.1"); res != lanehost.LookupAmbiguous {
		t.Fatalf("Lookup(\"192.0.2.1\") after re-sharing = %v, want LookupAmbiguous", res)
	}
	if _, res := idx.Lookup("192.0.2.2"); res != lanehost.LookupUnknown {
		t.Errorf("Lookup(\"192.0.2.2\") after re-sharing = %v, want LookupUnknown", res)
	}

	idx.ApplyListing([]*attachv1.ListedDevice{dev1})

	entry, res = idx.Lookup("192.0.2.1")
	if res != lanehost.LookupFound || entry.DeviceID != deviceOne {
		t.Errorf("Lookup(\"192.0.2.1\") after deviceTwo left = (%v, %v), want (%s, LookupFound)", entry, res, deviceOne)
	}
}

func listedDeviceWithAddr(deviceID string, octets []byte) *attachv1.ListedDevice {
	listed := listedDevice(deviceID, 30*time.Second)
	listed.SetIp(addrv1.IpAddress_builder{
		V4: addrv1.Ipv4Address_builder{Octets: octets}.Build(),
	}.Build())
	if err := protovalidate.Validate(listed); err != nil {
		panic(err)
	}
	return listed
}

func TestDeviceIndex_SharedAddressAcrossTwoOnboarders(t *testing.T) {
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
		listedDeviceWithAddr(deviceTwo, []byte{192, 0, 2, 1}),
	}}}
	reg2 := newRegistrar()
	reg2.failing[deviceTwo] = true
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

	if _, res := idx.Lookup("192.0.2.1"); res != lanehost.LookupAmbiguous {
		t.Errorf("Lookup(\"192.0.2.1\") with shared address across two onboarders = %v, want LookupAmbiguous", res)
	}
}

func TestDeviceIndex_ApplyListing_SharerReplacedRemainsAmbiguous(t *testing.T) {
	t.Parallel()

	idx := lanehost.NewDeviceIndex()
	dev1 := listedDeviceWithAddr(deviceOne, []byte{192, 0, 2, 1})
	dev2 := listedDeviceWithAddr(deviceTwo, []byte{192, 0, 2, 1})
	dev3 := listedDeviceWithAddr("0192e6a0-0000-7000-8000-000000000003", []byte{192, 0, 2, 1})

	idx.ApplyListing([]*attachv1.ListedDevice{dev1, dev2})

	if _, res := idx.Lookup("192.0.2.1"); res != lanehost.LookupAmbiguous {
		t.Fatalf("Lookup before replacement = %v, want LookupAmbiguous", res)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan struct{}, 2)

	spawn.Go(ctx, "sharer-writer", func() {
		toggle := false
		for ctx.Err() == nil {
			if toggle {
				idx.ApplyListing([]*attachv1.ListedDevice{dev1, dev2})
			} else {
				idx.ApplyListing([]*attachv1.ListedDevice{dev2, dev3})
			}
			toggle = !toggle
		}
		done <- struct{}{}
	}, spawn.ReportTo(func(err error) {
		t.Error(err)
		done <- struct{}{}
	}))

	spawn.Go(ctx, "sharer-reader", func() {
		for ctx.Err() == nil {
			_, res := idx.Lookup("192.0.2.1")
			if res != lanehost.LookupAmbiguous {
				t.Errorf("Lookup resolved to %v during sharer replacement, want LookupAmbiguous throughout", res)
				break
			}
		}
		done <- struct{}{}
	}, spawn.ReportTo(func(err error) {
		t.Error(err)
		done <- struct{}{}
	}))

	<-done
	<-done

	if _, res := idx.Lookup("192.0.2.1"); res != lanehost.LookupAmbiguous {
		t.Errorf("Lookup after test = %v, want LookupAmbiguous", res)
	}
}

func TestDeviceIndex_PruneBesideLookupConcurrent(t *testing.T) {
	t.Parallel()

	idx := lanehost.NewDeviceIndex()
	dev1 := listedDeviceWithAddr(deviceOne, []byte{192, 0, 2, 1})
	idx.ApplyListing([]*attachv1.ListedDevice{dev1})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan struct{}, 2)

	spawn.Go(ctx, "prune-worker", func() {
		for ctx.Err() == nil {
			idx.ApplyListing(nil)
			idx.ApplyListing([]*attachv1.ListedDevice{dev1})
		}
		done <- struct{}{}
	}, spawn.ReportTo(func(err error) {
		t.Error(err)
		done <- struct{}{}
	}))

	spawn.Go(ctx, "lookup-worker", func() {
		for ctx.Err() == nil {
			_, _ = idx.Lookup("192.0.2.1")
		}
		done <- struct{}{}
	}, spawn.ReportTo(func(err error) {
		t.Error(err)
		done <- struct{}{}
	}))

	<-done
	<-done
}

// TestDeviceIndex_MappedIPv4MatchesPlainIPv4 and
// TestDeviceIndex_LinkLocalIPv6WithZone list the plain form of a V4 and a V6
// address and look up the form a dual-stack listener reports, after a first
// listing and after a second with the same rows.
func TestDeviceIndex_MappedIPv4MatchesPlainIPv4(t *testing.T) {
	t.Parallel()

	idx := lanehost.NewDeviceIndex()
	rows := []*attachv1.ListedDevice{
		listedDeviceWithAddr(deviceOne, []byte{192, 0, 2, 10}),
		listedDeviceWithV6Addr(deviceTwo, netip.MustParseAddr("fe80::1")),
	}
	for _, step := range []string{"first listing", "second listing"} {
		idx.ApplyListing(rows)

		if entry, res := idx.Lookup("::ffff:192.0.2.10"); res != lanehost.LookupFound || entry.DeviceID != deviceOne {
			t.Errorf("Lookup of the mapped form after the %s = (%v, %v), want (%s, LookupFound)", step, entry, res, deviceOne)
		}
		if _, res := idx.Lookup("192.0.2.10"); res != lanehost.LookupFound {
			t.Errorf("Lookup of the plain form after the %s = %v, want LookupFound", step, res)
		}
	}
}

func TestDeviceIndex_LinkLocalIPv6WithZone(t *testing.T) {
	t.Parallel()

	idx := lanehost.NewDeviceIndex()
	rows := []*attachv1.ListedDevice{
		listedDeviceWithAddr(deviceOne, []byte{192, 0, 2, 10}),
		listedDeviceWithV6Addr(deviceTwo, netip.MustParseAddr("fe80::1")),
	}
	for _, step := range []string{"first listing", "second listing"} {
		idx.ApplyListing(rows)

		if entry, res := idx.Lookup("fe80::1%en0"); res != lanehost.LookupFound || entry.DeviceID != deviceTwo {
			t.Errorf("Lookup(\"fe80::1%%en0\") after the %s = (%v, %v), want (%s, LookupFound)", step, entry, res, deviceTwo)
		}
	}
}

func listedDeviceWithV6Addr(deviceID string, addr netip.Addr) *attachv1.ListedDevice {
	octets := addr.As16()
	listed := listedDevice(deviceID, 30*time.Second)
	listed.SetIp(addrv1.IpAddress_builder{
		V6: addrv1.Ipv6Address_builder{Octets: octets[:]}.Build(),
	}.Build())
	if err := protovalidate.Validate(listed); err != nil {
		panic(err)
	}
	return listed
}

// TestDeviceIndex_ApplyListingSkipsNilRowAndEmptyDeviceID builds a row the wire
// refuses, so it asserts the rejection first: the listing is then known to
// carry what the index defends against. A nil row is skipped the same way, but
// the generated getters are nil-safe, so the empty-id guard alone already
// covers it.
func TestDeviceIndex_ApplyListingSkipsNilRowAndEmptyDeviceID(t *testing.T) {
	t.Parallel()

	noID := listedDeviceWithAddr(deviceTwo, []byte{192, 0, 2, 9})
	noID.SetDeviceId("")
	if protovalidate.Validate(noID) == nil {
		t.Fatal("protovalidate accepted a listed device with no device ID, want a rejection")
	}
	valid := listedDeviceWithAddr(deviceOne, []byte{192, 0, 2, 1})

	idx := lanehost.NewDeviceIndex()
	idx.ApplyListing([]*attachv1.ListedDevice{nil, noID, valid})

	if entry, res := idx.Lookup("192.0.2.1"); res != lanehost.LookupFound || entry.DeviceID != deviceOne {
		t.Errorf("Lookup(\"192.0.2.1\") = (%v, %v), want (%s, LookupFound)", entry, res, deviceOne)
	}
	if entry, res := idx.Lookup("192.0.2.9"); res != lanehost.LookupUnknown {
		t.Errorf("Lookup of the address listed with no device ID = (%v, %v), want LookupUnknown", entry, res)
	}
}

// TestDeviceIndex_ApplyListingWithEmptyBindingIDKeepsARef pins that a listed
// binding id of "" still yields a ref, carrying the empty id, rather than nil.
// The wire refuses the listing, so the test asserts that first.
func TestDeviceIndex_ApplyListingWithEmptyBindingIDKeepsARef(t *testing.T) {
	t.Parallel()

	listed := listedDeviceWithAddr(deviceOne, []byte{192, 0, 2, 1})
	listed.SetBindingId("")
	if protovalidate.Validate(listed) == nil {
		t.Fatal("protovalidate accepted a listed device with no binding ID, want a rejection")
	}

	idx := lanehost.NewDeviceIndex()
	idx.ApplyListing([]*attachv1.ListedDevice{listed})

	entry, res := idx.Lookup("192.0.2.1")
	if res != lanehost.LookupFound {
		t.Fatalf("Lookup(\"192.0.2.1\") = %v, want LookupFound", res)
	}
	if entry.Binding == nil {
		t.Fatal("Binding = nil, want a ref carrying the empty id")
	}
	if got := entry.Binding.GetBinding().GetId(); got != "" {
		t.Errorf("Binding ID = %q, want empty", got)
	}
}

func listedDeviceOneWithBinding(octets []byte, bindingID string) *attachv1.ListedDevice {
	listed := listedDeviceWithAddr(deviceOne, octets)
	listed.SetBindingId(bindingID)
	if err := protovalidate.Validate(listed); err != nil {
		panic(err)
	}
	return listed
}

func TestDeviceIndex_DeviceListedAtTwoAddressesResolvesFromBothWithEachBinding(t *testing.T) {
	t.Parallel()

	idx := lanehost.NewDeviceIndex()
	idx.ApplyListing([]*attachv1.ListedDevice{
		listedDeviceOneWithBinding([]byte{192, 0, 2, 1}, bindingOne),
		listedDeviceOneWithBinding([]byte{192, 0, 2, 2}, bindingTwo),
	})

	for address, binding := range map[string]string{"192.0.2.1": bindingOne, "192.0.2.2": bindingTwo} {
		entry, res := idx.Lookup(address)
		if res != lanehost.LookupFound || entry.DeviceID != deviceOne {
			t.Errorf("Lookup(%q) = (%v, %v), want (%s, LookupFound)", address, entry, res, deviceOne)
			continue
		}
		if got := entry.Binding.GetBinding().GetId(); got != binding {
			t.Errorf("Lookup(%q) binding = %q, want %s", address, got, binding)
		}
	}
}

func TestDeviceIndex_RowWithUnusableAddressClaimsNothingAndOthersApply(t *testing.T) {
	t.Parallel()

	idx := lanehost.NewDeviceIndex()
	idx.ApplyListing([]*attachv1.ListedDevice{listedDeviceWithAddr(deviceOne, []byte{192, 0, 2, 1})})

	// The wire refuses a two-octet address, so the row is built without
	// validation to reach the index's own refusal.
	unusable := listedDevice(deviceOne, 30*time.Second)
	unusable.SetIp(addrv1.IpAddress_builder{
		V4: addrv1.Ipv4Address_builder{Octets: []byte{192, 0}}.Build(),
	}.Build())
	if protovalidate.Validate(unusable) == nil {
		t.Fatal("protovalidate accepted a listed device with a two-octet address, want a rejection")
	}
	idx.ApplyListing([]*attachv1.ListedDevice{
		unusable,
		listedDeviceWithAddr(deviceTwo, []byte{192, 0, 2, 2}),
	})

	if entry, res := idx.Lookup("192.0.2.1"); res != lanehost.LookupUnknown {
		t.Errorf("Lookup(\"192.0.2.1\") = (%v, %v), want LookupUnknown", entry, res)
	}
	if entry, res := idx.Lookup("192.0.2.2"); res != lanehost.LookupFound || entry.DeviceID != deviceTwo {
		t.Errorf("Lookup(\"192.0.2.2\") = (%v, %v), want (%s, LookupFound)", entry, res, deviceTwo)
	}
	if entry, res := idx.Lookup(""); res != lanehost.LookupUnknown {
		t.Errorf("Lookup(\"\") = (%v, %v), want LookupUnknown", entry, res)
	}
}

func TestDeviceIndex_SameDeviceTwiceAtOneAddressIsOneClaimWithTheLaterBinding(t *testing.T) {
	t.Parallel()

	idx := lanehost.NewDeviceIndex()
	idx.ApplyListing([]*attachv1.ListedDevice{
		listedDeviceOneWithBinding([]byte{192, 0, 2, 1}, bindingOne),
		listedDeviceOneWithBinding([]byte{192, 0, 2, 1}, bindingTwo),
	})

	entry, res := idx.Lookup("192.0.2.1")
	if res != lanehost.LookupFound {
		t.Fatalf("Lookup(\"192.0.2.1\") = %v, want LookupFound", res)
	}
	if got := entry.Binding.GetBinding().GetId(); got != bindingTwo {
		t.Errorf("binding = %q, want the later row's %s", got, bindingTwo)
	}
}

func TestDeviceIndex_ZeroValueResolvesAfterOneApplyListing(t *testing.T) {
	t.Parallel()

	var idx lanehost.DeviceIndex
	if _, res := idx.Lookup("192.0.2.1"); res != lanehost.LookupUnknown {
		t.Fatalf("Lookup on the zero value = %v, want LookupUnknown", res)
	}

	idx.ApplyListing([]*attachv1.ListedDevice{listedDeviceOneWithBinding([]byte{192, 0, 2, 1}, bindingOne)})

	entry, res := idx.Lookup("192.0.2.1")
	if res != lanehost.LookupFound || entry.DeviceID != deviceOne {
		t.Fatalf("Lookup(\"192.0.2.1\") = (%v, %v), want (%s, LookupFound)", entry, res, deviceOne)
	}
	if entry.Device.GetDevice().GetId() != deviceOne || entry.Binding.GetBinding().GetId() != bindingOne {
		t.Errorf("entry refs = (%v, %v), want device %s and binding %s", entry.Device, entry.Binding, deviceOne, bindingOne)
	}
}
