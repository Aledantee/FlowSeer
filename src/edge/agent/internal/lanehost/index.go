package lanehost

import (
	"net/netip"
	"sync"

	"google.golang.org/protobuf/proto"

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
	// LookupUnknown indicates the address is not claimed or its claimant is not served.
	LookupUnknown LookupResult = iota
	// LookupFound indicates exactly one listed device is mapped to the address and served.
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
// Invariant: each device ID is associated with at most one address, and an
// address resolves to a device entry if and only if exactly one listed device
// carries that address and the lane serves that device. An address claimed by
// two or more listed devices resolves to no device (LookupAmbiguous). An address
// with no claimant or whose sole claimant is not served by the lane resolves
// to no device (LookupUnknown).
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

// Add records or updates a device mapping for the given address and marks it served,
// dropping any other address previously associated with deviceID. If another device already
// carries this address, the address becomes shared and resolves to no device.
func (idx *DeviceIndex) Add(address, deviceID string, binding *inventoryv1.BindingGlobalRef) {
	if idx == nil || address == "" || deviceID == "" {
		return
	}
	if parsed, err := netip.ParseAddr(address); err == nil {
		address = Key(parsed)
	}
	devRef := inventoryv1.DeviceGlobalRef_builder{
		Device: inventoryv1.DeviceLocalRef_builder{Id: proto.String(deviceID)}.Build(),
	}.Build()

	idx.mu.Lock()
	defer idx.mu.Unlock()

	if idx.devices == nil {
		idx.devices = make(map[string]deviceRecord)
	}
	if idx.claims == nil {
		idx.claims = make(map[string]map[string]struct{})
	}

	if old, ok := idx.devices[deviceID]; ok && old.address != "" && old.address != address {
		if claimants, exists := idx.claims[old.address]; exists {
			delete(claimants, deviceID)
			if len(claimants) == 0 {
				delete(idx.claims, old.address)
			}
		}
	}

	claimants := idx.claims[address]
	if claimants == nil {
		claimants = make(map[string]struct{})
		idx.claims[address] = claimants
	}
	claimants[deviceID] = struct{}{}

	idx.devices[deviceID] = deviceRecord{
		deviceID: deviceID,
		address:  address,
		binding:  binding,
		device:   devRef,
		served:   true,
	}
}

// RecordClaim records a listed device's address claim, dropping any other address
// previously claimed by deviceID. An empty address drops any claim for deviceID
// without recording a new one. If the device was previously served at the same
// address, its served status is preserved; otherwise, the device is recorded as
// not served until marked served via Add.
func (idx *DeviceIndex) RecordClaim(address, deviceID string, binding *inventoryv1.BindingGlobalRef) {
	if idx == nil || deviceID == "" {
		return
	}
	if address != "" {
		if parsed, err := netip.ParseAddr(address); err == nil {
			address = Key(parsed)
		}
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()

	if idx.devices == nil {
		idx.devices = make(map[string]deviceRecord)
	}
	if idx.claims == nil {
		idx.claims = make(map[string]map[string]struct{})
	}

	old, hadOld := idx.devices[deviceID]
	if hadOld && old.address != "" && old.address != address {
		if claimants, exists := idx.claims[old.address]; exists {
			delete(claimants, deviceID)
			if len(claimants) == 0 {
				delete(idx.claims, old.address)
			}
		}
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

	claimants := idx.claims[address]
	if claimants == nil {
		claimants = make(map[string]struct{})
		idx.claims[address] = claimants
	}
	claimants[deviceID] = struct{}{}

	devRef := inventoryv1.DeviceGlobalRef_builder{
		Device: inventoryv1.DeviceLocalRef_builder{Id: proto.String(deviceID)}.Build(),
	}.Build()

	served := hadOld && old.address == address && old.served

	idx.devices[deviceID] = deviceRecord{
		deviceID: deviceID,
		address:  address,
		binding:  binding,
		device:   devRef,
		served:   served,
	}
}

// Prune removes any device from the index whose device ID is not in listedIDs.
// Any address that was previously shared by a pruned device and a remaining device
// resolves again to the remaining device once only one claimant remains.
func (idx *DeviceIndex) Prune(listedIDs []string) {
	if idx == nil {
		return
	}
	keep := make(map[string]struct{}, len(listedIDs))
	for _, id := range listedIDs {
		keep[id] = struct{}{}
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()

	for devID, rec := range idx.devices {
		if _, ok := keep[devID]; !ok {
			delete(idx.devices, devID)
			if rec.address != "" {
				if claimants, exists := idx.claims[rec.address]; exists {
					delete(claimants, devID)
					if len(claimants) == 0 {
						delete(idx.claims, rec.address)
					}
				}
			}
		}
	}
}

// Lookup returns the device entry mapped to address and the outcome of the resolution.
// It returns [LookupFound] and the entry when exactly one device claims the address
// and that device is served by the lane.
// If two or more listed devices share the address, it returns [LookupAmbiguous] and an empty entry.
// If the address is not held in the index, or its sole claimant is not served by the lane,
// it returns [LookupUnknown] and an empty entry.
func (idx *DeviceIndex) Lookup(address string) (DeviceEntry, LookupResult) {
	if idx == nil {
		return DeviceEntry{}, LookupUnknown
	}
	if parsed, err := netip.ParseAddr(address); err == nil {
		address = Key(parsed)
	}

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
