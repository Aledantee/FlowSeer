// Package FlowSeer anchors repository-wide code generation.
//
// It holds no runtime code and is imported by nothing. Its only purpose
// is to host the go:generate directive below, so that `go generate .`
// at the repository root regenerates the committed SNMP MIB bindings
// under generated/go/mib from mibgen.yaml.
package FlowSeer

//go:generate go run ./src/common/snmp/cmd/mibgen -config ./mibgen.yaml
