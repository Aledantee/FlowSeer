package bridge

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
)

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
