package fabric

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/netsim/stream"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
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
}

// AttachStream adds a finite source to the run. It refuses a nil source, an
// invalid origin, a zero flow, an unknown retention, or a start before the
// clock after stepping has begun. The source is read by Step and Run, and Fork
// copies its current cursor.
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
	if _, isHost := f.cfg.Hosts[att.Origin.Node]; isHost {
		if att.Origin.Port != "" {
			return errs.New().Attr("node", att.Origin.Node).Attr("port", att.Origin.Port).
				Msgf("host origin %q must have empty port", att.Origin.Node)
		}
		if _, ok := f.linkEnd(att.Origin.Node, ""); !ok {
			return errs.New().Attr("host", att.Origin.Node).Msgf("host %q has no connected cable", att.Origin.Node)
		}
	} else if sw, isSwitch := f.cfg.Switches[att.Origin.Node]; isSwitch {
		if _, ok := sw.Ports.Port(att.Origin.Port); !ok {
			return errs.New().Attr("node", att.Origin.Node).Attr("port", att.Origin.Port).
				Msgf("port %q not found on switch %q", att.Origin.Port, att.Origin.Node)
		}
	} else if _, isReflector := f.cfg.Reflectors[att.Origin.Node]; isReflector {
		return errs.New().Attr("node", att.Origin.Node).
			Msgf("reflector %q cannot originate an injection: it has no host or switch port to inject at", att.Origin.Node)
	} else {
		return errs.New().Attr("node", att.Origin.Node).
			Msgf("origin node %q not found in fabric", att.Origin.Node)
	}

	start := f.cfg.Start.Add(att.Start)
	if f.runStarted() && start.Before(f.clock) {
		return errs.New().Attr("at", start).Attr("clock", f.clock).
			Msg("stream start precedes fabric clock")
	}

	f.attachments = append(f.attachments, attachedSource{attachment: att})
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

func (f *Fabric) pullSources() {
	for i := range f.attachments {
		source := &f.attachments[i]
		if source.ended || source.peeked {
			continue
		}
		offset, frame, ok := source.attachment.Source.Next()
		if !ok {
			source.ended = true
			continue
		}
		source.at = f.cfg.Start.Add(source.attachment.Start).Add(offset)
		source.frame = frame
		source.peeked = true
	}

	for {
		selected := -1
		for i := range f.attachments {
			source := &f.attachments[i]
			if source.peeked && (selected < 0 || source.at.Before(f.attachments[selected].at)) {
				selected = i
			}
		}
		if selected < 0 {
			return
		}

		source := &f.attachments[selected]
		if len(f.queue) > 0 && source.at.After(f.queue[0].At) {
			return
		}
		if _, isHost := f.cfg.Hosts[source.attachment.Origin.Node]; isHost {
			ref, _ := f.linkEnd(source.attachment.Origin.Node, "")
			if ref.end.Oper == port.Down {
				source.ended = true
				source.peeked = false
				continue
			}
		}

		_, err := f.Inject(Injection{
			At: source.at, Origin: source.attachment.Origin, Frame: source.frame,
			Flow: source.attachment.Flow, Retention: source.attachment.Retention,
		})
		if err != nil {
			f.err = errs.Wrap(err, "inject attached stream frame")
			return
		}
		source.peeked = false
		offset, frame, ok := source.attachment.Source.Next()
		if !ok {
			source.ended = true
			continue
		}
		source.at = f.cfg.Start.Add(source.attachment.Start).Add(offset)
		source.frame = frame
		source.peeked = true
	}
}
