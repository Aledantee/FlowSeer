// Package edgebus provides central's embedded NATS hub, JetStream stores,
// and OTLP collector client. The hub has one data account, CENTRAL, and no
// edge-facing listener. The agent delivers ingest records and its own
// telemetry over authenticated Connect calls, buffered in separate segmented logs.
//
// See docs/architecture/2026-10-04-edge-delivery-over-connect-direction.md
// and this package's README for the delivery path and central stores.
package edgebus
