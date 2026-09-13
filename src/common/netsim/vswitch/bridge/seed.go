package bridge

import (
	"bytes"
	"fmt"
	"slices"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// NormalizeSeeds validates forwarding database seeds against the relay and
// port configuration, resolves member ports to their logical LAG, and sorts
// the result by FID and MAC. Two seeds for the same FID and MAC are invalid.
func NormalizeSeeds(cfg Config, ports port.Table, seeds []Seed) ([]Seed, error) {
	if len(seeds) == 0 {
		return nil, nil
	}

	normalized := make([]Seed, 0, len(seeds))
	seen := make(map[fdbKey]int, len(seeds))
	for i, seed := range seeds {
		prefix := fmt.Sprintf("seeds.%d", i)
		if seed.MAC == (netaddr.MAC{}) || seed.MAC.IsGroup() {
			return nil, errs.New().
				Attr("field", prefix+".mac").
				Attr("mac", seed.MAC).
				Msgf("forwarding seed %d has unusable MAC %s", i, seed.MAC)
		}

		logical, ok := ports.Resolve(seed.Port)
		if !ok {
			return nil, errs.New().
				Attr("field", prefix+".port").
				Attr("port", seed.Port).
				Msgf("forwarding seed %d port %q does not resolve to a logical port", i, seed.Port)
		}
		seed.Port = logical.Name

		if err := validateSeedFID(cfg, seed, prefix); err != nil {
			return nil, err
		}

		key := fdbKey{fid: seed.FID, mac: seed.MAC}
		if previous, exists := seen[key]; exists {
			return nil, errs.New().
				Attr("field", prefix).
				Attr("duplicate_of", previous).
				Attr("fid", seed.FID).
				Attr("mac", seed.MAC).
				Msgf("forwarding seed %d duplicates seed %d", i, previous)
		}
		seen[key] = i
		normalized = append(normalized, seed)
	}

	slices.SortFunc(normalized, func(a, b Seed) int {
		if a.FID < b.FID {
			return -1
		}
		if a.FID > b.FID {
			return 1
		}
		return bytes.Compare(a.MAC[:], b.MAC[:])
	})

	return normalized, nil
}

func validateSeedFID(cfg Config, seed Seed, prefix string) error {
	if cfg.VLAN == nil {
		if seed.FID != 0 {
			return errs.New().
				Attr("field", prefix+".fid").
				Attr("fid", seed.FID).
				Msgf("forwarding seed FID %d requires VLAN awareness", seed.FID)
		}

		return nil
	}

	if !seed.FID.Valid() {
		return errs.New().
			Attr("field", prefix+".fid").
			Attr("fid", seed.FID).
			Msgf("forwarding seed FID %d is outside 1 through 4094", seed.FID)
	}
	if _, ok := cfg.VLAN.Table[seed.FID]; !ok {
		return errs.New().
			Attr("field", prefix+".fid").
			Attr("fid", seed.FID).
			Msgf("forwarding seed FID %d is absent from the VLAN table", seed.FID)
	}

	switchport, ok := cfg.VLAN.Switchports[seed.Port]
	if !ok || !switchportAdmitsFID(switchport, seed.FID) {
		return errs.New().
			Attr("field", prefix+".port").
			Attr("port", seed.Port).
			Attr("fid", seed.FID).
			Msgf("forwarding seed port %q is not admitted to FID %d", seed.Port, seed.FID)
	}

	return nil
}

func switchportAdmitsFID(switchport Switchport, fid vlan.ID) bool {
	return switchport.PVID != nil && *switchport.PVID == fid ||
		slices.Contains(switchport.Tagged, fid) ||
		slices.Contains(switchport.Untagged, fid) ||
		switchport.Tunnel != nil && switchport.Tunnel.VID == fid
}
