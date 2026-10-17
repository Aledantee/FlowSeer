// Package l2 provides L2 switching behaviors for the netpen runner.
// [Behaviors] lists them for the shipped catalog. The runner supplies the
// legs, the target, the emitter, the teardown handle, and the sysctl seam
// and applies the catalog gates. Each behavior reads the VLANs, domain,
// victim, and gateway it acts on from the target and its own MAC from the
// attack leg. A value that only has to be well-formed, such as a flood
// frame's stamped MAC or an MVRP VLAN id, stays a constant in the behavior.
//
// Each behavior reads the wire for the effect its probe should have, with
// the predicates in observe.go, and returns confirmed or resisted from what
// it saw, or pending with the reason the leg cannot carry the observable.
// A behavior whose protocol speaks without a probe (a BPDU, a DTP frame, a
// VTP summary, an LLDP-MED advertisement) first reads the leg for what the
// segment already says and confirms only on a change from that. The armed
// restore steps each send the frame or write the setting their name says.
// VLAN enumeration passively reads the leg.
package l2
