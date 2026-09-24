package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/edge/netsimload"
	"go.aledante.io/FlowSeer/src/edge/netsimload/packetio"
	"go.aledante.io/FlowSeer/src/modules/capture/rawsocket"
)

func TestTransmitRejectsInterfaceConfigurationBeforeOpening(t *testing.T) {
	frame, err := (ethernet.Frame{Payload: make([]byte, netsimload.SignatureSize)}).Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	base := `{"version":1,"tx_interface":"tx0","rx_interface":"rx0","flows":[{"id":1,"frame_hex":"` + hex.EncodeToString(frame) + `","frames_per_second":1000,"count":1}]}`
	cases := []struct {
		name     string
		input    string
		resolver func(string) (netsimload.InterfaceInfo, error)
		want     string
	}{
		{name: "missing", input: strings.Replace(base, `"tx_interface":"tx0",`, ``, 1), want: "required"},
		{name: "equal", input: strings.Replace(base, `"rx_interface":"rx0"`, `"rx_interface":"tx0"`, 1), resolver: upResolver, want: "must differ"},
		{name: "down", input: base, resolver: func(name string) (netsimload.InterfaceInfo, error) { return netsimload.InterfaceInfo{Name: name}, nil }, want: "down"},
		{name: "unknown", input: base, resolver: func(name string) (netsimload.InterfaceInfo, error) {
			return netsimload.InterfaceInfo{}, errors.New("unknown " + name)
		}, want: "resolve interface"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opened := 0
			deps := netsimload.Dependencies{
				ResolveInterface: tc.resolver,
				OpenSender: func(string) (packetio.Sender, error) {
					opened++
					return nil, errors.New("sender opener called")
				},
				OpenReceiver: func(string) (rawsocket.Source, error) {
					opened++
					return nil, errors.New("receiver opener called")
				},
			}
			var output, diagnostics bytes.Buffer
			if code := run([]string{"transmit"}, strings.NewReader(tc.input), &output, &diagnostics, deps); code == 0 {
				t.Fatal("run returned success")
			}
			if !strings.Contains(diagnostics.String(), tc.want) {
				t.Fatalf("diagnostics = %q, want substring %q", diagnostics.String(), tc.want)
			}
			if output.Len() != 0 || opened != 0 {
				t.Fatalf("output=%q opened=%d, want no output and no opener", output.String(), opened)
			}
		})
	}
}

func TestTransmitRejectsInvalidStreamShapeBeforeOpening(t *testing.T) {
	input := `{"version":1,"tx_interface":"tx0","rx_interface":"rx0","flows":[{"id":1,"frame_hex":"0000000000000000000000000800","frames_per_second":1000,"count":1,"duration":"1s"}]}`
	var output, diagnostics bytes.Buffer
	if code := run([]string{"transmit"}, strings.NewReader(input), &output, &diagnostics, netsimload.Dependencies{}); code == 0 {
		t.Fatal("run returned success")
	}
	if !strings.Contains(diagnostics.String(), "exactly one of count or duration") {
		t.Fatalf("diagnostics = %q", diagnostics.String())
	}
	if output.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", output.String())
	}
}

func TestComparePreservesIssuesAndObservationLabels(t *testing.T) {
	input := `{"version":1,"destination":"host","simulator":{"status":"incomplete","issues":[{"code":"queue-full","status":"incomplete","scope":"node[\"sw1\"]","message":"queue bounded"}],"flows":[{"id":1,"offered":10,"delivered":{"host":8},"drops":{"queue-full":2},"lost":0,"unresolved":0,"rejected":0,"held":0,"copies":{},"latency":{"Min":0,"Max":0,"Sum":0,"Count":0},"status":"incomplete","issues":[]}]},"lab":{"flows":{"1":{"sent":10,"unique_received":8,"missing":2,"duplicates":0,"reordered":0,"malformed":0,"late_after_close":0,"latency":{"min":0,"max":0,"sum":0,"count":0}}},"malformed":0,"interface_drops":0}}`
	var output, diagnostics bytes.Buffer
	if code := run([]string{"compare"}, strings.NewReader(input), &output, &diagnostics, netsimload.Dependencies{}); code != 0 {
		t.Fatalf("run code = %d, diagnostics = %q", code, diagnostics.String())
	}
	if diagnostics.Len() != 0 || !strings.Contains(output.String(), `"queue-full"`) || !strings.Contains(output.String(), `"lab_unreceived": 2`) {
		t.Fatalf("stdout=%s stderr=%s", output.String(), diagnostics.String())
	}
}

func upResolver(name string) (netsimload.InterfaceInfo, error) {
	return netsimload.InterfaceInfo{Name: name, Up: true}, nil
}
