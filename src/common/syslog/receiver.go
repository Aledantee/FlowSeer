package syslog

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"unsafe"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// ListenConfig describes one listener. Empty Framing chooses Auto for TCP and
// OctetCounting for TLS. TLSConfig and objects it references must remain immutable.
// TLS callbacks must honor their handshake context; caller caches must be finite.
type ListenConfig struct {
	Transport Transport
	Address   string
	Framing   Framing
	TLSConfig *tls.Config
}

// ReceiverOptions configures shared admission across every listener. Limits here
// also govern parsing; Parse.Limits must be zero to avoid conflicting budgets.
type ReceiverOptions struct {
	Parse  ParseOptions
	Limits Limits
}

// Endpoint is a successfully bound local address, including its assigned port.
type Endpoint struct {
	Transport Transport
	Address   string
}

type (
	boundListener struct {
		config ListenConfig
		packet net.PacketConn
		stream net.Listener
	}
	receivedFrame struct {
		payload     []byte
		observation Observation
	}
)

// Receiver owns sockets and a finite handoff queue. Next permits one concurrent
// call. Close is idempotent and interrupts reads, handshakes, and queue waiters.
// Returned records remain valid after Close; retained caller data is not budgeted.
type Receiver struct {
	limits      Limits
	parser      *Parser
	listeners   []boundListener
	endpoints   []Endpoint
	admission   *admission
	initial     int
	queue       chan receivedFrame
	handshakes  chan struct{}
	stats       counters
	cancel      context.CancelFunc
	stopped     chan struct{}
	done        chan struct{}
	closed      atomic.Bool
	stopOnce    sync.Once
	wg          sync.WaitGroup
	mu          sync.Mutex
	connections []net.Conn
	terminal    error
	nextMu      sync.Mutex
	pending     *receivedFrame
}

// Listen binds all endpoints or closes everything on failure. Cancellation ends
// the receiver lifetime. TLS/X.509, goroutine stacks, and kernel socket buffers are
// separately bounded by connection counts; MaxBytes covers protocol allocations.
func Listen(ctx context.Context, configs []ListenConfig, options ReceiverOptions) (*Receiver, error) {
	if options.Parse.Limits != (Limits{}) {
		return nil, errs.Msg("set receiver limits on ReceiverOptions")
	}
	l, err := options.Limits.normalized()
	if err != nil {
		return nil, err
	}
	if len(configs) == 0 || len(configs) > l.MaxListeners {
		return nil, ErrLimit
	}
	options.Parse.Limits = l
	parser, err := NewParser(options.Parse)
	if err != nil {
		return nil, err
	}
	// Fixed allowances include queue headers, listener state, socket slots, and
	// parser result/scratch. Payload buffers are charged at their full capacity.
	initial := l.headroom()
	charge := func(count, size int) bool {
		if count > (l.MaxBytes-initial)/size {
			return false
		}
		initial += count * size
		return true
	}
	if !charge(l.MaxFrames, int(unsafe.Sizeof(receivedFrame{}))) || !charge(l.MaxConnections, 32) || !charge(len(configs), 1024) {
		return nil, ErrLimit
	}
	for _, config := range configs {
		if config.Transport == UDP && !charge(1, 65536) {
			return nil, ErrLimit
		}
	}
	minimum := l.MaxPayload
	for _, config := range configs {
		if config.Transport == TCP || config.Transport == TLS {
			minimum += streamAllowance
			break
		}
	}
	if minimum > l.MaxBytes-initial {
		return nil, errs.Wrap(ErrLimit, "syslog budget cannot admit a listener frame")
	}
	admission, err := newAdmission(l, initial)
	if err != nil {
		return nil, err
	}
	life, cancel := context.WithCancel(ctx)
	r := &Receiver{limits: l, parser: parser, admission: admission, initial: initial, queue: make(chan receivedFrame, l.MaxFrames), handshakes: make(chan struct{}, l.MaxHandshakes), cancel: cancel, stopped: make(chan struct{}), done: make(chan struct{}), connections: make([]net.Conn, l.MaxConnections)}
	for _, config := range configs {
		config.Framing = framingDefault(config.Framing, config.Transport)
		if !validFraming(config.Framing) {
			err = ErrFraming
			break
		}
		b := boundListener{config: config}
		lc := net.ListenConfig{}
		switch config.Transport {
		case UDP:
			b.packet, err = lc.ListenPacket(life, "udp", config.Address)
		case TCP:
			b.stream, err = lc.Listen(life, "tcp", config.Address)
		case TLS:
			b.config.TLSConfig, err = tlsConfiguration(config.TLSConfig, true)
			if err == nil {
				b.stream, err = lc.Listen(life, "tcp", config.Address)
			}
		default:
			err = errs.Msg("invalid syslog listen transport")
		}
		if err != nil {
			break
		}
		r.listeners = append(r.listeners, b)
		address := ""
		if b.packet != nil {
			address = b.packet.LocalAddr().String()
		} else {
			address = b.stream.Addr().String()
		}
		r.endpoints = append(r.endpoints, Endpoint{Transport: config.Transport, Address: address})
	}
	if err != nil {
		r.stop(err)
		return nil, errs.Wrap(err, "listen syslog")
	}
	for _, b := range r.listeners {
		r.wg.Add(1)
		if b.packet != nil {
			go func() { defer r.wg.Done(); r.receiveUDP(life, b) }()
		} else {
			go func() { defer r.wg.Done(); r.accept(life, b) }()
		}
	}
	go func() {
		<-life.Done()
		r.stop(nil)
		r.wg.Wait()
		r.nextMu.Lock()
		if r.pending != nil {
			r.admission.release(l.MaxPayload, true)
			r.stats.shutdownDiscarded.Add(1)
			r.pending = nil
		}
		for {
			select {
			case <-r.queue:
				r.admission.release(l.MaxPayload, true)
				r.stats.shutdownDiscarded.Add(1)
			default:
				r.admission.release(r.initial, false)
				r.queue = nil
				r.connections = nil
				r.listeners = nil
				r.parser = nil
				r.nextMu.Unlock()
				close(r.done)
				return
			}
		}
	}()
	return r, nil
}

// Addresses returns an independent list of the bound endpoints.
func (r *Receiver) Addresses() []Endpoint { return append([]Endpoint(nil), r.endpoints...) }

// Next waits for a frame and parses it into owned data. Cancellation before a
// claim leaves the queue intact; cancellation after a claim returns that record.
// Concurrent calls return ErrBusy immediately. Close discards queued frames.
func (r *Receiver) Next(ctx context.Context) (Record, error) {
	if !r.nextMu.TryLock() {
		return Record{}, ErrBusy
	}
	defer r.nextMu.Unlock()
	if r.closed.Load() {
		return Record{}, r.closeError()
	}
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	var frame receivedFrame
	if r.pending != nil {
		frame = *r.pending
		r.pending = nil
	} else {
		select {
		case <-r.stopped:
			return Record{}, r.closeError()
		case <-ctx.Done():
			return Record{}, ctx.Err()
		case frame = <-r.queue:
		}
	}
	// Cancellation can race queue readiness. Keep an unclaimed frame at the head
	// for the next consumer instead of reinserting it behind newer messages.
	if err := ctx.Err(); err != nil {
		r.pending = &frame
		return Record{}, err
	}
	if r.closed.Load() {
		r.pending = &frame
		return Record{}, r.closeError()
	}
	record, err := r.parser.Parse(frame.payload, frame.observation)
	r.admission.release(r.limits.MaxPayload, true)
	if err != nil {
		return Record{}, err
	}
	r.stats.delivered.Add(1)
	if record.Status != Complete {
		r.stats.partial.Add(1)
	}
	return record, nil
}

// Close stops the receiver and waits for library goroutines and reservations.
// Caller TLS callbacks must obey cancellation to meet the shutdown bound.
func (r *Receiver) Close() error { r.stop(nil); <-r.done; return r.closeErrorUnlessNormal() }

func (r *Receiver) closeError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.terminal != nil {
		return r.terminal
	}
	return ErrClosed
}

func (r *Receiver) closeErrorUnlessNormal() error {
	err := r.closeError()
	if errors.Is(err, ErrClosed) {
		return nil
	}
	return err
}

func (r *Receiver) stop(err error) {
	r.stopOnce.Do(func() {
		r.closed.Store(true)
		r.mu.Lock()
		r.terminal = err
		r.mu.Unlock()
		close(r.stopped)
		r.cancel()
		r.admission.stop()
		for _, b := range r.listeners {
			if b.packet != nil {
				_ = b.packet.Close()
			}
			if b.stream != nil {
				_ = b.stream.Close()
			}
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, c := range r.connections {
			if c != nil {
				_ = c.Close()
			}
		}
	})
}

func addrPort(a net.Addr) netip.AddrPort {
	switch v := a.(type) {
	case *net.UDPAddr:
		return v.AddrPort()
	case *net.TCPAddr:
		return v.AddrPort()
	default:
		return netip.AddrPort{}
	}
}
