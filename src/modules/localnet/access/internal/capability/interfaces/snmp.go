package interfaces

import (
	"context"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/access/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/localnet/collect"
	"go.aledante.io/FlowSeer/src/modules/localnet/snmpmap"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// ErrCodeInterfaceNotFound identifies an SNMP read that found no interface
// by the requested name among the interfaces the device reported.
var ErrCodeInterfaceNotFound = errs.NewCode("interfaces/not-found")

// ReadSNMP reads one interface's description, admin status, and oper
// status over SNMP and reports it as an InterfaceObservation.
// InterfaceMapper applies to every device (it declares no
// SysObjectIDPrefixes), so this walks ifTable/ifXTable/ifStackTable
// directly through [collect.Read] rather than a full [collect.Collector]
// cycle — the same read [snmpmap.Interfaces] does.
//
// The returned observation's Provenance is always unset: snmpmap and
// collect know nothing about bindings, edges, or firmware fingerprints, so
// the caller (the capability handler in adapter.go) fills Provenance in
// once it has decided which route answered.
//
// Completeness is COMPLETE only when the matched interface has an observed
// description, admin status, and oper status. An unobserved ifAlias column
// is PARTIAL, never an empty description: only an interface whose ifAlias
// was observed and empty reports an empty description.
func ReadSNMP(ctx context.Context, sess snmp.Session, name string) (*accessv1.InterfaceObservation, error) {
	snap := collect.Read(ctx, sess, snmpmap.InterfaceMapper)

	ifaces, err := snmpmap.InterfacesFromSnapshot(snap)
	if err != nil {
		return nil, errs.Wrap(err, "map interfaces")
	}

	var iface *interfacev1.Interface

	for _, candidate := range ifaces {
		if candidate.GetName() == name {
			iface = candidate

			break
		}
	}

	if iface == nil {
		return nil, errs.New().Code(ErrCodeInterfaceNotFound).
			Attr("interface_name", name).
			Msgf("no interface named %q", name)
	}

	obs := &accessv1.InterfaceObservation{}
	obs.SetInterfaceName(name)

	complete := iface.HasDescription() && iface.HasAdminStatus() && iface.HasOperStatus()
	if complete {
		obs.SetDescription(iface.GetDescription())
		obs.SetAdminStatus(iface.GetAdminStatus())
		obs.SetOperStatus(iface.GetOperStatus())
		obs.SetCompleteness(accessv1.Completeness_COMPLETENESS_COMPLETE)
	} else {
		obs.SetCompleteness(accessv1.Completeness_COMPLETENESS_PARTIAL)
	}

	return obs, nil
}
