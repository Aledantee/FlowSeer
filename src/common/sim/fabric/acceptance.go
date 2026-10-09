package fabric

import (
	"fmt"
	"strconv"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func (f *Fabric) recordAcceptance(j *journey, name, port string, frame ethernet.Frame, at time.Time, res device.Result) Entry {
	var cable *Cable
	if res.Acceptance == device.DeliveryAcceptance {
		cable = f.portCable(name, port)
	}
	if res.Acceptance == device.DeliveryAcceptance {
		f.record(j, Entry{At: at, Kind: EntryArrival, Device: name, Cable: cable})
	}
	kind := EntryRejection
	var raised []analysis.Issue
	if res.Outcome == trace.Consumed {
		kind = EntryReflection
		if res.Acceptance == device.DeliveryAcceptance {
			kind = EntryDelivery
			j.Deliveries = append(j.Deliveries, Delivery{Host: name, At: at, Frame: frame})
		}
	} else if res.Metadata.Status() == analysis.Incomplete {
		kind = EntryUnresolved
		raised = append(raised, analysis.Issue{Code: analysis.IssueCode(res.Reason), Status: analysis.Incomplete, Scope: analysis.JourneyScope(strconv.FormatUint(uint64(j.FrameID), 10)), Message: acceptanceMessage(res.Acceptance, name, j.FrameID)})
	}
	step := res.Steps[len(res.Steps)-1]
	entry := Entry{At: at, Kind: kind, Device: name, Port: port, Cable: cable, Step: &step, Reason: res.Reason}
	f.record(j, entry, raised...)
	return entry
}

func acceptanceMessage(kind device.AcceptanceKind, name string, fid FrameID) string {
	if kind == device.DeliveryAcceptance {
		return fmt.Sprintf("host %q cannot decide on frame %d: its IP header does not decode", name, fid)
	}
	return fmt.Sprintf("reflector %q cannot decide on frame %d: its header does not decode", name, fid)
}
