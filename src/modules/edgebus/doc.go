// Package edgebus provides central's embedded NATS hub, JetStream stores,
// and OTLP collector client. The agent delivers ingest records and its own
// telemetry over authenticated Connect calls. It buffers them in separate
// segmented logs and joins no bus.
//
// The leaf, per-edge streams, and forwarder remain pending removal. The agent
// does not use them. Central still binds the leaf listener.
//
// See docs/architecture/2026-10-04-edge-delivery-over-connect-direction.md
// and this package's README for the delivery path and the retained bus.
package edgebus
