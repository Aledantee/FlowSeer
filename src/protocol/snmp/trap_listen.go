package snmp

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/service"
)

// trap_listen.go is the v1/v2c/v3 trap listener. It owns the UDP
// socket, decodes each datagram with the package BER codec, drives the
// public [TrapStream], and translates an SNMPv1 Trap-PDU into the
// canonical SNMPv2 varbind form (RFC 2576 §3.1) so consumers see the
// same sysUpTime.0 / snmpTrapOID.0-led shape regardless of wire version.

// defaultTrapPort is the well-known UDP port for SNMP trap reception.
const defaultTrapPort uint16 = 162

// trapBufSize is the listener receive buffer; SNMP messages are capped at
// 65535 octets, so a smaller buffer would silently truncate a large trap.
const trapBufSize = 65535

// RFC 2576 §3.1 canonical OIDs used to translate a v1 Trap-PDU into the
// v2 notification varbind form.
var (
	oidSysUpTime          = MustOID(1, 3, 6, 1, 2, 1, 1, 3, 0)
	oidSnmpTrapOID        = MustOID(1, 3, 6, 1, 6, 3, 1, 1, 4, 1, 0)
	oidSnmpTrapEnterprise = MustOID(1, 3, 6, 1, 6, 3, 1, 1, 4, 3, 0)
	oidGenericTrapPrefix  = MustOID(1, 3, 6, 1, 6, 3, 1, 1, 5)
)

// listenerRegistered is a package-level hook test code may set to capture
// the (TrapStream, listener) pair so a test can read the listener's bound
// UDP port. Production code leaves it nil.
var listenerRegistered func(*TrapStream, *listener)

// ListenTraps binds a native trap listener at addr, wires it to a
// [*TrapStream], and starts the listener goroutine. The returned
// stream owns the UDP socket; [TrapStream.Close] (or a context
// cancellation) releases it.
//
// addr accepts the same shapes as [NewSession]'s target plus an empty string
// (":162") and a bare ":port"; a non-UDP scheme is rejected and a port of
// 0 selects a kernel-assigned port (useful in tests).
//
// SNMPv3 USM notifications are supported: non-authoritative trap reception
// and authoritative inform reception (with Response ack and engine-discovery
// responder). Register sender credentials at listen time via
// [WithUSMTable] or dynamically via [TrapStream.RegisterEngine]
// (keyed by the composite (engineID, userName)); set the receiver's
// authoritative engineID for the inform role via [WithOwnEngineID]. A
// v3 datagram that fails to decode, verify, or match a registered engine is
// dropped and counted via [TrapStream.Dropped].
func ListenTraps(ctx context.Context, addr string, opts ...TrapOption) (ts *TrapStream, err error) {
	cfg := ApplyTrapOptions(opts...)

	bindAddr, err := normalizeTrapAddr(addr)
	if err != nil {
		return nil, err
	}
	udpAddr, err := net.ResolveUDPAddr("udp", bindAddr)
	if err != nil {
		return nil, errs.Wrapf(err, "resolve %q", bindAddr)
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, errs.Wrapf(err, "bind %q", bindAddr)
	}

	ts = NewTrapStream(ctx, cfg.BufferSize())
	defer func() {
		if err != nil {
			_ = udpConn.Close()
			if ts != nil {
				_ = ts.Close()
			}
			ts = nil
		}
	}()

	ownEngineID, err := resolveOwnEngineID(cfg.OwnEngineID())
	if err != nil {
		return nil, err
	}

	l := &listener{
		ts:          ts,
		conn:        udpConn,
		matcher:     newNetMatcher(cfg.AllowedSources()),
		limiter:     newTokenBucket(cfg.MaxTrapsPerSecond()),
		engines:     newEngineTable(ctx),
		ownEngineID: ownEngineID,
		logCtx:      context.WithoutCancel(ctx),
		loopDone:    make(chan struct{}),
	}
	l.startedNanos.Store(time.Now().UnixNano())

	// Seed the v3 USM table from the ListenTraps-time entries, then wire
	// RegisterEngine to the live table so dynamically-discovered engines can
	// be added later. Replace-on-duplicate is at the composite
	// (engineID, userName) granularity.
	if err := l.engines.seed(cfg.USMTable()); err != nil {
		return nil, err
	}
	ts.installEngineHandler(l.engines.register)
	ts.installCloser(func() {
		l.close()
		<-l.loopDone
	})

	l.start()
	if listenerRegistered != nil {
		listenerRegistered(ts, l)
	}
	return ts, nil
}

// listener owns the UDP socket and bridges inbound traps onto the
// TrapStream.
type listener struct {
	ts   *TrapStream
	conn *net.UDPConn

	matcher netMatcher
	limiter *tokenBucket
	engines *engineTable

	// ownEngineID is the receiver's authoritative engineID: the dual-role
	// routing discriminator (a datagram whose msgAuthoritativeEngineID equals
	// it takes the authoritative inform/discovery branch) and the engineID
	// the discovery responder advertises.
	ownEngineID []byte
	// startedNanos is the listener-start reference (UnixNano) for the
	// authoritative snmpEngineTime — a single monotonic base read lock-free
	// via atomics.
	startedNanos atomic.Int64

	logCtx context.Context

	closeOnce sync.Once
	loopDone  chan struct{}
}

func (l *listener) start() {
	go l.listenLoop()
	go l.watchStop()
}

// watchStop tears the listener down when the TrapStream terminates (Close
// or context cancellation).
func (l *listener) watchStop() {
	<-l.ts.stopped()
	l.close()
}

// listenLoop reads each datagram and hands it to handlePacket. It exits
// when the socket is closed (the orchestrated shutdown path) or on any
// other socket error.
func (l *listener) listenLoop() {
	defer close(l.loopDone)
	defer l.ts.pump.Done()

	buf := make([]byte, trapBufSize)
	for {
		n, remote, err := l.conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-l.ts.stopped():
				return // clean shutdown
			default:
			}
			l.ts.pump.Fail(errs.Wrap(err, "trap listener"))
			return
		}
		l.handlePacket(buf[:n], remote)
	}
}

// handlePacket applies the source filter and rate limit, decodes the
// datagram, and pushes the translated trap. A disallowed source is
// dropped without counting (it never entered the pipeline); a rate-limit
// rejection, a decode failure (including a v3 datagram, S7), and a
// translation failure are each counted via Dropped().
func (l *listener) handlePacket(msg []byte, remote *net.UDPAddr) {
	if remote != nil && !l.matcher.allow(remote.IP) {
		return
	}
	if !l.limiter.allow() {
		l.ts.recordDropped()
		return
	}

	v1v2, dec, err := decodeAnyMessage(msg)
	if err != nil {
		// Malformed wire (v1/v2c or v3): count and debug-log, but keep the
		// listener running. A packet that cannot be decoded is exactly the
		// "could not turn into a Trap" drop case.
		l.ts.recordDropped()
		service.Logger(l.logCtx).DebugContext(l.logCtx,
			"dropping undecodable trap",
			slog.String(string(semconv.ErrorTypeKey), classifyError(err)))
		return
	}
	if dec != nil {
		l.handleV3Notification(dec, remote)
		return
	}
	// Surface tolerated decode warnings (e.g. Counter64 in a v1 trap, RFC 2576
	// §3) on the trap path too, mirroring the reactor read-loop — the value is
	// preserved and pushed, but the spec breach is logged, not silently swallowed
	// (enc-counter64-v1).
	for _, w := range v1v2.warnings {
		service.Logger(l.logCtx).WarnContext(l.logCtx,
			"tolerated decode warning",
			slog.String(string(semconv.ErrorTypeKey), classifyError(w)))
	}
	l.ts.Push(translateTrap(v1v2, remote))
}

// close shuts the UDP socket. Idempotent.
func (l *listener) close() {
	l.closeOnce.Do(func() { _ = l.conn.Close() })
}

// translateTrap converts a decoded v1/v2c trap message into an
// [Trap]. A v1 Trap-PDU's fixed fields are mapped to the RFC 2576
// canonical varbinds; a v2c trap's varbinds are already canonical. It
// cannot fail: the values were already decoded and typed by decodeMessage.
func translateTrap(m *message, remote *net.UDPAddr) Trap {
	t := Trap{
		Received:  time.Now(),
		Version:   m.version,
		Community: m.community,
	}
	if remote != nil {
		if v4 := remote.IP.To4(); v4 != nil {
			t.Source = v4
		} else {
			t.Source = remote.IP
		}
	}
	if m.pdu.trapV1 != nil {
		t.VarBinds = v1TrapVarBinds(m.pdu.trapV1, m.pdu.varbinds)
	} else {
		t.VarBinds = m.pdu.varbinds
	}
	return t
}

// v1TrapVarBinds maps an SNMPv1 Trap-PDU's fixed fields and payload to the
// canonical SNMPv2 notification varbind list (RFC 2576 §3.1):
// sysUpTime.0, snmpTrapOID.0, the original varbinds, then
// snmpTrapEnterprise.0.
func v1TrapVarBinds(tr *trapV1Fields, payload []VarBind) []VarBind {
	out := make([]VarBind, 0, len(payload)+3)
	out = append(out, TimeTicksVar{
		Header: Header{OID: oidSysUpTime, Kind: KindTimeTicks},
		Value:  tr.timestamp,
	})

	var trapOID OID
	if tr.generic >= 0 && tr.generic <= 5 {
		// Generic traps map to snmpTraps.(generic+1).
		trapOID = oidGenericTrapPrefix.Append(uint32(tr.generic) + 1)
	} else {
		// enterpriseSpecific (6): enterprise.0.specific.
		trapOID = tr.enterprise.Append(0, uint32(tr.specific))
	}
	out = append(out, ObjectIDVar{
		Header: Header{OID: oidSnmpTrapOID, Kind: KindObjectID},
		Value:  trapOID,
	})

	out = append(out, payload...)

	out = append(out, ObjectIDVar{
		Header: Header{OID: oidSnmpTrapEnterprise, Kind: KindObjectID},
		Value:  tr.enterprise,
	})
	return out
}

// normalizeTrapAddr maps the caller-supplied addr onto a "host:port"
// suitable for net.ResolveUDPAddr, mirroring gosnmp's accepted
// shapes. A non-UDP scheme is rejected; port "0" selects a kernel-assigned
// port.
func normalizeTrapAddr(addr string) (string, error) {
	if addr == "" {
		return fmt.Sprintf(":%d", defaultTrapPort), nil
	}
	body := addr
	if i := strings.Index(body, "://"); i >= 0 {
		scheme := body[:i]
		if scheme != "udp" {
			return "", errs.New().Attr("addr", addr).Msg("only udp:// scheme is supported for trap addr")
		}
		body = body[i+3:]
	}
	if len(body) > 0 && body[0] == ':' {
		return body, nil
	}
	host, port, err := net.SplitHostPort(body)
	if err != nil {
		//nolint:nilerr // SplitHostPort's "missing port" error is the documented signal that body is a bare host; we recover by appending the default trap port.
		return net.JoinHostPort(body, fmt.Sprintf("%d", defaultTrapPort)), nil
	}
	if port == "" {
		return net.JoinHostPort(host, fmt.Sprintf("%d", defaultTrapPort)), nil
	}
	return body, nil
}

// netMatcher is an allow-list of networks. An empty matcher admits every
// source.
//
// Empty-allow-list default: like gosnmp, an
// unset [WithAllowedSources] admits traps from any source. Production
// deployments SHOULD restrict sources via the trap option; an open trap
// receiver will accept spoofed or unintended traps.
type netMatcher []net.IPNet

func newNetMatcher(nets []net.IPNet) netMatcher {
	if len(nets) == 0 {
		return nil
	}
	out := make(netMatcher, len(nets))
	copy(out, nets)
	return out
}

func (m netMatcher) allow(ip net.IP) bool {
	if len(m) == 0 {
		return true
	}
	for i := range m {
		if m[i].Contains(ip) {
			return true
		}
	}
	return false
}

// tokenBucket is a coarse one-second-window rate limiter. A nil receiver
// (no limit configured) admits every call.
type tokenBucket struct {
	mu          sync.Mutex
	perSecond   int
	count       int
	windowStart time.Time
}

func newTokenBucket(perSecond int) *tokenBucket {
	if perSecond <= 0 {
		return nil
	}
	return &tokenBucket{perSecond: perSecond}
}

func (b *tokenBucket) allow() bool {
	if b == nil || b.perSecond <= 0 {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	if b.windowStart.IsZero() || now.Sub(b.windowStart) >= time.Second {
		b.windowStart = now
		b.count = 0
	}
	if b.count >= b.perSecond {
		return false
	}
	b.count++
	return true
}
