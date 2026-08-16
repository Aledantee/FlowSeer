# snmp

FlowSeer's SNMP client library — a long-lived **collection framework**, not a
thin RPC wrapper. The three first-class streaming primitives (`Walker`,
`Watcher`, `TrapStream`) share a common channel-pump substrate and one set of
lifecycle conventions: a constructor spawns the producer, an `iter.Seq2`-shaped
range-over-func is the idiomatic consumer surface, `Close` is idempotent and
terminates the producer within one PDU round-trip, and terminal errors latch
via `Err()`.

The package is **pure types + USM + Walker + Watcher + TrapStream + the
`Session` interface**; it has no I/O entry point and no public `Backend`
abstraction. Backend selection is by *import* — you import a concrete backend
package and call its top-level `Dial` / `ListenTraps`.

```go
sess, err := gosnmp.Dial(ctx, "udp://10.0.0.1:161",
    snmp.WithVersion(snmp.V2c), snmp.WithCommunity("public"))
```

The package's own `doc.go` is the authoritative reference (including the
Adaptive Watch conformance matrix); this README maps the surface.

## Public surface

**Session** (`session.go`) — the I/O interface every backend satisfies:
`Get`, `GetNext`, `GetBulk`, `Walk`, `BulkWalk`, `Set`, `Close`. A `Session`
is concurrency-safe but internally serialized (one wire connection per
session, mutex-guarded) — for parallelism, dial one `Session` per target.

**OID** (`oid.go`) — the typed object identifier: `OID`, `NewOID(subs...)`,
`MustOID(subs...)`, `ParseOID(string)`.

**VarBind** (`varbind.go`) — a **sealed sum type** over every SMIv2 base type
plus the three exception variants. Concrete arms include `Integer32Var`,
`Uinteger32Var`, `OctetStringVar`, `ObjectIDVar`, `Counter32Var`,
`Gauge32Var`, `TimeTicksVar`, `Counter64Var`, `IPAddressVar`, `OpaqueVar`,
`OpaqueFloatVar`, `OpaqueDoubleVar`, `NullVar`, and the exception variants
`NoSuchObjectVar`, `NoSuchInstanceVar`, `EndOfMibViewVar`. `IsException(vb)`
classifies the exception arms.

**Decode helpers** (`decode.go`) — `DecodeInt32`, `DecodeUint32`,
`DecodeUint64`, `DecodeBytes`, `DecodeOID`, `DecodeIP`. Generated MIB
accessors and SMIv2 textual conventions delegate here.

**Walker** (`walker.go`) — a Scanner-shaped subtree-walk consumer. Exposes
both a range-over-func (`Walker.Iter`) and the Scanner triple (`Next` /
`Current` / `Err`); pick one per loop and always check `Err` afterward.

**Watcher** (`watcher.go`, `watch.go`) — `Watcher[Row]`, the long-lived
counterpart to `Walker`: observes a per-(target, table) view over time,
probing a declarative `ChangeIndicator` (`NewPerRowIndicator` /
`NewScalarIndicator`) at an adaptive cadence and emitting typed
`WatchEvent[Row]` values (`ChangeKind` added/modified/removed). Range-over-func
only (`Watcher.Iter`); inspect degraded state via `Watcher.Fallback` and
per-tick errors via `Watcher.LastTickErr`. Tunable through `WatchOption`s
(`WithCadenceBounds`, `WithColumnTier`, `WithProbeWindow`,
`WithForcedWalkInterval`, …).

**TrapStream** (`trap.go`) — received-trap stream with drop-oldest
backpressure and a monotonic `Dropped` counter. Configured via `TrapOption`s
(`WithAllowedSources`, `WithMaxTrapsPerSecond`, `WithUSMTable`, …).

**Options** (`options.go`) — `Option` / `CallOption` functional options:
`WithVersion`, `WithCommunity`, `WithTimeout`, `WithRetries`, `WithMaxOIDs`,
`WithUSM`, `WithMinSecurity`, and the per-call overrides.

## gosnmp backend (`backend/gosnmp/`)

The `github.com/gosnmp/gosnmp`-backed wire implementation, and the **single
point in the FlowSeer tree** that imports gosnmp. All translation between
gosnmp's wire types and `snmp.VarBind` happens at this seam; no gosnmp
identifier escapes upward. Two top-level entry points: `Dial` (constructs an
`snmp.Session`) and `ListenTraps` (binds a trap listener, returns a
`*snmp.TrapStream`). Selection is by import — there is no init-time
registration.

## mibgen codegen tool (`cmd/mibgen/`)

`mibgen` generates Go bindings for SMIv2 MIB modules; each module becomes one
package under `generated/go/mib/<module>/`. The generated code consumes only
`snmp`'s public API (typed scalar Get-accessors, columns via `snmp.NewColumn`,
per-table row structs and Walkers, SMI enum types, an OID→dispatch map, and
`snmp.Decode*` for well-known textual conventions). It is a one-shot CLI and
is exempt from the `as.Service` rule (AGENTS.md R14).

```sh
go run ./common/snmp/cmd/mibgen          # regenerate every MIB package
go tool mibgen -verify                    # load-only; no codegen
go tool mibgen -check                     # exit non-zero on output drift
```

Config defaults to `common/snmp/cmd/mibgen/mibgen.yaml` (search paths, module
list with `depends_on` edges, per-OID type overrides). The emitter lives in
`emit.go` and the per-shape `emit_*.go` companions.

## Integration tests (`integration/`)

End-to-end tests against real SNMP agents, gated by build tags so bare
`go test ./...` runs **zero** integration tests. Run via `task`, not `make`:

| Command | Tier | Substrate |
| --- | --- | --- |
| `task snmp:t1` | t1 | Net-SNMP `snmpd` in Docker — USM auth/priv matrix + forged edge cases. |
| `task snmp:t2` | t2 | Nokia SR Linux via containerlab — dense-row collector flow + real-NOS traps. |
| `task snmp:t3` | t3 | `snmpsim` replay of committed `.snmprec` captures — vendor regression coverage. |
| `SNMP_T4_TARGETS=… task snmp:t4` | t4 | Operator-supplied live device; opt-in, skips cleanly when unset. |

Each tier installs its own build-tag-guarded `TestMain` and owns its
container/lab lifecycle (cleanup runs even on panic); selecting two tier tags
at once is a deliberate compile error. All tiers dial through the
`testenv.Dialer` / `testenv.TrapListener` seam so a future native backend
swaps in with two lines. Prerequisites and per-tier walkthroughs are in
[`integration/README.md`](integration/README.md).

## Conventions

Style and behavioural rules are repo-wide; see
[`docs/conventions/`](../../docs/conventions/) (notably `go-style.md`,
`naming.md`, `errors.md`). The `mibgen`-emitted MIB packages under
`generated/go/mib/` are subject to the full lint set like hand-written code.
