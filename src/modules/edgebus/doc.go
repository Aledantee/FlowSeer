// Package edgebus is the NATS carrier between an edge and central, assembled
// by both hosts: central starts the hub (an embedded nats-server in operator
// mode with the JetStream stores the device service writes and a leaf
// listener the edges dial) and the forwarder that pushes what edges publish
// on to central's OpenTelemetry endpoint; the edge starts the leaf node (an
// embedded nats-server with its own JetStream domain and a bounded,
// file-backed buffer) and the loopback receiver its own telemetry runtime
// exports into. The leaf carries what the edge publishes and nothing the
// edge must act on: the agent's own logs, metrics, and traces today, and the
// device logs, traps, and change events the ingestion sources will add on
// the same buffer. Every decision between the two processes is a Connect
// call in integration/device/v1 and event/device/v1, never a subject here.
//
// See docs/architecture/2026-08-20-device-service-and-inventory-direction.md,
// "Edge attachment and enrollment", and this package's README for the
// subject layout, the credential an edge is minted, and the durability each
// server declares.
package edgebus
