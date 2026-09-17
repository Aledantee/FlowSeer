// Package interfaces is the interface capability's protocol-agnostic
// handler: it reads one interface's description, admin status, and oper
// status over whichever route completeness selects, verifies a mutation
// through a fresh read, and reports every result as a
// flowseer.device.access.v1.InterfaceObservation with a
// flowseer.model.inventory.v1.Provenance.
//
// snmp.go builds an observation through src/modules/localnet/collect and
// src/modules/localnet/snmpmap. completeness.go holds the typed
// Completeness, Freshness, and DelayedEffect values a route selector
// consumes, and the route-selection and conflict-detection functions built
// on them. adapter.go ties the two together and defines the ShellAdapter
// seam a firmware-specific shell mapping implements — the first is
// src/modules/localnet/access/internal/capability/fastiron, kept as a
// sibling package rather than a subpackage so this package never imports a
// firmware.
//
// See docs/architecture/2026-09-05-verified-device-access-direction.md,
// "Route to the integration, choose the protocol locally", "Every write has
// an independent semantic verification", and "Capabilities are typed all the
// way down".
package interfaces
