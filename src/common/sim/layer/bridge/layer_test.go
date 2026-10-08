package bridge_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func TestSetOperStatusRemovesPortFromFloodSet(t *testing.T) {
	ports := buildTestPorts(t, 3)
	br := mustNewBridge(t, bridge.Config{}, ports)

	frame := ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("test"),
	}
	resBefore := br.Forward(testTime0, "1/1/1", frame)
	if len(resBefore.Egress) != 2 {
		t.Fatalf("egress count before = %d, want 2", len(resBefore.Egress))
	}

	if err := br.SetOperStatus("1/1/2", port.Down); err != nil {
		t.Fatalf("SetOperStatus: %v", err)
	}

	resAfter := br.Forward(testTime0, "1/1/1", frame)
	if len(resAfter.Egress) != 1 || resAfter.Egress[0].Port != "1/1/3" {
		t.Fatalf("egress after SetOperStatus = %+v, want only 1/1/3", resAfter.Egress)
	}
}

func TestSetOperStatusRejectsInvalidStateWithoutMutation(t *testing.T) {
	ports := buildTestPorts(t, 3)
	frame := ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("test"),
	}

	for _, portName := range []string{"1/1/2", "missing"} {
		t.Run(portName, func(t *testing.T) {
			br := mustNewBridge(t, bridge.Config{}, ports)
			before := br.Forward(testTime0, "1/1/1", frame)

			err := br.SetOperStatus(portName, port.LinkState("invalid"))
			if err == nil {
				t.Fatal("SetOperStatus with invalid state succeeded, want error")
			}
			attrs := errs.Attributes(err)
			wantField := "ports." + portName + ".oper_status"
			var wantState any = port.LinkState("invalid")
			if portName == "missing" {
				wantField = "ports." + portName
				wantState = nil
			}
			if got := attrs["field"]; got != wantField {
				t.Errorf("field attribute = %v, want %s", got, wantField)
			}
			if got := attrs["oper_status"]; got != wantState {
				t.Errorf("oper_status attribute = %v, want %v", got, wantState)
			}
			if got := attrs["name"]; got != portName {
				t.Errorf("name attribute = %v, want %s", got, portName)
			}

			after := br.Forward(testTime0, "1/1/1", frame)
			if len(after.Egress) != len(before.Egress) {
				t.Fatalf("egress count after invalid update = %d, want unchanged %d", len(after.Egress), len(before.Egress))
			}
			for i := range before.Egress {
				if after.Egress[i].Port != before.Egress[i].Port || after.Egress[i].Dropped != before.Egress[i].Dropped {
					t.Errorf("egress after invalid update = %+v, want unchanged %+v", after.Egress, before.Egress)
					break
				}
			}
		})
	}
}
