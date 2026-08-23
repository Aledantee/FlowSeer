// register.go exports the L2 behavior functions for the runner's
// [runner.Options.Behaviors] map. Each behavior is a thin function over the
// toolkit; durability metadata (class, legs, teardown) lives in the catalog
// (registered in catalog/registrations.go by U4) and is the single source
// the runner's gate consults.
//
// The catalog rows for these 11 behavior(+mode) pairs are already registered
// in catalog/registrations.go and verified by the oracle test
// (TestOracleDurabilityClassification). This file adds only the run functions;
// it does not re-register metadata (no second registration mechanism — KTD8).

package l2

import "go.aledante.io/FlowSeer/src/netpen/runner"

// Behaviors returns the L2 behavior map for [runner.Options.Behaviors].
// The keys are the attack names the catalog registered: dtp, doubletag,
// vlanenum, vlanhop, voicevlan, stproot, camflood, vtp, mvrp, portsteal,
// etherchannel. Mode-gated variants (e.g. portsteal --relay, dtp --keep-trunk)
// share the same behavior function; the function reads the mode from
// [runner.Deps.Entry] to select its code path.
func Behaviors() map[string]runner.Behavior {
	return map[string]runner.Behavior{
		"dtp":          RunDTP,
		"doubletag":    RunDoubleTag,
		"vlanenum":     RunVlanEnum,
		"vlanhop":      RunVlanHop,
		"voicevlan":    RunVoiceVLAN,
		"stproot":      RunSTPRoot,
		"camflood":     RunCAMFlood,
		"vtp":          RunVTP,
		"mvrp":         RunMVRP,
		"portsteal":    RunPortSteal,
		"etherchannel": RunEtherChannel,
	}
}
