package fabric

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/stream"
)

// StreamAttachment gives the fabric ownership of a source at an origin. Start
// is an offset from the fabric's configured start; each source offset is added
// to it. Flow must be nonzero. The zero retention keeps each journey.
// The fabric and the attached source are not safe for concurrent use.
type StreamAttachment struct {
	Origin    Endpoint
	Source    stream.Source
	Start     time.Duration
	Flow      FlowID
	Retention Retention
}

type attachedSource struct {
	attachment StreamAttachment
	at         time.Time
	frame      ethernet.Frame
	peeked     bool
	ended      bool
	ordinal    int
}

// AttachStream adds a finite source to the run. It refuses a nil source, an
// invalid origin, a zero flow, an unknown retention, or a start before the
// clock after stepping has begun. Attachment reads its first frame. Each stream
// event introduces that frame and reads its successor. Fork copies the cursor.
// A refused codec frame or backward offset ends only that stream, with an issue on its flow.
func (f *Fabric) AttachStream(att StreamAttachment) error {
	if att.Source == nil {
		return errs.New().Msg("stream source is nil")
	}
	if att.Flow == 0 {
		return errs.New().Msg("stream attachment requires a nonzero flow")
	}
	if att.Retention > RetainAggregate {
		return errs.New().Attr("retention", att.Retention).Msgf("unknown retention %d", att.Retention)
	}
	if _, unnamed := f.topology.nodes[att.Origin.Node].physical[""]; unnamed {
		if att.Origin.Port != "" {
			return errs.New().Attr("node", att.Origin.Node).Attr("port", att.Origin.Port).
				Msgf("host origin %q must have empty port", att.Origin.Node)
		}
		if _, ok := f.linkEnd(att.Origin.Node, ""); !ok {
			return errs.New().Attr("host", att.Origin.Node).Msgf("host %q has no connected cable", att.Origin.Node)
		}
	} else if n, isNode := f.topology.nodes[att.Origin.Node]; isNode && n.snapshot {
		if _, ok := n.ports.Port(att.Origin.Port); !ok {
			return errs.New().Attr("node", att.Origin.Node).Attr("port", att.Origin.Port).
				Msgf("port %q not found on switch %q", att.Origin.Port, att.Origin.Node)
		}
	} else if _, exists := f.topology.nodes[att.Origin.Node]; exists {
		return errs.New().Attr("node", att.Origin.Node).
			Msgf("reflector %q cannot originate an injection: it has no host or switch port to inject at", att.Origin.Node)
	} else {
		return errs.New().Attr("node", att.Origin.Node).
			Msgf("origin node %q not found in fabric", att.Origin.Node)
	}

	start := f.topology.start.Add(att.Start)
	if f.runStarted() && start.Before(f.clock) {
		return errs.New().Attr("at", start).Attr("clock", f.clock).
			Msg("stream start precedes fabric clock")
	}

	f.initRunState()
	f.attachments = append(f.attachments, attachedSource{attachment: att})
	f.nextStreamFrame(len(f.attachments) - 1)
	return nil
}

func (f *Fabric) sourcesPending() bool {
	for i := range f.attachments {
		if !f.attachments[i].ended {
			return true
		}
	}
	return false
}

func (f *Fabric) nextStreamFrame(index int) {
	source := &f.attachments[index]
	offset, frame, ok := source.attachment.Source.Next()
	if !ok {
		source.ended = true
		source.peeked = false
		return
	}
	at := f.topology.start.Add(source.attachment.Start).Add(offset)
	if source.peeked && at.Before(source.at) {
		f.endStream(source, errs.Msg("stream offset runs backward"))
		return
	}
	source.at, source.frame, source.peeked = at, cloneFrame(frame), true
	seq := f.nextSeq
	f.nextSeq++
	f.enqueue(Arrival{At: at, Kind: ArrivalStream, Seq: seq, Device: source.attachment.Origin.Node, Port: source.attachment.Origin.Port, source: index})
}

func (f *Fabric) stepStream(at time.Time, index int) {
	source := &f.attachments[index]
	defer func() { source.ordinal++ }()
	if _, err := source.frame.Encode(); err != nil {
		f.endStream(source, errs.Wrap(err, "encode attached stream frame"))
		return
	}
	if _, unnamed := f.topology.nodes[source.attachment.Origin.Node].physical[""]; unnamed {
		ref, _ := f.linkEnd(source.attachment.Origin.Node, "")
		if ref.end.Oper == port.Down {
			source.ended, source.peeked = true, false
			source.frame = ethernet.Frame{}
			return
		}
	}
	_, err := f.inject(Injection{
		At: at, Origin: source.attachment.Origin, Frame: source.frame,
		Flow: source.attachment.Flow, Retention: source.attachment.Retention,
	}, false)
	if err != nil {
		f.endStream(source, errs.Wrap(err, "inject attached stream frame"))
		return
	}
	f.nextStreamFrame(index)
}

func (f *Fabric) endStream(source *attachedSource, err error) {
	source.ended, source.peeked = true, false
	source.frame = ethernet.Frame{}
	issue := analysis.Issue{
		Code: IssueStreamFault, Status: analysis.Unsupported,
		Scope: analysis.WholeScope(), Message: err.Error(),
	}
	stats := f.flowAccumulator(source.attachment.Flow)
	stats.Metadata = stats.Metadata.Merge(analysis.NewMetadata(analysis.WholeScope(), []analysis.Issue{issue}, analysis.EvidenceCatalog{}, nil))
}
