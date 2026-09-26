package conformance

import (
	"testing"

	"google.golang.org/protobuf/proto"

	filterv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/filter/v1"
	packetv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/packet/v1"
	qosv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/qos/v1"
)

// TestClassifierRules holds a classifier term's remarks to values that fit
// their header fields and a classifier's term ids to one term each.
func TestClassifierRules(t *testing.T) {
	term := func(id string) *qosv1.ClassifierTerm_builder {
		return &qosv1.ClassifierTerm_builder{
			Id: proto.String(id),
			Match: filterv1.FilterMatch_builder{
				Dscps: []packetv1.IpDscp{packetv1.IpDscp_IP_DSCP_EF},
			}.Build(),
		}
	}
	withDscp := func(dscp packetv1.IpDscp) *qosv1.ClassifierTerm {
		b := term("voice")
		b.SetDscp = dscp.Enum()
		return b.Build()
	}
	withPcp := term("voice")
	withPcp.SetPcp = proto.Uint32(8)
	withoutID := term("voice")
	withoutID.Id = nil
	classifier := func(terms ...*qosv1.ClassifierTerm) *qosv1.Classifier {
		return qosv1.Classifier_builder{Name: proto.String("voice-in"), Terms: terms}.Build()
	}

	runFieldCases(t, []fieldCase{
		{name: "DSCP remark", message: withDscp(packetv1.IpDscp_IP_DSCP_EF)},
		{name: "DSCP remark above 63", message: withDscp(64), wantRule: "enum.ip_dscp"},
		{name: "PCP remark above seven", message: withPcp.Build(), wantField: "set_pcp", wantText: "priority code point"},
		{name: "term without id", message: withoutID.Build(), wantField: "id", wantText: "value is required"},
		{name: "distinct term ids", message: classifier(term("voice").Build(), term("video").Build())},
		{name: "repeated term id", message: classifier(term("voice").Build(), term("voice").Build()), wantRule: "classifier.term_ids_unique"},
		{name: "classifier without name", message: qosv1.Classifier_builder{}.Build(), wantField: "name", wantText: "value is required"},
	})
}

// TestPolicerRules holds a policer to a committed rate, a peak rate no lower
// than it, and a peak burst only beside a peak rate.
func TestPolicerRules(t *testing.T) {
	runFieldCases(t, []fieldCase{
		{name: "single rate", message: qosv1.Policer_builder{CommittedRateBps: proto.Uint64(1_000_000), CommittedBurstBytes: proto.Uint64(1500)}.Build()},
		{name: "two rate", message: qosv1.Policer_builder{CommittedRateBps: proto.Uint64(1_000_000), PeakRateBps: proto.Uint64(2_000_000), PeakBurstBytes: proto.Uint64(3000)}.Build()},
		{name: "peak below committed", message: qosv1.Policer_builder{CommittedRateBps: proto.Uint64(2_000_000), PeakRateBps: proto.Uint64(1_000_000)}.Build(), wantRule: "policer.peak_rate_ordered"},
		{name: "peak burst without peak rate", message: qosv1.Policer_builder{CommittedRateBps: proto.Uint64(1_000_000), PeakBurstBytes: proto.Uint64(1500)}.Build(), wantRule: "policer.peak_burst_needs_peak_rate"},
		{name: "committed rate absent", message: qosv1.Policer_builder{PeakRateBps: proto.Uint64(1_000_000)}.Build(), wantField: "committed_rate_bps", wantText: "value is required"},
	})
}

// TestQosInterfaceRules holds the per-interface row to an interface name and
// its queues to an identity, ordered rate and bandwidth bounds, and distinct
// ids and names.
func TestQosInterfaceRules(t *testing.T) {
	row := func(queues ...*qosv1.Queue) *qosv1.QosInterface_builder {
		return &qosv1.QosInterface_builder{
			InterfaceName: proto.String("1/0/1"),
			TrustMode:     qosv1.TrustMode_TRUST_MODE_DSCP.Enum(),
			Queues:        queues,
		}
	}
	queue := func(id uint32) *qosv1.Queue_builder {
		return &qosv1.Queue_builder{QueueId: proto.Uint32(id)}
	}
	wred := queue(0)
	wred.Discipline = qosv1.QueueDiscipline_QUEUE_DISCIPLINE_WRED.Enum()
	wred.MinBandwidthBasisPoints = proto.Uint32(1000)
	unidentified := &qosv1.Queue_builder{Discipline: qosv1.QueueDiscipline_QUEUE_DISCIPLINE_DROP_TAIL.Enum()}
	rates := queue(1)
	rates.MinRateBps = proto.Uint64(2000)
	rates.MaxRateBps = proto.Uint64(1000)
	bandwidths := queue(1)
	bandwidths.MinBandwidthBasisPoints = proto.Uint32(5000)
	bandwidths.MaxBandwidthBasisPoints = proto.Uint32(4000)
	overFull := queue(1)
	overFull.MaxBandwidthBasisPoints = proto.Uint32(10001)
	named := func(name string) *qosv1.Queue {
		return qosv1.Queue_builder{Name: proto.String(name)}.Build()
	}
	withoutName := row()
	withoutName.InterfaceName = nil

	runFieldCases(t, []fieldCase{
		{name: "trusted DSCP with a WRED queue", message: row(wred.Build()).Build()},
		{name: "named queues", message: row(named("voice"), named("best-effort")).Build()},
		{name: "interface name absent", message: withoutName.Build(), wantField: "interface_name", wantText: "value is required"},
		{name: "queue with neither name nor id", message: row(unidentified.Build()).Build(), wantRule: "queue.identified"},
		{name: "queue id above 255", message: row(queue(256).Build()).Build(), wantField: "queues[0].queue_id", wantText: "less than or equal to 255"},
		{name: "minimum rate above maximum", message: row(rates.Build()).Build(), wantRule: "queue.rate_ordered"},
		{name: "minimum bandwidth above maximum", message: row(bandwidths.Build()).Build(), wantRule: "queue.bandwidth_ordered"},
		{name: "bandwidth above 100 percent", message: row(overFull.Build()).Build(), wantField: "queues[0].max_bandwidth_basis_points", wantText: "basis-point"},
		{name: "repeated queue id", message: row(queue(3).Build(), queue(3).Build()).Build(), wantRule: "qos_interface.queue_ids_unique"},
		{name: "repeated queue name", message: row(named("voice"), named("voice")).Build(), wantRule: "qos_interface.queue_names_unique"},
	})
}
