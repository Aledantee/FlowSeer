---
title: A Schema Bound on a Streaming Payload Is Documentation Unless a Test Enforces It
date: 2026-09-18
last_verified: 2026-09-18
category: conventions
module: src/modules/capture
problem_type: convention
component: data_model
severity: high
applies_when:
  - "Declaring or changing a protovalidate constraint on a message carried on a streaming RPC"
  - "Sizing batch constants, buffers, or chunk messages produced for a client-streaming or server-streaming RPC"
  - "A streaming message violates schema rules without being rejected on arrival, or fails mid-stream at large payload sizes"
related_components: [edge, service_layer, testing_framework]
tags: [protovalidate, streaming-rpc, connect, schema-bounds, batch-size, packet-capture]
---

# A Schema Bound on a Streaming Payload Is Documentation Unless a Test Enforces It

## The situation

In remote packet capture, `capture.Engine` batches packet records into slices
that the edge wraps into `CapturePacketChunk` messages on the `UploadCapture`
client stream. The engine configured `batchMaxRecords = 512`, intending to
keep each batch well under message limits so the host never has to split.

The schema in `spec/proto/flowseer/model/capture/v1/capture_chunk.proto:30`
declares a tighter cap:

```protobuf
repeated flowseer.net.capture.v1.PacketRecord packets = 3 [(buf.validate.field).repeated = {max_items: 256}];
```

Central sizes the upload stream's read bound for 256 packets of 65535 octets.
At the default 128-octet snap length, 512 packets fit within 64 KiB, far below
the transport frame ceiling. The stream delivered 512 records per chunk and
central accepted them unnoticed, which violated the schema bound on every full
batch. At a wide snap length, 512 packets exceeded 32 MiB and central refused
the connection mid-capture.

## What is true, and why

Server-level validation interceptors in Connect wrap unary RPCs. Streaming RPCs
pass messages through without running protovalidate on receive:

```go
// src/services/device/internal/host/validation.go:73-75
func (v validatingInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}
```

Streaming handlers check routing and authentication fields against internal
state (resolving session references and validating signed assertions) rather
than evaluating schema constraints over every stream message.

Because stream messages bypass interceptor validation, schema rules such as
`max_items`, `max_len`, or numerical ranges do not defend the service
boundary. An oversized batch or invalid payload crosses the wire without
refusal until a secondary limit, such as a transport byte ceiling or an
incompatible store write, halts execution.

A schema constraint on a streamed message is documentation unless an executable
test compares the producer's payload against protovalidate.

## How to apply it

Protect streaming payload bounds in two places:

1. Pin producer batch constants and buffer sizes directly to the schema's
   declared cap.
2. Add an explicit test that builds the message a full batch produces and runs
   `protovalidate.Validate`.

```go
// src/modules/capture/engine_test.go:586-615
func TestBatchCapFitsTheChunkSchema(t *testing.T) {
	packets := make([]*capturev1.PacketRecord, batchMaxRecords)
	for i := range packets {
		rec := &capturev1.PacketRecord{}
		rec.SetSequence(uint64(i))
		rec.SetCapturedAt(timestamppb.New(time.Unix(0, 0)))
		rec.SetOriginalLength(64)
		rec.SetData([]byte{0x01})
		packets[i] = rec
	}

	chunk := modelcapturev1.CapturePacketChunk_builder{
		Session:       testSessionRef,
		FirstSequence: proto.Uint64(0),
		Packets:       packets,
	}.Build()

	if err := protovalidate.Validate(chunk); err != nil {
		t.Fatalf("a full batch does not fit CapturePacketChunk: %v", err)
	}
}
```

The Go compiler cannot check protobuf options against Go constants across
package boundaries. The test prevents the producer constant and the schema
rule from drifting apart.

## Evidence

- Schema repeated item constraint: `spec/proto/flowseer/model/capture/v1/capture_chunk.proto:30`.
- Streaming interceptor skipping validation: `src/services/device/internal/host/validation.go:52-75`.
- Producer constant aligned to schema cap: `src/modules/capture/engine.go:26-31`.
- Conformance test verifying batch fits schema: `src/modules/capture/engine_test.go:586-615`.
- Mutation caught by test: mutating `batchMaxRecords` from 256 to 257 fails `TestBatchCapFitsTheChunkSchema` with `packets: must contain no more than 256 item(s)`.

## What this does not cover

Unary RPCs do not need producer-side schema conformance tests; `ValidatingInterceptor`
enforces protovalidate rules at server ingress before handlers run. This
pattern also does not replace transport-level frame sizing configured for
worst-case byte bounds. It is distinct from [documenting intentional schema deviations](document-intentional-schema-deviations-with-comment-and-test.md),
which governs rules relaxed for forward compatibility rather than unvalidated
stream boundaries.
