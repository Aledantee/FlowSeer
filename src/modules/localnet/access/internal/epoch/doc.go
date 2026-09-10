// Package epoch implements the route-independent identity probe. A device's
// firmware fingerprint is read directly over SNMP rather than through the
// interface capability's own route selection, so a probe can run even when
// every capability's evidence is stale or absent.
package epoch
