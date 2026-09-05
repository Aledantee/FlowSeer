// Package epoch implements the identity probe the direction record's
// decision 7 requires to be independent of any specific capability's route
// evidence: a device's firmware fingerprint is read directly over SNMP
// rather than through the interface capability's own route selection, so a
// probe can run even when every capability's evidence is stale or absent.
package epoch
