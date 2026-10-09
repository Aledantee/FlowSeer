package fabric

import "go.aledante.io/FlowSeer/src/common/sim/analysis"

// ExportFabricProtocolIssues exposes internal state to external package tests.
func ExportFabricProtocolIssues(x *Fabric) *[]analysis.Issue { return &x.protocolIssues }
