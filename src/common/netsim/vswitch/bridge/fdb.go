package bridge

import (
	"strconv"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// FDBLookupScope returns the exact construction metadata scope for a forwarding
// database lookup by filtering database ID and destination MAC address.
func FDBLookupScope(nodeID string, fid vlan.ID, mac netaddr.MAC) analysis.Scope {
	return fdbLookupScope(
		analysis.ProtocolScope(nodeID, string(port.LayerRelay), "0"),
		fid,
		mac,
	)
}

func fdbLookupScope(parent analysis.Scope, fid vlan.ID, mac netaddr.MAC) analysis.Scope {
	return analysis.FieldScope(parent, "fdb", strconv.Itoa(int(fid)), mac.String())
}

// Counters records forwarding database lifecycle events on a [Bridge].
type Counters struct {
	Learned uint64
	Expired uint64
	Evicted uint64
	Moved   uint64
}

// Seed represents a forwarding database entry to preload into a [Bridge].
type Seed struct {
	FID       vlan.ID
	MAC       netaddr.MAC
	Port      string
	Static    bool
	LearnedAt time.Time
}

// Entry represents an active record in the forwarding database.
type Entry struct {
	FID       vlan.ID
	MAC       netaddr.MAC
	Port      string
	Static    bool
	LearnedAt time.Time
}

type fdbKey struct {
	fid vlan.ID
	mac netaddr.MAC
}
