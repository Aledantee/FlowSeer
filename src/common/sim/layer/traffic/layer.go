package traffic

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

// Layer manages traffic control, including policing and mirroring.
// A Layer is not safe for concurrent use.
type Layer struct {
	cfg     Config
	buckets map[string]*bucket
}

// New builds a traffic layer with one bucket per configured policer.
func New(cfg Config, env layer.Env) (*Layer, error) {
	norm := cfg.Normalize(env)
	if err := norm.Validate(env); err != nil {
		return nil, err
	}
	buckets := make(map[string]*bucket, len(norm.Policers))
	for name, policer := range norm.Policers {
		b, err := newBucket(policer)
		if err != nil {
			return nil, errs.Wrapf(err, "create policer bucket for %q", name)
		}
		buckets[name] = b
	}
	return &Layer{
		cfg:     norm,
		buckets: buckets,
	}, nil
}

// Admit refills the policer named name through now and takes octets when they fit.
// An unknown policer or zero-rate policer admits every frame.
func (l *Layer) Admit(now time.Time, name string, octets int) bool {
	if l == nil {
		return true
	}
	b, ok := l.buckets[name]
	if !ok {
		return true
	}
	return b.Admit(now, octets)
}

// Clone returns an independent deep copy of the traffic layer.
func (l *Layer) Clone() *Layer {
	if l == nil {
		return nil
	}
	cp := &Layer{
		cfg:     l.cfg.Clone(),
		buckets: make(map[string]*bucket, len(l.buckets)),
	}
	for k, b := range l.buckets {
		cp.buckets[k] = b.Clone()
	}
	return cp
}

// Retain carries over existing bucket tokens from prev for policers whose configuration
// has not changed.
func (l *Layer) Retain(prev *Layer) {
	if l == nil || prev == nil {
		return
	}
	for name, policer := range l.cfg.Policers {
		if current, ok := prev.cfg.Policers[name]; ok && current == policer {
			if b, ok := prev.buckets[name]; ok {
				l.buckets[name] = b.Clone()
			}
		}
	}
}

// Copies returns the mirror copies selected from one relay result.
func (l *Layer) Copies(switchports map[string]Switchport, ingress string, vid vlan.ID, received ethernet.Frame, egress []Egress) []Copy {
	if l == nil {
		return nil
	}
	return copies(l.cfg, switchports, ingress, vid, received, egress)
}

// MaxRate returns the configured maximum rate in bits per second for the queue on port
// with traffic class pcp, and whether an explicit rate was configured.
func (l *Layer) MaxRate(name string, pcp vlan.PCP) (uint64, bool) {
	if l == nil {
		return 0, false
	}
	return l.cfg.MaxRate(name, pcp)
}

// QueueBuffer returns the configured buffer size in octets for the queue on port with
// traffic class pcp, and whether an explicit buffer size was configured.
func (l *Layer) QueueBuffer(name string, pcp vlan.PCP) (uint64, bool) {
	if l == nil {
		return 0, false
	}
	return l.cfg.QueueBuffer(name, pcp)
}

// MirrorForOutput reports whether port is configured as the output port for a mirror,
// returning the mirror name.
func (l *Layer) MirrorForOutput(port string) (name string, ok bool) {
	if l == nil {
		return "", false
	}
	for _, m := range l.cfg.Mirrors {
		if m.OutputPort == port {
			return m.Name, true
		}
	}
	return "", false
}
