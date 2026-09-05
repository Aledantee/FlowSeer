package interfaces_test

import (
	"context"
	"testing"

	"go.aledante.io/FlowSeer/generated/go/mib/ifmib"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

var ifEntry = snmp.MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1)

// ifRow is a minimal ifTable row: ifIndex idx, ifDescr descr (also its
// name, since no ifXTable ifName is served), ifType ethernetCsmacd,
// ifAdminStatus and ifOperStatus both up.
func ifRow(idx uint32, descr string) []vbFixture {
	return []vbFixture{
		integerVar(ifEntry, 1, idx, int32(idx)),
		stringVar(ifEntry, 2, idx, []byte(descr)),
		integerVar(ifEntry, 3, idx, 6),
		integerVar(ifEntry, 7, idx, int32(ifmib.IfAdminStatusValueUp)),
		integerVar(ifEntry, 8, idx, int32(ifmib.IfOperStatusValueUp)),
	}
}

func TestReadSNMP_Complete(t *testing.T) {
	vbs := append(ifRow(1, "ethernet 1/1/1"), stringVar(ifXEntry, 18, 1, []byte("uplink to core")))

	obs, err := interfaces.ReadSNMP(context.Background(), &fakeSession{vbs: vbs}, "ethernet 1/1/1")
	if err != nil {
		t.Fatalf("ReadSNMP: %v", err)
	}

	if got := obs.GetCompleteness(); got != accessv1.Completeness_COMPLETENESS_COMPLETE {
		t.Errorf("completeness = %v, want COMPLETE", got)
	}

	if got := obs.GetDescription(); got != "uplink to core" {
		t.Errorf("description = %q, want %q", got, "uplink to core")
	}

	if got := obs.GetAdminStatus(); got != interfacev1.AdminStatus_ADMIN_STATUS_UP {
		t.Errorf("admin_status = %v, want ADMIN_STATUS_UP", got)
	}

	if got := obs.GetOperStatus(); got != interfacev1.OperStatus_OPER_STATUS_UP {
		t.Errorf("oper_status = %v, want OPER_STATUS_UP", got)
	}
}

func TestReadSNMP_UnobservedDescriptionIsPartial(t *testing.T) {
	// No ifAlias fixture: ifXTable's ifAlias column is never observed, so
	// the interface has no description at all, not an empty one.
	obs, err := interfaces.ReadSNMP(context.Background(), &fakeSession{vbs: ifRow(1, "ethernet 1/1/1")}, "ethernet 1/1/1")
	if err != nil {
		t.Fatalf("ReadSNMP: %v", err)
	}

	if got := obs.GetCompleteness(); got != accessv1.Completeness_COMPLETENESS_PARTIAL {
		t.Errorf("completeness = %v, want PARTIAL", got)
	}

	if obs.HasDescription() {
		t.Error("a PARTIAL observation from an unobserved ifAlias should not set description")
	}
}

func TestReadSNMP_ObservedEmptyDescriptionIsComplete(t *testing.T) {
	// ifAlias observed and empty: a real, reportable empty description.
	vbs := append(ifRow(1, "ethernet 1/1/1"), stringVar(ifXEntry, 18, 1, []byte("")))

	obs, err := interfaces.ReadSNMP(context.Background(), &fakeSession{vbs: vbs}, "ethernet 1/1/1")
	if err != nil {
		t.Fatalf("ReadSNMP: %v", err)
	}

	if got := obs.GetCompleteness(); got != accessv1.Completeness_COMPLETENESS_COMPLETE {
		t.Errorf("completeness = %v, want COMPLETE", got)
	}

	if got := obs.GetDescription(); got != "" {
		t.Errorf("description = %q, want empty", got)
	}
}

func TestReadSNMP_InterfaceNotFound(t *testing.T) {
	vbs := append(ifRow(1, "ethernet 1/1/1"), ifRow(2, "ethernet 1/1/2")...)

	_, err := interfaces.ReadSNMP(context.Background(), &fakeSession{vbs: vbs}, "ethernet 1/1/3")
	if err == nil {
		t.Fatal("ReadSNMP did not error for an unknown interface name")
	}
}
