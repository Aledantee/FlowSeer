package conformance

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	flowv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/flow/v1"
	packetv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/packet/v1"
)

// TestSflowRules holds the sFlow rows to a collector address and to the
// values the MIB does not use for "disabled": a receiver index, sampling
// rate, and polling interval of at least one.
func TestSflowRules(t *testing.T) {
	sampler := func(rate uint32) *flowv1.SflowSampler {
		return flowv1.SflowSampler_builder{
			InterfaceName: proto.String("ge-0/0/1"),
			ReceiverIndex: proto.Uint32(1),
			SamplingRate:  proto.Uint32(rate),
		}.Build()
	}
	poller := func(seconds int64) *flowv1.SflowPoller {
		return flowv1.SflowPoller_builder{
			InterfaceName: proto.String("ge-0/0/1"),
			ReceiverIndex: proto.Uint32(1),
			Interval:      durationpb.New(time.Duration(seconds) * time.Second),
		}.Build()
	}
	receiver := func(index uint32) *flowv1.SflowReceiver_builder {
		return &flowv1.SflowReceiver_builder{
			Index:   proto.Uint32(index),
			Address: testNet1(20),
			Port:    proto.Uint32(6343),
		}
	}
	withoutAddress := receiver(1)
	withoutAddress.Address = nil
	unclaimedReceiver := flowv1.SflowSampler_builder{
		InterfaceName: proto.String("ge-0/0/1"),
		ReceiverIndex: proto.Uint32(0),
		SamplingRate:  proto.Uint32(4096),
	}.Build()

	runFieldCases(t, []fieldCase{
		{name: "sampler", message: sampler(4096)},
		{name: "sampling disabled", message: sampler(0), wantField: "sampling_rate", wantText: "greater than or equal to 1"},
		{name: "sampler without a receiver", message: unclaimedReceiver, wantField: "receiver_index", wantText: "greater than or equal to 1"},
		{name: "poller", message: poller(30)},
		{name: "polling disabled", message: poller(0), wantField: "interval", wantText: "greater than or equal to"},
		{name: "receiver", message: receiver(1).Build()},
		{name: "receiver without address", message: withoutAddress.Build(), wantField: "address", wantText: "value is required"},
		{name: "receiver index 0", message: receiver(0).Build(), wantField: "index", wantText: "greater than or equal to 1"},
	})
}

// TestFlowExporterRules holds an exporter to a collector address, a DSCP that
// fits its field, IOS-XE's template refresh range, and its name length.
func TestFlowExporterRules(t *testing.T) {
	exporter := func() *flowv1.FlowExporter_builder {
		return &flowv1.FlowExporter_builder{
			Name:        proto.String("ex1"),
			Destination: testNet1(30),
			Port:        proto.Uint32(4739),
			Protocol:    flowv1.FlowExportProtocol_FLOW_EXPORT_PROTOCOL_IPFIX.Enum(),
		}
	}
	withoutDestination := exporter()
	withoutDestination.Destination = nil
	withDscp := exporter()
	withDscp.Dscp = packetv1.IpDscp(64).Enum()
	withRefresh := func(seconds int64) *flowv1.FlowExporter {
		b := exporter()
		b.TemplateRefresh = durationpb.New(time.Duration(seconds) * time.Second)
		return b.Build()
	}
	withName := func(name string) *flowv1.FlowExporter {
		b := exporter()
		b.Name = proto.String(name)
		return b.Build()
	}

	runFieldCases(t, []fieldCase{
		{name: "IPFIX exporter", message: exporter().Build()},
		{name: "destination absent", message: withoutDestination.Build(), wantField: "destination", wantText: "value is required"},
		{name: "DSCP above 63", message: withDscp.Build(), wantField: "dscp", wantText: "DSCP"},
		{name: "longest template refresh", message: withRefresh(86400)},
		{name: "template refresh above a day", message: withRefresh(86401), wantField: "template_refresh", wantText: "less than or equal to"},
		{name: "template refresh of zero", message: withRefresh(0), wantField: "template_refresh", wantText: "greater than or equal to"},
		{name: "60-character name", message: withName(strings.Repeat("e", 60))},
		{name: "61-character name", message: withName(strings.Repeat("e", 61)), wantField: "name", wantText: "at most 60"},
	})
}
