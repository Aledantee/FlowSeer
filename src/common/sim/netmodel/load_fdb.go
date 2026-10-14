package netmodel

import (
	"fmt"
	"strconv"
	"time"

	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
)

// Issue codes used by netmodel.
const (
	IssueConflictFDB      analysis.IssueCode = "netmodel.fdb.conflict"
	IssueInvalidFDBStatus analysis.IssueCode = "netmodel.fdb.invalid_status"
	IssueInvalidFDBKind   analysis.IssueCode = "netmodel.fdb.invalid_kind"
)

func (c *loadContext) loadFDB(now time.Time, fdb []*switchingv1.FdbEntry, normCfg vswitch.Config) []bridge.Seed {
	var seeds []bridge.Seed
	fdbRows, fdbConflicts := resolveFacts(fdb, func(entry *switchingv1.FdbEntry) (factKey, string, bool) {
		if entry == nil {
			return factKey{}, "", false
		}
		switch {
		case !entry.HasStatus():
			c.addSkipped(entry.GetInterfaceName(), "fdb_entry", "status is unreported", analysis.Incomplete, IssueInvalidFDBStatus)
			return factKey{}, "", false
		case entry.GetStatus() == switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_UNSPECIFIED:
			c.addSkipped(entry.GetInterfaceName(), "fdb_entry", "status is UNSPECIFIED", analysis.Incomplete, IssueInvalidFDBStatus)
			return factKey{}, "", false
		case entry.GetStatus() == switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_INVALID:
			c.addSkipped(entry.GetInterfaceName(), "fdb_entry", "status is INVALID", analysis.Unsupported, IssueInvalidFDBStatus)
			return factKey{}, "", false
		case entry.GetStatus() != switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_ACTIVE:
			c.addSkipped(entry.GetInterfaceName(), "fdb_entry", fmt.Sprintf("status has unrecognized value %d", entry.GetStatus()), analysis.Unsupported, IssueInvalidFDBStatus)
			return factKey{}, "", false
		}
		switch {
		case !entry.HasKind():
			c.addSkipped(entry.GetInterfaceName(), "fdb_entry", "kind is unreported", analysis.Incomplete, IssueInvalidFDBKind)
			return factKey{}, "", false
		case entry.GetKind() == switchingv1.FdbEntryKind_FDB_ENTRY_KIND_UNSPECIFIED:
			c.addSkipped(entry.GetInterfaceName(), "fdb_entry", "kind is UNSPECIFIED", analysis.Incomplete, IssueInvalidFDBKind)
			return factKey{}, "", false
		case entry.GetKind() != switchingv1.FdbEntryKind_FDB_ENTRY_KIND_STATIC &&
			entry.GetKind() != switchingv1.FdbEntryKind_FDB_ENTRY_KIND_DYNAMIC:
			c.addSkipped(entry.GetInterfaceName(), "fdb_entry", fmt.Sprintf("kind %s cannot be represented as a forwarding seed", entry.GetKind()), analysis.Unsupported, IssueInvalidFDBKind)
			return factKey{}, "", false
		}
		vid := vlan.ID(entry.GetVlanId())
		if !vid.Valid() || uint64(entry.GetVlanId()) > uint64(vlan.MaxID) {
			c.addSkipped(entry.GetInterfaceName(), "fdb_entry", "vlan_id outside 1 through 4094", analysis.Unsupported, IssueInvalidVLANID)
			return factKey{}, "", false
		}

		if _, ok := normCfg.Ports.Port(entry.GetInterfaceName()); !ok {
			c.addSkipped(entry.GetInterfaceName(), "fdb_entry", "absent from port table", analysis.Incomplete, IssueUnknownPort)
			return factKey{}, "", false
		}

		mac, err := parseEUI48(entry.GetMac())
		if err != nil || mac.IsGroup() {
			why := "mac is not a usable six-octet individual EUI-48 address"
			if err != nil {
				why = err.Error()
			}
			c.addSkipped(entry.GetInterfaceName(), "fdb_entry", why, analysis.Incomplete, IssueInvalidMAC)
			return factKey{}, "", false
		}

		fdbKey := strconv.Itoa(int(vid)) + "\x00" + mac.String()
		value := fmt.Sprintf("port=%q, kind=%s, status=ACTIVE", entry.GetInterfaceName(), entry.GetKind())
		return factKey{
			id:      fdbKey,
			display: fmt.Sprintf("%d/%s", vid, mac),
			scope:   bridge.FDBLookupScope(c.src.DeviceID, vid, mac),
		}, value, true
	})
	for _, conflict := range fdbConflicts {
		c.addConflict(conflict.key.scope, "fdb_entry", conflict.key.display, conflict.detail(), IssueConflictFDB)
	}
	for _, fdbKey := range sortedKeys(fdbRows) {
		entry := fdbRows[fdbKey]
		vid := vlan.ID(entry.GetVlanId())
		mac, _ := parseEUI48(entry.GetMac())
		origin, lifetime := bridge.Observed, bridge.Aging
		if entry.GetKind() == switchingv1.FdbEntryKind_FDB_ENTRY_KIND_STATIC {
			origin, lifetime = bridge.Configured, bridge.Static
		}
		seed, why, code, ok := normalizeLoadedFDBSeed(normCfg, bridge.Seed{
			FID:       vid,
			MAC:       mac,
			Port:      entry.GetInterfaceName(),
			Origin:    origin,
			Lifetime:  lifetime,
			LearnedAt: now.UTC(),
		})
		if !ok {
			c.addSkipped(entry.GetInterfaceName(), "fdb_entry", why, analysis.Incomplete, code)
			continue
		}
		seeds = append(seeds, seed)
	}

	return seeds
}

func normalizeLoadedFDBSeed(cfg vswitch.Config, seed bridge.Seed) (bridge.Seed, string, analysis.IssueCode, bool) {
	if cfg.Bridge == nil {
		return bridge.Seed{}, "bridge relay is absent", IssueSkippedMissingFacet, false
	}
	if cfg.Bridge.VLAN == nil {
		return bridge.Seed{}, "VLAN awareness is absent", IssueSkippedMissingFacet, false
	}

	normalized, err := bridge.NormalizeSeeds(*cfg.Bridge, layer.Env{Ports: cfg.Ports}, []bridge.Seed{seed})
	if err == nil {
		return normalized[0], "", "", true
	}

	code := IssueSkippedMissingFacet
	if routingUsesPort(cfg.Routing, seed.Port) {
		code = IssueSkippedInterfaceRouted
	}
	return bridge.Seed{}, err.Error(), code, false
}

func routingUsesPort(cfg *routing.Config, portName string) bool {
	if cfg == nil {
		return false
	}
	for _, vrf := range cfg.VRFs {
		for _, iface := range vrf.Interfaces {
			if iface.Port == portName {
				return true
			}
		}
	}
	return false
}
