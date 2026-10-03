package lanehost

import (
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

// DeviceIndex maps peer addresses to device identities and bindings, safe for
// concurrent use.
type DeviceIndex struct {
	mu      sync.RWMutex
	entries map[string]DeviceEntry
}

// NewDeviceIndex returns an empty index.
func NewDeviceIndex() *DeviceIndex {
	return &DeviceIndex{entries: make(map[string]DeviceEntry)}
}

// Add records or updates a device mapping for the given address.
func (idx *DeviceIndex) Add(address, deviceID string, binding *inventoryv1.BindingGlobalRef) {
	if idx == nil || address == "" {
		return
	}
	devRef := inventoryv1.DeviceGlobalRef_builder{
		Device: inventoryv1.DeviceLocalRef_builder{Id: proto.String(deviceID)}.Build(),
	}.Build()
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.entries[address] = DeviceEntry{
		DeviceID: deviceID,
		Device:   devRef,
		Binding:  binding,
	}
}

// Lookup returns the device entry mapped to address, if present.
func (idx *DeviceIndex) Lookup(address string) (DeviceEntry, bool) {
	if idx == nil {
		return DeviceEntry{}, false
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	entry, ok := idx.entries[address]
	return entry, ok
}
