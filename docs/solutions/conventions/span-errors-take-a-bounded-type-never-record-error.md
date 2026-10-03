---
title: Span Errors Take a Bounded Type Attribute, Never RecordError or Error Text
date: 2026-10-03
category: conventions
module: src/common/service
problem_type: convention
component: telemetry
severity: high
applies_when:
  - "Recording an error on an OpenTelemetry span in Go code."
  - "Choosing between span.RecordError, span.SetStatus, and semconv.ErrorTypeKey on a failed operation."
  - "Reviewing span instrumentation to ensure error messages, request text, or unbounded diagnostics do not leak onto spans."
related_components: [observability, snmp, authz]
tags: [telemetry, opentelemetry, tracing, error-handling, observability]
---

# Span Errors Take a Bounded Type Attribute, Never RecordError or Error Text

OpenTelemetry Go provides `span.RecordError(err)` to record an error on a span.
Under the hood, `span.RecordError` creates an event named `exception` and populates
`exception.message` with `err.Error()`. Calling `span.SetStatus(codes.Error, err.Error())`
similarly attaches the error string as the span status description. In FlowSeer,
recording raw error strings on spans violates privacy bounds and creates
uncontrolled attribute cardinality.

## The trap, from the tree

In both `src/protocol/snmp/instrument.go` and
`src/services/device/internal/authz/openfga/checker.go`, failure paths called
`span.RecordError(err)` and set status descriptions to the error message. Error
strings often contain URLs, file paths, auth tokens, device addresses, or user
input. Putting those strings onto span exception events leaks sensitive details
into distributed traces and bypasses Collector redaction rules designed for
structured logs.

Telemetry conventions in `docs/conventions/observability.md` restrict error
attributes to classified, low-cardinality tokens under standard keys
(`error.type`). A span exception event duplicates error logging while adding
unbounded cardinality to the tracing backend.

## How to apply

On final failure, set span status with a fixed, bounded description or code, set
the classified code in `error.type`, and record zero span events:

```go
// src/services/device/internal/authz/openfga/checker.go:310-313
errType = c.callErrorType(ctx, err)
span.SetAttributes(semconv.ErrorTypeKey.String(errType))
span.SetStatus(codes.Error, "engine call failed")
```

For protocol clients with categorized errors:

```go
// src/protocol/snmp/instrument.go:204-210
if err != nil {
    kind := classifyError(err)
    span.SetAttributes(semconv.ErrorTypeKey.String(kind))
    span.SetStatus(codes.Error, kind)
    in.recordError(ctx, op, err)
}
```

The accompanying unit test asserts that no exception events were added to the
span and that status descriptions contain no error text:

```go
// src/services/device/internal/authz/openfga/checker_test.go:1292-1297
if span.Status().Description == "" || strings.Contains(span.Status().Description, err.Error()) {
    t.Errorf("span status description = %q, want fixed text free of the error's own", span.Status().Description)
}
if len(span.Events()) != 0 {
    t.Errorf("span events = %v, want none", span.Events())
}
```

Diagnostic error details belong in structured logs with field-level allowlists
and redaction, not on distributed trace spans.

## Evidence

- OpenFGA checker span instrumentation: `src/services/device/internal/authz/openfga/checker.go:310-313`.
- OpenFGA checker span test asserting no span events and no error text in status:
  `src/services/device/internal/authz/openfga/checker_test.go:1288-1300`.
- SNMP instrumentation without `RecordError`: `src/protocol/snmp/instrument.go:204-210`.
- SNMP test asserting zero span events on failure: `src/protocol/snmp/instrument_test.go:156-161`.
- Conformance gate: restoring `span.RecordError(err)` fails `TestEngineCallFailureTelemetryAndErrors`
  and `TestInstrument_PDUErrorRecordsStatus`.

## What this does not cover

This convention applies to internal span instrumentation. It does not replace
structured error logging via `log/slog` or telemetry views, where sanitized error
context is emitted under controlled policies.
