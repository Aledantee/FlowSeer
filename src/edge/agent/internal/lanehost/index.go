package lanehost

import (
	"net/netip"
	"sync"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"

	attachv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/attach/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

// DeviceEntry is one listed device resolved by its management peer address.
// Device, Binding, and Integration come from the same listing row, so the
// integration is the one that owns the binding.
type DeviceEntry struct {
	DeviceID    string
	Device      *inventoryv1.DeviceGlobalRef
	Binding     *inventoryv1.BindingGlobalRef
	Integration *inventoryv1.IntegrationGlobalRef
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
// binding and integration. Two identical rows are one claim.
//
// [DeviceIndex.ApplyListing] replaces the claims as a whole, so a device the
// next listing omits or moves stops resolving at the old address. A listing
// that is malformed, or whose rows contradict each other, is refused whole and
// the previous claims stand, because a half-trusted listing would resolve
// devices to a binding and integration central never paired. The index
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

func integrationRefOf(integrationID string) *inventoryv1.IntegrationGlobalRef {
	return inventoryv1.IntegrationGlobalRef_builder{
		Integration: inventoryv1.IntegrationLocalRef_builder{Id: proto.String(integrationID)}.Build(),
	}.Build()
}

// claimTable maps an address to the claim each listed device makes on it.
type claimTable map[string]map[string]DeviceEntry

// claimsOf validates a whole device listing and returns the claims it makes.
// It refuses the listing when any row is nil or fails the schema's rules
// (device, binding, and integration ids, and the management address), when two
// rows claim one device at one address with different bindings or integrations,
// or when one binding is paired with two integrations. It refuses rather than
// skips a bad row, so a caller either applies the listing central sent or keeps
// the one it already holds.
func claimsOf(devices []*attachv1.ListedDevice) (claimTable, error) {
	claims := make(claimTable, len(devices))
	integrationOf := make(map[string]string, len(devices))
	for row, listed := range devices {
		if listed == nil {
			return nil, errs.New().Code(ErrCodeOnboard).Attr("row", row).Msg("listing carries an empty row")
		}
		if err := protovalidate.Validate(listed); err != nil {
			return nil, errs.From(err).Code(ErrCodeOnboard).Attr("row", row).Msg("listed device fails its schema rules")
		}
		deviceID, bindingID, integrationID := listed.GetDeviceId(), listed.GetBindingId(), listed.GetIntegrationId()
		rawAddr, err := addressOf(listed.GetIp())
		if err != nil {
			return nil, errs.From(err).Code(ErrCodeOnboard).Attr("row", row).Attr("device", deviceID).Msg("listed device has no usable address")
		}
		if owner, ok := integrationOf[bindingID]; ok && owner != integrationID {
			return nil, errs.New().Code(ErrCodeOnboard).Attr("row", row).Attr("binding", bindingID).
				Msg("listing pairs one binding with two integrations")
		}
		integrationOf[bindingID] = integrationID

		addr := normalizeAddress(rawAddr)
		claimants := claims[addr]
		if claimants == nil {
			claimants = make(map[string]DeviceEntry)
			claims[addr] = claimants
		}
		if prior, ok := claimants[deviceID]; ok {
			if prior.Binding.GetBinding().GetId() != bindingID || prior.Integration.GetIntegration().GetId() != integrationID {
				return nil, errs.New().Code(ErrCodeOnboard).Attr("row", row).Attr("device", deviceID).
					Msg("listing claims one device at one address with two bindings or integrations")
			}
			continue
		}
		claimants[deviceID] = DeviceEntry{
			DeviceID:    deviceID,
			Device:      deviceRefFor(deviceID),
			Binding:     bindingRefOf(bindingID),
			Integration: integrationRefOf(integrationID),
		}
	}
	return claims, nil
}

// replace swaps in a validated claim table.
func (idx *DeviceIndex) replace(claims claimTable) {
	idx.mu.Lock()
	idx.claims = claims
	idx.mu.Unlock()
}

// ApplyListing replaces the claims with those of a whole device listing, so a
// lookup never sees a partly applied listing. A listing [claimsOf] refuses
// leaves the previous claims in place and returns the refusal.
func (idx *DeviceIndex) ApplyListing(devices []*attachv1.ListedDevice) error {
	claims, err := claimsOf(devices)
	if err != nil {
		return err
	}
	if idx != nil {
		idx.replace(claims)
	}
	return nil
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
