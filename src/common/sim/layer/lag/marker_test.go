package lag_test

import (
	"bytes"
	"reflect"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/lacp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
)

func TestMarkerResponderWithoutCollecting(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		mode lag.LACPMode
	}{
		{name: "Passive", mode: lag.Passive},
		{name: "Off", mode: lag.Off},
		{name: "Active", mode: lag.Active},
	} {
		t.Run(tc.name, func(t *testing.T) {
			system := mustMAC(t, "02:00:00:00:00:14")
			l := mustNewLAG(t, lag.Config{LAGs: map[string]lag.LAG{"lag1": {LACP: lag.LACPConfig{Mode: tc.mode, SystemID: system}}}}, lagTwoPortTable(t), mustMAC(t, "02:00:00:00:00:0a"))
			now := time.Unix(1700000000, 0)
			if tc.mode != lag.Off {
				l.LinkChange(now, "1/1/1", true)
			}
			before := l.PortInfo("1/1/1")
			if before.Enabled || before.Attached {
				t.Fatalf("member = %+v, want detached and disabled", before)
			}
			// IEEE P802.1AX-REV/D4.54 Figure 6-27 and packet-marker.c locate
			// the requester port, system, and transaction in octets 4 to 15.
			payload := make([]byte, 110)
			copy(payload, []byte{2, 2, 1, 16, 0, 0x12, 0, 4, 0x96, 0x1f, 0x50, 0x6a, 1, 2, 3, 4, 0xab, 0xcd})
			for i := 20; i < len(payload); i++ {
				payload[i] = byte(i + 108)
			}
			request := ethernet.Frame{Dst: lacp.GroupAddress, Src: netaddr.MAC{0xde, 0xad, 0xbe, 0xef, 0, 1}, EtherType: ethernet.EtherTypeSlowProtocols, Tags: []vlan.Tag{{VID: 7}}, Payload: payload}
			want := bytes.Clone(payload)
			want[2] = 2
			fx := l.ReceiveMarker(now, "1/1/1", request)
			if len(fx.Emissions) != 1 || fx.Emissions[0].Port != "1/1/1" {
				t.Fatalf("Marker emissions = %+v, want one on 1/1/1", fx.Emissions)
			}
			response := fx.Emissions[0].Frame
			if response.Src != system || response.Dst != lacp.GroupAddress || response.EtherType != request.EtherType || !bytes.Equal(response.Payload, want) || !reflect.DeepEqual(response.Tags, request.Tags) {
				t.Fatalf("Marker response = %+v, want source %s, request tags, and payload %x", response, system, want)
			}
			response.Payload[4]++
			response.Tags[0].VID++
			if request.Payload[4] != 0 || request.Tags[0].VID != 7 || l.PortInfo("1/1/1") != before || len(fx.Changed) != 0 {
				t.Fatal("Marker response mutated the request or protocol state")
			}
			request.Payload = want
			if fx := l.ReceiveMarker(now, "1/1/1", request); len(fx.Emissions) != 0 {
				t.Fatalf("Marker Response emissions = %+v, want none", fx.Emissions)
			}
			request.Payload = payload[:17]
			if fx := l.ReceiveMarker(now, "1/1/1", request); len(fx.Emissions) != 0 {
				t.Fatalf("short Marker emissions = %+v, want none", fx.Emissions)
			}
			request.Payload = payload
			if fx := l.ReceiveMarker(now, "unknown", request); len(fx.Emissions) != 0 {
				t.Fatalf("unknown member emissions = %+v, want none", fx.Emissions)
			}
		})
	}
}
