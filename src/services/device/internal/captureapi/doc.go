// Package captureapi manages remote packet capture sessions and retained
// pcapng artifacts on central.
//
// Capture session metadata is stored in JetStream key-value under the
// captures bucket. Packet batches are appended into per-session pcapng files
// located under <StateDir>/captures/<session_id>.pcapng.
//
// The package implements two Connect services:
//   - EdgeService (CaptureEdgeServiceHandler): streams owed capture
//     assignments to edges and receives packet chunk upload streams
//     authenticated via in-stream SignedEdgeAssertions.
//   - OperatorService (CaptureServiceHandler): handles capture session CRUD,
//     bounds validation, live chunk tailing via an in-memory broadcaster,
//     and chunked pcapng artifact downloads.
package captureapi
