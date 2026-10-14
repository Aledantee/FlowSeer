package historysink

import (
	"context"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// retained is a batch whose insert failed with an open outcome: the server may
// have stored the block before the connection broke. Its members come back from
// the stream one by one. They are held until every member has returned and the
// batch is inserted again with the same members, hence the same token, so the
// server drops the block if it already applied it. Records that arrive in the
// meantime form their own batches and never join one.
type retained struct {
	members  []pending
	index    map[uint64]int
	arrived  []bool
	count    int
	deadline time.Time
}

// retain holds a batch whose members the sink just negatively acknowledged. A
// member a peer sink took never returns, so the batch is let go at its
// deadline. The deadline stays below the member's acknowledgement wait for any
// retry delay under 55 seconds, which keeps the stream from offering a held
// record again while the sink still owns it.
func (s *Sink) retain(batch []pending, now time.Time) {
	r := &retained{
		members:  append([]pending(nil), batch...),
		index:    make(map[uint64]int, len(batch)),
		arrived:  make([]bool, len(batch)),
		deadline: now.Add(2*s.cfg.RetryDelay + retainGrace),
	}
	for i, p := range batch {
		r.index[p.sequence] = i
		s.retained[p.sequence] = r
	}
	s.batches = append(s.batches, r)
}

// arrive replaces the member's delivery with the redelivered message, which
// carries the acknowledgement the server honors.
func (r *retained) arrive(sequence uint64, msg jetstream.Msg) {
	i := r.index[sequence]
	r.members[i].msg = msg
	if !r.arrived[i] {
		r.arrived[i] = true
		r.count++
	}
}

// settleRetained inserts every held batch whose members have all returned and
// gives up on one that waited past its deadline. A returned member of a
// given-up batch is negatively acknowledged, and every member is a new record
// from then on. It reports whether a retried insert failed transiently.
func (s *Sink) settleRetained(ctx context.Context, now time.Time) (failed bool) {
	held := s.batches
	s.batches = nil
	for _, r := range held {
		switch {
		case r.count == len(r.members):
			s.release(r)
			if s.insert(ctx, r.members) {
				failed = true
			}
		case now.After(r.deadline):
			s.release(r)
			for i, p := range r.members {
				if r.arrived[i] {
					s.nak(ctx, p.msg)
				}
			}
		default:
			s.batches = append(s.batches, r)
		}
	}
	return failed
}

// releaseRetained lets go of every held batch when the sink stops. A returned
// member is negatively acknowledged so the stream offers it to the next
// incarnation. No retry is counted, since the sink did not choose to retry.
func (s *Sink) releaseRetained() {
	for _, r := range s.batches {
		for i, p := range r.members {
			if r.arrived[i] {
				_ = p.msg.Nak()
			}
		}
	}
	s.batches = nil
	clear(s.retained)
}

func (s *Sink) release(r *retained) {
	for _, p := range r.members {
		if s.retained[p.sequence] == r {
			delete(s.retained, p.sequence)
		}
	}
}
