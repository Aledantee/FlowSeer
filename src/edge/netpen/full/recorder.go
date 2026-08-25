package full

// recorder.go holds the shared record-collection type embedded by the
// [Full] and [Scan] orchestrators. It wires the mutex-guarded findings
// slice to a 64-buffered live channel so the CLI can stream records as
// they arrive while Run executes, and collect them all once Run returns.
//
// The type is safe for concurrent use: appendRecord may be called from
// multiple goroutines (the burst phase dispatches concurrently), and
// Records may be called concurrently with appendRecord.

import (
	"fmt"
	"sync"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
)

// recorder is the shared record collector embedded by orchestrators. It
// pairs a mutex-guarded slice with a 64-buffered live channel.
type recorder struct {
	mu     sync.Mutex
	recs   []findings.Record
	recsCh chan findings.Record // live stream: emits records as appended (closed on Run completion)
}

// newRecorder constructs a recorder with a 64-buffered live channel.
func newRecorder() recorder {
	return recorder{recsCh: make(chan findings.Record, 64)}
}

// Records returns the findings collected so far (thread-safe).
func (r *recorder) Records() []findings.Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]findings.Record, len(r.recs))
	copy(out, r.recs)
	return out
}

// RecordChan returns the live record channel. Records are emitted as
// they are appended during Run; the channel is closed when Run completes.
func (r *recorder) RecordChan() <-chan findings.Record {
	return r.recsCh
}

// closeRecords closes the live record channel, signaling the consumer
// that the run is complete.
func (r *recorder) closeRecords() {
	if r.recsCh != nil {
		close(r.recsCh)
	}
}

// appendRecord adds a findings record to the collection and emits it on
// the live record channel.
func (r *recorder) appendRecord(rec findings.Record) {
	r.mu.Lock()
	r.recs = append(r.recs, rec)
	r.mu.Unlock()
	if r.recsCh != nil {
		r.recsCh <- rec
	}
}

// missingWatchLegErr builds the R2 deviation error for a named-but-absent
// watch leg. Both orchestrators fail fast before recon with the same
// code, attributes, exit code, user message, and hint.
func missingWatchLegErr(name string) error {
	return errs.New().
		Code(catalog.ErrCodeWatchLegMissing).
		Attr("watch", name).
		ExitCode(1).
		UserMsg(fmt.Sprintf("watch interface %q does not exist", name)).
		Hint("pass an existing -w <iface>, or omit -w for single-leg operation").
		Msgf("named watch leg %q is absent", name)
}
