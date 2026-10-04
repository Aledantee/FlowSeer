package vswitch_test

import (
	"bytes"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/lacp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestMarkerResponderAtSwitch(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{2, 0, 0, 0, 0, 10}
	system := netaddr.MAC{2, 0, 0, 0, 0, 20}
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}))
	sw := mustSwitch(t, vswitch.Config{MAC: mac, Ports: ports, LAG: &lag.Config{LAGs: map[string]lag.LAG{"lag1": {LACP: lag.LACPConfig{Mode: lag.Passive, SystemID: system}}}}})
	now := time.Unix(1700000000, 0)
	sw.Start(now)
	if info := sw.MemberInfo("1/1/1"); info.Attached || info.Enabled || info.Status != lag.Expired {
		t.Fatalf("member = %+v, want Expired, detached and disabled", info)
	}
	if emissions := sw.Drain(); len(emissions) != 0 {
		t.Fatalf("passive startup emissions = %+v, want none", emissions)
	}
	// The requester fields follow IEEE P802.1AX-REV/D4.54 Figure 6-27 and
	// Wireshark's packet-marker.c. Version and reserved octets are opaque.
	payload := make([]byte, 110)
	copy(payload, []byte{2, 2, 1, 16, 0, 0x12, 0, 4, 0x96, 0x1f, 0x50, 0x6a, 1, 2, 3, 4, 0xab, 0xcd})
	for i := 20; i < len(payload); i++ {
		payload[i] = byte(i + 108)
	}
	request := ethernet.Frame{Dst: lacp.GroupAddress, Src: netaddr.MAC{0xde, 0xad, 0xbe, 0xef, 0, 1}, EtherType: ethernet.EtherTypeSlowProtocols, Payload: payload}
	before := sw.MemberInfo("1/1/1")
	if res := sw.Peek(now, "1/1/1", request); res.Outcome != trace.Consumed || len(res.Steps) == 0 || res.Steps[0].RuleID != "lag.marker.respond" {
		t.Fatalf("Marker Peek = %+v, want Consumed under lag.marker.respond", res)
	}
	if emissions := sw.Drain(); len(emissions) != 0 || sw.MemberInfo("1/1/1") != before {
		t.Fatalf("Marker Peek emissions = %+v, member = %+v, want no mutation", emissions, sw.MemberInfo("1/1/1"))
	}
	res := sw.Forward(now, "1/1/1", request)
	if res.Outcome != trace.Consumed || len(res.Steps) == 0 || res.Steps[0].RuleID != "lag.marker.respond" || len(res.Egress) != 0 {
		t.Fatalf("Marker result = %+v, want consumed without relay egress", res)
	}
	if facts := res.Steps[0].Outputs; len(facts) != 1 || facts[0].TypeID() != "lag.marker_response" || facts[0].Canonical() != "requester=00120004961f506a01020304;response_source=\"02:00:00:00:00:14\"" {
		t.Fatalf("Marker facts = %+v, want requester transaction and response source", facts)
	}
	emissions := sw.Drain()
	if len(emissions) != 1 || emissions[0].Port != "1/1/1" || !emissions[0].Protocol {
		t.Fatalf("Marker emissions = %+v, want one on 1/1/1", emissions)
	}
	want := bytes.Clone(payload)
	want[2] = 2
	response := emissions[0].Frame
	if response.Src != system || response.Dst != lacp.GroupAddress || response.EtherType != ethernet.EtherTypeSlowProtocols || !bytes.Equal(response.Payload, want) {
		t.Fatalf("Marker response = %+v, want source %s and payload %x", response, system, want)
	}
	if sw.MemberInfo("1/1/1") != before || request.Payload[2] != 1 {
		t.Fatal("Marker response changed LACP state or request payload")
	}

	for _, tc := range []struct {
		name    string
		payload []byte
	}{
		{name: "short", payload: []byte{2}},
		{name: "response", payload: want},
		{name: "bad length", payload: bytes.Clone(payload)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "bad length" {
				tc.payload[3] = 15
			}
			bad := request
			bad.Payload = tc.payload
			beforeBad := sw.MemberInfo("1/1/1").BadLACPDUs
			if peek := sw.Peek(now, "1/1/1", bad); peek.Outcome != trace.Dropped || peek.Reason != lag.ReasonUnsupportedLACPDU || sw.MemberInfo("1/1/1").BadLACPDUs != beforeBad {
				t.Fatalf("refused Marker Peek = %+v, want unsupported-lacpdu without counter change", peek)
			}
			badRes := sw.Forward(now, "1/1/1", bad)
			if badRes.Outcome != trace.Dropped || badRes.Reason != lag.ReasonUnsupportedLACPDU || len(badRes.Steps) == 0 || badRes.Steps[0].RuleID != lag.RuleLACPDUUnsupported {
				t.Fatalf("refused Marker = %+v, want unsupported-lacpdu drop", badRes)
			}
			if got := sw.MemberInfo("1/1/1").BadLACPDUs; got != beforeBad+1 {
				t.Fatalf("BadLACPDUs = %d, want %d", got, beforeBad+1)
			}
			if emissions := sw.Drain(); len(emissions) != 0 {
				t.Fatalf("refused Marker emissions = %+v, want none", emissions)
			}
		})
	}
}
