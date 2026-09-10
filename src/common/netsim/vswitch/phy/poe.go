package phy

import (
	"cmp"
	"slices"

	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

// Priority is a PSE port's power-delivery priority when a group's budget is
// oversubscribed. The vocabulary follows the pethPsePortPowerPriority object
// of the RFC 3621 Power Ethernet MIB.
type Priority string

const (
	// PriorityCritical allocates before every other priority.
	PriorityCritical Priority = "Critical"

	// PriorityHigh allocates after critical ports.
	PriorityHigh Priority = "High"

	// PriorityLow allocates after high-priority ports.
	PriorityLow Priority = "Low"
)

// rank orders critical first; an unset priority sorts behind low, since a
// port the source never prioritized has the weakest claim on the budget.
func (p Priority) rank() int {
	switch p {
	case PriorityCritical:
		return 0
	case PriorityHigh:
		return 1
	case PriorityLow:
		return 2
	default:
		return 3
	}
}

// Group is one power-sourcing equipment group with its nominal power budget,
// per the pethMainPseTable of the RFC 3621 Power Ethernet MIB.
type Group struct {
	PowerMilliwatts uint32
}

// PsePort is one power-sourcing port. MaxClass is the highest powered-device
// class the port can source; no net/phy message carries it, so the loader
// supplies it. Limit optionally caps the power the port may draw. PDClass is
// the class of the attached powered device.
type PsePort struct {
	Group    string
	MaxClass uint8
	Enabled  bool
	Limit    *uint32
	Priority Priority
	PDClass  uint8
}

// PoE is the power-sourcing configuration of a virtual switch: its groups
// with their budgets and its PSE ports by port name.
type PoE struct {
	Groups map[string]Group
	Ports  map[string]PsePort
}

// Denial reasons recorded by [Config.Allocate] for a port granted no power.
const (
	// ReasonDisabled denies power because delivery on the port is disabled.
	ReasonDisabled trace.Reason = "disabled"

	// ReasonBudget denies power because the class power exceeds the group's
	// remaining budget.
	ReasonBudget trace.Reason = "budget"

	// ReasonLimit denies power because the class power exceeds the port's
	// configured limit.
	ReasonLimit trace.Reason = "limit"

	// ReasonClassUnsupported denies power because the attached device's class
	// is above the port's maximum class.
	ReasonClassUnsupported trace.Reason = "class-unsupported"
)

// classPowerMW is the power a powered device of each IEEE 802.3 class draws
// at the PSE, per the 802.3bt power levels.
var classPowerMW = [...]uint32{15_400, 4_000, 7_000, 15_400, 30_000, 45_000, 60_000, 75_000, 90_000}

// maxClass is the highest class IEEE 802.3bt defines.
const maxClass = uint8(len(classPowerMW) - 1)

// ClassPowerMW returns the power in milliwatts a powered device of class
// draws at the PSE. It reports false for a class above 8.
func ClassPowerMW(class uint8) (uint32, bool) {
	if class > maxClass {
		return 0, false
	}

	return classPowerMW[class], true
}

// Allocation is the result of distributing each group's budget over its PSE
// ports.
type Allocation struct {
	Ports  map[string]PortAllocation
	Groups map[string]GroupAllocation
}

// PortAllocation records the power granted to one port, or the reason it was
// denied. Denial is empty when power was granted.
type PortAllocation struct {
	Milliwatts uint32
	Denial     trace.Reason
}

// GroupAllocation records a group's budget, the power allocated from it, and
// the unallocated remainder, in milliwatts.
type GroupAllocation struct {
	BudgetMilliwatts    uint32
	AllocatedMilliwatts uint32
	RemainderMilliwatts uint32
}

// Allocate distributes each group's budget over its ports, critical priority
// first with the port name as tie-break, charging each port its class power.
// Priority order is what keeps the budget invariant under oversubscription:
// a port whose class power exceeds the remainder is denied rather than
// overdrawing the group. A port naming an unknown group is skipped;
// [Config.Validate] rejects such a configuration. Allocate returns empty
// maps when the PoE capability is absent.
func (c Config) Allocate() Allocation {
	result := Allocation{}
	if c.PoE == nil {
		return result
	}
	result.Ports = make(map[string]PortAllocation, len(c.PoE.Ports))
	result.Groups = make(map[string]GroupAllocation, len(c.PoE.Groups))

	byGroup := make(map[string][]string, len(c.PoE.Groups))
	for name, p := range c.PoE.Ports {
		byGroup[p.Group] = append(byGroup[p.Group], name)
	}

	for _, groupName := range sortedKeys(c.PoE.Groups) {
		names := byGroup[groupName]
		slices.SortFunc(names, func(x, y string) int {
			if r := cmp.Compare(c.PoE.Ports[x].Priority.rank(), c.PoE.Ports[y].Priority.rank()); r != 0 {
				return r
			}

			return cmp.Compare(x, y)
		})

		budget := c.PoE.Groups[groupName].PowerMilliwatts
		remainder := budget
		var allocated uint32

		for _, name := range names {
			p := c.PoE.Ports[name]
			power, known := ClassPowerMW(p.PDClass)
			pa := PortAllocation{}

			switch {
			case !p.Enabled:
				pa.Denial = ReasonDisabled
			case !known || p.PDClass > p.MaxClass:
				pa.Denial = ReasonClassUnsupported
			case p.Limit != nil && power > *p.Limit:
				pa.Denial = ReasonLimit
			case power > remainder:
				pa.Denial = ReasonBudget
			default:
				pa.Milliwatts = power
				remainder -= power
				allocated += power
			}
			result.Ports[name] = pa
		}

		result.Groups[groupName] = GroupAllocation{
			BudgetMilliwatts:    budget,
			AllocatedMilliwatts: allocated,
			RemainderMilliwatts: remainder,
		}
	}

	return result
}
