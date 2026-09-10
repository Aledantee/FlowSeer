package fabric

import (
	"maps"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

// Counters holds what a device port counted during a run, in the shape of the
// interfaces group of RFC 2863: octets and frames by class in and out, errors,
// and discards, with the discards also broken down by the reason the run gave.
// OutErrors stays zero, since no egress fault exists in this phase.
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
	OutErrors    uint64
	InDiscards   uint64
	OutDiscards  uint64
	Discards     map[trace.Reason]uint64
}

// Clone returns an independent copy of c, so a snapshot stops changing when
// the run continues.
func (c Counters) Clone() Counters {
	cp := c
	cp.Discards = make(map[trace.Reason]uint64, len(c.Discards))
	maps.Copy(cp.Discards, c.Discards)

	return cp
}

type frameClass int

const (
	classUnicast frameClass = iota
	classMulticast
	classBroadcast
)

var broadcastMAC = netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}

func classifyMAC(m netaddr.MAC) frameClass {
	if m == broadcastMAC {
		return classBroadcast
	}
	if m.IsGroup() {
		return classMulticast
	}

	return classUnicast
}

// counter returns the live counters of one device port, creating them on the
// first use so a port that never saw a frame costs nothing.
func (f *Fabric) counter(device, portName string) *Counters {
	if f.counters == nil {
		f.counters = make(map[Endpoint]*Counters)
	}
	ep := Endpoint{Node: device, Port: portName}
	c, ok := f.counters[ep]
	if !ok {
		c = &Counters{Discards: make(map[trace.Reason]uint64)}
		f.counters[ep] = c
	}

	return c
}

func (f *Fabric) countIngress(device, portName string, inOctets uint64, class frameClass) {
	c := f.counter(device, portName)
	c.InOctets += inOctets
	switch class {
	case classBroadcast:
		c.InBroadcast++
	case classMulticast:
		c.InMulticast++
	case classUnicast:
		c.InUnicast++
	}
}

func (f *Fabric) countCorruptIngress(device, portName string, inOctets uint64) {
	c := f.counter(device, portName)
	c.InOctets += inOctets
	c.InErrors++
	c.InDiscards++
	c.Discards[ReasonBadFrame]++
}

func (f *Fabric) countWholeFrameDrop(device, portName string, reason trace.Reason) {
	c := f.counter(device, portName)
	c.InDiscards++
	c.Discards[reason]++
}

func (f *Fabric) countEgress(device, portName string, outOctets uint64, class frameClass) {
	c := f.counter(device, portName)
	c.OutOctets += outOctets
	switch class {
	case classBroadcast:
		c.OutBroadcast++
	case classMulticast:
		c.OutMulticast++
	case classUnicast:
		c.OutUnicast++
	}
}

func (f *Fabric) countEgressDrop(device, portName string, reason trace.Reason) {
	c := f.counter(device, portName)
	c.OutDiscards++
	c.Discards[reason]++
}

// snapshotCounters copies every port's counters for one device, with a zero
// row for a port that counted nothing, so a reader sees the whole table.
func (f *Fabric) snapshotCounters(device string) map[string]Counters {
	sw, ok := f.switches[device]
	if !ok {
		return nil
	}

	ports := sw.Ports().Ports()
	out := make(map[string]Counters, len(ports))
	for _, p := range ports {
		if c, exists := f.counters[Endpoint{Node: device, Port: p.Name}]; exists {
			out[p.Name] = c.Clone()
		} else {
			out[p.Name] = Counters{Discards: make(map[trace.Reason]uint64)}
		}
	}

	return out
}
