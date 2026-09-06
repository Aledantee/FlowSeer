// Package snmp is FlowSeer's SNMP client library.
//
// # Identity
//
// What this package is: a long-lived SNMP collection framework, not
// just a typed RPC wrapper. The three first-class primitives —
// [Walker], [Watcher], and [TrapStream] — all share a common
// channel-pump substrate (an internal generic pump that owns the
// buffered data channel, stop signal, sendMu close discipline, and
// derived ctx) and a single set of lifecycle conventions:
// constructor spawns producer, [iter.Seq2]-shaped iteration is the
// idiomatic consumer surface, idempotent Close terminates the
// producer within one PDU round-trip, terminal errors latch via
// Err(). Anything added to this collection extends the same shape
// rather than expanding into one-shot RPC sugar.
//
// # Surface
//
// The library exposes a typed [OID], a sealed [VarBind] sum type
// covering every SMIv2 base type plus the three exception variants,
// a typed [PDUError], a [Session] interface, the [Option]
// functional-options surface, a Scanner-shaped [Walker], a
// [TrapStream] for received traps, and a [Watcher][Row] for
// long-lived per-table change streams — plus the two I/O entry
// points [NewSession] and [ListenTraps]. There is no "backend" or
// "driver" abstraction: the SNMP wire implementation lives in this
// package as unexported logic, reached only through [NewSession] /
// [ListenTraps].
//
//	sess, err := snmp.NewSession(ctx, "udp://10.0.0.1:161",
//	    snmp.V2c, snmp.WithCommunity(secret.NewString("public")))
//
// # Wire implementation
//
// The package owns every byte on the wire: a hand-rolled
// SNMP-specific BER codec (ber.go), a structured PDU model (pdu.go)
// that doubles as the trace/fixture representation, and a
// per-session UDP reactor (reactor.go) whose single read-loop
// demultiplexes replies by request-id — delivering injected
// first-class OpenTelemetry traces and metrics, truly cancellable
// I/O, and concurrent in-flight requests per session. It depends
// only on the OpenTelemetry API and noop packages, never the SDK;
// the consuming binary owns SDK setup and passes providers via
// [WithTracerProvider] / [WithMeterProvider]. SNMPv3/USM is
// supported; [NewSession] localizes keys lazily and performs no I/O
// at construction.
//
// Generated MIB bindings produced by the mibgen tool (see
// common/snmp/cmd/mibgen) live under generated/go/mib/<module>/.
// Generated code consumes only this package's public API.
//
// # Concurrency model
//
// A Session is concurrency-safe and supports concurrent in-flight
// requests: the per-session reactor's single read-loop demultiplexes
// replies by request-id, so many operations can proceed at once on
// one Session. For independent polling cadences, dial one Session per
// target.
//
// # Walker iteration
//
// [Walker] exposes both a range-over-function shape via
// [Walker.Iter] and a Scanner-style triple ([Walker.Next] /
// [Walker.Current] / [Walker.Err]). Either form is valid; mixing
// both in a single loop is undefined behavior. Always check
// [Walker.Err] after the loop.
//
// # Generated table walks
//
// Generated Walk methods retrieve only selected columns through [WalkColumns].
// Rows are a sorted union of indexes with selected values; selecting no columns
// performs no I/O. Select an identity column explicitly when a row census matters.
// Numeric index arcs determine order, including composite indexes. Duplicate
// selections are ignored; foreign or unknown columns report [ErrForeignColumn].
//
// WalkWithOptions accepts [TableWalkOptions]. For example, in a caller importing
// generated/go/mib/ifmib:
//
//	w := ifmib.IfTable.WalkWithOptions(ctx, sess,
//	    snmp.TableWalkOptions{MaxRepetitions: 25, MaxColumns: 2},
//	    ifmib.IfDescr, ifmib.IfOperStatus)
//	defer w.Close()
//	for index, row := range w.Iter() {
//	    fmt.Println(index, row.IfDescr, row.Observed(ifmib.IfOperStatus))
//	}
//	if err := w.Err(); err != nil { return err }
//
// Construction is lazy. Iteration is single-use and single-consumer; Close and
// Err are safe concurrently. Breaking iteration stops further requests. Parent
// cancellation remains an error, while consumer stop alone succeeds. A later
// decoder or transport failure preserves rows already delivered; it does not
// imply that the scan completed. Tables changing during retrieval are not atomic
// snapshots. SNMPv1 remains unsupported for generated table walks.
//
// Queues retain at most one batch per selected column, independent of table size.
// Response frames and caller-retained values also consume memory. Watch has a
// separate full-table assembly and persistent snapshot; this bound applies to
// Walk, not Watch.
//
// # Row keys, table descriptors, and OID sets
//
// [DecodeIndex] reads the index suffix a walker yields against the row's
// INDEX shapes ([IndexShape]) and returns typed parts. A malformed suffix
// reports ok=false with zero parts rather than an error, because a decode
// error inside a generated walk ends the whole table; generated rows keep
// a key-valid flag and are still delivered.
//
// [TableDescriptor] names a table by root OID, optional [ChangeIndicator],
// and key type, without its row or walker types, so a collector can hold
// tables from many generated packages in one slice.
// [TableDescriptor.Present] issues one GetNext and reports whether the
// agent holds an instance under the root; an implemented but empty table
// reads as absent.
//
// [OIDSet] is a sorted set with [OIDSet.Longest], the longest-prefix
// lookup a sysObjectID identity table needs. It is a sorted slice with
// binary search, sized for once-per-device lookups.
//
// # Watcher iteration
//
// [Watcher][Row] is the long-lived counterpart to Walker: it
// observes a per-(target, table) view over time, probing a
// declarative [ChangeIndicator] at an adaptive cadence and emitting
// typed [WatchEvent][Row] values whenever a row changes. The shape
// is range-over-func only ([Watcher.Iter]); the Scanner triple
// shape is omitted because Watcher consumers are uniformly
// stream-driven. Always check [Watcher.Err] after the loop; observe
// degraded-but-operational state via [Watcher.Fallback]; surface
// transient per-tick errors via [Watcher.LastTickErr]. The
// Watcher's tick goroutine respects the same Close discipline as
// Walker (idempotent, non-blocking, one-PDU-round-trip exit).
//
// # Trap stream backpressure
//
// [TrapStream] uses drop-oldest backpressure with a monotonic
// [TrapStream.Dropped] counter. Source filtering and rate limiting
// are advisory; deployments needing hard guarantees should configure
// kernel-level filtering.
//
// # Conformance matrix (Adaptive Watch)
//
// The Watcher primitive's behaviors map to public symbols and
// pinning tests as follows.
//
//	lifecycle types               → [Watcher], [NewWatcher]
//	                                TestWatcher_ColdStartEmitsAddedForEveryRow
//	generated Watch entry-point   → emit_watch.go (per-table)
//	                                TestT1_Watch_ColdStartEmitsAddedForEveryInterface
//	byte-equality indicator diff  → indicatorVBEqual (internal)
//	                                TestIndicatorVBEqual_*
//	per-row & scalar shapes       → [NewPerRowIndicator], [NewScalarIndicator]
//	                                TestNewPerRowIndicator_*, TestNewScalarIndicator_*
//	per-column tier visibility    → emit_tier.go (per-package)
//	                                TestClassifyTier_*, TestTierMap_GatedOnIndicator
//	caller tier override          → [WithColumnTier]
//	                                TestWatchConfig_OverrideKeyedByOIDString
//	cold-start full walk          → coldStart (internal)
//	                                TestWatcher_ColdStartEmitsAddedForEveryRow
//	per-row two-phase tick        → perRowTick (internal)
//	                                TestWatcher_PerRow_IndicatorAdvanceTargetedGet
//	                                TestWatcher_PerRow_Chunking*
//	scalar full-walk tick         → scalarTick (internal)
//	                                TestWatcher_Scalar_AdvanceEmitsAddedModifiedRemoved
//	row-level diff + ChangeKind   → [ChangeKind], [WatchEvent]
//	                                TestWatcher_PerRow_IndicatorAdvanceButRowEqual_NoEmit
//	row-removal forced full walk  → [WithForcedWalkInterval]
//	                                TestWatcher_ForcedFullWalkEmitsRemoved
//	transient errors              → [Watcher.LastTickErr]
//	                                TestWatcher_TransientGetErrorSurfaceLastTickErrNotEvents
//	adaptive cadence              → [WithCadenceBounds], [WithCadenceStepPolicy]
//	                                TestUpdateStateInterval_*,
//	                                TestWatcher_AdaptiveCadence_*
//	fallback on exception variant → [Watcher.Fallback], enterFallback (internal)
//	                                TestWatcher_Scalar_*Fallback,
//	                                TestWatcher_FallbackDoesNotTerminate_ErrStaysNil
//	stuck-zero probe window       → [WithProbeWindow]
//	                                TestWatcher_StuckZeroFallbackOnCounterMovement,
//	                                TestWatcher_ProbeWindowDisabledByZero
//	pump-pattern goroutine        → embedded *pump[WatchEvent[Row]]
//	                                TestWatcher_CloseIsIdempotent,
//	                                TestWatcher_NestedWalkerNoLeakAcrossCycles
//	in-process snapshot only      → map[string]snapshotEntry[Row] (internal)
//	                                (no test; structural invariant)
//
// A consumer who wants to know "what does the library do when X
// happens?" — particularly for the fallback path which is the
// operator-visible degradation contract — can reach the answer via
// the matrix above without grepping the implementation.
//
// # Conformance corpus (test completeness & quirk hardening)
//
// Separate from the Adaptive Watch matrix above, the SNMP
// test-completeness & conformance-hardening effort maintains a
// provenance-citing regression corpus covering the encoding, walk, and
// SNMPv3/USM quirks transcribed from a decade of gosnmp/net-snmp lessons.
// The committed coverage map is CONFORMANCE.md, generated from the
// manifest in conformance_corpus_test.go and enforced by
// TestConformanceCorpusIntegrity (always-on) plus TestConformanceCorpusComplete
// (build tag snmp_conformance_complete, run at the merge gate). Each pinned
// quirk cites its source in a `// Covers conformance matrix row: <id>`
// comment on the test that exercises it.
package snmp
