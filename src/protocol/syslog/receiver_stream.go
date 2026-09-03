package syslog

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"time"
)

const streamAllowance = 8192

func (r *Receiver) accept(ctx context.Context, b boundListener) {
	for {
		raw, err := b.stream.Accept()
		if err != nil {
			if ctx.Err() == nil {
				r.stop(err)
			}
			return
		}
		slot := -1
		r.mu.Lock()
		if !r.closed.Load() {
			for i, c := range r.connections {
				if c == nil {
					slot = i
					r.connections[i] = raw
					break
				}
			}
		}
		r.mu.Unlock()
		if slot < 0 {
			r.stats.connectionRejected.Add(1)
			_ = raw.Close()
			continue
		}
		if err := r.admission.acquire(ctx, streamAllowance, false, false); err != nil {
			r.releaseConnection(slot, raw)
			r.stats.connectionRejected.Add(1)
			continue
		}
		r.stats.connections.Add(1)
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			defer r.admission.release(streamAllowance, false)
			defer r.stats.connections.Add(-1)
			defer r.releaseConnection(slot, raw)
			r.receiveStream(ctx, b, raw)
		}()
	}
}

func (r *Receiver) releaseConnection(slot int, raw net.Conn) {
	_ = raw.Close()
	r.mu.Lock()
	r.connections[slot] = nil
	r.mu.Unlock()
}

func (r *Receiver) receiveStream(ctx context.Context, b boundListener, raw net.Conn) {
	conn := raw
	authenticated := false
	if b.config.Transport == TLS {
		select {
		case r.handshakes <- struct{}{}:
		default:
			r.stats.connectionRejected.Add(1)
			return
		}
		r.stats.handshakes.Add(1)
		secure := tls.Server(raw, b.config.TLSConfig)
		handshake, cancel := context.WithTimeout(ctx, r.limits.HandshakeTimeout)
		// A raw deadline also interrupts TLS alert writes after a rejected handshake.
		err := raw.SetDeadline(time.Now().Add(r.limits.HandshakeTimeout))
		if err == nil {
			err = secure.HandshakeContext(handshake)
		}
		cancel()
		<-r.handshakes
		r.stats.handshakes.Add(-1)
		if err != nil {
			r.stats.handshakeErrors.Add(1)
			return
		}
		if err := raw.SetDeadline(time.Time{}); err != nil {
			return
		}
		authenticated = len(secure.ConnectionState().VerifiedChains) > 0
		conn = secure
	}
	// TLS reads can write control records, which must obey the same deadline.
	reader := streamReader{reader: conn, buffer: make([]byte, 4096), deadline: raw.SetDeadline, idle: r.limits.IdleTimeout, frame: r.limits.FrameTimeout}
	for {
		if err := reader.beginFrame(); err != nil {
			if ctx.Err() == nil && !errors.Is(err, io.EOF) {
				r.stats.framingErrors.Add(1)
			}
			return
		}
		deadline := time.Now().Add(r.limits.PressureTimeout)
		if reader.frameDeadline.Before(deadline) {
			deadline = reader.frameDeadline
		}
		pressure, cancel := context.WithDeadline(ctx, deadline)
		err := r.admission.acquire(pressure, r.limits.MaxPayload, true, true)
		cancel()
		if err != nil {
			if ctx.Err() == nil {
				r.stats.pressureClosed.Add(1)
			}
			return
		}
		storage := make([]byte, r.limits.MaxPayload)
		payload, at, err := readFrame(&reader, b.config.Framing, storage)
		if err != nil {
			r.admission.release(r.limits.MaxPayload, true)
			if ctx.Err() == nil && !errors.Is(err, io.EOF) {
				if errors.Is(err, ErrLimit) {
					r.stats.oversized.Add(1)
				} else {
					r.stats.framingErrors.Add(1)
				}
			}
			return
		}
		r.stats.received.Add(1)
		frame := receivedFrame{payload: payload, observation: Observation{ReceivedAt: at, Peer: addrPort(raw.RemoteAddr()), Local: addrPort(raw.LocalAddr()), Transport: b.config.Transport, Authenticated: authenticated}}
		timer := time.NewTimer(r.limits.PressureTimeout)
		select {
		case r.queue <- frame:
			r.stats.queued.Add(1)
			timer.Stop()
		case <-ctx.Done():
			timer.Stop()
			r.admission.release(r.limits.MaxPayload, true)
			r.stats.shutdownDiscarded.Add(1)
			return
		case <-timer.C:
			r.admission.release(r.limits.MaxPayload, true)
			if ctx.Err() == nil {
				r.stats.pressureClosed.Add(1)
			} else {
				r.stats.shutdownDiscarded.Add(1)
			}
			return
		}
	}
}
