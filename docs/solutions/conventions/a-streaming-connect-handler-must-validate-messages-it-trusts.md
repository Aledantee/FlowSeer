---
title: A Streaming Connect Handler Must Validate the Messages It Trusts
date: 2026-09-30
last_verified: 2026-09-30
category: conventions
module: src/services/device/internal/host
problem_type: convention
component: service_layer
severity: high
applies_when:
  - "Adding or changing a protovalidate rule on a message received by a Connect streaming RPC"
  - "Changing a streaming handler to trust a field that the service interceptor does not inspect"
  - "Reviewing a streaming RPC test that sends an invalid protobuf payload and expects handler refusal"
related_components: [api_layer, data_model, testing_framework]
tags: [connect, streaming-rpc, protovalidate, validation, service-boundary]
---

# A streaming Connect handler must validate the messages it trusts

Connect services can install one interceptor list for unary and streaming
procedures. The list does not give streaming handlers unary validation. The
host's `WrapUnary` calls `validateMessage`, while
`WrapStreamingHandler` returns the next handler unchanged
(`src/services/device/internal/host/validation.go:46-75`).

Every streaming handler therefore owns the schema check for each message it
accepts into state or passes to a store. Authentication, identity matching, and
schema validation answer different questions. Keep all three checks before the
message can change service state.

`UploadCapture` applies the rule as soon as it extracts a chunk. It validates
the chunk before resolving its session or comparing its edge identity
(`src/services/device/internal/captureapi/edge_service.go:535-550`):

```go
chunk := msg.GetChunk()
if chunk == nil {
	continue
}
if err := protovalidate.Validate(chunk); err != nil {
	s.failStream(ctx, sessionID, "capture chunk fails its schema rules")
	return nil, connecterr.WrapAs(
		connect.CodeInvalidArgument,
		"the capture chunk does not satisfy its schema rules",
		errs.From(err).Msg("capture chunk fails its schema rules"),
	)
}
```

The test mounts the generated upload handler directly and sends invalid final
and non-final chunks. It expects `CodeInvalidArgument` and a failed session
(`src/services/device/internal/captureapi/edge_service_test.go:809-870`). This
test reaches the handler without the host's validating interceptor because
`newUploadServer` mounts the generated handler itself
(`edge_service_test.go:902-912`). It therefore proves that the stream handler,
not an incidental outer layer, owns this safety boundary.

## Evidence

- `src/services/device/internal/host/validation.go:46-52` calls
  `validateMessage` for unary requests.
- `src/services/device/internal/host/validation.go:55-75` says streaming
  validation is absent from the interceptor and returns `next` unchanged.
- `src/services/device/internal/captureapi/edge_service.go:535-546` validates
  each uploaded chunk before the handler reads its session fields.
- `src/services/device/internal/captureapi/edge_service_test.go:852-868`
  asserts invalid streamed chunks return `CodeInvalidArgument` and fail the
  session.

## What this does not cover

This rule does not replace transport limits, assertion verification, or
authorization checks. Unary procedures remain covered by the validating
interceptor. A stream that accepts no message fields still needs whatever
authentication and lifecycle checks its procedure requires.
