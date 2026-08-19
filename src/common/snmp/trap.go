package snmp

import (
	"context"
	"iter"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// Trap is the decoded form of an SNMPv1 trap, SNMPv2c TRAP2, or SNMPv3
// INFORM/TRAP received by a [*TrapStream]. Source is the apparent UDP
// source address; Community is populated for v1/v2c; EngineID and
// UserName are populated for v3. VarBinds carries the trap payload in
// wire order, including the SNMPv2-MIB sysUpTime.0 and snmpTrapOID.0
// canonical leading bindings.
type Trap struct {
	// Source is the apparent UDP source address of the trap. Backends
	// should normalise IPv4-mapped IPv6 addresses to 4-byte form.
	Source net.IP
	// Community is the SNMPv1/v2c community string. Empty for v3.
	Community string
	// Version is the SNMP protocol version observed on the wire.
	Version Version
	// EngineID is the v3 authoritative engine ID. Empty for v1/v2c.
	EngineID []byte
	// UserName is the v3 USM securityName. Empty for v1/v2c.
	UserName string
	// VarBinds is the trap payload in wire order.
	VarBinds []VarBind
	// Received is the wall-clock time the Backend observed the trap.
	Received time.Time
}

// defaultTrapBuffer is the buffer size [NewTrapStream] uses when the
// caller passes a non-positive bufferSize. The value is small enough
// that a stalled consumer pins only a few KiB of memory while still
// absorbing typical burst sizes.
const defaultTrapBuffer = 256

// ErrTrapStreamNotV3Capable is returned by [TrapStream.RegisterEngine]
// when the running listener was not configured for SNMPv3 (i.e., the
// Backend never installed a v3 engine handler). It is also the error a
// stand-alone TrapStream returns when no Backend is wired in — the
// Scanner-style triple keeps working, but engine registration has no
// listener to attach to.
var ErrTrapStreamNotV3Capable = errs.Msg("TrapStream has no v3 engine handler installed")

// TrapStream is the consumer-facing view of a running trap listener.
// Instances are supplied by the [ListenTraps] function; the trap
// pump owns its own UDP socket
// independent of any [Session].
//
// Two iteration shapes are exposed: a range-over-function
// [TrapStream.Iter], and a Scanner-style triple
// [TrapStream.Next] / [TrapStream.Current] / [TrapStream.Err]. The two
// shapes share state — mixing them in a single loop is documented
// undefined behavior but is safe (one consumer steals from the other).
// The Iter name mirrors [Walker.Iter] for symmetry across the library.
//
// Backpressure is drop-oldest with an observable counter
// ([TrapStream.Dropped]). The counter increments on
// buffered-channel overflow, Backend-side rate-limit rejection, and
// Backend-side decode/translation failures (a packet the listener
// received but could not turn into a [Trap]). Source-filter rejects
// do NOT count, since they never enter the stream's pipeline.
//
// v3 trap reception is treated as a smoke-test bar, not a hard
// stability guarantee; the public TrapStream contract is unaffected if
// a future plan demotes v3 trap reception.
//
// The zero value is not usable; construct via [NewTrapStream] or via
// the [ListenTraps] function.
//
// # Synchronization model
//
// TrapStream embeds a generic [*pump] of [Trap] which owns the shared
// channel-pump state machine (data channel, stop signal, idempotent
// close guards, send/close RWMutex, derived context, and first-
// terminal-error latch). TrapStream-specific state — the atomic drop
// counter, the v3 engine handler hook, the Backend closer hook, and
// the "current" latch for the Scanner shape — lives on TrapStream
// itself.
type TrapStream struct {
	*pump[Trap]

	// dropped is the monotonic count of traps lost to buffer overflow or
	// rate-limiting. Loaded with acquire semantics by [TrapStream.Dropped].
	dropped atomic.Uint64

	// currentMu guards current. Distinct from the embedded pump's
	// terminal-error mutex so a Current/Next call cannot block on a
	// concurrent Fail/Err read.
	currentMu sync.Mutex
	current   Trap // latched by Next for Current

	// engineHandler is installed by the Backend (via
	// installEngineHandler) and delegates
	// [TrapStream.RegisterEngine] to the running listener's USM table.
	// A nil handler means the stream is not v3-capable; the call returns
	// [ErrTrapStreamNotV3Capable].
	engineMu      sync.Mutex
	engineHandler func(USMConfig) error

	// closer is an optional Backend-side hook invoked synchronously by
	// [TrapStream.Close] after the stream's own resources are released.
	// Backends install a closer (via installCloser) so
	// Close returns only after the underlying socket and pump goroutine
	// are fully torn down — making the next "re-bind the same port"
	// safe.
	closerMu sync.Mutex
	closer   func()
}

// NewTrapStream constructs a TrapStream tied to ctx with the given
// buffer size. bufferSize <= 0 defaults to [defaultTrapBuffer]. The
// returned stream is empty; Backends drive it via [TrapStream.Push],
// fail, and done.
//
// The returned TrapStream derives a cancellable child context from ctx;
// the caller's ctx is not stored long-term and may be released after
// NewTrapStream returns.
func NewTrapStream(ctx context.Context, bufferSize int) *TrapStream {
	if bufferSize <= 0 {
		bufferSize = defaultTrapBuffer
	}
	ts := &TrapStream{
		pump: newPump[Trap](ctx, bufferSize),
	}
	// Spawn a small watcher goroutine so a ctx cancel triggers Fail
	// with ctx.Err() — matching the Walker contract that a canceled
	// context surfaces as the terminal error.
	go ts.watchCtx()
	return ts
}

// watchCtx watches the derived context and, on cancellation, surfaces
// ctx.Err() as the stream's terminal error. The goroutine exits as soon
// as either stop or ctx.Done() fires.
func (ts *TrapStream) watchCtx() {
	select {
	case <-ts.ctx.Done():
		// Only record context errors as terminal; a clean Close /
		// Done also cancels the context but those code paths beat
		// us to closing stop first.
		select {
		case <-ts.stop:
			return
		default:
		}
		ts.fail(ts.ctx.Err())
	case <-ts.stop:
		return
	}
}

// Push delivers one trap to the consumer. On a full buffer the oldest
// trap is dropped, the [TrapStream.Dropped] counter is incremented, and
// the new trap is sent. The drop-oldest mechanism uses a double-select
// to tolerate a race where the consumer drains between the read and the
// retry; a value that loses the retry is itself counted as a drop.
//
// Push is the trap-injection entry point: a trap source feeds received
// traps in, and tests use it to drive a [NewTrapStream] synthetically. It
// is non-blocking with respect to the consumer and safe to call
// concurrently with [TrapStream.Close]. If the stream is already
// terminating, Push silently drops the value without incrementing the
// drop counter — a clean shutdown is not a loss event.
func (ts *TrapStream) Push(t Trap) {
	_, dropped := ts.trySendDropOldest(t)
	if dropped > 0 {
		ts.dropped.Add(uint64(dropped))
	}
}

// installEngineHandler wires up v3 engine registration: the listener
// passes a closure that adds (or replaces) the supplied USMConfig in its
// internal trap-listener USM table. [TrapStream.RegisterEngine] delegates
// here. A nil handler leaves the stream non-v3-capable, the correct state
// for v1/v2c-only listeners.
func (ts *TrapStream) installEngineHandler(fn func(USMConfig) error) {
	ts.engineMu.Lock()
	ts.engineHandler = fn
	ts.engineMu.Unlock()
}

// stopped returns a channel closed when the stream is terminating,
// mirroring [context.Context.Done]. The listener selects on it to know
// when to release its resources.
func (ts *TrapStream) stopped() <-chan struct{} {
	return ts.stop
}

// installCloser registers a listener-side teardown callback that
// [TrapStream.Close] invokes synchronously after the stream's own
// resources are released. The callback should block until the underlying
// socket and pump goroutine have exited, so a caller observing Close's
// return can immediately re-bind the same port. Invoked at most once.
func (ts *TrapStream) installCloser(fn func()) {
	ts.closerMu.Lock()
	ts.closer = fn
	ts.closerMu.Unlock()
}

// recordDropped counts one rejected trap without sending a value
// through the channel. Used when a trap is rejected independently of
// buffer overflow (e.g. a per-second rate cap). Buffer-overflow drops are
// counted internally by push; callers should not invoke recordDropped for
// that case.
func (ts *TrapStream) recordDropped() {
	ts.dropped.Add(1)
}

// Iter returns a range-over-function form of the stream. Iterating
// yields each [Trap] in receive order until the stream terminates;
// callers should consult [TrapStream.Err] after the loop ends to
// distinguish clean shutdown from a terminal error.
//
// Breaking out of the loop signals the pump to terminate; Iter drains
// any buffered values so the pump goroutine does not block on a full
// channel. Mixing Iter with [TrapStream.Next] / [TrapStream.Current]
// in a single loop is undefined behavior but is safe at runtime.
//
// The name mirrors [Walker.Iter] for symmetry across the library.
func (ts *TrapStream) Iter() iter.Seq[Trap] {
	return func(yield func(Trap) bool) {
		for t := range ts.ch {
			if !yield(t) {
				ts.signalStop()
				for range ts.ch {
				}
				return
			}
		}
	}
}

// Next advances the Scanner-shape state machine to the next trap.
// Returns false when the stream is closed or its context has been
// canceled. Callers should check [TrapStream.Err] after Next returns
// false.
func (ts *TrapStream) Next() bool {
	t, ok := ts.recv()
	if ok {
		ts.currentMu.Lock()
		ts.current = t
		ts.currentMu.Unlock()
	}
	return ok
}

// Current returns the trap most recently produced by [TrapStream.Next].
// Calling Current before Next has returned true at least once returns
// the zero [Trap].
func (ts *TrapStream) Current() Trap {
	ts.currentMu.Lock()
	defer ts.currentMu.Unlock()
	return ts.current
}

// Dropped returns the monotonic count of traps lost to one of:
//
//   - buffered-channel overflow (drop-oldest from [TrapStream.Push]);
//   - Backend-side rate-limit rejection (e.g. [WithMaxTrapsPerSecond]);
//   - Backend-side decode or translation failure (a wire packet the
//     listener received but could not turn into a [Trap]).
//
// Source-filter rejects are NOT counted — they never enter the
// stream's pipeline. The counter is an [atomic.Uint64] loaded with
// acquire semantics and never decreases; it is suitable for tight-loop
// polling without surprising memory-ordering effects.
func (ts *TrapStream) Dropped() uint64 {
	return ts.dropped.Load()
}

// RegisterEngine installs a [USMConfig] entry in the running
// listener's v3 security table. Required for dynamic device discovery
// where the engine identity is not known when the stream is opened.
//
// Duplicate semantics are replace-on-write so a passphrase rotation
// cleanly retires the old material. The duplicate key is the composite
// (EngineID, userName): re-registering the same EngineID with a
// *different* userName coexists (a multi-user authoritative engine),
// while re-registering the same (EngineID, userName) replaces the
// credential. The learned per-engine replay baseline (snmpEngineBoots /
// snmpEngineTime) persists across a credential replace, so a rotation
// does not reset RFC 3414 §3.2 replay protection.
//
// Either way the rotation is atomic from the trap-decode path's
// perspective.
//
// If the stream is not v3-capable (no Backend handler installed),
// RegisterEngine returns [ErrTrapStreamNotV3Capable] without modifying
// any state.
func (ts *TrapStream) RegisterEngine(cfg USMConfig) error {
	ts.engineMu.Lock()
	fn := ts.engineHandler
	ts.engineMu.Unlock()
	if fn == nil {
		return ErrTrapStreamNotV3Capable
	}
	return fn(cfg)
}

// Close releases the listener's resources and stops the pump. Close is
// idempotent. It cancels the stream's derived context so any pump
// goroutine observing ctx.Done() exits promptly, and synchronously
// invokes the Backend-installed closer (if any) so socket teardown is
// complete on return.
//
// Close-semantics asymmetry with [Walker.Close]: TrapStream.Close
// closes the data channel immediately, so any traps still buffered in
// the channel become unobservable to a consumer that has not yet
// iterated past them. [Walker.Close], by contrast, only signals the
// pump to stop and leaves buffered items available for the consumer to
// drain. The asymmetry reflects the different semantics of the two
// pipelines: a trap stream is realtime — a trap the caller decided to
// stop listening for is no longer useful — whereas a walk is a
// request-response operation whose buffered results the caller may
// still want.
func (ts *TrapStream) Close() error {
	// done() = signalStop + closeData; both are idempotent and safe
	// to interleave with cancel().
	ts.done()
	ts.cancel()
	// Pull the closer atomically so we invoke it at most once even
	// under concurrent Close calls.
	ts.closerMu.Lock()
	fn := ts.closer
	ts.closer = nil
	ts.closerMu.Unlock()
	if fn != nil {
		fn()
	}
	return nil
}

// TrapConfig is the exported accumulator for [TrapOption] values. It is
// populated by [ListenTraps] before the trap pump
// starts. The fields are unexported; Backends read configured values
// through the accessor methods ([TrapConfig.AllowedSources],
// [TrapConfig.MaxTrapsPerSecond], [TrapConfig.USMTable],
// [TrapConfig.BufferSize]).
type TrapConfig struct {
	// allowedSources, when non-empty, restricts accepted traps to
	// packets whose source IP falls inside one of the supplied
	// networks. An empty slice accepts every source.
	allowedSources []net.IPNet
	// maxTrapsPerSecond is an advisory rate limit; the Backend may
	// implement it via token bucket or similar. Zero means no limit.
	maxTrapsPerSecond int
	// usmTable seeds the v3 USM security table at listener start.
	// Additional entries can be added later via
	// [TrapStream.RegisterEngine].
	usmTable []USMConfig
	// ownEngineID is the receiver's authoritative engineID for the v3
	// authoritative role (inform reception / discovery responder). Empty
	// means the Backend derives a local one.
	ownEngineID []byte
	// bufferSize is the size of the drop-oldest channel between the
	// pump and the consumer. Zero means "use the Backend default".
	bufferSize int
}

// TrapOption configures a [*TrapStream] at [ListenTraps] time.
type TrapOption func(*TrapConfig)

// WithAllowedSources restricts the stream to traps whose source IP
// falls inside one of the supplied networks. The default (no option) is
// to accept every source.
func WithAllowedSources(nets ...net.IPNet) TrapOption {
	return func(c *TrapConfig) {
		c.allowedSources = append(c.allowedSources, nets...)
	}
}

// WithMaxTrapsPerSecond sets an advisory rate limit. The listener
// implements the cap as a token bucket; over-rate traps count
// against [TrapStream.Dropped]. Zero or negative disables the limit.
//
// Rate-limit precision: the listener uses a one-second-window
// token bucket whose window resets on the first allow() after the
// prior window expires. A bursting peer can therefore observe up to
// 2*n admissions across a one-second boundary (n at the tail of one
// window plus n at the head of the next) before the next window's
// remaining tokens cut over. This is by design — the option is
// documented as advisory and a strict sliding-window cap would add
// per-call list maintenance without a clear FlowSeer use case.
func WithMaxTrapsPerSecond(n int) TrapOption {
	return func(c *TrapConfig) { c.maxTrapsPerSecond = n }
}

// WithUSMTable seeds the v3 USM security table with the supplied
// entries. Additional entries can be added later via
// [TrapStream.RegisterEngine].
func WithUSMTable(entries []USMConfig) TrapOption {
	return func(c *TrapConfig) {
		c.usmTable = append(c.usmTable, entries...)
	}
}

// WithOwnEngineID sets the receiver's authoritative engineID for the
// SNMPv3 authoritative role (inform reception and the discovery
// responder). v3 inform senders localize their keys to this value, so it
// MUST be durable across restarts (a configured value or a persisted
// random engineID) — an engineID that changes on restart silently
// invalidates every registered inform sender's keys until they
// re-discover. When unset, the Backend derives a local engineID (a
// dev/single-host fallback; configuration is the recommended production
// path). The value is treated as raw octets and must be 5..32 octets per
// RFC 3411.
func WithOwnEngineID(engineID []byte) TrapOption {
	return func(c *TrapConfig) {
		c.ownEngineID = append([]byte(nil), engineID...)
	}
}

// WithTrapBufferSize sets the size of the drop-oldest channel between
// the trap pump and the consumer. Zero defers to the Backend default
// (currently 256).
func WithTrapBufferSize(n int) TrapOption {
	return func(c *TrapConfig) { c.bufferSize = n }
}

// ApplyTrapOptions returns a [TrapConfig] populated by applying the
// supplied options in order. Backends in subpackages call this helper
// rather than touching TrapConfig fields directly; the accessor
// methods on TrapConfig are the supported read surface.
func ApplyTrapOptions(opts ...TrapOption) *TrapConfig {
	cfg := &TrapConfig{}
	for _, o := range opts {
		if o != nil {
			o(cfg)
		}
	}
	return cfg
}

// AllowedSources returns the configured allow-list. Used by Backends.
func (c *TrapConfig) AllowedSources() []net.IPNet { return c.allowedSources }

// MaxTrapsPerSecond returns the configured rate limit. Used by Backends.
func (c *TrapConfig) MaxTrapsPerSecond() int { return c.maxTrapsPerSecond }

// USMTable returns the seeded v3 USM entries. Used by Backends.
func (c *TrapConfig) USMTable() []USMConfig { return c.usmTable }

// OwnEngineID returns the configured authoritative engineID, or nil if the
// Backend should derive one. Used by Backends.
func (c *TrapConfig) OwnEngineID() []byte { return c.ownEngineID }

// BufferSize returns the configured drop-oldest buffer size, or 0 if
// unset (meaning "Backend default"). Used by Backends.
func (c *TrapConfig) BufferSize() int { return c.bufferSize }
