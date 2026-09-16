// Package loopprotect implements netsim's own loop-protection mechanism for
// the virtual switch: a probe frame that floods a foreign switch but returns
// to its own sender through an unmanaged loop, and a per-port action and
// recovery timer that reacts to it. It is independent of spanning tree and
// parses no vendor's proprietary loop-detection frame.
package loopprotect

import (
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// DefaultInterval is netsim's own probe interval (5 s), used whenever a
// Config leaves Interval unset.
const DefaultInterval time.Duration = 5 * time.Second

// Action names what a port does once a probe it sent has returned.
type Action string

const (
	// Block denies both learning and forwarding on the port, and keeps
	// sending probes so LoopCleared recovery can observe the loop persist.
	Block Action = "Block"

	// NoLearn stops learning on the port but keeps forwarding. It contains
	// the MAC flapping a loop causes; it does not break the loop.
	NoLearn Action = "NoLearn"

	// Disable denies both learning and forwarding and stops sending probes.
	Disable Action = "Disable"
)

// RecoveryMode names the rule that lifts an applied action.
type RecoveryMode string

const (
	// Manual holds the action until an explicit clear or a link down-then-up.
	Manual RecoveryMode = "Manual"

	// Timer lifts the action Recovery.Duration after it was applied, whether
	// or not the loop is gone, and a later returned probe reapplies it.
	Timer RecoveryMode = "Timer"

	// LoopCleared lifts the action Recovery.Duration after the last returned
	// probe. Every returned probe restarts the wait.
	LoopCleared RecoveryMode = "LoopCleared"
)

// Recovery configures how an applied action is lifted.
type Recovery struct {
	Mode     RecoveryMode
	Duration time.Duration
}

// Port defines the loop-protection settings for one network port.
type Port struct {
	Action   Action
	Recovery Recovery

	// VLANs lists the VLANs the port probes on. An empty VLANs probes VID 0,
	// which the switch sends on the port's PVID.
	VLANs []vlan.ID
}

// TypeID returns the fact type identifier for Port.
func (p Port) TypeID() string { return "loopprotect.port" }

// Canonical returns the canonical string representation of the Port fact.
func (p Port) Canonical() string {
	return "action=" + string(p.Action) +
		",recovery_mode=" + string(p.Recovery.Mode) +
		",recovery_duration=" + p.Recovery.Duration.String() +
		",vlans=" + VLANListFact(p.VLANs).Canonical()
}

// Config defines the loop-protection configuration of a virtual switch.
type Config struct {
	Interval time.Duration
	Ports    map[string]Port
}

// Clone returns a deep copy of the loop-protection configuration.
func (c Config) Clone() Config {
	cloned := c
	if c.Ports != nil {
		cloned.Ports = make(map[string]Port, len(c.Ports))
		for k, v := range c.Ports {
			v.VLANs = slices.Clone(v.VLANs)
			cloned.Ports[k] = v
		}
	}

	return cloned
}

func effectiveInterval(d time.Duration) time.Duration {
	if d == 0 {
		return DefaultInterval
	}

	return d
}

func effectiveDuration(d, interval time.Duration) time.Duration {
	if d == 0 {
		return 3 * interval
	}

	return d
}

func effectiveMode(m RecoveryMode, action Action) RecoveryMode {
	if m != "" {
		return m
	}
	if action == Disable {
		return Manual
	}

	return LoopCleared
}

// Normalize returns a normalized copy of the loop-protection configuration,
// filling unspecified fields with standard defaults.
func (c Config) Normalize() Config {
	cloned := c.Clone()
	cloned.Interval = effectiveInterval(cloned.Interval)

	for name, p := range cloned.Ports {
		p.Recovery.Duration = effectiveDuration(p.Recovery.Duration, cloned.Interval)
		p.Recovery.Mode = effectiveMode(p.Recovery.Mode, p.Action)
		vids := slices.Clone(p.VLANs)
		slices.Sort(vids)
		p.VLANs = slices.Compact(vids)
		cloned.Ports[name] = p
	}

	return cloned
}

// Validate checks the configuration against the port table: Interval must
// not be negative, every configured port must exist in the port table and
// must not be a LAG member, each port's Action and Recovery.Mode must be one
// of the defined values, Recovery.Duration must not be negative, and
// LoopCleared cannot pair with Disable because a disabled port sends no
// probes and can never observe the loop clearing.
func (c Config) Validate(ports port.Table) error {
	if c.Interval < 0 {
		return errs.New().
			Attr("field", "interval").
			Attr("interval", c.Interval).
			Msgf("loop protection interval %s cannot be negative", c.Interval)
	}

	for _, name := range sortedKeys(c.Ports) {
		p, ok := ports.Port(name)
		if !ok {
			return errs.New().
				Attr("field", "ports."+name).
				Attr("port", name).
				Msgf("loop protection port %q absent from port table", name)
		}
		if p.LagParent != "" {
			return errs.New().
				Attr("field", "ports."+name).
				Attr("port", name).
				Attr("parent", p.LagParent).
				Msgf("loop protection port %q cannot be a LAG member", name)
		}

		cfgPort := c.Ports[name]

		switch cfgPort.Action {
		case Block, NoLearn, Disable:
		default:
			return errs.New().
				Attr("field", "ports."+name+".action").
				Attr("port", name).
				Attr("action", cfgPort.Action).
				Msgf("unknown loop protection action %q on port %q", cfgPort.Action, name)
		}

		switch cfgPort.Recovery.Mode {
		case "", Manual, Timer, LoopCleared:
		default:
			return errs.New().
				Attr("field", "ports."+name+".recovery.mode").
				Attr("port", name).
				Attr("mode", cfgPort.Recovery.Mode).
				Msgf("unknown loop protection recovery mode %q on port %q", cfgPort.Recovery.Mode, name)
		}

		if cfgPort.Recovery.Duration < 0 {
			return errs.New().
				Attr("field", "ports."+name+".recovery.duration").
				Attr("port", name).
				Attr("duration", cfgPort.Recovery.Duration).
				Msgf("loop protection recovery duration %s on port %q cannot be negative", cfgPort.Recovery.Duration, name)
		}

		// A disabled port sends no probes, so LoopCleared recovery — which
		// lifts only once no probe returns — could never observe the loop
		// clearing and would hold the port down forever.
		if cfgPort.Recovery.Mode == LoopCleared && cfgPort.Action == Disable {
			return errs.New().
				Attr("field", "ports."+name+".recovery.mode").
				Attr("port", name).
				Msgf("loop protection port %q cannot pair LoopCleared recovery with Disable", name)
		}
	}

	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	return keys
}
