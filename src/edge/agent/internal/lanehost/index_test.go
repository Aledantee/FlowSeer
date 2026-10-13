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
	bindingOne     = "0192e6a0-0000-7000-8000-0000000000b1"
	bindingTwo     = "0192e6a0-0000-7000-8000-0000000000b2"
	integrationTwo = "0192e6a0-0000-7000-8000-0000000000c2"
)

// apply applies one listing and fails the test when the index refuses it.
func apply(t *testing.T, idx *lanehost.DeviceIndex, rows ...*attachv1.ListedDevice) {
	t.Helper()
	if err := idx.ApplyListing(rows); err != nil {
		t.Fatalf("ApplyListing: %v", err)
	}
}

func TestDeviceIndex_PruneKeepsListedIDsAndDropsTheRest(t *testing.T) {
	t.Parallel()

	idx := lanehost.NewDeviceIndex()
	dev1 := listedDeviceWithAddr(deviceOne, []byte{192, 0, 2, 1})
	dev2 := listedDeviceWithAddr(deviceTwo, []byte{192, 0, 2, 2})
	apply(t, idx, dev1, dev2)

	apply(t, idx, dev1)

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
	apply(t, idx, dev1, dev2)

	entry, res := idx.Lookup("192.0.2.1")
	if res != lanehost.LookupAmbiguous {
		t.Fatalf("Lookup(\"192.0.2.1\") = (%v, %v), want LookupAmbiguous", entry, res)
	}
	if entry.DeviceID != "" {
		t.Errorf("entry.DeviceID = %q, want empty for ambiguous address", entry.DeviceID)
	}

	dev2Moved := listedDeviceWithAddr(deviceTwo, []byte{192, 0, 2, 2})
	apply(t, idx, dev1, dev2Moved)

	entry, res = idx.Lookup("192.0.2.1")
	if res != lanehost.LookupFound || entry.DeviceID != deviceOne {
		t.Errorf("Lookup(\"192.0.2.1\") after deviceTwo moved = (%v, %v), want (%s, LookupFound)", entry, res, deviceOne)
	}
	if entry, res := idx.Lookup("192.0.2.2"); res != lanehost.LookupFound || entry.DeviceID != deviceTwo {
		t.Errorf("Lookup(\"192.0.2.2\") = (%v, %v), want (%s, LookupFound)", entry, res, deviceTwo)
	}

	apply(t, idx, dev1, dev2)
	if _, res := idx.Lookup("192.0.2.1"); res != lanehost.LookupAmbiguous {
		t.Fatalf("Lookup(\"192.0.2.1\") after re-sharing = %v, want LookupAmbiguous", res)
	}
	if _, res := idx.Lookup("192.0.2.2"); res != lanehost.LookupUnknown {
		t.Errorf("Lookup(\"192.0.2.2\") after re-sharing = %v, want LookupUnknown", res)
	}

	apply(t, idx, dev1)

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

	apply(t, idx, dev1, dev2)

	if _, res := idx.Lookup("192.0.2.1"); res != lanehost.LookupAmbiguous {
		t.Fatalf("Lookup before replacement = %v, want LookupAmbiguous", res)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan struct{}, 2)

	spawn.Go(ctx, "sharer-writer", func() {
		toggle := false
		for ctx.Err() == nil {
			var err error
			if toggle {
				err = idx.ApplyListing([]*attachv1.ListedDevice{dev1, dev2})
			} else {
				err = idx.ApplyListing([]*attachv1.ListedDevice{dev2, dev3})
			}
			if err != nil {
				t.Errorf("ApplyListing: %v", err)
				break
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
	apply(t, idx, dev1)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan struct{}, 2)

	spawn.Go(ctx, "prune-worker", func() {
		for ctx.Err() == nil {
			if err := idx.ApplyListing(nil); err != nil {
				t.Errorf("ApplyListing of an empty listing: %v", err)
				break
			}
			if err := idx.ApplyListing([]*attachv1.ListedDevice{dev1}); err != nil {
				t.Errorf("ApplyListing: %v", err)
				break
			}
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
		apply(t, idx, rows...)

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
		apply(t, idx, rows...)

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

// TestDeviceIndex_ApplyListingRefusesAMalformedListingWhole builds rows the
// wire refuses, so it asserts the rejection first: the listing is then known to
// carry what the index defends against. Each case lists one good device beside
// the bad row and proves the good device does not resolve, since a half-applied
// listing would resolve devices to a binding and integration central did not
// pair. The snapshot taken before the refused listing stands.
func TestDeviceIndex_ApplyListingRefusesAMalformedListingWhole(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		row  func() *attachv1.ListedDevice
		// wireRefuses is false for a row the schema cannot see is wrong.
		wireRefuses bool
	}{
		{name: "nil row", row: func() *attachv1.ListedDevice { return nil }},
		{name: "no device id", wireRefuses: true, row: func() *attachv1.ListedDevice {
			row := listedDeviceWithAddr(deviceTwo, []byte{192, 0, 2, 9})
			row.SetDeviceId("")
			return row
		}},
		{name: "no binding id", wireRefuses: true, row: func() *attachv1.ListedDevice {
			row := listedDeviceWithAddr(deviceTwo, []byte{192, 0, 2, 9})
			row.SetBindingId("")
			return row
		}},
		{name: "no integration id", wireRefuses: true, row: func() *attachv1.ListedDevice {
			row := listedDeviceWithAddr(deviceTwo, []byte{192, 0, 2, 9})
			row.ClearIntegrationId()
			return row
		}},
		{name: "integration id that is not a uuid", wireRefuses: true, row: func() *attachv1.ListedDevice {
			row := listedDeviceWithAddr(deviceTwo, []byte{192, 0, 2, 9})
			row.SetIntegrationId("integration-1")
			return row
		}},
		{name: "two-octet address", wireRefuses: true, row: func() *attachv1.ListedDevice {
			row := listedDevice(deviceTwo, 30*time.Second)
			row.SetIp(addrv1.IpAddress_builder{V4: addrv1.Ipv4Address_builder{Octets: []byte{192, 0}}.Build()}.Build())
			return row
		}},
		{name: "no address", wireRefuses: true, row: func() *attachv1.ListedDevice {
			row := listedDeviceWithAddr(deviceTwo, []byte{192, 0, 2, 9})
			row.ClearIp()
			return row
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bad := tt.row()
			if tt.wireRefuses && protovalidate.Validate(bad) == nil {
				t.Fatal("protovalidate accepted the row, want a rejection")
			}
			prior := listedDeviceWithAddr(deviceOne, []byte{192, 0, 2, 1})
			good := listedDeviceWithAddr("0192e6a0-0000-7000-8000-000000000003", []byte{192, 0, 2, 3})

			idx := lanehost.NewDeviceIndex()
			apply(t, idx, prior)
			if err := idx.ApplyListing([]*attachv1.ListedDevice{good, bad}); err == nil {
				t.Fatal("ApplyListing error = nil, want the whole listing refused")
			}

			if entry, res := idx.Lookup("192.0.2.1"); res != lanehost.LookupFound || entry.DeviceID != deviceOne {
				t.Errorf("Lookup of the prior snapshot = (%v, %v), want (%s, LookupFound)", entry, res, deviceOne)
			}
			if entry, res := idx.Lookup("192.0.2.3"); res != lanehost.LookupUnknown {
				t.Errorf("Lookup of the refused listing's good row = (%v, %v), want LookupUnknown", entry, res)
			}
		})
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

func listedDeviceInIntegration(deviceID string, octets []byte, bindingID, integrationID string) *attachv1.ListedDevice {
	listed := listedDeviceWithAddr(deviceID, octets)
	listed.SetBindingId(bindingID)
	listed.SetIntegrationId(integrationID)
	if err := protovalidate.Validate(listed); err != nil {
		panic(err)
	}
	return listed
}

func TestDeviceIndex_DeviceListedAtTwoAddressesResolvesFromBothWithEachBinding(t *testing.T) {
	t.Parallel()

	idx := lanehost.NewDeviceIndex()
	apply(t, idx,
		listedDeviceInIntegration(deviceOne, []byte{192, 0, 2, 1}, bindingOne, integrationID),
		listedDeviceInIntegration(deviceOne, []byte{192, 0, 2, 2}, bindingTwo, integrationTwo),
	)

	for address, want := range map[string]struct{ binding, integration string }{
		"192.0.2.1": {bindingOne, integrationID},
		"192.0.2.2": {bindingTwo, integrationTwo},
	} {
		entry, res := idx.Lookup(address)
		if res != lanehost.LookupFound || entry.DeviceID != deviceOne {
			t.Errorf("Lookup(%q) = (%v, %v), want (%s, LookupFound)", address, entry, res, deviceOne)
			continue
		}
		if got := entry.Binding.GetBinding().GetId(); got != want.binding {
			t.Errorf("Lookup(%q) binding = %q, want %s", address, got, want.binding)
		}
		if got := entry.Integration.GetIntegration().GetId(); got != want.integration {
			t.Errorf("Lookup(%q) integration = %q, want the integration of the same row, %s", address, got, want.integration)
		}
	}
}

// TestDeviceIndex_EntryCarriesTheIntegrationOfItsRow pins that the integration
// travels with the device and binding of one row, so two devices at different
// addresses keep their own.
func TestDeviceIndex_EntryCarriesTheIntegrationOfItsRow(t *testing.T) {
	t.Parallel()

	idx := lanehost.NewDeviceIndex()
	apply(t, idx,
		listedDeviceInIntegration(deviceOne, []byte{192, 0, 2, 1}, bindingOne, integrationID),
		listedDeviceInIntegration(deviceTwo, []byte{192, 0, 2, 2}, bindingTwo, integrationTwo),
	)

	for address, want := range map[string]string{"192.0.2.1": integrationID, "192.0.2.2": integrationTwo} {
		entry, res := idx.Lookup(address)
		if res != lanehost.LookupFound {
			t.Fatalf("Lookup(%q) = %v, want LookupFound", address, res)
		}
		if got := entry.Integration.GetIntegration().GetId(); got != want {
			t.Errorf("Lookup(%q) integration = %q, want %s", address, got, want)
		}
	}
}

// TestDeviceIndex_ApplyListingRefusesConflictingClaims covers the rows that are
// each well formed and contradict one another. Choosing the last row would let
// the order of a listing decide which binding and integration a device's
// records are attributed to.
func TestDeviceIndex_ApplyListingRefusesConflictingClaims(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rows []*attachv1.ListedDevice
	}{
		{
			name: "one device at one address with two bindings",
			rows: []*attachv1.ListedDevice{
				listedDeviceInIntegration(deviceOne, []byte{192, 0, 2, 1}, bindingOne, integrationID),
				listedDeviceInIntegration(deviceOne, []byte{192, 0, 2, 1}, bindingTwo, integrationID),
			},
		},
		{
			name: "one device at one address with two integrations",
			rows: []*attachv1.ListedDevice{
				listedDeviceInIntegration(deviceOne, []byte{192, 0, 2, 1}, bindingOne, integrationID),
				listedDeviceInIntegration(deviceOne, []byte{192, 0, 2, 1}, bindingOne, integrationTwo),
			},
		},
		{
			name: "one binding paired with two integrations across devices",
			rows: []*attachv1.ListedDevice{
				listedDeviceInIntegration(deviceOne, []byte{192, 0, 2, 1}, bindingOne, integrationID),
				listedDeviceInIntegration(deviceTwo, []byte{192, 0, 2, 2}, bindingOne, integrationTwo),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			idx := lanehost.NewDeviceIndex()
			apply(t, idx, listedDeviceWithAddr(deviceOne, []byte{192, 0, 2, 9}))
			if err := idx.ApplyListing(tt.rows); err == nil {
				t.Fatal("ApplyListing error = nil, want the conflicting listing refused")
			}
			if _, res := idx.Lookup("192.0.2.9"); res != lanehost.LookupFound {
				t.Errorf("Lookup of the prior snapshot = %v, want LookupFound", res)
			}
			if _, res := idx.Lookup("192.0.2.1"); res != lanehost.LookupUnknown {
				t.Errorf("Lookup of the refused listing = %v, want LookupUnknown", res)
			}
		})
	}
}

func TestDeviceIndex_IdenticalRowsForOneDeviceAtOneAddressAreOneClaim(t *testing.T) {
	t.Parallel()

	idx := lanehost.NewDeviceIndex()
	apply(t, idx,
		listedDeviceOneWithBinding([]byte{192, 0, 2, 1}, bindingOne),
		listedDeviceOneWithBinding([]byte{192, 0, 2, 1}, bindingOne),
	)

	entry, res := idx.Lookup("192.0.2.1")
	if res != lanehost.LookupFound || entry.DeviceID != deviceOne {
		t.Fatalf("Lookup(\"192.0.2.1\") = (%v, %v), want (%s, LookupFound): identical rows are one claim, not an ambiguity", entry, res, deviceOne)
	}
}

func TestDeviceIndex_ZeroValueResolvesAfterOneApplyListing(t *testing.T) {
	t.Parallel()

	var idx lanehost.DeviceIndex
	if _, res := idx.Lookup("192.0.2.1"); res != lanehost.LookupUnknown {
		t.Fatalf("Lookup on the zero value = %v, want LookupUnknown", res)
	}

	apply(t, &idx, listedDeviceOneWithBinding([]byte{192, 0, 2, 1}, bindingOne))

	entry, res := idx.Lookup("192.0.2.1")
	if res != lanehost.LookupFound || entry.DeviceID != deviceOne {
		t.Fatalf("Lookup(\"192.0.2.1\") = (%v, %v), want (%s, LookupFound)", entry, res, deviceOne)
	}
	if entry.Device.GetDevice().GetId() != deviceOne || entry.Binding.GetBinding().GetId() != bindingOne {
		t.Errorf("entry refs = (%v, %v), want device %s and binding %s", entry.Device, entry.Binding, deviceOne, bindingOne)
	}
	if entry.Integration.GetIntegration().GetId() != integrationID {
		t.Errorf("entry integration = %v, want %s", entry.Integration, integrationID)
	}
}
