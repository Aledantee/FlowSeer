// Package full implements netpen's two orchestrator commands: `scan`
// (passive/active segment discovery with dual-segment observe) and `full`
// (the four-phase evidence-gated audit). Both are thin coordinators over
// the runner: they build [runner.Options], drive the runner, and stream
// findings into the output layer selected by the CLI.
//
// The orchestration contract is split across the files in this
// package:
//
//   - evidence.go: the typed [Evidence] map recon emits and both gate
//     surfaces consume.
//   - gates.go: the two gate surfaces (burst arming + follow-up
//     selection), ported verbatim from the baseline's l2l3-audit.
//   - full.go: the four-phase orchestration (recon → burst → follow-ups
//     → report).
//   - scan.go: the passive/active scan command.
package full

// Evidence is the typed evidence map the recon phase emits. Both gate
// surfaces — burst arming (phase 2) and follow-up selection (phase 3) —
// consume only this map; nothing reads recon state directly. The keys are
// the stable evidence identifiers:
//
//   - "ra6":   IPv6 Router Advertisements were observed in recon.
//   - "vlans": 802.1Q tags were observed on the wire.
//   - "voicevlan": a CDP voice-VLAN TLV leaked the voice VLAN id.
//   - "vtp-domain": a VTP domain name was observed.
//   - "vtp-rev": a VTP config revision was observed.
//   - "mvrp": MVRP/GVRP dynamic-VLAN traffic was observed.
//   - "macs": at least one host MAC was resolved (ARP sweep or recon).
//   - "vrids": at least one VRRP virtual-router id was detected.
//
// An empty Evidence (recon saw nothing) still fires the burst's
// unconditional core (baseline parity); the armed workers simply do
// not arm. The values are the raw evidence payloads (counts, sets, or
// typed structs) the gate surfaces and the report read; tests assert on
// them directly.
type Evidence map[string]any

// Evidence keys. These are the only strings the gate surfaces match on;
// they are exported so tests and the report can reference them stably.
const (
	EvRA6       = "ra6"        // bool: RA observed
	EvVLANs     = "vlans"      // []int: tagged VLAN ids observed
	EvVoiceVLAN = "voicevlan"  // int: CDP voice VLAN id
	EvVTPDomain = "vtp-domain" // string: VTP domain name
	EvVTPRev    = "vtp-rev"    // int: VTP config revision
	EvMVRP      = "mvrp"       // bool: MVRP traffic observed
	EvMACs      = "macs"       // []string: resolved MAC addresses
	EvVRIDs     = "vrids"      // []int: detected VRRP vrids
	EvWatchLeg  = "watch-leg"  // bool: a watch leg is attached
)

// Has reports whether the named evidence key is present and (for the
// boolean-shaped keys) truthy. A key present but zero-valued (e.g. an
// empty slice) reports false, matching the gate surfaces' "presence"
// semantics: an empty VLAN set does not arm vlanhop.
func (e Evidence) Has(key string) bool {
	v, ok := e[key]
	if !ok {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case int:
		return t != 0
	case string:
		return t != ""
	case []int:
		return len(t) > 0
	case []string:
		return len(t) > 0
	default:
		return v != nil
	}
}

// VLANs returns the observed VLAN ids, or nil when none were observed.
func (e Evidence) VLANs() []int {
	if v, ok := e[EvVLANs].([]int); ok {
		return v
	}
	return nil
}

// VoiceVLAN returns the CDP voice VLAN id, or 0 when none was observed.
func (e Evidence) VoiceVLAN() int {
	if v, ok := e[EvVoiceVLAN].(int); ok {
		return v
	}
	return 0
}

// VTPDomain returns the VTP domain name, or "" when none was observed.
func (e Evidence) VTPDomain() string {
	if v, ok := e[EvVTPDomain].(string); ok {
		return v
	}
	return ""
}

// VTPRev returns the VTP config revision, or -1 when none was observed.
func (e Evidence) VTPRev() int {
	if v, ok := e[EvVTPRev].(int); ok {
		return v
	}
	return -1
}

// MACs returns the resolved MAC addresses, or nil when none were resolved.
func (e Evidence) MACs() []string {
	if v, ok := e[EvMACs].([]string); ok {
		return v
	}
	return nil
}

// VRIDs returns the detected VRRP virtual-router ids, or nil when none
// were detected.
func (e Evidence) VRIDs() []int {
	if v, ok := e[EvVRIDs].([]int); ok {
		return v
	}
	return nil
}
