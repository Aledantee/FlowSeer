package snmp

import (
	"bytes"
	"context"
	"errors"
	"iter"
	"sync"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// ErrWalkNoProgress means a column supplied neither a successor nor proof of
// exhaustion, including after a singleton GETNEXT recovery attempt.
var ErrWalkNoProgress = errs.Msg("table walk made no progress")

// ColumnCell associates a raw value with its zero-based position in the roots
// passed to [WalkColumns]. Values remain valid after advancing or closing.
// Concurrent reads are safe while neither the cell nor its referenced payloads
// are mutated. Callers must synchronize mutations with all readers and writers.
type ColumnCell struct {
	Column int
	Value  RawVarBind
}

// ColumnWalker merges selected columns by numeric index suffix. Iteration is
// single-consumer and single-use; Close and Err may be called concurrently.
// Construct it with [WalkColumns]; the zero value is not usable.
type ColumnWalker struct {
	ctx, parent               context.Context
	cancel                    context.CancelFunc
	requester                 *columnRequester
	roots                     []OID
	mu                        sync.Mutex
	err                       error
	started, closed, finished bool
}

// WalkColumns constructs a lazy, bounded selected-column walk. Each root must
// name a distinct, non-overlapping column. Validation errors are returned by Err
// before I/O. Empty roots produce an empty success. Rows are the sorted union of
// indexes with real values; absent cells are omitted. At most one batch per
// column is queued, independent of table cardinality. SNMPv1 is unsupported.
func WalkColumns(ctx context.Context, sess Session, roots []OID, opts TableWalkOptions) *ColumnWalker {
	pctx, cancel := context.WithCancel(ctx)
	w := &ColumnWalker{ctx: pctx, parent: ctx, cancel: cancel, roots: append([]OID(nil), roots...)}
	w.requester, w.err = newColumnRequester(sess, opts)
	for i, root := range roots {
		if root.Len() < 2 {
			w.err = errs.Msg("table walk column has no valid OID")
			break
		}
		for _, prev := range roots[:i] {
			if root.HasPrefix(prev) || prev.HasPrefix(root) {
				w.err = errs.Msg("table walk columns overlap")
				break
			}
		}
	}
	if w.err != nil {
		cancel()
	}
	return w
}

// Err returns the first terminal error. Consumer stop and Close alone are
// successful; parent cancellation during a walk preserves the context error.
func (w *ColumnWalker) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

// Close cancels in-flight retrieval and prevents further requests. It is
// idempotent and safe concurrently with Iter. Iterator-owned buffers are released
// when the current request or consumer callback returns.
func (w *ColumnWalker) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.closed && !w.finished && w.err == nil {
		w.err = w.parent.Err()
	}
	w.closed = true
	w.cancel()
}

// Fail terminates the walk with a decoder error. Generated bindings call it only
// while decoding the current row, before yielding that row to the consumer.
func (w *ColumnWalker) Fail(err error) {
	w.mu.Lock()
	if w.err == nil && !w.closed {
		w.err = err
	}
	w.mu.Unlock()
	w.Close()
}

// Iter yields complete selected-column rows in numeric OID suffix order. A
// false yield closes the walk immediately; no requests run in the background.
// Rows already yielded survive later transport, protocol, or decoder errors.
func (w *ColumnWalker) Iter() iter.Seq2[OID, []ColumnCell] {
	return func(yield func(OID, []ColumnCell) bool) {
		w.mu.Lock()
		if w.started || w.closed || w.err != nil {
			w.mu.Unlock()
			return
		}
		w.started = true
		w.mu.Unlock()
		var walkErr error
		defer func() {
			w.mu.Lock()
			if w.err == nil && !w.closed {
				if w.parent.Err() != nil {
					w.err = w.parent.Err()
				} else {
					w.err = walkErr
				}
			}
			w.finished = true
			w.roots = nil
			w.mu.Unlock()
			w.cancel()
		}()
		state := columnMerge{w: w, columns: make([]columnCursor, len(w.roots))}
		for i, root := range w.roots {
			state.columns[i] = columnCursor{root: root.WireBytes(), cursor: root.WireBytes()}
		}
		walkErr = state.run(yield)
	}
}

type columnCursor struct {
	root, cursor  []byte
	queue         []RawVarBind
	done, getNext bool
}

type columnMerge struct {
	w            *ColumnWalker
	columns      []columnCursor
	count, start int
}

func (m *columnMerge) run(yield func(OID, []ColumnCell) bool) error {
	for {
		if err := m.w.ctx.Err(); err != nil {
			return err
		}
		if err := m.fill(); err != nil {
			return err
		}
		var index []byte
		for i := range m.columns {
			c := &m.columns[i]
			if len(c.queue) == 0 {
				continue
			}
			suffix := c.queue[0].OID[len(c.root):]
			if index == nil || cmpOIDWire(suffix, index) < 0 {
				index = suffix
			}
		}
		if index == nil {
			return nil
		}
		idx, err := DecodeIndexArcs(index)
		if err != nil {
			return err
		}
		cells := make([]ColumnCell, 0, len(m.columns))
		for i := range m.columns {
			c := &m.columns[i]
			if len(c.queue) == 0 || !bytes.Equal(c.queue[0].OID[len(c.root):], index) {
				continue
			}
			cells = append(cells, ColumnCell{Column: i, Value: c.queue[0]})
			c.queue[0] = RawVarBind{}
			c.queue = c.queue[1:]
			if len(c.queue) == 0 {
				c.queue = nil
			}
		}
		if m.w.ctx.Err() != nil {
			return m.w.ctx.Err()
		}
		if !yield(idx, cells) {
			m.w.Close()
			return nil
		}
	}
}

func (m *columnMerge) fill() error {
	for {
		var ids []int
		for off := range m.columns {
			i := (m.start + off) % len(m.columns)
			c := &m.columns[i]
			if !c.done && len(c.queue) == 0 {
				ids = append(ids, i)
			}
		}
		if len(ids) == 0 {
			return nil
		}
		for len(ids) > 0 {
			n := min(len(ids), m.w.requester.width)
			if err := m.fetch(ids[:n], false); err != nil {
				return err
			}
			ids = ids[n:]
		}
	}
}

func (m *columnMerge) fetch(ids []int, recovery bool) error {
	r := m.w.requester
	// A cursor that degraded to GETNEXT stays singleton for this walk.
	for _, id := range ids {
		if m.columns[id].getNext && len(ids) > 1 {
			for _, single := range ids {
				if err := m.fetch([]int{single}, recovery); err != nil {
					return err
				}
			}
			return nil
		}
	}
	oids := make([]OID, len(ids))
	for i, id := range ids {
		var err error
		oids[i], err = decodeOID(m.columns[id].cursor)
		if err != nil {
			return err
		}
	}
	var items []RawVarBind
	for {
		next := recovery || (len(ids) == 1 && m.columns[ids[0]].getNext)
		var err error
		items, err = r.request(m.w.ctx, oids, r.repetitions, next)
		if err == nil {
			break
		}
		pe, ok := errors.AsType[*PDUError](err)
		if !ok || pe.Status != TooBig || next {
			return err
		}
		if reps, retry := nextBulkReps(r.repetitions); retry {
			r.repetitions = reps
			continue
		}
		if len(ids) > 1 {
			half := len(ids) / 2
			r.width = min(r.width, max(1, half))
			if err := m.fetch(ids[:half], false); err != nil {
				return err
			}
			return m.fetch(ids[half:], false)
		}
		m.columns[ids[0]].getNext = true
	}
	progress := make([]bool, len(ids))
	for pos, rv := range items {
		slot := pos % len(ids)
		c := &m.columns[ids[slot]]
		if c.done {
			continue
		}
		switch rv.exceptionTag() {
		case tagEndOfMibView, tagNoSuchObject:
			c.done = true
			progress[slot] = true
			continue
		}
		if !bytes.HasPrefix(rv.OID, c.root) {
			c.done = true
			progress[slot] = true
			continue
		}
		if len(rv.OID) == len(c.root) {
			return errs.Msg("table walk value has an empty index suffix")
		}
		cmp := cmpOIDWire(rv.OID, c.cursor)
		if cmp == 0 || (cmp < 0 && !r.ignoreNonIncreasing) {
			return errs.Wrapf(ErrOIDNotIncreasing, "column %s returned %s after %s", rawOIDString(c.root), rawOIDString(rv.OID), rawOIDString(c.cursor))
		}
		if cmp < 0 {
			continue
		}
		m.count++
		if r.maxVars > 0 && m.count > r.maxVars {
			return errs.Wrapf(ErrMaxWalkVars, "budget %d exceeded", r.maxVars)
		}
		c.cursor = rv.OID
		progress[slot] = true
		if rv.exceptionTag() != tagNoSuchInstance {
			if c.queue == nil {
				c.queue = make([]RawVarBind, 0, r.repetitions)
			}
			c.queue = append(c.queue, rv)
		}
	}
	// A cursor must not pin a response after its column queue drains.
	for _, id := range ids {
		m.columns[id].cursor = bytes.Clone(m.columns[id].cursor)
	}
	served := min(len(items), len(ids))
	if served > 0 {
		m.start = (ids[served-1] + 1) % len(m.columns)
	}
	for slot, id := range ids {
		if progress[slot] || (len(items) > 0 && slot >= served) {
			continue
		}
		if recovery {
			return errs.Wrapf(ErrWalkNoProgress, "column %s", rawOIDString(m.columns[id].root))
		}
		if err := m.fetch([]int{id}, true); err != nil {
			return err
		}
	}
	return nil
}
