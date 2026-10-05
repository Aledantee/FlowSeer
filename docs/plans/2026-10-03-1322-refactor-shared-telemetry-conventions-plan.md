---
title: Shared Telemetry Conventions Package - Plan
type: refactor
date: 2026-10-03
artifact_contract: flowseer-plan/v2
execution: mixed
---

# Shared Telemetry Conventions Package - Plan

## Goal

Every package that emits a cross-package `flowseer.*` attribute, an
`error.type` value, or a named event takes it from one package,
`src/common/fsconv`, so two packages cannot disagree on a key or a value by
typing it differently. The means: a small package of key constants, `slog`
attribute constructors, one error classifier, and two thin helpers, followed
by a migration of the call sites in `src/protocol`, `src/common/service`,
`src/edge/agent`, `src/modules`, and `src/services/device`.

**Stop condition:** a consumer needs `error.type` to differ from what
`fsconv.ErrorTypeOf` returns for an error that carries an `errs` code or a
context cause, which would mean the shared vocabulary is wrong and not just
duplicated.

## Decisions

- The package is `src/common/fsconv`. Why: a project package named `semconv`
  collides with the alias every instrumented file already uses for
  `go.opentelemetry.io/otel/semconv/v1.43.0` (16 imports in the module), and
  `fsconv.DeviceID(id)` reads like the OTel package it sits beside (decided
  by the user, 2026-10-03).
- `fsconv` passes both tests in `src/common/README.md`, "What belongs here".
  It holds string constants and functions over `slog` and OTel types, so it
  has no domain types and imports nothing from `generated/`. Five trees
  import it after this plan (`src/protocol`, `src/common/service`,
  `src/edge`, `src/modules`, `src/services`). Why state it: the keys name
  domain entities (`CONCEPTS.md`, "Device", "Edge", "Capture Session"), and
  the README rule is about types and generated schemas, not vocabulary.
- A key enters `fsconv` when more than one tree under `src/` emits it, or
  when it names the ambient tenant. That gives six keys today:

  | Constant | Key | Constructor |
  | --- | --- | --- |
  | `DeviceIDKey` | `flowseer.device.id` | `DeviceID(string) slog.Attr` |
  | `DeviceSequenceKey` | `flowseer.device.sequence` | `DeviceSequence(uint64) slog.Attr` |
  | `EdgeIDKey` | `flowseer.edge.id` | `EdgeID(string) slog.Attr` |
  | `TenantIDKey` | `flowseer.tenant.id` | `TenantID(string) slog.Attr` |
  | `CaptureSessionIDKey` | `flowseer.capture.session.id` | `CaptureSessionID(string) slog.Attr` |
  | `CaptureChunkFirstSequenceKey` | `flowseer.capture.chunk.first_sequence` | `CaptureChunkFirstSequence(uint64) slog.Attr` |

  Why: `flowseer.device.id` is typed as a literal in seven packages and
  `flowseer.edge.id` in four, and the edge agent and the device service only
  correlate because the literals happen to match. A key one package owns
  stays a private constant next to its instrument.
- Constructors return `slog.Attr` only. Why: every call site of the six keys
  outside tests is a `slog` call. The constants are untyped strings, so a
  later span or metric caller writes
  `attribute.String(fsconv.DeviceIDKey, id)` without a second constructor
  family existing first.
- One classifier, `ErrorTypeOf(err error) string`: the empty string for a nil
  error, the `errs` code when the error carries one,
  `context.deadline_exceeded` or `context.canceled` for the two context
  causes, otherwise `_OTHER`. Why: ten private classifiers exist with four
  fallback vocabularies. `src/protocol/netconf/session.go`,
  `src/protocol/gnmi/session.go`, `src/protocol/restconf/errors.go`, and
  `src/common/service/telemetry.go` fall back to the Go type through `%T`.
  Six others fall back to `unknown`, with or without the context causes,
  among them `classifyError` in `src/modules/localnet/access/lane.go`.
  `_OTHER` is the stable fallback the pinned package defines as
  `semconv.ErrorTypeOther`
  (`~/go/pkg/mod/go.opentelemetry.io/otel@v1.46.0/semconv/v1.43.0/attribute_group.go`,
  "Enum values for error.type"). `fsconv` reads the `error.type` key, the
  fallback, and the `otel.event.name` key (`semconv.OTelEventNameKey`) from
  that generated package and does not retype them (decided by the user,
  2026-10-03).
- A package with its own bounded vocabulary keeps a local wrapper, in one of
  two shapes:
  - `src/edge/agent/internal/capture/upload.go` calls `fsconv.ErrorTypeOf`
    first and substitutes `connect.<code>` only when the result is
    `fsconv.ErrorTypeOther`. This is the order the function has today. A
    cancelled Connect call wraps `context.Canceled`
    (`~/go/pkg/mod/connectrpc.com/connect@v1.21.0/error.go`, `Unwrap`), so the
    opposite order would turn `context.canceled` into `connect.canceled`.
  - `src/protocol/snmp/instrument.go` matches its sentinel errors first and
    calls `fsconv.ErrorTypeOf` where it returned `other`.

  Why: `fsconv` stays free of `connectrpc.com/connect`, which no package
  under `src/common` imports today (decided by the user, 2026-10-03, as
  "local wrappers for their own codes". The order within each wrapper is
  this plan's).
- `src/services/device/internal/host/interceptor.go` is not touched. Its
  `errorType(err, code)` returns the `errs` code or the bare Connect code,
  and that code is already bounded by the protocol, including Connect's own
  `unknown`. `src/services/device/internal/host/interceptor_test.go` pins the
  `errs` code winning.
- A span records the error the caller receives. NETCONF and RESTCONF classify
  the transport error today and map it to an `errs` code one line later
  (`src/protocol/netconf/session.go`, `finish`. `src/protocol/restconf/session.go`,
  `do`). With `%T` gone, the unmapped error would always read `_OTHER`, so
  both sites classify the mapped error.
- These emitted values change, which is a telemetry-schema change under
  `docs/conventions/observability.md`, "Attributes": `unknown` becomes
  `_OTHER`, a Go type name becomes an `errs` code, a context cause, or
  `_OTHER`, and SNMP's `context_canceled`, `context_deadline`, and `other`
  become `context.canceled`, `context.deadline_exceeded`, and `_OTHER`.
  Nothing external consumes them (`AGENTS.md`, Agent behavior).
- `fsconv` carries these building blocks and no more (decided by the user,
  2026-10-03):

  | Block | Sites | Why it earns a place |
  | --- | --- | --- |
  | `ErrorType(err) slog.Attr` | 19 of the 36 `error.type` log attributes | Binds the key to the one classifier. |
  | `ErrorTypeString(value string) slog.Attr` and `ErrorTypeKey` | the other 17, whose value is a wrapper's result or a refusal code | Leaves no `"error.type"` literal outside `fsconv`. |
  | `EventName(name string) slog.Attr` | 21 `slog.String("otel.event.name", ...)` calls in 10 files | The bridge attribute is a fixed key that the shared log pipeline maps to EventName. |
  | `SpanError(span trace.Span, err error, description string)` | gNMI (1), NETCONF (1), RESTCONF (2), and the 9 callers of `recordSpanError` in `src/common/service` | Writes the pair that `observability.md`, "Traces", requires on a failed span, with the shared classifier. |

  `SpanError` does not fit four of the nine `SetStatus(codes.Error, ...)`
  sites, and they keep their own lines: the RESTCONF HTTP-status site, SNMP
  (local classifier and `RecordError`), the outcome path in
  `src/common/service/telemetry.go`, and
  `src/modules/localnet/access/internal/telemetry/spans.go`, whose classifier
  is a caller's parameter.

  Rejected, with the evidence:
  - A logging wrapper such as `Event(ctx, logger, name, msg, attrs...)`.
    `observability.md`, "Distinguish logs from named events", says not to
    build an event adapter, and the one that exists
    (`src/modules/localnet/access/internal/telemetry/events.go`) has one
    consumer.
  - `Tracer` and `Meter` factories that bind `semconv.SchemaURL`. The call is
    one line of standard OTel API at 12 sites, and the files still import the
    versioned `semconv` package for other symbols, so a version bump touches
    them either way.
  - Shared outcome or signal enums. The three outcome keys
    (`flowseer.module.lifecycle.outcome`, `flowseer.device.outcome`,
    `flowseer.device.drift.outcome`) have different value sets and one owner
    each.
  - Metric instrument builders. Names, units, and descriptions are per
    instrument, and no two packages share one.
- The durable rule goes into `docs/conventions/observability.md`, not a
  direction record. Why: that convention is the binding record for telemetry
  naming (`docs/README.md`), and a second record would restate it.
- Consumer tests keep their string literals. Why: a test that asserts
  `"flowseer.device.id"` on emitted output is the independent check that the
  constant did not change.

## Requirements

1. Each of the six keys, `error.type`, and `otel.event.name` has one
   definition under `src/`. Example:
   `grep -rIn --include='*.go' -E '"flowseer\.device\.id"|"error\.type"|"otel\.event\.name"' src | grep -v _test.go`
   prints one line, the `DeviceIDKey` constant in `src/common/fsconv/keys.go`.
2. `ErrorTypeOf` returns the documented vocabulary. Examples: `nil` gives the
   empty string. An error built with `tenant.ErrCodeNoTenant` gives
   `tenant/no-tenant`. `fmt.Errorf("x: %w", context.Canceled)` gives
   `context.canceled`. `errors.New("boom")` gives `_OTHER`. An `errs` error
   that wraps `context.DeadlineExceeded` gives its `errs` code.
3. The private classifiers are gone. Example:
   `grep -rIn --include='*.go' -E 'func (errorType|errorTypeOf|telemetryErrorType|ErrorType|recordSpanError|classifyErrorOrEmpty)\(' src | grep -v _test.go`
   prints three lines: `errorType` in
   `src/edge/agent/internal/capture/upload.go`, `errorType` in
   `src/services/device/internal/host/interceptor.go`, and `ErrorType` in
   `src/common/fsconv/errortype.go`. `grep -n 'func classifyError' src/modules/localnet/access/lane.go`
   prints nothing.
4. The capture wrapper lets `fsconv` win. Examples: a
   `connect.NewError(connect.CodeUnavailable, errors.New("x"))` gives
   `connect.unavailable`. A `connect.NewError(connect.CodeCanceled, context.Canceled)`
   gives `context.canceled`. `errors.New("boom")` gives `_OTHER`.
5. `SpanError` sets status Error with the given description and one
   `error.type` attribute, and does nothing for a nil error. Example: called
   with `errors.New("boom")` and `"rpc failed"` on a recording span, the
   ended span has status code Error, description `rpc failed`, and
   `error.type` equal to `_OTHER`.
6. `EventName("flowseer.edge.device.onboarded")` equals
   `slog.String("otel.event.name", "flowseer.edge.device.onboarded")`.
7. A failed NETCONF or RESTCONF transport call records its mapped code.
   Example: a NETCONF RPC whose transport fails ends its span with
   `error.type` equal to `netconf/transport`.

## Out of scope

- A conformance gate that rejects a stray `"flowseer.` key literal. It is a
  new enforcement surface and needs its own guardrail review (`AGENTS.md`,
  Hard boundaries).
- Metric, span, and event names, and attribute keys with one owning package.
- `attribute.KeyValue` constructors, until a span or metric emits a shared
  key.
- A YAML registry or generated code in the style of OTel Weaver.
- `flowseer.device.interface` and `flowseer.device.drift.outcome`, which two
  packages of the device service share. The last unit points `drift` at the
  service's own constants and adds nothing to `fsconv` for them.
- Whether the edge agent's `connect.<code>` and the device service
  interceptor's bare Connect code should agree (see Open questions).

## Units

### U1. Add `src/common/fsconv` and record the rule

Files: `src/common/fsconv/doc.go`, `src/common/fsconv/keys.go`,
`src/common/fsconv/errortype.go`, `src/common/fsconv/span.go`,
`src/common/fsconv/fsconv_test.go`, `src/common/fsconv/README.md`,
`src/common/README.md`, `docs/conventions/observability.md`
After: none
Change: `keys.go` holds the six constants and constructors from Decisions
plus `EventName`, whose key is `string(semconv.OTelEventNameKey)`.
`errortype.go` holds `ErrorTypeOf`, `ErrorType`, `ErrorTypeString`, the
constant `ErrorTypeKey = string(semconv.ErrorTypeKey)`, and the package
variable `ErrorTypeOther = semconv.ErrorTypeOther.Value.AsString()`. It is a
variable because `semconv.ErrorTypeOther` is an `attribute.KeyValue` variable
and not a constant expression. `span.go` holds `SpanError`. The README states
the admission rule for a key, the `error.type` vocabulary, and the two
wrapper shapes with a working example. `src/common/README.md` gains the
table row. `observability.md` gains, under "Attributes", the rule that a key
emitted by more than one tree comes from `fsconv`, and, under "Traces" and
"Distinguish logs from named events", the pointers to `SpanError`,
`ErrorType`, and `EventName`. Its event example uses `fsconv.EventName`.
Tests: `fsconv_test.go` pins each constant against its literal wire name in
one table, including `error.type`, `otel.event.name`, and `_OTHER`. It
checks each constructor's key and `slog.Kind`, runs the five classifier cases
of Requirement 2 with `tenant.ErrCodeNoTenant` (a new code in a test file
would fail the uniqueness scan in `src/common/errs/code_test.go`), and checks
Requirement 5 with `tracetest.NewSpanRecorder`, including that a nil error
leaves status and attributes unset.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/fsconv src/common/README.md docs/conventions/observability.md`

### U2. Migrate `src/protocol` and `src/common/service`

Files: `src/protocol/netconf/session.go`, `src/protocol/gnmi/session.go`,
`src/protocol/restconf/errors.go`, `src/protocol/restconf/session.go`,
`src/protocol/snmp/instrument.go`, `src/protocol/snmp/trap_listen.go`,
`src/protocol/snmp/reactor.go`, `src/protocol/snmp/instrument_test.go`,
`src/common/service/telemetry.go`, `src/common/service/delivery.go`,
`src/common/service/message.go`,
`src/common/service/telemetry_schema_test.go`,
`src/protocol/netconf/session_test.go`,
`src/protocol/restconf/session_test.go`
After: U1
Change: the three `errorType` functions, `telemetryErrorType`, and
`recordSpanError` are deleted. The nine callers of `recordSpanError` in
`delivery.go`, `message.go`, and `telemetry.go` call `fsconv.SpanError`. The
outcome path in `telemetry.go` keeps its own two lines and takes its error
value from `fsconv.ErrorTypeOf`. gNMI calls `fsconv.SpanError`. NETCONF's
`finish` maps the error first and passes the mapped error to
`fsconv.SpanError`. RESTCONF's two transport sites do the same with the
result of `transportError`, and its HTTP-status site stays as it is. SNMP's
`classifyError` keeps its sentinel cases, drops the nil case (no caller
passes nil) and the two context cases, and returns `fsconv.ErrorTypeOf(err)`
where it returned `other`. The three SNMP log sites use
`fsconv.ErrorTypeString`.
Tests: `netconf/session_test.go` and `restconf/session_test.go` each gain the
case of Requirement 7 for their own transport code.
`telemetry_schema_test.go` calls `fsconv.SpanError` where it called
`recordSpanError` and keeps its `service/admission` assertion.
`instrument_test.go` adds a table case for a context cancellation
(`context.canceled`) and for an unmatched error (`_OTHER`), beside the
existing `ErrAuthFailed` case.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/netconf src/protocol/gnmi src/protocol/restconf src/protocol/snmp src/common/service`

### U3. Migrate `src/edge/agent`

Files: `src/edge/agent/host/host.go`,
`src/edge/agent/internal/capture/upload.go`,
`src/edge/agent/internal/capture/subscribe.go`,
`src/edge/agent/internal/dispatch/demux.go`,
`src/edge/agent/internal/dispatch/subscribe.go`,
`src/edge/agent/internal/lanehost/onboard.go`,
`src/edge/agent/internal/lanehost/heartbeat.go`,
`src/edge/agent/internal/report/drain.go`,
`src/edge/agent/internal/report/queue.go`,
`src/edge/agent/internal/subscribeloop/loop.go`,
`src/edge/agent/internal/capture/capture_test.go`
After: U1
Change: every literal of the six keys becomes its constructor, every
`slog.String("otel.event.name", ...)` becomes `fsconv.EventName`, and the
`errorType` functions in `lanehost` and `subscribeloop` are deleted. Their
log sites use `fsconv.ErrorType`, and `renderAddress` in `onboard.go` uses
`fsconv.ErrorTypeOf`. `capture/upload.go` keeps `errorType` in the first
wrapper shape from Decisions, and its ten log sites use
`fsconv.ErrorTypeString`, as do the code-valued sites in `report/drain.go`
and `dispatch/demux.go`. `flowseer.edge.freeze.error_type` keeps its key and
takes its value from `fsconv.ErrorTypeOf`.
Tests: `capture_test.go` is `package capture_test`, so it reaches the wrapper
through the failure log of an upload whose source fails to open, once for
each of the three errors in Requirement 4. The existing assertions in
`lanehost/heartbeat_test.go`, `lanehost/onboard_test.go`, and
`dispatch/subscribe_test.go` keep their literals and pass unchanged.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/agent`

### U4. Migrate `src/modules`

Files: `src/modules/edgebus/forwarder.go`, `src/modules/edgebus/hub.go`,
`src/modules/localnet/access/lane.go`,
`src/modules/localnet/access/README.md`,
`src/modules/localnet/access/internal/telemetry/events.go`,
`src/modules/localnet/access/internal/telemetry/spans.go`,
`src/modules/localnet/access/internal/telemetry/telemetry_test.go`
After: U1
Change: `errorTypeOf` in `hub.go` is deleted in favor of `fsconv.ErrorType`.
`classifyError` and `classifyErrorOrEmpty` in `lane.go` are deleted, and
their callers pass or call `fsconv.ErrorTypeOf`, which already returns the
empty string for nil. `spans.go` keeps its caller-supplied classifier and
its default becomes `fsconv.ErrorTypeOther`. The `flowseer.edge.id`,
`flowseer.device.id`, and `flowseer.device.sequence` literals become
constructors, and the event marker in `forwarder.go`, `hub.go`, and
`View.event` becomes `fsconv.EventName`. The README's value list for
`error.type` on `flowseer.device.operation.duration` names `_OTHER`.
Tests: `telemetry_test.go` adds a case that a span ended with a non-nil
error and a nil classifier carries `_OTHER`. A test beside `lane.go` asserts
that an operation failing with an uncoded error records `_OTHER` on
`flowseer.device.operation.duration`. `src/modules/edgebus/attribution_test.go`
keeps its literals and passes unchanged.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/edgebus src/modules/localnet/access`

### U5. Migrate `src/services/device`

Files: `src/services/device/internal/telemetry/telemetry.go`,
`src/services/device/internal/drift/drift.go`,
`src/services/device/internal/dispatchapi/relay.go`,
`src/services/device/internal/dispatchapi/report.go`,
`src/services/device/internal/captureapi/edge_service.go`,
`src/services/device/internal/edgeapi/enroll.go`,
`src/services/device/internal/edgeapi/middleware.go`,
`src/services/device/internal/host/host.go`
After: U1
Change: `telemetry.ErrorType` and `attrKeyDevice` are deleted, and their
callers use `fsconv`. The six keys and the event marker become constructors.
The code-valued `error.type` site in `edgeapi/middleware.go` uses
`fsconv.ErrorTypeString`. `telemetry` exports `InterfaceKey` and
`DriftOutcomeKey`, and `drift.go` uses them in place of its two literals.
Tests: `src/services/device/internal/telemetry/telemetry_test.go`,
`src/services/device/internal/host/interceptor_test.go`, and the two files
under `src/services/device/test/integration/` keep their literal assertions
and pass unchanged. The classifier's own cases live in `fsconv_test.go`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device`

Waves: U1 | U2 U3 U4 U5

## Verification

```bash
go test -race ./src/common/fsconv/... ./src/common/service/... ./src/protocol/... ./src/edge/agent/... ./src/modules/edgebus/... ./src/modules/localnet/access/... ./src/services/device/...
grep -rIn --include='*.go' -E '"flowseer\.(device\.id|device\.sequence|edge\.id|tenant\.id|capture\.session\.id|capture\.chunk\.first_sequence)"|"otel\.event\.name"|"error\.type"' src | grep -v _test.go
```

The second command prints six lines, all in `src/common/fsconv/keys.go`. The
grep of Requirement 3 prints its three lines.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `src/common/fsconv/README.md`, `src/common/README.md`, and
      `docs/conventions/observability.md` updated in the unit that adds the
      package.
- [ ] `src/modules/localnet/access/README.md` names `_OTHER` in its
      `error.type` value list.
- [ ] This plan's `status` set, with an outcome note under its title.
- [ ] No plan labels in code.

## Open questions

- The edge agent emits `connect.unavailable` and the device service
  interceptor emits `unavailable` for the same Connect code. The pinned
  `error.type` comment recommends a separate domain attribute beside
  `error.type` for a domain with its own identifiers, which the interceptor
  already has (`rpc.response.status_code`) and the capture uploader does not.
  This plan keeps both as they are. Aligning them is a separate schema
  change.
