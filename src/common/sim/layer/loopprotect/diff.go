package loopprotect

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// LayerLoopProtect identifies the loop-protection layer in trace steps and diff subjects.
const LayerLoopProtect trace.Layer = "loopprotect"

// durationFact wraps a time.Duration as a trace.Fact.
type durationFact time.Duration

// TypeID returns the fact type identifier for durationFact.
func (f durationFact) TypeID() string { return "loopprotect.duration" }

// Canonical returns the formatted duration string.
func (f durationFact) Canonical() string { return time.Duration(f).String() }

// actionFact wraps an Action as a trace.Fact.
type actionFact Action

// TypeID returns the fact type identifier for actionFact.
func (f actionFact) TypeID() string { return "loopprotect.action" }

// Canonical returns the action string.
func (f actionFact) Canonical() string { return string(f) }

// recoveryModeFact wraps a RecoveryMode as a trace.Fact.
type recoveryModeFact RecoveryMode

// TypeID returns the fact type identifier for recoveryModeFact.
func (f recoveryModeFact) TypeID() string { return "loopprotect.recovery_mode" }

// Canonical returns the recovery mode string.
func (f recoveryModeFact) Canonical() string { return string(f) }

// vlanListFact wraps a set of VLAN identifiers as a trace.Fact.
type vlanListFact []vlan.ID

// TypeID returns the fact type identifier for vlanListFact.
func (f vlanListFact) TypeID() string { return "loopprotect.vlans" }

// Canonical returns the VLAN identifiers sorted and joined with commas.
func (f vlanListFact) Canonical() string {
	sorted := slices.Clone(f)
	slices.Sort(sorted)

	ids := make([]string, len(sorted))
	for i, vid := range sorted {
		ids[i] = strconv.FormatUint(uint64(vid), 10)
	}

	return strings.Join(ids, ",")
}

// Diff computes the difference between two loop-protection configurations,
// reporting changes to the probe interval and, per port, action, recovery
// mode, recovery duration, and VLAN membership.
func Diff(a, b Config) []trace.Change {
	a = a.Normalize(layer.Env{})
	b = b.Normalize(layer.Env{})

	var changes []trace.Change

	bridge := trace.Subject{Kind: "bridge", Key: ""}

	if a.Interval != b.Interval {
		changes = append(changes, trace.Change{
			Layer:   LayerLoopProtect,
			Subject: bridge,
			Field:   "interval",
			From:    durationFact(a.Interval),
			To:      durationFact(b.Interval),
		})
	}

	for _, name := range sortedKeys(a.Ports) {
		ap := a.Ports[name]
		subject := trace.Subject{Kind: "port", Key: name}

		bp, exists := b.Ports[name]
		if !exists {
			changes = append(changes, trace.Change{
				Layer:   LayerLoopProtect,
				Subject: subject,
				Field:   "",
				From:    ap,
				To:      nil,
			})

			continue
		}

		changes = append(changes, diffPort(ap, bp, subject)...)
	}

	for _, name := range sortedKeys(b.Ports) {
		if _, exists := a.Ports[name]; !exists {
			changes = append(changes, trace.Change{
				Layer:   LayerLoopProtect,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "",
				From:    nil,
				To:      b.Ports[name],
			})
		}
	}

	return changes
}

// diffPort computes the differences between two normalized ports sharing one subject.
func diffPort(ap, bp Port, subject trace.Subject) []trace.Change {
	var changes []trace.Change

	if ap.Action != bp.Action {
		changes = append(changes, trace.Change{
			Layer: LayerLoopProtect, Subject: subject, Field: "action",
			From: actionFact(ap.Action), To: actionFact(bp.Action),
		})
	}
	if ap.Recovery.Mode != bp.Recovery.Mode {
		changes = append(changes, trace.Change{
			Layer: LayerLoopProtect, Subject: subject, Field: "recovery.mode",
			From: recoveryModeFact(ap.Recovery.Mode), To: recoveryModeFact(bp.Recovery.Mode),
		})
	}
	if ap.Recovery.Duration != bp.Recovery.Duration {
		changes = append(changes, trace.Change{
			Layer: LayerLoopProtect, Subject: subject, Field: "recovery.duration",
			From: durationFact(ap.Recovery.Duration), To: durationFact(bp.Recovery.Duration),
		})
	}
	if !slices.Equal(ap.VLANs, bp.VLANs) {
		changes = append(changes, trace.Change{
			Layer: LayerLoopProtect, Subject: subject, Field: "vlans",
			From: vlanListFact(ap.VLANs), To: vlanListFact(bp.VLANs),
		})
	}

	return changes
}
