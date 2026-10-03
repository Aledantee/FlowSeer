package lanehost

import (
	"net/netip"
	"sync"

	"google.golang.org/protobuf/proto"

	attachv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/attach/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
)

// DeviceEntry is one onboarded device resolved by its management peer address.
type DeviceEntry struct {
	DeviceID string
	Device   *inventoryv1.DeviceGlobalRef
	Binding  *inventoryv1.BindingGlobalRef
}

// LookupResult classifies the resolution of an address in [DeviceIndex].
type LookupResult uint8

const (
	// LookupUnknown indicates the address is not claimed or its sole claimant was not
	// onboarded by a lane attempt of this process and listed at this address.
	LookupUnknown LookupResult = iota
	// LookupFound indicates exactly one listed device claims the address and was onboarded
	// by a lane attempt of this process and listed at this address.
	LookupFound
	// LookupAmbiguous indicates two or more listed devices share the address.
	LookupAmbiguous
)

type deviceRecord struct {
	deviceID string
	address  string
	binding  *inventoryv1.BindingGlobalRef
	device   *inventoryv1.DeviceGlobalRef
	served   bool
}

// DeviceIndex maps peer addresses to device identities and bindings, safe for
// concurrent use: the onboarder writes while the syslog source reads.
//
// Invariant: the index holds a record for every listed device, unserved and
// address-less ones included. Each device ID is associated with at most one address.
// An address resolves to a device entry (LookupFound) if and only if exactly one
// listed device claims that address and that device was onboarded by a lane attempt
// of this process and listed at this address. An address claimed by two or more listed
// devices resolves to no device (LookupAmbiguous). An address with no claimant or whose
// sole claimant was not onboarded by a lane attempt of this process and listed at
// this address resolves to no device (LookupUnknown).
//
// The index follows the listing: for a held device, the index reflects the listed
// address and binding, while the lane session stays on the onboarded address until
// the attempt restarts. That state outlives the lane attempt: a lane restart keeps
// that state, and a process restart starts with an empty index.
type DeviceIndex struct {
	mu      sync.RWMutex                   // guards claims, devices
	claims  map[string]map[string]struct{} // address -> set of device IDs claiming it
	devices map[string]deviceRecord        // device ID -> device record
}

// NewDeviceIndex returns an empty index.
func NewDeviceIndex() *DeviceIndex {
	return &DeviceIndex{
		claims:  make(map[string]map[string]struct{}),
		devices: make(map[string]deviceRecord),
	}
}

// Key returns the index key for an address, which is the address with an
// IPv4-mapped IPv6 form unmapped and any IPv6 zone removed: a dual-stack listener
// reports an IPv4 peer in the mapped form, a link-local peer may arrive with a
// zone, and a device is listed in the plain form.
func Key(addr netip.Addr) string {
	return addr.Unmap().WithZone("").String()
}

func normalizeAddress(address string) string {
	if parsed, err := netip.ParseAddr(address); err == nil {
		return Key(parsed)
	}
	return address
}

func (idx *DeviceIndex) dropClaim(address, deviceID string) {
	if address == "" {
		return
	}
	if claimants, exists := idx.claims[address]; exists {
		delete(claimants, deviceID)
		if len(claimants) == 0 {
			delete(idx.claims, address)
		}
	}
}

func (idx *DeviceIndex) addClaim(address, deviceID string) {
	if address == "" {
		return
	}
	claimants := idx.claims[address]
	if claimants == nil {
		claimants = make(map[string]struct{})
		idx.claims[address] = claimants
	}
	claimants[deviceID] = struct{}{}
}

func (idx *DeviceIndex) setDeviceLocked(deviceID, address string, binding *inventoryv1.BindingGlobalRef, served bool) {
	if old, ok := idx.devices[deviceID]; ok && old.address != "" && old.address != address {
		idx.dropClaim(old.address, deviceID)
	}
	if address == "" {
		idx.devices[deviceID] = deviceRecord{
			deviceID: deviceID,
			address:  "",
			binding:  nil,
			device:   nil,
			served:   false,
		}
		return
	}
	idx.addClaim(address, deviceID)
	idx.devices[deviceID] = deviceRecord{
		deviceID: deviceID,
		address:  address,
		binding:  binding,
		device:   deviceRefFor(deviceID),
		served:   served,
	}
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

// Add records or updates a device mapping for the given address and marks it served,
// dropping any other address previously associated with deviceID. If another device already
// carries this address, the address becomes shared and resolves to no device.
func (idx *DeviceIndex) Add(address, deviceID string, binding *inventoryv1.BindingGlobalRef) {
	if idx == nil || address == "" || deviceID == "" {
		return
	}
	address = normalizeAddress(address)

	idx.mu.Lock()
	defer idx.mu.Unlock()

	idx.setDeviceLocked(deviceID, address, binding, true)
}

// ApplyListing applies a whole device listing to the index under a single write lock.
// It prunes devices not present in the listing, records one address claim per listed device
// with a usable address (recording devices with unusable addresses with no claim),
// drops each device's claim on any other address, and re-asserts as served, with the listed
// address and binding, every listed device present in heldIDs.
// If a listed device was already served at the listed address by an earlier onboarder in this
// process, its served status is preserved.
func (idx *DeviceIndex) ApplyListing(devices []*attachv1.ListedDevice, heldIDs map[string]struct{}) {
	if idx == nil {
		return
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()

	keep := make(map[string]struct{}, len(devices))
	for _, listed := range devices {
		if listed != nil && listed.GetDeviceId() != "" {
			keep[listed.GetDeviceId()] = struct{}{}
		}
	}

	for devID, rec := range idx.devices {
		if _, ok := keep[devID]; !ok {
			delete(idx.devices, devID)
			idx.dropClaim(rec.address, devID)
		}
	}

	for _, listed := range devices {
		if listed == nil {
			continue
		}
		devID := listed.GetDeviceId()
		if devID == "" {
			continue
		}

		var addr string
		if rawAddr, err := addressOf(listed.GetIp()); err == nil {
			addr = normalizeAddress(rawAddr)
		}

		old, hadOld := idx.devices[devID]
		_, isHeld := heldIDs[devID]
		served := isHeld || (hadOld && old.address == addr && old.served)

		var binding *inventoryv1.BindingGlobalRef
		if addr != "" {
			binding = bindingRefOf(listed.GetBindingId())
		}
		idx.setDeviceLocked(devID, addr, binding, served)
	}
}

// Lookup returns the device entry mapped to address and the outcome of the resolution.
// It returns [LookupFound] and the entry when exactly one device claims the address
// and that device was onboarded by a lane attempt of this process and listed at this address.
// If two or more listed devices share the address, it returns [LookupAmbiguous] and an empty entry.
// If the address is not held in the index, or its sole claimant was not onboarded by a lane
// attempt of this process and listed at this address, it returns [LookupUnknown] and an empty entry.
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
		for devID := range claimants {
			rec, ok := idx.devices[devID]
			if !ok || !rec.served {
				return DeviceEntry{}, LookupUnknown
			}
			return DeviceEntry{
				DeviceID: rec.deviceID,
				Device:   rec.device,
				Binding:  rec.binding,
			}, LookupFound
		}
	default:
		return DeviceEntry{}, LookupAmbiguous
	}
	return DeviceEntry{}, LookupUnknown
}
