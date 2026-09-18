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

// Origin names who installed a forwarding database record: an operator or a
// preloaded seed (Configured), or ingress learning from a live frame's
// source address (Observed). It is independent of Lifetime: a configured
// record can still age, and an observed one can be pinned past its aging
// time.
type Origin string

const (
	// Configured is the zero value: an operator or a preloaded seed
	// installed the record.
	Configured Origin = ""

	// Observed is a record ingress learning installed from a live frame.
	Observed Origin = "observed"
)

// Lifetime names whether a forwarding database record ages out on its own.
type Lifetime string

const (
	// Aging is the zero value: [Bridge.Age] removes the record once it has
	// sat longer than the bridge's configured aging time.
	Aging Lifetime = ""

	// Static is a record [Bridge.Age] never removes.
	Static Lifetime = "static"
)

// Seed represents a forwarding database entry to preload into a [Bridge].
type Seed struct {
	FID       vlan.ID
	MAC       netaddr.MAC
	Port      string
	Origin    Origin
	Lifetime  Lifetime
	LearnedAt time.Time
}

// Entry represents an active record in the forwarding database.
type Entry struct {
	FID       vlan.ID
	MAC       netaddr.MAC
	Port      string
	Origin    Origin
	Lifetime  Lifetime
	LearnedAt time.Time
}

type fdbKey struct {
	fid vlan.ID
	mac netaddr.MAC
}
