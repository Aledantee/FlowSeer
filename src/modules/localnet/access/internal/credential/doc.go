// Package credential defines the narrow interfaces this module owns over
// EdgeService's two device-credential RPCs — AcquireReadCredential and
// OpenDeviceSubmission — so the mutation state machine and the interface
// capability never import the generated Connect client directly and a test
// can inject a fake instead of a real EdgeService. Per
// docs/architecture/2026-09-05-verified-device-access-direction.md, decision
// 9, credentials never ride the local bus and never cross this module's own
// public API; they exist only behind these two interfaces and the adapter
// in connect_adapter.go that satisfies them against a real
// edgev1connect.EdgeServiceClient.
package credential
