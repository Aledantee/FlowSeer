package lanehost

import (
	"net/netip"
	"sync"

	"google.golang.org/protobuf/proto"

	attachv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/attach/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
)

// DeviceEntry is one listed device resolved by its management peer address.
type DeviceEntry struct {
	DeviceID string
	Device   *inventoryv1.DeviceGlobalRef
	Binding  *inventoryv1.BindingGlobalRef
}

// LookupResult classifies the resolution of an address in [DeviceIndex].
type LookupResult uint8

const (
	// LookupUnknown indicates no listed device claims the address.
	LookupUnknown LookupResult = iota
	// LookupFound indicates exactly one listed device claims the address.
	LookupFound
	// LookupAmbiguous indicates two or more listed devices claim the address.
	LookupAmbiguous
)

// DeviceIndex maps peer addresses to device identities and bindings, safe for
// concurrent use: the onboarder writes while the syslog source reads. The zero
// value is an empty index.
//
// The index follows the device listing and nothing else. An address resolves to
// a device entry (LookupFound) when exactly one listed device claims it,
// whether or not the lane onboarded that device. An address claimed by two or
// more listed devices resolves to no device (LookupAmbiguous), and an address
// no listed device claims resolves to none either (LookupUnknown). A device ID
// listed at two addresses is a claimant at both, each with its own row's
// binding. Two rows with one device ID at one address are one claim, and the
// later row's binding stands.
//
// [DeviceIndex.ApplyListing] replaces the claims as a whole, so a device the
// next listing omits or moves stops resolving at the old address. The index
// outlives a lane attempt: a lane restart keeps the last listing, and a process
// restart starts with an empty index until the first listing.
type DeviceIndex struct {
	mu     sync.RWMutex                      // guards claims
	claims map[string]map[string]DeviceEntry // address -> claim by device ID
}

// NewDeviceIndex returns an empty index.
func NewDeviceIndex() *DeviceIndex {
	return &DeviceIndex{}
}

// key returns the index key for an address, which is the address with an
// IPv4-mapped IPv6 form unmapped and any IPv6 zone removed: a dual-stack listener
// reports an IPv4 peer in the mapped form, a link-local peer may arrive with a
// zone, and a device is listed in the plain form.
func key(addr netip.Addr) string {
	return addr.Unmap().WithZone("").String()
}

func normalizeAddress(address string) string {
	if parsed, err := netip.ParseAddr(address); err == nil {
		return key(parsed)
	}
	return address
}

func deviceRefFor(deviceID string) *inventoryv1.DeviceGlobalRef {
	return inventoryv1.DeviceGlobalRef_builder{
		Device: inventoryv1.DeviceLocalRef_builder{Id: proto.String(deviceID)}.Build(),
	}.Build()
}

func bindingRefOf(bindingID string) *inventoryv1.BindingGlobalRef {
	return inventoryv1.BindingGlobalRef_builder{
		Binding: inventoryv1.BindingLocalRef_builder{Id: proto.String(bindingID)}.Build(),
	}.Build()
}

// ApplyListing replaces the claims with those of a whole device listing, so a
// lookup never sees a partly applied listing. A nil row, a row with an empty
// device ID, and a row whose address is unusable claim nothing. The other rows
// each claim their address, in order.
func (idx *DeviceIndex) ApplyListing(devices []*attachv1.ListedDevice) {
	if idx == nil {
		return
	}

	claims := make(map[string]map[string]DeviceEntry, len(devices))
	for _, listed := range devices {
		deviceID := listed.GetDeviceId()
		if deviceID == "" {
			continue
		}
		rawAddr, err := addressOf(listed.GetIp())
		if err != nil {
			continue
		}
		addr := normalizeAddress(rawAddr)
		claimants := claims[addr]
		if claimants == nil {
			claimants = make(map[string]DeviceEntry)
			claims[addr] = claimants
		}
		claimants[deviceID] = DeviceEntry{
			DeviceID: deviceID,
			Device:   deviceRefFor(deviceID),
			Binding:  bindingRefOf(listed.GetBindingId()),
		}
	}

	idx.mu.Lock()
	idx.claims = claims
	idx.mu.Unlock()
}

// Lookup returns the device entry mapped to address and the outcome of the resolution.
// It returns [LookupFound] and the entry when exactly one listed device claims the
// address. If two or more listed devices claim it, it returns [LookupAmbiguous] and
// an empty entry. If none does, it returns [LookupUnknown] and an empty entry.
func (idx *DeviceIndex) Lookup(address string) (DeviceEntry, LookupResult) {
	if idx == nil {
		return DeviceEntry{}, LookupUnknown
	}
	address = normalizeAddress(address)

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	claimants := idx.claims[address]
	switch len(claimants) {
	case 0:
		return DeviceEntry{}, LookupUnknown
	case 1:
		for _, entry := range claimants {
			return entry, LookupFound
		}
	}
	return DeviceEntry{}, LookupAmbiguous
}
