package vswitch

import (
	"slices"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
)

// ConstructionSpec holds the normalized configuration, preloaded FDB seeds,
// stable node identity, and construction trust metadata needed to reproduce a
// [Switch]. NodeID keys node and port scopes; an empty value identifies an
// anonymous standalone switch. ConstructionSpec is safe for concurrent reads
// when its exported fields are not mutated.
type ConstructionSpec struct {
	Config   Config
	Seeds    []bridge.Seed
	NodeID   string
	Metadata analysis.Metadata
}

// Normalize validates and returns an independent construction specification
// with normalized configuration and forwarding database seeds. Seed timestamps
// are canonical UTC wall-clock instants without monotonic readings.
func (s ConstructionSpec) Normalize() (ConstructionSpec, error) {
	owned := s.Clone()
	// Traffic field paths name submitted slice positions, which sorting and
	// compaction would otherwise replace with normalized positions.
	if owned.Config.Traffic != nil {
		if err := owned.Config.Traffic.Validate(layer.Env{Ports: owned.Config.Ports.Normalize()}); err != nil {
			return ConstructionSpec{}, err
		}
		if err := owned.Config.validateTrafficVLANs(); err != nil {
			return ConstructionSpec{}, err
		}
	}
	owned.Config = owned.Config.Normalize()
	if err := owned.Config.Validate(); err != nil {
		return ConstructionSpec{}, err
	}
	if len(owned.Seeds) > 0 && owned.Config.Bridge == nil {
		return ConstructionSpec{}, errs.New().
			Attr("field", "seeds").
			Msg("forwarding database seeds require bridge configuration")
	}
	if owned.Config.Bridge != nil {
		seeds, err := bridge.NormalizeSeeds(*owned.Config.Bridge, owned.Config.env(), owned.Seeds)
		if err != nil {
			return ConstructionSpec{}, err
		}
		for i := range seeds {
			seeds[i].LearnedAt = seeds[i].LearnedAt.Round(0).UTC()
		}
		owned.Seeds = seeds
	}
	if err := validateConstructionMetadata(owned.NodeID, owned.Metadata); err != nil {
		return ConstructionSpec{}, err
	}

	return owned, nil
}

// Clone returns an independent deep copy of the construction specification.
func (s ConstructionSpec) Clone() ConstructionSpec {
	cp := ConstructionSpec{
		Config:   s.Config.Clone(),
		NodeID:   s.NodeID,
		Metadata: s.Metadata.Clone(),
	}
	if len(s.Seeds) > 0 {
		cp.Seeds = slices.Clone(s.Seeds)
	}
	return cp
}

// Equal reports whether two construction specifications carry the same fields,
// treating seed timestamps at the same instant as equal.
func (s ConstructionSpec) Equal(other ConstructionSpec) bool {
	if s.NodeID != other.NodeID || !s.Config.Equal(other.Config) || !s.Metadata.Equal(other.Metadata) {
		return false
	}
	if len(s.Seeds) != len(other.Seeds) {
		return false
	}
	for i := range s.Seeds {
		a, b := s.Seeds[i], other.Seeds[i]
		if a.FID != b.FID || a.MAC != b.MAC || a.Port != b.Port || a.Origin != b.Origin || a.Lifetime != b.Lifetime || !a.LearnedAt.Equal(b.LearnedAt) {
			return false
		}
	}
	return true
}

// Spec returns an independent [ConstructionSpec] capturing every construction
// input needed to reproduce this switch, including its node identity and trust
// metadata.
func (s *Switch) Spec() ConstructionSpec {
	spec := ConstructionSpec{
		Config:   s.cfg.Clone(),
		NodeID:   s.nodeID,
		Metadata: s.metadata.Clone(),
	}
	if len(s.seeds) > 0 {
		spec.Seeds = slices.Clone(s.seeds)
	}
	return spec
}

// Learn validates and preloads the switch's bridge forwarding database with the
// provided seeds. It records normalized seeds in the construction specification.
// Invalid or duplicate seeds leave the switch unchanged.
func (s *Switch) Learn(seeds []bridge.Seed) error {
	if len(seeds) == 0 {
		return nil
	}
	if s.bridge == nil || s.cfg.Bridge == nil {
		return errs.New().
			Attr("field", "seeds").
			Msg("forwarding database seeds require bridge configuration")
	}

	combined := append(slices.Clone(s.seeds), seeds...)
	normalized, err := bridge.NormalizeSeeds(*s.cfg.Bridge, s.env(), combined)
	if err != nil {
		return err
	}
	if err := s.bridge.InstallSeeds(seeds); err != nil {
		return err
	}
	s.seeds = normalized

	return nil
}

// Forget removes a forwarding database entry with the given FID and MAC address from
// the switch's bridge relay, reporting whether an entry was present. It returns false
// when the switch has no bridge relay.
func (s *Switch) Forget(fid vlan.ID, mac netaddr.MAC) bool {
	if s.bridge == nil {
		return false
	}

	return s.bridge.Forget(fid, mac)
}
