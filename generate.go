// Package flowseer hosts the repository's MIB generation entrypoint.
//
// Run go generate . from the repository root to regenerate SNMP MIB
// bindings under generated/go/mib from mibgen.yaml.
package flowseer

//go:generate go run ./src/protocol/snmp/cmd/mibgen -config ./mibgen.yaml
