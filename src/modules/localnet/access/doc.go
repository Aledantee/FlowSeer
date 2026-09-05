// Package access holds the local-network integration's device-access
// capabilities and the facade a host calls them through: typed Go that
// reads and mutates one device family over SNMP or SSH and reports the
// result as a flowseer.device.access.v1.InterfaceObservation.
//
// Each capability lives under access/internal/capability in its own
// directory (interfaces/ for the interface capability), pairing a
// protocol-agnostic handler with one shell adapter per firmware as a
// sibling directory (capability/fastiron/ is the first) — kept internal so
// a firmware adapter's types never leak past this package's facade. This package
// itself carries no mutation-intent submission, journal, or per-device
// lane: those belong to the central and edge processes that will call the
// facade (see
// docs/architecture/2026-09-05-verified-device-access-direction.md,
// decisions 3, 4, 9), not to the capability itself.
package access
