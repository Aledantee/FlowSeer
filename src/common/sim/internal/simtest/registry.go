package simtest

// RegisterBaselineCases populates registry with the baseline cases.
func RegisterBaselineCases(registry *Registry) {
	registry.MustRegister(caseShadowingMSTModel(true))
	registry.MustRegister(caseShadowingMSTModel(false))
	registry.MustRegister(casePlanningPortVLANChange())
	registry.MustRegister(casePlanningCandidateForkDiverges())
	registry.MustRegister(caseShadowingPartialUnknownPort())
	registry.MustRegister(caseTroubleshootingUnicastForwarding())
}

// RegisterPhysicalTopologyCases populates registry with the cases covering tri-state physical
// facts, topology-derived link and port state, protocol link-state input, and host acceptance.
func RegisterPhysicalTopologyCases(registry *Registry) {
	registry.MustRegister(caseTopologyShadowingUnresolvedTransceiver())
	registry.MustRegister(caseTopologyShadowingUnresolvedTransceiverKnownDelivery())
	registry.MustRegister(caseTopologyShadowingUncabledPortDefiniteDrop())
	registry.MustRegister(caseTopologyShadowingUnreportedNegotiation())
	registry.MustRegister(caseTopologyShadowingUnknownUplinkSTP())
	registry.MustRegister(caseTroubleshootingHostRejectsForeignUnicast())
}

// RegisterLAGMulticastCases populates registry with the cases covering LAG
// bucket persistence across a member fault, active-backup failback, and
// per-source multicast admission and leave timing.
func RegisterLAGMulticastCases(registry *Registry) {
	registry.MustRegister(casePlanningLAGMemberFaultKeepsSurvivingFlows())
	registry.MustRegister(caseTroubleshootingActiveBackupNoFailback())
	registry.MustRegister(caseTroubleshootingSSMRejectsUnjoinedSource())
	registry.MustRegister(caseTroubleshootingLeaveLastMemberQuery())
}

// RegisterRoutingCases populates registry with the cases covering the equal-cost
// candidate set a lookup records and the withdrawal of an unresolvable static route.
func RegisterRoutingCases(registry *Registry) {
	registry.MustRegister(casePlanningECMPCandidatesRecorded())
	registry.MustRegister(caseTroubleshootingNeighborResolutionPending())
	registry.MustRegister(caseTroubleshootingRecursiveRouteNotInstalled())
}

// DefaultRegistry returns a newly allocated registry containing every admitted case.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	RegisterBaselineCases(r)
	RegisterPhysicalTopologyCases(r)
	RegisterLAGMulticastCases(r)
	RegisterSTPCases(r)
	RegisterLoopProtectCases(r)
	RegisterRoutingCases(r)
	RegisterScenarioCases(r)
	RegisterMDNSCases(r)
	RegisterReflectorCases(r)
	RegisterFilterCases(r)
	r.MustRegister(casePlanningOversubscribedTrunkStatedBuffer())
	r.MustRegister(casePlanningOversubscribedTrunkUnstatedBuffer())
	r.MustRegister(casePlanningPolicedStream())
	return r
}
