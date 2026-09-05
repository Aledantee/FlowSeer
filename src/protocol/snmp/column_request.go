package snmp

import (
	"context"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// TableWalkOptions bounds selected-column retrieval. The zero value requests
// 50 repetitions and at most 10 columns per request. Options are copied at
// construction; callers must not mutate CallOptions during construction.
type TableWalkOptions struct {
	// MaxRepetitions is 1..255, or zero for 50. It bounds each column's queue.
	MaxRepetitions int
	// MaxColumns is 1..128, or zero for 10. A smaller native session or call
	// MaxOIDs further limits request width.
	MaxColumns int
	// CallOptions supplies timeout, retry, and walk-guard overrides. RowBuffer
	// does not change this pull iterator's prefetch window.
	CallOptions []CallOption
}

type columnSource interface {
	columnDefaults() (maxOIDs, maxVars int, ignoreNonIncreasing bool)
	requestColumns(context.Context, []OID, int, bool, *CallConfig) ([]RawVarBind, error)
}

type columnRequester struct {
	sess                        Session
	cfg                         *CallConfig
	opts                        []CallOption
	repetitions, width, maxVars int
	ignoreNonIncreasing         bool
}

func newColumnRequester(sess Session, opts TableWalkOptions) (*columnRequester, error) {
	if opts.MaxRepetitions < 0 || opts.MaxRepetitions > 255 || opts.MaxColumns < 0 || opts.MaxColumns > 128 {
		return nil, errs.Msg("table walk repetitions must be 0..255 and columns 0..128")
	}
	r := &columnRequester{sess: sess, cfg: ApplyCallOptions(opts.CallOptions...), opts: append([]CallOption(nil), opts.CallOptions...), repetitions: opts.MaxRepetitions, width: opts.MaxColumns}
	if r.repetitions == 0 {
		r.repetitions = 50
	}
	if r.width == 0 {
		r.width = 10
	}
	if src, ok := sess.(columnSource); ok {
		maxOIDs, maxVars, ignore := src.columnDefaults()
		if maxOIDs > 0 {
			r.width = min(r.width, maxOIDs)
		}
		r.maxVars, r.ignoreNonIncreasing = maxVars, ignore
	}
	if r.cfg.MaxOIDs > 0 {
		r.width = min(r.width, r.cfg.MaxOIDs)
	}
	if r.cfg.MaxWalkVars > 0 {
		r.maxVars = r.cfg.MaxWalkVars
	}
	if r.cfg.IgnoreNonIncreasingSet {
		r.ignoreNonIncreasing = r.cfg.IgnoreNonIncreasing
	}
	return r, nil
}

func (r *columnRequester) request(ctx context.Context, oids []OID, reps int, next bool) ([]RawVarBind, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if src, ok := r.sess.(columnSource); ok {
		return src.requestColumns(ctx, oids, reps, next, r.cfg)
	}
	var vbs []VarBind
	var err error
	if next {
		vbs, err = r.sess.GetNext(ctx, oids, r.opts...)
	} else {
		vbs, err = r.sess.GetBulk(ctx, 0, uint8(reps), oids, r.opts...)
	}
	if err != nil {
		return nil, err
	}
	limit := len(oids) * reps
	if next {
		limit = len(oids)
	}
	if len(vbs) > limit {
		return nil, errs.Msg("table walk response exceeds requested varbind count")
	}
	items := make([]RawVarBind, len(vbs))
	for i, vb := range vbs {
		if vb == nil {
			return nil, errs.Msg("table walk received a nil varbind")
		}
		items[i] = RawVarBind{OID: encodeOIDContent(vb.GetHeader().OID), VB: vb}
	}
	return items, nil
}

func (s *session) columnDefaults() (int, int, bool) {
	return s.maxOIDs, s.maxWalkVars, s.ignoreNonIncreasing
}

func (s *session) requestColumns(ctx context.Context, oids []OID, reps int, next bool, cfg *CallConfig) (items []RawVarBind, err error) {
	if s.version == V1 {
		return nil, ErrBulkUnsupported
	}
	op := "GetBulk"
	var req *message
	limit := len(oids) * reps
	if next {
		op = "GetNext"
		req = s.newRequest(pduGetNextRequest, nullVarbinds(oids))
		limit = len(oids)
	} else {
		req = s.newBulkRequest(oids, 0, reps)
	}
	ctx, span := s.inst.startOp(ctx, op)
	defer span.End()
	var pe *PDUError
	defer func() { s.inst.finishOp(ctx, span, op, len(items), pe, err) }()
	req.wantRaw = true
	resp, err := s.exchange(ctx, op, req, cfg)
	if err != nil {
		return nil, err
	}
	enrichRawErrorVarbinds(&resp.pdu)
	if pe = pduError(resp); pe != nil {
		return nil, pe
	}
	raw, err := rawItemsOfLimit(&resp.pdu, limit)
	if err != nil {
		return nil, err
	}
	items = make([]RawVarBind, len(raw))
	for i, it := range raw {
		items[i] = it.rv()
	}
	return items, nil
}
