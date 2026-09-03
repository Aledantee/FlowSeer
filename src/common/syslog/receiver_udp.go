package syslog

import (
	"context"
	"time"
)

func (r *Receiver) receiveUDP(ctx context.Context, b boundListener) {
	scratch := make([]byte, 65536)
	for {
		n, peer, err := b.packet.ReadFrom(scratch)
		at := time.Now()
		if err != nil {
			if ctx.Err() == nil {
				r.stop(err)
			}
			return
		}
		r.stats.received.Add(1)
		if n > r.limits.MaxPayload {
			r.stats.oversized.Add(1)
			continue
		}
		if err := r.admission.acquire(ctx, r.limits.MaxPayload, true, false); err != nil {
			r.stats.udpDropped.Add(1)
			continue
		}
		payload := make([]byte, r.limits.MaxPayload)
		copy(payload, scratch[:n])
		frame := receivedFrame{payload: payload[:n], observation: Observation{ReceivedAt: at, Peer: addrPort(peer), Local: addrPort(b.packet.LocalAddr()), Transport: UDP}}
		select {
		case <-r.stopped:
			r.admission.release(r.limits.MaxPayload, true)
			r.stats.shutdownDiscarded.Add(1)
			return
		case r.queue <- frame:
			r.stats.queued.Add(1)
		default:
			r.admission.release(r.limits.MaxPayload, true)
			r.stats.udpDropped.Add(1)
		}
	}
}
