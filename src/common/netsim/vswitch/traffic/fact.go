package traffic

import (
	"strconv"

	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

const (
	// RuleQueueDrop identifies an egress queue decision that drops a frame for
	// want of stated buffer.
	RuleQueueDrop trace.RuleID = "traffic.queue.drop"

	// ReasonQueueFull identifies a frame dropped because its egress queue has no
	// room in its stated buffer.
	ReasonQueueFull trace.Reason = "queue-full"
)

type mirrorDecisionFact string

func (f mirrorDecisionFact) TypeID() string    { return "traffic.mirror_decision" }
func (f mirrorDecisionFact) Canonical() string { return string(f) }

type policerDecisionFact string

func (f policerDecisionFact) TypeID() string    { return "traffic.policer_decision" }
func (f policerDecisionFact) Canonical() string { return string(f) }

// MirrorDecisionFact returns an immutable snapshot of a mirror output decision.
func MirrorDecisionFact(mirror, portName string, frameOctets int, reason trace.Reason) trace.Fact {
	return mirrorDecisionFact("mirror=" + strconv.Quote(mirror) +
		";port=" + strconv.Quote(portName) +
		";frame_octets=" + strconv.Itoa(frameOctets) +
		";reason=" + strconv.Quote(string(reason)))
}

type queueDropFact string

func (f queueDropFact) TypeID() string    { return "traffic.queue_decision" }
func (f queueDropFact) Canonical() string { return string(f) }

// QueueDropFact returns an immutable snapshot of an egress queue tail drop: the
// queue depth before the frame, the stated buffer, and the frame's encoded octets.
func QueueDropFact(depthOctets, bufferOctets uint64, frameOctets int) trace.Fact {
	return queueDropFact("depth_octets=" + strconv.FormatUint(depthOctets, 10) +
		";buffer_octets=" + strconv.FormatUint(bufferOctets, 10) +
		";frame_octets=" + strconv.Itoa(frameOctets))
}

// PolicerDecisionFact returns an immutable snapshot of an ingress policer decision.
func PolicerDecisionFact(rateBPS uint64, burstOctets, frameOctets int, admitted bool) trace.Fact {
	return policerDecisionFact("rate_bps=" + strconv.FormatUint(rateBPS, 10) +
		";burst_octets=" + strconv.Itoa(burstOctets) +
		";frame_octets=" + strconv.Itoa(frameOctets) +
		";admitted=" + strconv.FormatBool(admitted))
}
