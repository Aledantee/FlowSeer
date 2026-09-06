// Package registry is the device service's registry: the devices it serves,
// the edge and integration that reach them, and the policies their handles
// resolve to. It is read once from an operator-written prototext file until an
// inventory service exists, validated against the schema's own rules — every
// device's access policy naming a listed policy version among them — so a
// malformed registry is refused at load rather than surfacing per request.
//
// It is the DeviceResolver behind the dispatch relay's seam: it names the
// devices an edge hosts, a device's delayed-apply horizon, whether it lists a
// device, and whether an edge hosts one. The one integration reaches through
// one edge, so every device belongs to that edge and Hosts is an edge-match
// plus a lookup.
package registry

import (
	"context"
	"os"
	"time"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/encoding/prototext"

	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

// Error codes the registry returns.
var (
	// ErrCodeLoad is a registry file that cannot be read or parsed.
	ErrCodeLoad = errs.NewCode("registry/load")
	// ErrCodeInvalid is a registry that parses but fails its schema rules.
	ErrCodeInvalid = errs.NewCode("registry/invalid")
	// ErrCodeHorizonUnset is a device whose delayed-apply horizon is not set,
	// so a mutation's deadline cannot be measured and the row stays owed.
	ErrCodeHorizonUnset = errs.NewCode("device/horizon-unset")
)

const defaultReadCredentialTTL = 5 * time.Minute

// Registry is a loaded, validated device registry. It is immutable after
// [Load] and safe for concurrent use.
type Registry struct {
	registry *storev1.DeviceRegistry
	edgeID   string
	devices  map[string]*storev1.RegistryDevice
	policies map[policyKey]*storev1.RegistryPolicy
}

type policyKey struct {
	key     string
	version uint64
}

// Load reads and validates the prototext registry at path.
func Load(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeLoad).Attr("path", path).Msg("read registry file")
	}
	return parse(data, path)
}

func parse(data []byte, path string) (*Registry, error) {
	reg := &storev1.DeviceRegistry{}
	if err := prototext.Unmarshal(data, reg); err != nil {
		return nil, errs.From(err).Code(ErrCodeLoad).Attr("path", path).Msg("parse registry prototext")
	}
	if err := protovalidate.Validate(reg); err != nil {
		// The schema's CEL enforces the cross-message invariants here — each
		// device once, each policy key once, and every device's access policy
		// naming a listed policy version — so they are not re-checked in Go.
		return nil, errs.From(err).Code(ErrCodeInvalid).Attr("path", path).Msg("registry fails its schema rules")
	}

	r := &Registry{
		registry: reg,
		edgeID:   reg.GetIntegration().GetEdge().GetEdge().GetId(),
		devices:  make(map[string]*storev1.RegistryDevice, len(reg.GetDevices())),
		policies: make(map[policyKey]*storev1.RegistryPolicy, len(reg.GetPolicies())),
	}
	for _, d := range reg.GetDevices() {
		r.devices[d.GetConfig().GetRef().GetDevice().GetId()] = d
	}
	for _, p := range reg.GetPolicies() {
		h := p.GetHandle()
		r.policies[policyKey{h.GetKey(), h.GetVersion()}] = p
	}
	return r, nil
}

// EdgeID is the id of the one edge this registry's integration reaches
// through.
func (r *Registry) EdgeID() string { return r.edgeID }

// Device returns the registry entry for a device id.
func (r *Registry) Device(deviceID string) (*storev1.RegistryDevice, bool) {
	d, ok := r.devices[deviceID]
	return d, ok
}

// Policy returns the registry entry a policy handle resolves to.
func (r *Registry) Policy(key string, version uint64) (*storev1.RegistryPolicy, bool) {
	p, ok := r.policies[policyKey{key, version}]
	return p, ok
}

// ReadCredentialTTL is how long a delivered read credential stays valid for a
// policy, defaulting to five minutes when the policy leaves it unset.
func (r *Registry) ReadCredentialTTL(p *storev1.RegistryPolicy) time.Duration {
	if d := p.GetReadCredentialTtl(); d != nil {
		return d.AsDuration()
	}
	return defaultReadCredentialTTL
}

// Devices names the devices an edge hosts. Only the registry's own edge hosts
// any; every device belongs to it.
func (r *Registry) Devices(_ context.Context, edgeID string) ([]string, error) {
	if edgeID != r.edgeID {
		return nil, nil
	}
	ids := make([]string, 0, len(r.devices))
	for id := range r.devices {
		ids = append(ids, id)
	}
	return ids, nil
}

// Horizon is a device's delayed-apply horizon. An unset horizon is an error,
// so the dispatch relay leaves the mutation's row owed rather than dispatching
// a deadline it cannot measure.
func (r *Registry) Horizon(_ context.Context, deviceID string) (time.Duration, error) {
	d, ok := r.devices[deviceID]
	if !ok {
		return 0, errs.New().Code(ErrCodeHorizonUnset).Attr("device", deviceID).Msg("device is not listed in the registry")
	}
	h := d.GetDelayedApplyHorizon()
	if h == nil {
		return 0, errs.New().Code(ErrCodeHorizonUnset).Attr("device", deviceID).Msg("device has no delayed-apply horizon")
	}
	return h.AsDuration(), nil
}

// Lists reports whether the registry lists a device. The in-memory registry
// cannot fail the lookup; the error is in the signature for a store-backed
// resolver, where a transient failure must leave a refusal's row owed rather
// than read as "the device is gone".
func (r *Registry) Lists(_ context.Context, deviceID string) (bool, error) {
	_, ok := r.devices[deviceID]
	return ok, nil
}

// Hosts reports whether an edge hosts a device: the edge is the registry's own,
// and the device is listed.
func (r *Registry) Hosts(_ context.Context, edgeID, deviceID string) (bool, error) {
	if edgeID != r.edgeID {
		return false, nil
	}
	_, ok := r.devices[deviceID]
	return ok, nil
}
