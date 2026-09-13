// Package lag implements link aggregation (IEEE 802.1AX) for the virtual switch,
// providing member selection, bond modes, up/down delays, and the LACP exchange.
package lag

import (
	"fmt"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

const (
	// DefaultSystemPriority is the standard administrative system priority (32768).
	DefaultSystemPriority uint16 = 32768

	// DefaultPortPriority is the standard administrative port priority (32768).
	DefaultPortPriority uint16 = 32768

	// FastPeriod is the transmission interval for fast LACP (1 second).
	FastPeriod time.Duration = time.Second

	// SlowPeriod is the transmission interval for slow LACP (30 seconds).
	SlowPeriod time.Duration = 30 * time.Second

	// TimeoutMultiplier is the multiplier applied to the transmission period to compute receive timeouts (3).
	TimeoutMultiplier = 3
)

// Mode defines the frame distribution policy across aggregated links.
type Mode string

// TypeID returns the fact type identifier for Mode.
func (m Mode) TypeID() string { return "lag.bond_mode" }

// Canonical returns the string representation of the mode.
func (m Mode) Canonical() string {
	if m == "" {
		return string(ActiveBackup)
	}
	return string(m)
}

const (
	// ActiveBackup transmits through one primary link and fails over to secondary links.
	ActiveBackup Mode = ""

	// BalanceSLB balances outbound traffic by source MAC address and VLAN identifier.
	BalanceSLB Mode = "BalanceSLB"

	// BalanceTCP balances outbound TCP and UDP flows by layer 2, 3, and 4 fields.
	BalanceTCP Mode = "BalanceTCP"
)

// LACPMode controls whether the Link Aggregation Control Protocol negotiates aggregation with a peer.
type LACPMode string

// TypeID returns the fact type identifier for LACPMode.
func (m LACPMode) TypeID() string { return "lag.lacp_mode" }

// Canonical returns the string representation of the LACP mode.
func (m LACPMode) Canonical() string {
	if m == "" {
		return string(Off)
	}
	return string(m)
}

const (
	// Off disables LACP negotiation; link state alone controls enablement.
	Off LACPMode = ""

	// Active transmits LACPDUs periodically to negotiate with the partner.
	Active LACPMode = "Active"

	// Passive responds to partner LACPDUs but transmits only after an active partner is detected.
	Passive LACPMode = "Passive"
)

// Member defines administrative settings for one port in a link aggregation group.
type Member struct {
	Priority uint16
	Key      uint16
}

// TypeID returns the fact type identifier for Member.
func (m Member) TypeID() string { return "lag.member" }

// Canonical returns the canonical string representation of the Member fact.
func (m Member) Canonical() string {
	return fmt.Sprintf("priority=%d,key=%d", m.Priority, m.Key)
}

// LACPConfig defines the Link Aggregation Control Protocol settings for a link aggregation group.
type LACPConfig struct {
	Mode           LACPMode
	Fast           bool
	SystemPriority uint16
	SystemID       netaddr.MAC
	Key            uint16
	Fallback       bool
}

// LAG defines administrative settings for one link aggregation group.
type LAG struct {
	Mode      Mode
	Primary   string
	UpDelay   time.Duration
	DownDelay time.Duration
	HashBasis uint32
	MinLinks  int
	LACP      LACPConfig
	Members   map[string]Member
}

// Config defines the link aggregation configuration for a virtual switch.
type Config struct {
	LAGs map[string]LAG
}

// Clone returns an independent deep copy of the configuration.
func (c Config) Clone() Config {
	if c.LAGs == nil {
		return Config{}
	}
	cp := Config{
		LAGs: make(map[string]LAG, len(c.LAGs)),
	}
	for k, v := range c.LAGs {
		lagCp := v
		if v.Members != nil {
			lagCp.Members = make(map[string]Member, len(v.Members))
			for mk, mv := range v.Members {
				lagCp.Members[mk] = mv
			}
		}
		cp.LAGs[k] = lagCp
	}

	return cp
}

// Normalize returns the effective configuration for the supplied port table and
// system ID. It adds entries for configured LAG ports and fills omitted defaults.
func (c Config) Normalize(ports port.Table, systemID netaddr.MAC) Config {
	cloned := c.Clone()
	if cloned.LAGs == nil {
		cloned.LAGs = make(map[string]LAG)
	}

	lagKeys := make(map[string]uint16)
	lagMembers := make(map[string][]string)
	var lagNames []string
	for _, p := range ports.Ports() {
		if p.Kind == port.Lag {
			lagNames = append(lagNames, p.Name)
		}
	}
	slices.Sort(lagNames)
	for i, lagName := range lagNames {
		lagKeys[lagName] = uint16(i + 1)
		members := ports.Members(lagName)
		names := make([]string, 0, len(members))
		for _, member := range members {
			names = append(names, member.Name)
		}
		slices.Sort(names)
		lagMembers[lagName] = names
		if _, ok := cloned.LAGs[lagName]; !ok {
			cloned.LAGs[lagName] = LAG{}
		}
	}

	for _, lagName := range sortedKeys(cloned.LAGs) {
		lag := cloned.LAGs[lagName]
		if lag.Mode == "" {
			lag.Mode = ActiveBackup
		}
		if lag.LACP.Mode == "" {
			lag.LACP.Mode = Off
		}
		if lag.LACP.SystemPriority == 0 {
			lag.LACP.SystemPriority = DefaultSystemPriority
		}
		if lag.LACP.SystemID == (netaddr.MAC{}) {
			lag.LACP.SystemID = systemID
		}
		if lag.LACP.Key == 0 {
			lag.LACP.Key = lagKeys[lagName]
		}

		members, knownLAG := lagMembers[lagName]
		if lag.Primary == "" && len(members) > 0 {
			lag.Primary = members[0]
		}
		if knownLAG && lag.Members == nil {
			lag.Members = make(map[string]Member, len(members))
		}
		for _, memberName := range members {
			if _, ok := lag.Members[memberName]; !ok {
				lag.Members[memberName] = Member{}
			}
		}
		for memName, m := range lag.Members {
			if m.Priority == 0 {
				m.Priority = DefaultPortPriority
			}
			if m.Key == 0 && lag.LACP.Key != 0 {
				m.Key = lag.LACP.Key
			}
			lag.Members[memName] = m
		}
		cloned.LAGs[lagName] = lag
	}

	return cloned
}

// Validate verifies the configuration against the port table: every configured LAG
// must exist as a LAG port in the port table, every configured member must be a member
// of that LAG, Primary must be a member of the LAG, mode and LACP mode must be recognized,
// delays must not be negative, and MinLinks must not exceed the member count.
func (c Config) Validate(ports port.Table) error {
	for _, lagName := range sortedKeys(c.LAGs) {
		lagCfg := c.LAGs[lagName]
		p, ok := ports.Port(lagName)
		if !ok {
			return errs.New().
				Attr("field", "lags."+lagName).
				Attr("lag", lagName).
				Msgf("LAG port %q absent from port table", lagName)
		}
		if p.Kind != port.Lag {
			return errs.New().
				Attr("field", "lags."+lagName).
				Attr("lag", lagName).
				Attr("kind", p.Kind).
				Msgf("port %q is not a LAG", lagName)
		}

		switch lagCfg.Mode {
		case ActiveBackup, BalanceSLB, BalanceTCP:
		default:
			return errs.New().
				Attr("field", "lags."+lagName+".mode").
				Attr("lag", lagName).
				Attr("mode", lagCfg.Mode).
				Msgf("unknown bond mode %q", lagCfg.Mode)
		}

		switch lagCfg.LACP.Mode {
		case Off, Active, Passive:
		default:
			return errs.New().
				Attr("field", "lags."+lagName+".lacp.mode").
				Attr("lag", lagName).
				Attr("lacp_mode", lagCfg.LACP.Mode).
				Msgf("unknown LACP mode %q", lagCfg.LACP.Mode)
		}
		if lagCfg.LACP.SystemID.IsGroup() {
			return errs.New().
				Attr("field", "lags."+lagName+".lacp.system_id").
				Attr("lag", lagName).
				Attr("system_id", lagCfg.LACP.SystemID).
				Msgf("LACP system ID %s on LAG %q cannot be a group MAC", lagCfg.LACP.SystemID, lagName)
		}

		if lagCfg.UpDelay < 0 {
			return errs.New().
				Attr("field", "lags."+lagName+".up_delay").
				Attr("lag", lagName).
				Attr("up_delay", lagCfg.UpDelay).
				Msg("up delay cannot be negative")
		}
		if lagCfg.DownDelay < 0 {
			return errs.New().
				Attr("field", "lags."+lagName+".down_delay").
				Attr("lag", lagName).
				Attr("down_delay", lagCfg.DownDelay).
				Msg("down delay cannot be negative")
		}

		members := ports.Members(lagName)
		memberMap := make(map[string]struct{}, len(members))
		for _, m := range members {
			memberMap[m.Name] = struct{}{}
		}

		if lagCfg.MinLinks < 0 || lagCfg.MinLinks > len(members) {
			return errs.New().
				Attr("field", "lags."+lagName+".min_links").
				Attr("lag", lagName).
				Attr("min_links", lagCfg.MinLinks).
				Attr("member_count", len(members)).
				Msgf("min links %d exceeds member count %d", lagCfg.MinLinks, len(members))
		}

		if lagCfg.Primary != "" {
			if _, ok := memberMap[lagCfg.Primary]; !ok {
				return errs.New().
					Attr("field", "lags."+lagName+".primary").
					Attr("lag", lagName).
					Attr("primary", lagCfg.Primary).
					Msgf("primary %q is not a member of LAG %q", lagCfg.Primary, lagName)
			}
		}

		for _, memName := range sortedKeys(lagCfg.Members) {
			if _, ok := memberMap[memName]; !ok {
				return errs.New().
					Attr("field", "lags."+lagName+".members."+memName).
					Attr("lag", lagName).
					Attr("member", memName).
					Msgf("port %q is not a member of LAG %q", memName, lagName)
			}
		}
	}

	return nil
}

// Defaults returns the normalized effective configuration.
func (c Config) Defaults(ports port.Table, systemID netaddr.MAC) Config {
	return c.Normalize(ports, systemID)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	return keys
}
