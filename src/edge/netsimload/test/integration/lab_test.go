//go:build netsimload_lab

package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/stream"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/edge/netsimload"
	"go.aledante.io/FlowSeer/src/modules/capture/rawsocket"
)

const switchAddress = "172.16.0.6:22"

type labConfig struct {
	txInterface string
	rxInterface string
	txPort      string
	rxPort      string
	vlan        vlan.ID
	txMAC       netaddr.MAC
	rxMAC       netaddr.MAC
	txSpeedBPS  uint64
	rxSpeedBPS  uint64
}

func readLabConfig(getenv func(string) string) (labConfig, bool, error) {
	names := []string{
		"NETSIMLOAD_LAB_TX_INTERFACE", "NETSIMLOAD_LAB_RX_INTERFACE",
		"NETSIMLOAD_LAB_TX_PORT", "NETSIMLOAD_LAB_RX_PORT",
		"NETSIMLOAD_LAB_VLAN", "NETSIMLOAD_LAB_TX_MAC", "NETSIMLOAD_LAB_RX_MAC",
		"NETSIMLOAD_LAB_TX_SPEED_BPS", "NETSIMLOAD_LAB_RX_SPEED_BPS",
	}
	values := make(map[string]string, len(names))
	configured := false
	for _, name := range names {
		values[name] = strings.TrimSpace(getenv(name))
		configured = configured || values[name] != ""
	}
	if !configured {
		return labConfig{}, false, nil
	}
	for _, name := range names {
		if values[name] == "" {
			return labConfig{}, true, fmt.Errorf("%s is required when lab configuration is set", name)
		}
	}
	config := labConfig{
		txInterface: values[names[0]], rxInterface: values[names[1]],
		txPort: values[names[2]], rxPort: values[names[3]],
	}
	if config.txInterface == config.rxInterface || config.txPort == config.rxPort {
		return labConfig{}, true, fmt.Errorf("transmit and receive interfaces and switch ports must differ")
	}
	for _, name := range []string{config.txPort, config.rxPort} {
		if !strings.HasPrefix(name, "1/1/") {
			return labConfig{}, true, fmt.Errorf("switch port %q must be an ICX7150 1/1/N port", name)
		}
		n, err := strconv.Atoi(strings.TrimPrefix(name, "1/1/"))
		if err != nil || n < 1 || n > 24 {
			return labConfig{}, true, fmt.Errorf("switch port %q must be between 1/1/1 and 1/1/24", name)
		}
	}
	vid, err := strconv.ParseUint(values[names[4]], 10, 12)
	if err != nil || vid < 1 || vid > 4094 {
		return labConfig{}, true, fmt.Errorf("NETSIMLOAD_LAB_VLAN must be between 1 and 4094")
	}
	config.vlan = vlan.ID(vid)
	for i, target := range []*netaddr.MAC{&config.txMAC, &config.rxMAC} {
		mac, err := net.ParseMAC(values[names[5+i]])
		if err != nil || len(mac) != 6 || mac[0]&1 != 0 {
			return labConfig{}, true, fmt.Errorf("%s must be a unicast six-octet MAC", names[5+i])
		}
		copy(target[:], mac)
	}
	if config.txMAC == config.rxMAC {
		return labConfig{}, true, fmt.Errorf("transmit and receive MACs must differ")
	}
	for i, target := range []*uint64{&config.txSpeedBPS, &config.rxSpeedBPS} {
		speed, err := strconv.ParseUint(values[names[7+i]], 10, 64)
		if err != nil || (speed != 10_000_000 && speed != 100_000_000 && speed != 1_000_000_000) {
			return labConfig{}, true, fmt.Errorf("%s must be an ICX7150 access-port speed (10M, 100M, or 1G)", names[7+i])
		}
		*target = speed
	}
	return config, true, nil
}

func checkInterface(name string, want netaddr.MAC) error {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return fmt.Errorf("interface %q: %w", name, err)
	}
	if iface.Flags&net.FlagUp == 0 {
		return fmt.Errorf("interface %q is down", name)
	}
	if len(iface.HardwareAddr) != 6 || !bytes.Equal(iface.HardwareAddr, want[:]) {
		return fmt.Errorf("interface %q MAC %s differs from configured %s", name, iface.HardwareAddr, net.HardwareAddr(want[:]))
	}
	return nil
}

func oneSwitchFabric(config labConfig) (*fabric.Fabric, error) {
	ports := port.NewBuilder()
	for _, name := range []string{config.txPort, config.rxPort} {
		ports.Add(port.Port{Name: name, Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	}
	table, err := ports.Build()
	if err != nil {
		return nil, err
	}
	ethernetProfile := func(speed uint64) phy.Ethernet {
		return phy.Ethernet{
			SupportedSpeedsBPS:       []uint64{speed},
			AutoNegotiationSupported: phy.CapabilitySupported,
			Setting:                  &phy.Setting{AutoNegotiation: true},
		}
	}
	return fabric.New(fabric.Config{
		Start: time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC),
		Switches: map[string]vswitch.Config{"LABSW06": {
			Ports: table,
			Bridge: &bridge.Config{VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{config.vlan: "lab"},
				Switchports: map[string]bridge.Switchport{
					config.txPort: {PVID: &config.vlan, Untagged: []vlan.ID{config.vlan}},
					config.rxPort: {PVID: &config.vlan, Untagged: []vlan.ID{config.vlan}},
				},
			}},
		}},
		Hosts: map[string]fabric.Host{
			"tx": {Address: config.txMAC, Ethernet: ethernetProfile(config.txSpeedBPS)},
			"rx": {Address: config.rxMAC, Ethernet: ethernetProfile(config.rxSpeedBPS)},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "tx"}, B: fabric.Endpoint{Node: "LABSW06", Port: config.txPort}, Medium: fabric.TwistedPair, TopSpeedBPS: config.txSpeedBPS},
			{A: fabric.Endpoint{Node: "LABSW06", Port: config.rxPort}, B: fabric.Endpoint{Node: "rx"}, Medium: fabric.TwistedPair, TopSpeedBPS: config.rxSpeedBPS},
		},
		PhyAssumption: &fabric.PhyAssumption{Medium: fabric.TwistedPair, Ethernet: phy.Ethernet{
			SupportedSpeedsBPS:       []uint64{config.txSpeedBPS, config.rxSpeedBPS},
			AutoNegotiationSupported: phy.CapabilitySupported,
			Setting:                  &phy.Setting{AutoNegotiation: true},
		}},
	})
}

type captureSpan struct {
	sync.Mutex
	first time.Time
	last  time.Time
}

func (span *captureSpan) record(at time.Time) {
	span.Lock()
	defer span.Unlock()
	if span.first.IsZero() {
		span.first = at
	}
	span.last = at
}

func (span *captureSpan) duration() time.Duration {
	span.Lock()
	defer span.Unlock()
	return span.last.Sub(span.first)
}

type timedSource struct {
	rawsocket.Source
	span *captureSpan
	ids  map[fabric.FlowID]struct{}
}

type offsetSource struct {
	stream.Source
	offset time.Duration
}

func (source offsetSource) Next() (time.Duration, ethernet.Frame, bool) {
	at, frame, ok := source.Source.Next()
	return at + source.offset, frame, ok
}

func (source offsetSource) Clone() stream.Source {
	return offsetSource{Source: source.Source.Clone(), offset: source.offset}
}

func (source timedSource) Receive(ctx context.Context) <-chan rawsocket.Frame {
	in := source.Source.Receive(ctx)
	out := make(chan rawsocket.Frame)
	go func() {
		defer close(out)
		for frame := range in {
			select {
			case out <- frame:
				if frame.Err == nil {
					if signature, err := netsimload.DecodeWireSignature(frame.Data); err == nil {
						if _, ok := source.ids[signature.FlowID]; ok {
							source.span.record(frame.CapturedAt)
						}
					}
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

func TestLabConfigRefusesPartialAndMalformedValues(t *testing.T) {
	if _, configured, err := readLabConfig(func(string) string { return "" }); configured || err != nil {
		t.Fatalf("unset config = configured %t, error %v; want unset without error", configured, err)
	}
	valid := map[string]string{
		"NETSIMLOAD_LAB_TX_INTERFACE": "eth1", "NETSIMLOAD_LAB_RX_INTERFACE": "eth2",
		"NETSIMLOAD_LAB_TX_PORT": "1/1/1", "NETSIMLOAD_LAB_RX_PORT": "1/1/2",
		"NETSIMLOAD_LAB_VLAN":   "10",
		"NETSIMLOAD_LAB_TX_MAC": "02:00:00:00:00:01", "NETSIMLOAD_LAB_RX_MAC": "02:00:00:00:00:02",
		"NETSIMLOAD_LAB_TX_SPEED_BPS": "1000000000", "NETSIMLOAD_LAB_RX_SPEED_BPS": "1000000000",
	}
	if _, configured, err := readLabConfig(func(name string) string { return valid[name] }); !configured || err != nil {
		t.Fatalf("complete config = configured %t, error %v; want valid", configured, err)
	}
	for _, tc := range []struct {
		name  string
		value string
	}{
		{"NETSIMLOAD_LAB_RX_INTERFACE", ""},
		{"NETSIMLOAD_LAB_VLAN", "4095"},
		{"NETSIMLOAD_LAB_TX_MAC", "01:00:00:00:00:01"},
		{"NETSIMLOAD_LAB_RX_SPEED_BPS", "0"},
		{"NETSIMLOAD_LAB_RX_INTERFACE", "eth1"},
		{"NETSIMLOAD_LAB_RX_PORT", "1/1/1"},
		{"NETSIMLOAD_LAB_RX_MAC", "02:00:00:00:00:01"},
	} {
		_, configured, err := readLabConfig(func(name string) string {
			if name == tc.name {
				return tc.value
			}
			return valid[name]
		})
		if !configured || err == nil {
			t.Errorf("%s=%q = configured %t, error %v; want error", tc.name, tc.value, configured, err)
		}
	}
}

func TestOneSwitchFabricDeliversClonedSource(t *testing.T) {
	config := labConfig{
		txPort: "1/1/1", rxPort: "1/1/2", vlan: 10,
		txMAC: netaddr.MAC{2, 0, 0, 0, 0, 1}, rxMAC: netaddr.MAC{2, 0, 0, 0, 0, 2},
		txSpeedBPS: 1_000_000_000, rxSpeedBPS: 1_000_000_000,
	}
	lab, err := oneSwitchFabric(config)
	if err != nil {
		t.Fatalf("build one-switch fabric: %v", err)
	}
	spec := stream.Spec{
		Frame: ethernet.Frame{Src: config.txMAC, Dst: config.rxMAC, EtherType: ethernet.EtherType(0x88b5), Payload: make([]byte, 90)},
		Rate:  stream.Rate{FramesPerSecond: 1000}, Count: 2,
	}
	source, err := spec.Source()
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	if err := lab.AttachStream(fabric.StreamAttachment{Origin: fabric.Endpoint{Node: "tx"}, Source: source.Clone(), Flow: 1, Retention: fabric.RetainAggregate}); err != nil {
		t.Fatalf("attach source: %v", err)
	}
	if result := lab.Run(100); result.Stop != fabric.StopQueueDrained {
		t.Fatalf("simulator stop = %s, error %v; want %s", result.Stop, result.Err, fabric.StopQueueDrained)
	}
	stats := lab.Flows()[1]
	if stats.Offered != 2 || stats.Delivered["rx"] != 2 {
		t.Errorf("flow offered %d, delivered to rx %d; want 2 and 2", stats.Offered, stats.Delivered["rx"])
	}
}

func TestInterleavedSourcesAlternateEveryMillisecond(t *testing.T) {
	spec := stream.Spec{
		Frame: ethernet.Frame{Payload: make([]byte, 90)},
		Rate:  stream.Rate{FramesPerSecond: 500}, Count: 2,
	}
	base, err := spec.Source()
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	first := offsetSource{Source: base.Clone()}
	second := offsetSource{Source: base.Clone(), offset: time.Millisecond}
	for i, source := range []stream.Source{first, second, first, second} {
		at, _, ok := source.Next()
		if !ok || at != time.Duration(i)*time.Millisecond {
			t.Fatalf("frame %d offset = %s, ok %t; want %s", i, at, ok, time.Duration(i)*time.Millisecond)
		}
	}
}

func TestICX7150Comparison(t *testing.T) {
	if testing.Short() {
		t.Skip("live switch comparison is disabled in short tests")
	}
	config, configured, err := readLabConfig(os.Getenv)
	if !configured {
		t.Skip("lab configuration is unset")
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oneSwitchFabric(config); err != nil {
		t.Fatalf("invalid lab fabric configuration: %v", err)
	}
	for _, nic := range []struct {
		name string
		mac  netaddr.MAC
	}{{config.txInterface, config.txMAC}, {config.rxInterface, config.rxMAC}} {
		if err := checkInterface(nic.name, nic.mac); err != nil {
			t.Fatal(err)
		}
	}
	connection, err := net.DialTimeout("tcp", switchAddress, 2*time.Second)
	if err != nil {
		t.Fatalf("LABSW06 %s unreachable; stopped before traffic: %v", switchAddress, err)
	}
	if err := connection.Close(); err != nil {
		t.Fatalf("close LABSW06 reachability connection: %v", err)
	}

	for _, tc := range []struct {
		name    string
		firstID fabric.FlowID
		counts  []int
	}{
		{name: "single", firstID: 1, counts: []int{10_000}},
		{name: "interleaved", firstID: 2, counts: []int{5_000, 5_000}},
	} {
		if !t.Run(tc.name, func(t *testing.T) {
			lab, err := oneSwitchFabric(config)
			if err != nil {
				t.Fatalf("build one-switch fabric: %v", err)
			}
			flows := make([]netsimload.FlowSource, 0, len(tc.counts))
			ids := make(map[fabric.FlowID]struct{}, len(tc.counts))
			for i, count := range tc.counts {
				id := tc.firstID + fabric.FlowID(i)
				ids[id] = struct{}{}
				spec := stream.Spec{
					Frame: ethernet.Frame{Src: config.txMAC, Dst: config.rxMAC, EtherType: ethernet.EtherType(0x88b5), Payload: make([]byte, 90)},
					Rate:  stream.Rate{FramesPerSecond: uint64(1000 / len(tc.counts))},
					Count: count,
				}
				if got := spec.Frame.WireOctets(); got != 128 {
					t.Fatalf("flow %d wire octets = %d, want 128", id, got)
				}
				source, err := spec.Source()
				if err != nil {
					t.Fatalf("flow %d source: %v", id, err)
				}
				source = offsetSource{Source: source, offset: time.Duration(i) * time.Millisecond}
				if err := lab.AttachStream(fabric.StreamAttachment{Origin: fabric.Endpoint{Node: "tx"}, Source: source.Clone(), Flow: id, Retention: fabric.RetainAggregate}); err != nil {
					t.Fatalf("attach flow %d: %v", id, err)
				}
				flows = append(flows, netsimload.FlowSource{ID: id, Source: source.Clone()})
			}
			if result := lab.Run(200_000); result.Stop != fabric.StopQueueDrained {
				t.Fatalf("simulator stop = %s, error %v; want %s", result.Stop, result.Err, fabric.StopQueueDrained)
			}

			span := &captureSpan{}
			observation, err := netsimload.RunWith(context.Background(), netsimload.Config{
				TXInterface: config.txInterface, RXInterface: config.rxInterface,
				Drain: 250 * time.Millisecond, Flows: flows,
			}, netsimload.Dependencies{OpenReceiver: func(name string) (rawsocket.Source, error) {
				receiver, err := rawsocket.OpenLocalInterface(name, false, nil)
				if err != nil {
					return nil, err
				}
				return timedSource{Source: receiver, span: span, ids: ids}, nil
			}})
			if err != nil {
				t.Fatalf("live run stopped: %v", err)
			}
			if observation.InterfaceDrops != 0 {
				t.Errorf("capture interface drops = %d, want zero", observation.InterfaceDrops)
			}
			for i, count := range tc.counts {
				id := tc.firstID + fabric.FlowID(i)
				got := observation.Flows[id]
				if got.Sent != uint64(count) {
					t.Errorf("flow %d sent = %d, want %d", id, got.Sent, count)
				}
				if got.UniqueReceived+got.Missing != got.Sent {
					t.Errorf("flow %d unique %d + missing %d != sent %d", id, got.UniqueReceived, got.Missing, got.Sent)
				}
				if got.UniqueReceived == 0 {
					t.Errorf("flow %d has no signed receptions", id)
				}
			}
			measured := span.duration()
			want := 9999 * time.Millisecond
			delta := measured - want
			if delta < 0 {
				delta = -delta
			}
			t.Logf("receive first-to-last span=%s scheduled=%s deviation=%s (%.3f%%)", measured, want, delta, float64(delta)/float64(want)*100)
			if delta > want/100 {
				t.Errorf("receive pacing deviation = %s, want <= %s", delta, want/100)
			}
			report := netsimload.NewReport(lab.Flows(), lab.Metadata(), observation, "rx")
			if len(report.Simulator.Issues) != len(lab.Metadata().Issues()) {
				t.Fatalf("report simulator issues = %d, want %d", len(report.Simulator.Issues), len(lab.Metadata().Issues()))
			}
			for i, issue := range lab.Metadata().Issues() {
				if reported := report.Simulator.Issues[i]; reported.Code != issue.Code.String() || reported.Scope != issue.Scope.String() || reported.Status != issue.Status.String() || reported.Message != issue.Message || !reflect.DeepEqual(reported.Evidence, issue.Evidence) {
					t.Errorf("report issue %d = %+v, want %s at %s", i, report.Simulator.Issues[i], issue.Code, issue.Scope)
				}
			}
			for _, row := range report.Simulator.Flows {
				wantIssues := lab.Flows()[row.ID].Metadata.Issues()
				for _, issue := range wantIssues {
					found := false
					for _, reported := range row.Issues {
						if reported.Code == issue.Code.String() && reported.Scope == issue.Scope.String() && reported.Status == issue.Status.String() && reported.Message == issue.Message && reflect.DeepEqual(reported.Evidence, issue.Evidence) {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("flow %d report omits issue %s at %s", row.ID, issue.Code, issue.Scope)
					}
				}
			}
			if err := report.WriteJSON(os.Stdout); err != nil {
				t.Fatalf("write comparison report: %v", err)
			}
		}) {
			return
		}
	}
}
