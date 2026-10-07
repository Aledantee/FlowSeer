---
title: A Streaming Connect Handler Must Validate the Messages It Trusts
date: 2026-09-30
last_verified: 2026-10-04
category: conventions
module: src/services/device/internal/host
problem_type: convention
component: service_layer
severity: high
applies_when:
  - "Adding or changing a protovalidate rule on a message received by a Connect streaming RPC"
  - "Changing a streaming handler or its interceptor chain to trust a received field"
  - "Mounting one Connect service for operator and edge callers with different validation obligations"
  - "Reviewing a stream test that expects interceptor or handler refusal for an invalid protobuf payload"
related_components: [api_layer, data_model, testing_framework]
tags: [connect, streaming-rpc, protovalidate, validation, service-boundary]
---

# A streaming Connect handler must validate the messages it trusts

Unary and streaming Connect procedures need different validation seams. The
base `ValidatingInterceptor` validates unary requests and passes edge-facing
streams through. The operator chain uses `OperatorValidatingInterceptor`, which
wraps `Receive` and validates each message before the handler reads it
(`src/services/device/internal/host/validation.go:46-93`).

Edge handlers still own validation for fields they trust from their streams.
`UploadCapture` validates each packet chunk before it derives session or edge
state from the chunk (`src/services/device/internal/captureapi/edge_service.go:545-585`):

```go
if err := protovalidate.Validate(chunk); err != nil {
	s.failStream(ctx, tenantID, sessionID, "capture chunk fails its schema rules")
	return nil, connecterr.WrapAs(connect.CodeInvalidArgument,
		"the capture chunk does not satisfy its schema rules",
		errs.From(err).Msg("capture chunk fails its schema rules"))
}
```

The host test sends an invalid operator stream message and verifies that the
handler is not entered. Its edge variant verifies that the base interceptor
passes the message through (`src/services/device/internal/host/validation_test.go:196-243`).
The upload test mounts the edge handler directly, sends invalid final and
non-final chunks, and expects `CodeInvalidArgument` plus a failed session
(`src/services/device/internal/captureapi/edge_service_test.go:882-945`).

## Evidence

- `src/services/device/internal/host/validation.go:46-60` validates unary
  requests and passes edge-facing streams through.
- `src/services/device/internal/host/validation.go:68-93` validates every
  message received by an operator stream.
- `src/services/device/internal/host/validation_test.go:201-242` distinguishes
  operator interceptor refusal from edge handler entry.
- `src/services/device/internal/captureapi/edge_service.go:570-581` validates
  each uploaded chunk before using its session and edge fields.
- `src/services/device/internal/captureapi/edge_service_test.go:928-942`
  asserts invalid streamed chunks return `CodeInvalidArgument` and fail the
  session.

## What this does not cover

This rule does not replace transport limits, assertion verification, or
authorization checks. Authentication, identity matching, and schema validation
answer separate questions. A stream that accepts no message fields still needs
the authentication and lifecycle checks its procedure requires.
