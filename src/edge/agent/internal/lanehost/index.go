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
	// LookupUnknown indicates the address is not held in the index.
	LookupUnknown LookupResult = iota
	// LookupFound indicates exactly one listed device is mapped to the address.
	LookupFound
	// LookupAmbiguous indicates two or more listed devices share the address.
	LookupAmbiguous
)

// String returns the name of the lookup result.
func (r LookupResult) String() string {
	switch r {
	case LookupFound:
		return "found"
	case LookupAmbiguous:
		return "ambiguous"
	case LookupUnknown:
		return "unknown"
	default:
		return "unknown"
	}
}

// DeviceIndex maps peer addresses to device identities and bindings, safe for
// concurrent use: the onboarder writes while the syslog source reads.
//
// Invariant: each device ID is associated with at most one address, and an
// address resolves to a device entry if and only if exactly one listed device
// carries that address. An address shared by two or more listed devices resolves
// to no device until only one remains.
type DeviceIndex struct {
	mu        sync.RWMutex
	byAddress map[string]map[string]DeviceEntry // address -> deviceID -> DeviceEntry
	byDevice  map[string]string                 // deviceID -> address
}

// NewDeviceIndex returns an empty index.
func NewDeviceIndex() *DeviceIndex {
	return &DeviceIndex{
		byAddress: make(map[string]map[string]DeviceEntry),
		byDevice:  make(map[string]string),
	}
}

// Key returns the index key for an address, which is the address with an
// IPv4-mapped IPv6 form unmapped and any IPv6 zone removed: a dual-stack listener
// reports an IPv4 peer in the mapped form, a link-local peer may arrive with a
// zone, and a device is listed in the plain form.
func Key(addr netip.Addr) string {
	return addr.Unmap().WithZone("").String()
}

// Add records or updates a device mapping for the given address, dropping any
// other address previously associated with deviceID. If another device already
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

	if idx.byDevice == nil {
		idx.byDevice = make(map[string]string)
	}
	if idx.byAddress == nil {
		idx.byAddress = make(map[string]map[string]DeviceEntry)
	}

	oldAddr, hadOld := idx.byDevice[deviceID]
	if hadOld && oldAddr != address {
		if devs, ok := idx.byAddress[oldAddr]; ok {
			delete(devs, deviceID)
			if len(devs) == 0 {
				delete(idx.byAddress, oldAddr)
			}
		}
	}

	idx.byDevice[deviceID] = address
	devs := idx.byAddress[address]
	if devs == nil {
		devs = make(map[string]DeviceEntry)
		idx.byAddress[address] = devs
	}
	devs[deviceID] = DeviceEntry{
		DeviceID: deviceID,
		Device:   devRef,
		Binding:  binding,
	}
}

// Prune removes any device from the index whose device ID is not in listedIDs.
// Any address that was previously shared by a pruned device and a remaining device
// resolves again to the remaining device.
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

	for devID, addr := range idx.byDevice {
		if _, ok := keep[devID]; !ok {
			delete(idx.byDevice, devID)
			if devs, exists := idx.byAddress[addr]; exists {
				delete(devs, devID)
				if len(devs) == 0 {
					delete(idx.byAddress, addr)
				}
			}
		}
	}
}

// Lookup returns the device entry mapped to address and the outcome of the resolution.
// It returns [LookupFound] and the entry when exactly one device is at the address.
// If two or more listed devices share the address, it returns [LookupAmbiguous] and an empty entry.
// If the address is not held in the index, it returns [LookupUnknown] and an empty entry.
func (idx *DeviceIndex) Lookup(address string) (DeviceEntry, LookupResult) {
	if idx == nil {
		return DeviceEntry{}, LookupUnknown
	}
	if parsed, err := netip.ParseAddr(address); err == nil {
		address = Key(parsed)
	}

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	devs := idx.byAddress[address]
	switch len(devs) {
	case 0:
		return DeviceEntry{}, LookupUnknown
	case 1:
		for _, entry := range devs {
			return entry, LookupFound
		}
	default:
		return DeviceEntry{}, LookupAmbiguous
	}
	return DeviceEntry{}, LookupUnknown
}
