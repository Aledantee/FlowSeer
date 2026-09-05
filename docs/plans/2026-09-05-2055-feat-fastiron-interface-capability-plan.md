---
title: FastIron Interface Capability - Plan
type: feat
date: 2026-09-05
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: code
---

# FastIron Interface Capability - Plan

## Goal

Give `src/modules/localnet` the interface capability for one device family — a
Ruckus ICX switch on FastIron 10.0.10g — end to end: read one interface's
description, admin status, and oper status over SNMPv3 through `collect` and
`snmpmap`, read and mutate the same fields over SSH through a new typed
FastIron adapter on `src/protocol/ssh`, verify a mutation through a fresh
independent read, and map every result to
`flowseer.device.access.v1.InterfaceObservation` with a
`flowseer.api.inventory.v1.Provenance`. The means: a protocol-agnostic
capability handler under `src/modules/localnet/access/internal/capability/`
that a route selector drives with typed `Completeness`, freshness, and
delayed-effect values, backed by an SNMP read (`interfaces/snmp.go`) and a
FastIron shell adapter (`fastiron/`) that speaks only typed Go, never a
generic shell DSL, per
[decision 11](../architecture/2026-09-05-verified-device-access-direction.md).
Stop condition: if the SNMP agent on a real ICX7150 does not report `ifName`
in the CLI's own port spelling (`ethernet 1/1/1`), the identity match between
an SNMP-sourced and an SSH-sourced observation in Requirement 3 does not hold
and the join needs a device-side mapping table instead of a name compare —
this plan cannot be verified against the lab fixture and the assumption in
Decision 3 must be re-checked first.

## Decisions

1. **`fastiron` sits beside `interfaces`, not inside it.** Both live under
   `src/modules/localnet/access/internal/capability/`:
   `interfaces/{adapter,snmp,completeness}.go` is the protocol-agnostic
   handler; `fastiron/{adapter,commands,parser}.go` is the one shell mapping.
   Why: the direction record says the shell mapping lives "beside" the
   capability handler, and a sibling package (rather than a subpackage of
   `interfaces`) keeps `interfaces` free of any FastIron import, so a second
   firmware adapter added later imports `interfaces` the same way FastIron
   does, without touching it.
2. **No central wiring in this plan.** `MutationIntent` submission, the
   central journal, `MutationState`, and the edge's per-device lane
   (decisions 3, 4, 9) are out of scope: nothing exists yet to submit an
   intent to. This plan builds the capability an edge would call — read,
   route, mutate, verify — as directly callable Go, proven by tests that
   drive it with a fake SNMP session and a fake SSH transcript. Why: the
   task names the interface capability, not the edge host or the lane;
   building the lane without a caller would be undirected scaffolding.
3. **`interface_name` is the device's own spelling, taken from SNMP `ifName`
   and matched literally against the SSH route's port designator.**
   FastIron's `ifName` for a physical port is the same string the CLI
   accepts after `interface` (`ethernet 1/1/1`), which is why the
   `device/access/v1/README.md` example
   (`spec/proto/flowseer/device/access/v1/README.md:26`) uses that exact
   spelling as `interface_name`. Unconfirmed: no lab transcript exists to
   check the exact `ifName` string an ICX7150 on 10.0.10g reports; the SNMP
   read in this plan falls back to `ifDescr` only for logging, never for
   identity, so a mismatch fails the interface lookup loudly (a typed
   not-found error) rather than silently joining the wrong port.
4. **The physical mapper's tables move onto `collect.Snapshot`; its
   transceiver-diagnostics (DDM) walk does not, this round.**
   `snmpmap.Physical` currently issues five independent walks directly
   against a session (`src/modules/localnet/snmpmap/phy.go:138`); giving it a
   `collect.Spec` requires `Map` to work from a `Snapshot` alone, since
   `collect.Mapper.Map` takes no session
   (`src/modules/localnet/collect/mapper.go:255`). The EtherLike, MAU, and
   Power-Ethernet walks (four of the five) convert cleanly onto
   `collect.NewTable`, mirroring `ifmib.go`'s `ifTableRead` pattern
   (`src/modules/localnet/snmpmap/ifmib.go:117`). The DDM walk
   (`walkModules`, `src/modules/localnet/snmpmap/phy_ddm.go`) branches across
   three vendor-specific transceiver MIBs and stays a direct per-session walk;
   `Physical(ctx, sess)` keeps calling it after `PhysicalMapper.Map`, so the
   free function's behavior is unchanged and only the new `PhysicalMapper`
   value omits DDM facts. Why: the interface capability this plan builds
   never reads DDM data, and converting three vendor branches to the
   `Snapshot` pattern is a separable, larger change than "give the physical
   mapper a Spec."
5. **Freshness and delayed-effect are Go-only typed values, not new proto
   messages.** `flowseer.device.access.v1.Completeness` already exists
   (`spec/proto/flowseer/device/access/v1/interface.proto:14`); freshness and
   the delayed-apply horizon are edge-local route-selection inputs the
   direction record never puts on the wire (its decision 12 package list has
   no such message, and decision 5 calls the horizon "declared," not
   transmitted). They are exported Go types in `interfaces/completeness.go`
   so a route selector can hold them without a schema change.
6. **Verification opens the read on the same already-privileged SSH session,
   not a newly dialed one.** Decision 2 requires "a fresh session"; this
   plan reads that as fresh relative to the mutation's own command
   sequence (a new `show interfaces ethernet` after `end`, not a reused
   buffered result), not as a requirement to tear down and redial the TCP
   connection, because nothing in this plan's scope owns session lifecycle
   across a mutation and its verification — that is the edge lane's job
   (decision 3), not the capability's. The capability's contract is: it
   never reports a mutation verified from anything but a command it issued
   after the mutation's `end`.

## Requirements

1. **SNMP read.** Given a session and a device-local interface name, return
   an `InterfaceObservation` with `description`, `admin_status`,
   `oper_status`, and `completeness`. Acceptance: a fake session serving
   `ifTable`/`ifXTable` rows for `ifIndex` 1 with `ifAlias` "uplink to core",
   `ifAdminStatus` up, `ifOperStatus` up yields
   `{description: "uplink to core", admin_status: ADMIN_STATUS_UP,
   oper_status: OPER_STATUS_UP, completeness: COMPLETE}`.
2. **SNMP incomplete fallback.** An observation missing any compared field
   is `PARTIAL` and never authoritative. Acceptance: the same fixture with
   `ifAlias` unimplemented (not observed, not empty) yields
   `completeness: PARTIAL`; a caller composing SNMP with an SSH fallback
   (Requirement 6) gets the SSH-sourced `COMPLETE` result instead.
3. **FastIron read.** Given an authenticated shell and an interface name,
   `show interfaces ethernet <name>` parses into the same three fields.
   Acceptance: the transcript
   `GigabitEthernet1/1/1 is up, line protocol is up\n  Port name is uplink to core\n` parses to
   `{description: "uplink to core", admin_status: ADMIN_STATUS_UP,
   oper_status: OPER_STATUS_UP}`; `... is disabled, line protocol is down` with
   `No port name` parses to `{description: "", admin_status: ADMIN_STATUS_DOWN,
   oper_status: OPER_STATUS_DOWN}`
   ([`show interfaces ethernet`](https://docs.ruckuswireless.com/fastiron/08.0.60/fastiron-08060-commandref/GUID-54E45EEA-5E28-49B9-B4C7-DCA7811947F6.html)).
4. **FastIron mutation, running-config only.** Setting a description issues
   exactly `configure terminal`, `interface <name>`, `port-name <text>` (or
   `no port-name` when `text` is empty), `end` — never `write memory` or
   `copy running-config startup-config`
   ([`port-name`](https://docs.ruckuswireless.com/fastiron/08.0.60/fastiron-08060-commandref/GUID-E28909AD-2C2C-43FD-8AD8-C4A9FD289BA0.html)).
   Acceptance: a transcript fixture asserts the exact four-command sequence
   and its prompts, and a dedicated test scans every command builder's
   output for the substrings `write mem` and `copy running-config` and fails
   if either appears.
5. **Verification.** After a mutation, a fresh `show interfaces ethernet`
   compared against the intended description reports verified or not;
   within the declared delayed-effect horizon an unmatched read reports
   "not yet verified" rather than "failed." Acceptance: a transcript where
   the post-mutation read still shows the old description, then a second
   read (still inside the horizon) shows the new one, is reported
   `verified: true`; the same first read alone, past the horizon, is
   reported `verified: false`.
6. **Route selection.** Given a `COMPLETE` SNMP observation, no SSH read
   happens. Given a `PARTIAL` one, the SSH route answers and its result
   (always `COMPLETE` on a successful FastIron parse) is returned with its
   own provenance. Acceptance: a fake route selector call with an
   SNMP-complete observation never invokes the supplied SSH fallback
   function; one with SNMP-partial does, and returns the SSH observation.
7. **Conflicting reads.** Two `COMPLETE` observations of the same interface
   naming different values for any compared field are reported as
   conflicting, never silently merged. Acceptance: SNMP says `admin_status:
   UP` and SSH says `admin_status: DOWN` for the same `interface_name`;
   `ConflictingReads` reports `true` with the differing field named.
8. **Adapter robustness.** Pagination, an ambiguous/invalid command
   response, and control bytes embedded in device output (a description
   containing bytes that look like a prompt) never crash the parser and
   never produce a false-positive successful read. Acceptance: a
   `--More--, next page: Space, next line: Return key, quit: Control-c`
   marker mid-output
   ([Scroll control](https://docs.ruckuswireless.com/fastiron/08.0.60/fastiron-08060-commandref/GUID-69E560B4-597A-49FB-AA0B-F19E859F5D31.html))
   is consumed and excluded from the parsed block; an `Invalid input` or
   `Incomplete command.` transcript on `port-name` returns a typed
   command-rejected error, never a false "applied"; a description line
   containing `SSH@device#` as literal text does not end the command early.
9. **Physical mapper participates in one shared cycle.** `snmpmap.Physical`'s
   EtherLike, MAU, and Power-Ethernet reads are declared through
   `collect.NewTable` and exposed as a `collect.Mapper`. Acceptance: running
   `collect.New(snmpmap.InterfaceMapper, snmpmap.PhysicalMapper).Collect`
   against a fake session that counts `GetBulk` requests per table root
   issues each of those three-to-six table roots exactly once, even though
   both mappers are present; `snmpmap.Physical`'s existing exported
   behavior (including DDM facts) is unchanged.

## Out of scope

- Central `MutationIntent`/`MutationState` wiring, the edge lane, the
  journal barrier, and firmware-epoch gating (decisions 3, 4, 7, 9 of the
  direction record) — this plan builds the capability those will call.
- A shell DSL or a second firmware adapter (decision 11 explicitly defers
  this to a second firmware).
- `walkModules`'s three vendor DDM transceiver MIBs moving onto
  `collect.Snapshot` (Decision 4).
- Capturing a transcript from the lab ICX7150. Every fixture in this plan is
  authored from the cited vendor documentation and the ICX YANG models; none
  is a device capture.
- LLDP, counters, MTU, MAC, and every other `Interface` field `snmpmap`
  already maps: this capability only compares description, admin status,
  and oper status.

## Units

### U1. Physical mapper gets a `collect.Spec`
Files: `src/modules/localnet/snmpmap/phy.go`, `phy_test.go`
After: none
Change: Declare `collect.NewTable` reads for `dot3StatsTable`,
`dot3HCStatsTable`, `ifMauTable`, `ifMauAutoNegTable`, `pethPsePortTable`, and
`pethMainPseTable` (columns already named by the existing `*Columns` vars).
Rewrite `walkEtherLike`, `walkMau`, `walkPsePorts`, and `walkPseBudgets` to
take `*collect.Snapshot` and read via `Table.Rows`/`Table.Err` instead of
calling `<Module>Table.Walk(ctx, sess, cols...)` directly. Add
`physicalMapper` implementing `collect.Mapper`: `Spec()` lists the six tables
as `Optional` (a failed walk degrades `PhysicalFacts`, matching the existing
`errors.Join` non-fatal handling) and `Map(snap)` returns the
EtherLike/MAU/PSE facts. Export `var PhysicalMapper collect.Mapper =
physicalMapper{}`. `Physical(ctx, sess)` becomes: read the six tables via
`collect.Read`, call `physicalMapper{}.Map` for the core facts, then still
call `walkModules(ctx, sess, facts.Facets)` directly for DDM, so its exported
signature and full behavior (DDM included) do not change.
Tests: existing `phy_test.go`/`phy_ddm_test.go` pass unmodified. Add
`TestPhysicalMapper_Spec` (asserts the six declared table roots) and
`TestPhysicalMapper_Map` (a fake session fixture drives `collect.Read` then
`Map`, asserting the same facts `Physical` would build from the four
non-DDM walks) and `TestPhysical_SharesTablesWithInterfaceMapper` (a
request-counting fake session proves `collect.New(InterfaceMapper,
PhysicalMapper).Collect` walks `ifTable`/`dot3StatsTable`/etc. once each).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/localnet/snmpmap`

### U2. Completeness, freshness, and route selection
Files: `src/modules/localnet/access/internal/capability/interfaces/completeness.go`, `completeness_test.go`, `access/doc.go`
After: none
Change: New package `interfaces`. `Freshness{ObservedAt time.Time; MaxAge
time.Duration}` with `Stale(now time.Time) bool`. `DelayedEffect{Horizon
time.Duration}`. `Route` enum (`RouteSNMP`, `RouteSSH`). `SelectRoute(primary
*accessv1.InterfaceObservation, fallback func() (*accessv1.InterfaceObservation, error)) (*accessv1.InterfaceObservation, Route, error)`:
returns `primary` unchanged when its `Completeness` is `COMPLETE`; otherwise
calls `fallback` once and returns its result, erroring if `primary` is nil or
`fallback` also fails to produce a `COMPLETE` result. `ConflictingReads(a, b
*accessv1.InterfaceObservation) (conflict bool, field string)` compares
`description`, `admin_status`, `oper_status` field by field on two `COMPLETE`
observations naming the same `interface_name`; a `PARTIAL` input is a
programmer error (never called this way) and returns `conflict: false` with
an empty field rather than panicking. `DescriptionApplied(observation
*accessv1.InterfaceObservation, intent *accessv1.InterfaceDescriptionChange) bool`
compares the two `description` fields. `access/doc.go` documents the
module's scope and cites this plan's decisions.
Tests: table-driven cases for Requirements 2, 6, 7; `Freshness.Stale` at,
before, and after the boundary.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/localnet/access/internal/capability/interfaces/completeness.go src/modules/localnet/access/internal/capability/interfaces/completeness_test.go src/modules/localnet/access/doc.go`

### U3. SNMP interface read
Files: `.../interfaces/snmp.go`, `snmp_test.go`, `.../interfaces/doc.go`
After: none
Change: `ReadSNMP(ctx context.Context, sess snmp.Session, name string) (*accessv1.InterfaceObservation, error)` (provenance fields the caller does not yet have — binding, edge, firmware fingerprint — are filled in by the caller after this returns, since `snmpmap`/`collect` know nothing about bindings; this function returns the observation with `Provenance` unset and the capability handler in U6 fills it). Runs
`collect.New(snmpmap.InterfaceMapper).Collect(ctx, sess)`, finds the interface
in the mapped slice by `Name` (Decision 3), and reports `COMPLETE` only when
`HasDescription()`, `HasAdminStatus()`, and `HasOperStatus()` are all true on
the matched `*interfacev1.Interface` — an unobserved `ifAlias` is `PARTIAL`,
never an empty description (only an *observed* empty `ifAlias` is an empty
description). Returns a typed not-found error when no mapped interface's
name matches.
Tests: fake `snmp.Session` fixtures (following `snmpmap`'s
`fake_session_test.go` pattern) for Requirements 1 and 2, plus interface not
found and an identity-read failure that still returns the interface data
(mirrors `Collector.Collect`'s joined-error contract).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/localnet/access/internal/capability/interfaces/snmp.go src/modules/localnet/access/internal/capability/interfaces/snmp_test.go src/modules/localnet/access/internal/capability/interfaces/doc.go`

### U4. FastIron commands and parser
Files: `.../fastiron/commands.go`, `commands_test.go`, `parser.go`, `parser_test.go`, `doc.go`
After: none
Change: Package `fastiron`. `commands.go`: prompt patterns for unprivileged
(`>\s*$`), privileged (`^\S+#\s*$`), config (`^\S+\(config\)#\s*$`), and
per-interface config (`^\S+\(config-if-[^)]+\)#\s*$`, citing the
`(config-if-e1000-1/1/1)#` example on the `port-name` reference page above);
a `MorePrompt` (`--More--`) with keystroke `" "`, citing Scroll control.
Command builders: `ShowInterfaceCommand(name string) ssh.Command`,
`EnableCommand`, `ConfigureTerminalCommand`, `SelectInterfaceCommand(name
string) ssh.Command`, `PortNameCommand(text string) ssh.Command` (`no
port-name` when `text == ""`, else `port-name ` + `text`), `EndCommand`.
`parser.go`: `ParseShowInterface(output []byte) (description string, admin
interfacev1.AdminStatus, oper interfacev1.OperStatus, err error)` parsing the
header line (`disabled` → `ADMIN_STATUS_DOWN`; `up`/`down` → `ADMIN_STATUS_UP`;
`line protocol is` word → `OperStatus`) and the port-name line (`No port
name` → `""`; `Port name is <text>` → `<text>`, verbatim including embedded
spaces and punctuation); returns a typed parse error on an unrecognized
header, and a distinct typed command-rejected error when the output is an
`Invalid input` or `Incomplete command.` transcript instead of a
`show interfaces` block.
Tests: `commands_test.go` asserts exact `Line`/`Prompts`/`MorePattern` per
builder and the running-only guard (Requirement 4's substring scan).
`parser_test.go` covers Requirements 3 and 8: up/up, disabled/down, `No port
name`, a description with spaces and a trailing `%`, a `--More--`-paginated
transcript, an `Invalid input` transcript, and a transcript with control
bytes and an embedded `SSH@device#`-looking line inside the description.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/localnet/access/internal/capability/fastiron`

### U5. FastIron adapter
Files: `.../fastiron/adapter.go`, `adapter_test.go`
After: U4
Change: `type Adapter struct{ Session *ssh.Session }`. `Login(ctx
context.Context) error`: runs a command with `Prompts: [unprivileged,
privileged]`; if unprivileged matched, runs `EnableCommand` with `Prompts:
[enable-password, privileged]` and, if the password prompt matched, writes
the caller-supplied enable password (redacted) and expects privileged.
`ReadInterface(ctx, name string) (description string, admin
interfacev1.AdminStatus, oper interfacev1.OperStatus, err error)`: runs
`ShowInterfaceCommand` with the `MorePrompt` wired in, then
`ParseShowInterface`. `SetPortName(ctx, name, text string) error`: runs
`ConfigureTerminalCommand` (expect config prompt), `SelectInterfaceCommand`
(expect config-if prompt — a mismatch, e.g. still privileged, is the typed
ambiguous-submission error from Requirement 8), `PortNameCommand` (expect
config-if prompt again), `EndCommand` (expect privileged); never issues any
other command.
Tests: transcript-fixture tests (a fake `net.Conn`/`ssh.Session` server,
following `sshtest_helper_test.go`'s pattern) for: login happy path (both
enable-required and already-privileged), `ReadInterface` happy path with
pagination, `SetPortName` happy path, `SetPortName` receiving an `Invalid
input` reply to `interface <name>` (ambiguous submission — Requirement 8),
and a running-only assertion over the full recorded transcript.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/localnet/access/internal/capability/fastiron/adapter.go src/modules/localnet/access/internal/capability/fastiron/adapter_test.go`

### U6. Capability handler
Files: `.../interfaces/adapter.go`, `adapter_test.go`
After: U2, U3, U5
Change: `type ShellAdapter interface { ReadInterface(ctx context.Context, name string) (description string, admin interfacev1.AdminStatus, oper interfacev1.OperStatus, err error); SetPortName(ctx context.Context, name, text string) error }`
(`fastiron.Adapter` satisfies it structurally; no import from `interfaces` to
`fastiron`). `Read(ctx, sess snmp.Session, shell ShellAdapter, name string,
prov ProvenanceInputs) (*accessv1.InterfaceObservation, error)`: builds
the SNMP observation (U3), wraps a fallback closure that calls `shell.ReadInterface`
and builds a `COMPLETE` observation with `MANAGEMENT_PROTOCOL_SSH`
provenance, and returns `SelectRoute`'s result (U2) with `Provenance` filled
from `prov` and the winning route's protocol. `VerifyDescriptionChange(ctx,
sess, shell, intent *accessv1.InterfaceDescriptionChange, prov
ProvenanceInputs, effect DelayedEffect, observedAt time.Time) (observation
*accessv1.InterfaceObservation, verified bool, err error)`: calls `Read`,
compares with `DescriptionApplied` (U2); if unmatched and `effect.Horizon`
has not elapsed since `observedAt`, returns `verified: false, err: nil` (not
yet, distinguishable from failed) with the observation; past the horizon,
also `verified: false` but the caller is expected to treat it as failed
(the horizon-vs-failed distinction is carried by the caller comparing its
own elapsed time against `effect.Horizon`, since this capability does not
retry).
Tests: Requirements 5, 6, 7 end to end with fake `snmp.Session` and a fake
`ShellAdapter`: SNMP-complete short-circuits (fallback never called);
SNMP-partial falls through to SSH; conflicting complete reads from both
routes; delayed-effect not-yet-verified vs. past-horizon.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/localnet/access/internal/capability/interfaces/adapter.go src/modules/localnet/access/internal/capability/interfaces/adapter_test.go`

## Amendments (post-review)

An independent review of this plan found corrections before implementation
continued past U1 and U2. Recorded here rather than rewritten in place, so
the review's evidence stays traceable.

- **New unit, U0, before U3 and U6.** `InterfaceObservation`'s `description`,
  `admin_status`, `oper_status`, and `provenance` were all
  `(buf.validate.field).required = true`, so a `PARTIAL` observation (any
  compared field missing) could never be schema-valid — U3 and U6 cannot
  produce the `PARTIAL` values Requirements 2, 6, and 7 need.
  `spec/proto/flowseer/device/access/v1/interface.proto`'s
  `InterfaceObservation` message now drops `required` from those four fields
  and adds a message-level CEL rule,
  `interface_observation.complete_sets_every_compared_field`, requiring them
  present exactly when `completeness` is `COMPLETE`; each per-field value
  constraint (the enum's `not_in: [0]`, the string pattern) still applies
  whenever the field is set. This is the kind of pre-stability schema
  correction `AGENTS.md` directs making outright. Files:
  `spec/proto/flowseer/device/access/v1/interface.proto`, regenerated
  `generated/go/proto/flowseer/device/access/v1/interface.pb.go` (via `buf
  generate spec/proto`, run once for the whole workspace — a `--path`-scoped
  generate combined with this repo's `clean: true` deletes every other
  package's generated output). Verify: `buf lint spec/proto --path
  spec/proto/flowseer/device/access/v1/interface.proto`, then `go build
  ./...` and `go test ./...`. Done: added and passing in this worktree.
- **U3 uses `collect.Read`, not `collect.New(...).Collect(...)`.**
  `InterfaceMapper` has no `SysObjectIDPrefixes`, so detection never excludes
  it and the identity read buys U3 nothing `snmpmap.Interfaces` doesn't
  already get by calling `collect.Read(ctx, sess, InterfaceMapper)` directly
  (`src/modules/localnet/snmpmap/ifmib.go:146`). U3 does the same, dropping
  the identity-read-failure test case (nothing in U3 reads identity).
- **R2 and R6's `accessv1` package alias.** The generated Go package for
  `flowseer.device.access.v1` is `accessv1` (not `devaccessv1`, corrected
  throughout this plan); import it under that alias everywhere in U2, U3,
  and U6.
- **U4's prompt patterns need `(?m)` and must exclude parens from the
  hostname run, or privileged and config prompts collide.** `scanPrompt`
  matches against the whole accumulated buffer, and Go's `^`/`$` without
  `(?m)` anchor to the start/end of the *entire buffer*, not the last line —
  so `^\S+#\s*$` never matches a prompt that follows any prior output.
  Separately, `\S+` matches parentheses, so an unqualified privileged
  pattern also matches a config-mode prompt line, and ties resolve to
  whichever pattern is listed first in `Command.Prompts`. The corrected
  patterns, each requiring no parenthesis before the terminal punctuation:
  unprivileged `(?m)^[^()\r\n]+>\s*$`; privileged `(?m)^[^()\r\n]+#\s*$`;
  config `(?m)^[^()\r\n]+\(config\)#\s*$`; config-if
  `(?m)^[^()\r\n]+\(config-if-[^)]*\)#\s*$`.
- **U4's `ShowInterfaceCommand` must not repeat the `ethernet` keyword.**
  Decision 3 fixes `interface_name` as the full CLI spelling
  (`ethernet 1/1/1`), and the vendor syntax is `show interfaces ethernet
  stackid/slot/port` — so the command is `"show interfaces " + name`, not
  `"show interfaces ethernet " + name`. `SelectInterfaceCommand`'s
  `"interface " + name` was already correct under the same decision.
- **U4's `MorePrompt` must match the whole marker line, not just
  `--More--`.** `scanPrompt` only strips through the end of the matched
  span; matching just `--More--` would leave
  `, next page: Space, next line: Return key, quit: Control-c` in
  `Result.Output`. Since every fixture here is authored, not captured, the
  pattern matches the documented marker text in full
  (`regexp.QuoteMeta` of the string this plan's Requirement 8 quotes).
- **U5's `Adapter` needs a field for the enable password.** Add
  `EnablePassword string` to `Adapter`, written with `Command.Redacted` set
  (`src/protocol/ssh/command.go:19-21`) when `Login` sees the
  enable-password prompt.
- **U5's ambiguous-submission detection needs both prompts in one `Run`
  call, not a second call after a timeout.** `SelectInterfaceCommand` and
  `PortNameCommand` must list the expected next-level prompt *and* the
  prompt an `Invalid input`/`Incomplete command.` reply returns to (config
  for a rejected `interface`, config-if for a rejected `port-name`) in one
  `Command.Prompts`, and `Adapter.SetPortName` branches on
  `Result.MatchedPrompt`: the fallback prompt matching is the typed
  ambiguous-submission error, not a `Run` timeout.
- **U6's `VerifyDescriptionChange` returns a tri-state disposition, not a
  bare `bool`.** Requirement 5's acceptance is two separate calls (a
  post-mutation read still showing the old value, then a later call
  showing the new one) — not one call retrying internally. Signature:
  `VerifyDescriptionChange(...) (observation *accessv1.InterfaceObservation,
  disposition VerificationDisposition, err error)` with `disposition` one of
  `VerificationVerified`, `VerificationNotYetVerified` (unmatched, still
  within `effect.WithinHorizon(observedAt, now)`), or `VerificationFailed`
  (unmatched, past the horizon) — computed from one `Read` plus the elapsed
  time, so a caller polling this function across the horizon sees the
  states Requirement 5 names without the function retrying itself.
- **U6 gains an exported facade.** Everything under
  `.../capability/interfaces` stays `internal/` (Decision 1's reason holds),
  but nothing outside `access` could reach it, contradicting Decision 2's
  "the capability an edge would call." Add
  `src/modules/localnet/access/access.go` re-exporting `Read` and
  `VerifyDescriptionChange` (thin wrappers, no new logic) so a future edge
  host imports `go.aledante.io/FlowSeer/src/modules/localnet/access` rather
  than an `internal` path.
- **U5 gains a compile-time assertion that `fastiron.Adapter` satisfies
  `interfaces.ShellAdapter`.** `var _ interfaces.ShellAdapter =
  (*Adapter)(nil)` in `fastiron`'s own test file — a test-only import of
  `interfaces` from `fastiron` does not contradict Decision 1, which is
  about runtime imports, and it catches a signature drift between U5 and U6
  at compile time instead of at the point a future host wires them
  together.
- **U2's `Freshness` gets a caller.** `SelectRoute` (U6's `Read`) accepts an
  optional previously-read `*accessv1.InterfaceObservation` and its
  `Freshness`; when it is `COMPLETE` and not `Stale(now)`, `Read` returns it
  without a new SNMP or SSH round trip. Without this, `Freshness` had no
  consumer.
- **R9's acceptance, and the test already landed for U1, must name the
  tables they check.** "Walks each root once" is true even if a mapper
  declares nothing, so `TestPhysical_SharesTablesWithInterfaceMapper`
  (`src/modules/localnet/snmpmap/phy_test.go`) is tightened to assert the
  expected six-plus-three (`ifTable`, `ifXTable`, `ifStackTable`,
  `dot3StatsTable`, `dot3HCStatsTable`, `ifMauTable`, `ifMauAutoNegTable`,
  `pethPsePortTable`, `pethMainPseTable`) roots are present with count 1,
  not merely that every counted root's count is 1.
- **No `doc.go` cites this plan's path.** `docs/code-style.md`'s rule
  against process/planning identifiers in code applies to a plan file
  citation the same as a label; every `doc.go` states the rule its package
  enforces and cites the direction record's decisions only.
- **`src/modules/localnet/README.md`'s package table is updated** to
  describe the physical mapper's shared tables (U1, already landed) and add
  an `access` row.
- **Accepted, non-blocking:** the package name `interfaces` is plural
  against `docs/code-style.md`'s general preference; kept because the task
  that produced this plan specifies this exact directory name and it names
  a capability family, not a single type. The `(description, admin, oper,
  err)` return quadruple on `ParseShowInterface` and `ShellAdapter.ReadInterface`
  is accepted as three primitives plus an error, matching the rest of this
  codebase's style rather than introducing a single-use struct.

## Verification

```
go test -race ./src/modules/localnet/... ./src/protocol/ssh/...
.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/localnet/snmpmap src/modules/localnet/access
```

No unit reaches a real device; every SSH and SNMP test drives a fixture or a
fake session. No test dials `172.16.0.0/24`.

## Definition of done

- [ ] `go test -race ./src/modules/localnet/...` green.
- [ ] The verifier green for every path each unit names.
- [ ] `phy.go`'s exported behavior (including DDM) is unchanged; `snmpmap`'s
  existing tests pass without modification.
- [ ] Every fixture transcript's vendor-specific wording (prompts, pagination
  marker, `port-name` syntax, `disabled`/`No port name` wording) traces to a
  cited source.
- [ ] `access/doc.go`, `interfaces/doc.go`, and `fastiron/doc.go` each state
  their package's scope and cite this plan.
- [ ] This plan's `status` set to `implemented` (or `partially-implemented`
  with a note) when the units land.

## Open questions

- Decision 3 (SNMP `ifName` equals the CLI's own port spelling) is
  unconfirmed against a real ICX7150 on 10.0.10g; the first lab validation of
  this capability should check it before it is relied on for a real
  mutation.
- Decision 6 (verification reuses the mutating SSH session rather than a
  freshly dialed one) is a scope call for this plan; the edge lane that
  eventually owns session lifecycle across a mutation may decide otherwise
  without changing this capability's function signatures.
