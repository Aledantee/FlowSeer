package fabric

import (
	"sync"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

// Counters holds cumulative RFC 2863 traffic, error, and discard metrics for a device port.
type Counters struct {
	InOctets     uint64
	OutOctets    uint64
	InUnicast    uint64
	OutUnicast   uint64
	InMulticast  uint64
	OutMulticast uint64
	InBroadcast  uint64
	OutBroadcast uint64
	InErrors     uint64
	// OutErrors counts outbound packets not transmitted due to an error. It remains zero because
	// no egress transmission faults exist in the simulation.
	OutErrors   uint64
	InDiscards  uint64
	OutDiscards uint64
	Discards    map[trace.Reason]uint64
}

// Clone returns an independent deep copy of c.
func (c Counters) Clone() Counters {
	cp := c
	if len(c.Discards) > 0 {
		cp.Discards = make(map[trace.Reason]uint64, len(c.Discards))
		for k, v := range c.Discards {
			cp.Discards[k] = v
		}
	} else {
		cp.Discards = make(map[trace.Reason]uint64)
	}

	return cp
}

type frameClass int

const (
	classUnicast frameClass = iota
	classMulticast
	classBroadcast
)

var (
	broadcastMAC = netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}

	countersMu     sync.Mutex
	fabricCounters = make(map[*Fabric]map[string]map[string]Counters)
)

func classifyMAC(m netaddr.MAC) frameClass {
	if m == broadcastMAC {
		return classBroadcast
	}
	if m.IsGroup() {
		return classMulticast
	}

	return classUnicast
}

func (f *Fabric) ensureCountersLocked(device, portName string) Counters {
	devs := fabricCounters[f]
	if devs == nil {
		devs = make(map[string]map[string]Counters)
		fabricCounters[f] = devs
	}
	ports := devs[device]
	if ports == nil {
		ports = make(map[string]Counters)
		devs[device] = ports
	}
	c, ok := ports[portName]
	if !ok || c.Discards == nil {
		c.Discards = make(map[trace.Reason]uint64)
	}

	return c
}

func (f *Fabric) countIngress(device, portName string, inOctets uint64, class frameClass) {
	countersMu.Lock()
	defer countersMu.Unlock()

	c := f.ensureCountersLocked(device, portName)
	c.InOctets += inOctets
	switch class {
	case classBroadcast:
		c.InBroadcast++
	case classMulticast:
		c.InMulticast++
	case classUnicast:
		c.InUnicast++
	}
	fabricCounters[f][device][portName] = c
}

func (f *Fabric) countCorruptIngress(device, portName string, inOctets uint64) {
	countersMu.Lock()
	defer countersMu.Unlock()

	c := f.ensureCountersLocked(device, portName)
	c.InOctets += inOctets
	c.InErrors++
	c.InDiscards++
	c.Discards[ReasonBadFrame]++
	fabricCounters[f][device][portName] = c
}

func (f *Fabric) countWholeFrameDrop(device, portName string, reason trace.Reason) {
	countersMu.Lock()
	defer countersMu.Unlock()

	c := f.ensureCountersLocked(device, portName)
	c.InDiscards++
	c.Discards[reason]++
	fabricCounters[f][device][portName] = c
}

func (f *Fabric) countEgress(device, portName string, outOctets uint64, class frameClass) {
	countersMu.Lock()
	defer countersMu.Unlock()

	c := f.ensureCountersLocked(device, portName)
	c.OutOctets += outOctets
	switch class {
	case classBroadcast:
		c.OutBroadcast++
	case classMulticast:
		c.OutMulticast++
	case classUnicast:
		c.OutUnicast++
	}
	fabricCounters[f][device][portName] = c
}

func (f *Fabric) countEgressDrop(device, portName string, reason trace.Reason) {
	countersMu.Lock()
	defer countersMu.Unlock()

	c := f.ensureCountersLocked(device, portName)
	c.OutDiscards++
	c.Discards[reason]++
	fabricCounters[f][device][portName] = c
}

func (f *Fabric) snapshotCounters(device string) map[string]Counters {
	countersMu.Lock()
	defer countersMu.Unlock()

	sw, ok := f.switches[device]
	if !ok {
		return nil
	}

	devs := fabricCounters[f]
	var current map[string]Counters
	if devs != nil {
		current = devs[device]
	}

	ports := sw.Ports().Ports()
	out := make(map[string]Counters, len(ports))
	for _, p := range ports {
		if current != nil {
			if c, exists := current[p.Name]; exists {
				out[p.Name] = c.Clone()
				continue
			}
		}
		out[p.Name] = Counters{
			Discards: make(map[trace.Reason]uint64),
		}
	}

	for name, c := range current {
		if _, exists := out[name]; !exists {
			out[name] = c.Clone()
		}
	}

	return out
}
