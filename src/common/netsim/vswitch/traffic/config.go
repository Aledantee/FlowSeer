// Package traffic defines traffic mirroring, ingress policing, and egress queue
// configuration for a virtual switch.
package traffic

import (
	"maps"
	"slices"
	"strconv"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

const (
	// Layer identifies traffic configuration changes.
	Layer trace.Layer = "traffic"

	// RulePolicerRefuse identifies a token-bucket decision that drops an ingress frame.
	RulePolicerRefuse trace.RuleID = "traffic.policer.refuse"
	// RuleMirrorCopy identifies a mirror copy admitted to its configured output.
	RuleMirrorCopy trace.RuleID = "traffic.mirror.copy"
	// RuleMirrorCopyDrop identifies a mirror copy suppressed because its output cannot forward.
	RuleMirrorCopyDrop trace.RuleID = "traffic.mirror.copy_drop"

	// ReasonPoliced identifies a frame refused by an ingress policer.
	ReasonPoliced trace.Reason = "policed"

	// ReasonMirrorOutput identifies ordinary traffic refused on a reserved mirror output port.
	ReasonMirrorOutput trace.Reason = "mirror-output"
)

// Mirror defines the frames selected for copying and their output destination.
// Exactly one output must be set. SnapLen zero leaves copies untruncated.
// Mirror is not safe for concurrent use.
type Mirror struct {
	Name           string
	SelectAll      bool
	SelectSrcPorts []string
	SelectDstPorts []string
	SelectVLANs    []vlan.ID
	OutputPort     string
	OutputVLAN     *vlan.ID
	SnapLen        int
}

// Policer defines an ingress token bucket. RateBPS is in bits per second and
// BurstOctets is the bucket capacity; a zero rate disables policing. Policer is
// not safe for concurrent use.
type Policer struct {
	RateBPS     uint64
	BurstOctets int
}

// PortQueues defines maximum rates in bits per second by priority code point
// for one port. An absent priority has no configured maximum. PortQueues is not
// safe for concurrent use.
type PortQueues struct {
	MaxRateBPS map[vlan.PCP]uint64
}

// Config defines traffic handling for a virtual switch. Its zero value has no
// mirrors, policers, or queue limits. Callers must not mutate it concurrently.
type Config struct {
	Mirrors  []Mirror
	Policers map[string]Policer
	Queues   map[string]PortQueues
}

// Clone returns an independent deep copy of the configuration.
func (c Config) Clone() Config {
	cp := Config{}
	if c.Mirrors != nil {
		cp.Mirrors = make([]Mirror, len(c.Mirrors))
		for i, mirror := range c.Mirrors {
			cp.Mirrors[i] = cloneMirror(mirror)
		}
	}
	if c.Policers != nil {
		cp.Policers = make(map[string]Policer, len(c.Policers))
		maps.Copy(cp.Policers, c.Policers)
	}
	if c.Queues != nil {
		cp.Queues = make(map[string]PortQueues, len(c.Queues))
		for name, queues := range c.Queues {
			queueCopy := PortQueues{}
			if queues.MaxRateBPS != nil {
				queueCopy.MaxRateBPS = make(map[vlan.PCP]uint64, len(queues.MaxRateBPS))
				maps.Copy(queueCopy.MaxRateBPS, queues.MaxRateBPS)
			}
			cp.Queues[name] = queueCopy
		}
	}

	return cp
}

// Normalize returns a normalized copy of the configuration with mirror selectors sorted deterministically.
func (c Config) Normalize() Config {
	cp := c.Clone()
	for i := range cp.Mirrors {
		if len(cp.Mirrors[i].SelectSrcPorts) > 0 {
			slices.Sort(cp.Mirrors[i].SelectSrcPorts)
			cp.Mirrors[i].SelectSrcPorts = slices.Compact(cp.Mirrors[i].SelectSrcPorts)
		}
		if len(cp.Mirrors[i].SelectDstPorts) > 0 {
			slices.Sort(cp.Mirrors[i].SelectDstPorts)
			cp.Mirrors[i].SelectDstPorts = slices.Compact(cp.Mirrors[i].SelectDstPorts)
		}
		if len(cp.Mirrors[i].SelectVLANs) > 0 {
			slices.Sort(cp.Mirrors[i].SelectVLANs)
			cp.Mirrors[i].SelectVLANs = slices.Compact(cp.Mirrors[i].SelectVLANs)
		}
	}
	slices.SortFunc(cp.Mirrors, func(a, b Mirror) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	return cp
}

// Validate checks mirror names and destinations, logical selector ports and
// VLANs, policer bursts, and queue rates against the supplied port table.
func (c Config) Validate(ports port.Table) error {
	mirrorNames := make(map[string]struct{}, len(c.Mirrors))
	outputPorts := make(map[string]struct{}, len(c.Mirrors))
	for mirrorIndex, mirror := range c.Mirrors {
		prefix := "mirrors." + strconv.Itoa(mirrorIndex)
		if mirror.Name == "" {
			return errs.New().Attr("field", prefix+".name").Attr("mirror", "").Msg("mirror name cannot be empty")
		}
		if _, exists := mirrorNames[mirror.Name]; exists {
			return errs.New().
				Attr("field", prefix+".name").
				Attr("mirror", mirror.Name).
				Msgf("duplicate mirror name %q", mirror.Name)
		}
		mirrorNames[mirror.Name] = struct{}{}

		if (mirror.OutputPort == "") == (mirror.OutputVLAN == nil) {
			return errs.New().
				Attr("field", prefix+".output").
				Attr("mirror", mirror.Name).
				Msgf("mirror %q must have exactly one output", mirror.Name)
		}
		if mirror.OutputPort != "" {
			output, ok := ports.Port(mirror.OutputPort)
			if !ok {
				return errs.New().
					Attr("field", prefix+".output_port").
					Attr("mirror", mirror.Name).
					Attr("port", mirror.OutputPort).
					Msgf("mirror output port %q absent from port table", mirror.OutputPort)
			}
			if output.Kind == port.Lag || output.LagParent != "" {
				return errs.New().
					Attr("field", prefix+".output_port").
					Attr("mirror", mirror.Name).
					Attr("port", mirror.OutputPort).
					Msgf("mirror output port %q cannot be a LAG or LAG member", mirror.OutputPort)
			}
			outputPorts[mirror.OutputPort] = struct{}{}
		}
		if mirror.OutputVLAN != nil && !mirror.OutputVLAN.Valid() {
			return errs.New().
				Attr("field", prefix+".output_vlan").
				Attr("mirror", mirror.Name).
				Attr("vlan", *mirror.OutputVLAN).
				Msgf("mirror output VLAN %d is outside 1 through 4094", *mirror.OutputVLAN)
		}
		if mirror.SnapLen < 0 || mirror.SnapLen > 0 && mirror.SnapLen < 18 {
			return errs.New().
				Attr("field", prefix+".snap_len").
				Attr("mirror", mirror.Name).
				Attr("snap_len", mirror.SnapLen).
				Msg("mirror snap length must be zero or at least 18 octets")
		}

		for selectorIndex, name := range mirror.SelectSrcPorts {
			selected, ok := ports.Port(name)
			if !ok {
				return errs.New().
					Attr("field", prefix+".select_src_ports."+strconv.Itoa(selectorIndex)).
					Attr("mirror", mirror.Name).
					Attr("port", name).
					Msgf("mirror selector port %q absent from port table", name)
			}
			if selected.LagParent != "" {
				return errs.New().
					Attr("field", prefix+".select_src_ports."+strconv.Itoa(selectorIndex)).
					Attr("mirror", mirror.Name).
					Attr("port", name).
					Attr("lag", selected.LagParent).
					Msgf("mirror selector port %q is a physical LAG member", name)
			}
		}
		for selectorIndex, name := range mirror.SelectDstPorts {
			selected, ok := ports.Port(name)
			if !ok {
				return errs.New().
					Attr("field", prefix+".select_dst_ports."+strconv.Itoa(selectorIndex)).
					Attr("mirror", mirror.Name).
					Attr("port", name).
					Msgf("mirror selector port %q absent from port table", name)
			}
			if selected.LagParent != "" {
				return errs.New().
					Attr("field", prefix+".select_dst_ports."+strconv.Itoa(selectorIndex)).
					Attr("mirror", mirror.Name).
					Attr("port", name).
					Attr("lag", selected.LagParent).
					Msgf("mirror selector port %q is a physical LAG member", name)
			}
		}
		for selectorIndex, id := range mirror.SelectVLANs {
			if !id.Valid() {
				return errs.New().
					Attr("field", prefix+".select_vlans."+strconv.Itoa(selectorIndex)).
					Attr("mirror", mirror.Name).
					Attr("vlan", id).
					Msgf("mirror selector VLAN %d is outside 1 through 4094", id)
			}
		}
	}

	for mirrorIndex, mirror := range c.Mirrors {
		for selectorIndex, name := range mirror.SelectSrcPorts {
			if _, reserved := outputPorts[name]; reserved {
				return errs.New().
					Attr("field", "mirrors."+strconv.Itoa(mirrorIndex)+".select_src_ports."+strconv.Itoa(selectorIndex)).
					Attr("mirror", mirror.Name).
					Attr("port", name).
					Msgf("mirror selector port %q is reserved for mirror output", name)
			}
		}
		for selectorIndex, name := range mirror.SelectDstPorts {
			if _, reserved := outputPorts[name]; reserved {
				return errs.New().
					Attr("field", "mirrors."+strconv.Itoa(mirrorIndex)+".select_dst_ports."+strconv.Itoa(selectorIndex)).
					Attr("mirror", mirror.Name).
					Attr("port", name).
					Msgf("mirror selector port %q is reserved for mirror output", name)
			}
		}
	}

	for _, name := range sortedKeys(c.Policers) {
		policer := c.Policers[name]
		if _, ok := ports.Port(name); !ok {
			return errs.New().
				Attr("field", "policers."+name).
				Attr("port", name).
				Msgf("policer port %q absent from port table", name)
		}
		if policer.RateBPS > 0 && policer.BurstOctets < 1 {
			return errs.New().
				Attr("field", "policers."+name+".burst_octets").
				Attr("port", name).
				Attr("rate_bps", policer.RateBPS).
				Attr("burst_octets", policer.BurstOctets).
				Msgf("policer on port %q requires a positive burst", name)
		}
	}

	for _, name := range sortedKeys(c.Queues) {
		queues := c.Queues[name]
		if _, ok := ports.Port(name); !ok {
			return errs.New().
				Attr("field", "queues."+name).
				Attr("port", name).
				Msgf("queue port %q absent from port table", name)
		}
		pcps := make([]vlan.PCP, 0, len(queues.MaxRateBPS))
		for pcp := range queues.MaxRateBPS {
			pcps = append(pcps, pcp)
		}
		slices.Sort(pcps)
		for _, pcp := range pcps {
			rate := queues.MaxRateBPS[pcp]
			field := "queues." + name + ".max_rate_bps." + strconv.Itoa(int(pcp))
			if !pcp.Valid() {
				return errs.New().
					Attr("field", field).
					Attr("port", name).
					Attr("pcp", pcp).
					Msgf("queue PCP %d on port %q is invalid", pcp, name)
			}
			if rate == 0 {
				return errs.New().
					Attr("field", field).
					Attr("port", name).
					Attr("pcp", pcp).
					Msgf("queue maximum rate on port %q PCP %d must be positive", name, pcp)
			}
		}
	}

	return nil
}

// MaxRate returns the configured maximum rate for a port and priority.
func (c Config) MaxRate(name string, pcp vlan.PCP) (uint64, bool) {
	queues, ok := c.Queues[name]
	if !ok {
		return 0, false
	}
	rate, ok := queues.MaxRateBPS[pcp]

	return rate, ok
}

// OutputPorts returns the sorted set of ports reserved for mirror output.
func (c Config) OutputPorts() []string {
	ports := make(map[string]struct{}, len(c.Mirrors))
	for _, mirror := range c.Mirrors {
		if mirror.OutputPort != "" {
			ports[mirror.OutputPort] = struct{}{}
		}
	}

	return sortedKeys(ports)
}

func cloneMirror(mirror Mirror) Mirror {
	cp := mirror
	cp.SelectSrcPorts = slices.Clone(mirror.SelectSrcPorts)
	cp.SelectDstPorts = slices.Clone(mirror.SelectDstPorts)
	cp.SelectVLANs = slices.Clone(mirror.SelectVLANs)
	if mirror.OutputVLAN != nil {
		id := *mirror.OutputVLAN
		cp.OutputVLAN = &id
	}

	return cp
}

func sortedKeys[K ~string, V any](values map[K]V) []K {
	keys := make([]K, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	return keys
}
