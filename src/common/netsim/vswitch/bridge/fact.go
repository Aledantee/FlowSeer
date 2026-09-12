package bridge

import (
	"strconv"
	"strings"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

type frameFact string

func (f frameFact) TypeID() string    { return "bridge.frame" }
func (f frameFact) Canonical() string { return string(f) }

type vlanDecisionFact string

func (f vlanDecisionFact) TypeID() string    { return "bridge.vlan_decision" }
func (f vlanDecisionFact) Canonical() string { return string(f) }

type fdbDecisionFact string

func (f fdbDecisionFact) TypeID() string    { return "bridge.fdb_decision" }
func (f fdbDecisionFact) Canonical() string { return string(f) }

type egressDecisionFact string

func (f egressDecisionFact) TypeID() string    { return "bridge.egress_decision" }
func (f egressDecisionFact) Canonical() string { return string(f) }

func frameSnapshot(f ethernet.Frame) trace.Fact {
	var b strings.Builder
	b.WriteString("src=")
	b.WriteString(strconv.Quote(f.Src.String()))
	b.WriteString(";dst=")
	b.WriteString(strconv.Quote(f.Dst.String()))
	b.WriteString(";ether_type=")
	b.WriteString(strconv.FormatUint(uint64(f.EtherType), 10))
	b.WriteString(";tags=[")
	for i, tag := range f.Tags {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString("{tpid=")
		b.WriteString(strconv.FormatUint(uint64(tag.TPID), 10))
		b.WriteString(";pcp=")
		b.WriteString(strconv.FormatUint(uint64(tag.PCP), 10))
		b.WriteString(";dei=")
		b.WriteString(strconv.FormatBool(tag.DEI))
		b.WriteString(";vid=")
		b.WriteString(strconv.FormatUint(uint64(tag.VID), 10))
		b.WriteByte('}')
	}
	b.WriteString("];payload_len=")
	b.WriteString(strconv.Itoa(len(f.Payload)))

	return frameFact(b.String())
}

func vlanSnapshot(portName string, fid vlan.ID, pcp vlan.PCP, dei bool, form string) trace.Fact {
	return vlanDecisionFact("port=" + strconv.Quote(portName) +
		";fid=" + strconv.FormatUint(uint64(fid), 10) +
		";pcp=" + strconv.FormatUint(uint64(pcp), 10) +
		";dei=" + strconv.FormatBool(dei) +
		";form=" + strconv.Quote(form))
}

func fdbSnapshot(fid vlan.ID, mac netaddr.MAC, present bool, portName string, static bool) trace.Fact {
	return fdbDecisionFact("fid=" + strconv.FormatUint(uint64(fid), 10) +
		";mac=" + strconv.Quote(mac.String()) +
		";present=" + strconv.FormatBool(present) +
		";port=" + strconv.Quote(portName) +
		";static=" + strconv.FormatBool(static))
}

func egressSnapshot(portName, member string, fid vlan.ID, reason trace.Reason, eligible bool) trace.Fact {
	return egressDecisionFact("port=" + strconv.Quote(portName) +
		";member=" + strconv.Quote(member) +
		";fid=" + strconv.FormatUint(uint64(fid), 10) +
		";eligible=" + strconv.FormatBool(eligible) +
		";reason=" + strconv.Quote(string(reason)))
}

func replicationFacts(ports []port.Port, fid vlan.ID) []trace.Fact {
	facts := make([]trace.Fact, len(ports))
	for i, p := range ports {
		facts[i] = egressSnapshot(p.Name, "", fid, "", true)
	}

	return facts
}

func facts(values ...trace.Fact) []trace.Fact {
	result := make([]trace.Fact, 0, len(values))
	for _, value := range values {
		if value != nil {
			result = append(result, value)
		}
	}

	return result
}

func ingressTagForm(tagged, priorityTagged bool) string {
	if tagged {
		return "tagged"
	}
	if priorityTagged {
		return "priority-tagged"
	}
	return "untagged"
}
