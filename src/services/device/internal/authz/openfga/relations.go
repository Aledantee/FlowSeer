package openfga

import (
	"context"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz"
)

const maxTuplesPerWrite = 100

// Write writes and deletes relationships in OpenFGA.
// It validates all tuples, drops duplicate tuples, and splits operations into
// batches of at most 100 tuples per call, sending OnDuplicate="ignore" and
// OnMissing="ignore".
func (c *Checker) Write(ctx context.Context, writes, deletes []authz.Tuple) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if err := c.verify(ctx); err != nil {
		return err
	}

	for _, t := range writes {
		if !isValidTuple(t) {
			return errs.New().Code(ErrCodeInvalidTuple).Msg("invalid write tuple")
		}
	}
	for _, t := range deletes {
		if !isValidTuple(t) {
			return errs.New().Code(ErrCodeInvalidTuple).Msg("invalid delete tuple")
		}
	}

	var cleanWrites []authz.Tuple
	seenWrites := make(map[authz.Tuple]struct{})
	for _, t := range writes {
		if _, ok := seenWrites[t]; !ok {
			seenWrites[t] = struct{}{}
			cleanWrites = append(cleanWrites, t)
		}
	}

	var cleanDeletes []authz.Tuple
	seenDeletes := make(map[authz.Tuple]struct{})
	for _, t := range deletes {
		if _, ok := seenDeletes[t]; !ok {
			seenDeletes[t] = struct{}{}
			cleanDeletes = append(cleanDeletes, t)
		}
	}

	if len(cleanWrites) == 0 && len(cleanDeletes) == 0 {
		return nil
	}

	remWrites := cleanWrites
	remDeletes := cleanDeletes

	for len(remWrites) > 0 || len(remDeletes) > 0 {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}

		totalRem := len(remWrites) + len(remDeletes)
		batchSize := maxTuplesPerWrite
		if totalRem < batchSize {
			batchSize = totalRem
		}

		var nw, nd int
		switch {
		case len(remWrites) == 0:
			nd = batchSize
		case len(remDeletes) == 0:
			nw = batchSize
		default:
			nw = (batchSize * len(remWrites)) / totalRem
			if nw == 0 {
				nw = 1
			} else if nw == batchSize && batchSize > 1 {
				nw = batchSize - 1
			}
			if nw > len(remWrites) {
				nw = len(remWrites)
			}
			nd = batchSize - nw
			if nd > len(remDeletes) {
				nd = len(remDeletes)
				nw = batchSize - nd
			}
		}

		chunkWrites := remWrites[:nw]
		chunkDeletes := remDeletes[:nd]
		remWrites = remWrites[nw:]
		remDeletes = remDeletes[nd:]

		req := &openfgav1.WriteRequest{
			StoreId:              c.storeID,
			AuthorizationModelId: c.modelID,
		}
		if len(chunkWrites) > 0 {
			tupleKeys := make([]*openfgav1.TupleKey, len(chunkWrites))
			for i, t := range chunkWrites {
				tupleKeys[i] = &openfgav1.TupleKey{
					Object:   t.Object,
					Relation: t.Relation,
					User:     t.User,
				}
			}
			req.Writes = &openfgav1.WriteRequestWrites{
				TupleKeys:   tupleKeys,
				OnDuplicate: "ignore",
			}
		}
		if len(chunkDeletes) > 0 {
			tupleKeys := make([]*openfgav1.TupleKeyWithoutCondition, len(chunkDeletes))
			for i, t := range chunkDeletes {
				tupleKeys[i] = &openfgav1.TupleKeyWithoutCondition{
					Object:   t.Object,
					Relation: t.Relation,
					User:     t.User,
				}
			}
			req.Deletes = &openfgav1.WriteRequestDeletes{
				TupleKeys: tupleKeys,
				OnMissing: "ignore",
			}
		}

		callCtx, cancel := context.WithTimeoutCause(ctx, c.timeout, errCallTimeout)
		_, err := c.client.Write(callCtx, req)
		cancel()
		if err != nil {
			return c.handleError(ctx, err)
		}
	}

	return nil
}

// Read returns all tuples matching the given object filter.
// It sends HIGHER_CONSISTENCY and pages at 100 tuples per call.
func (c *Checker) Read(ctx context.Context, object string) ([]authz.Tuple, error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err := c.verify(ctx); err != nil {
		return nil, err
	}
	if !isValidObject(object) {
		return nil, errs.New().Code(ErrCodeInvalidTuple).Msg("invalid object")
	}

	var allTuples []authz.Tuple
	var continuationToken string

	for {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}

		req := &openfgav1.ReadRequest{
			StoreId: c.storeID,
			TupleKey: &openfgav1.ReadRequestTupleKey{
				Object: object,
			},
			PageSize:          wrapperspb.Int32(100),
			ContinuationToken: continuationToken,
			Consistency:       openfgav1.ConsistencyPreference_HIGHER_CONSISTENCY,
		}

		callCtx, cancel := context.WithTimeoutCause(ctx, c.timeout, errCallTimeout)
		resp, err := c.client.Read(callCtx, req)
		cancel()
		if err != nil {
			return nil, c.handleError(ctx, err)
		}

		for _, t := range resp.GetTuples() {
			k := t.GetKey()
			if k != nil {
				allTuples = append(allTuples, authz.Tuple{
					Object:   k.GetObject(),
					Relation: k.GetRelation(),
					User:     k.GetUser(),
				})
			}
		}

		continuationToken = resp.GetContinuationToken()
		if continuationToken == "" {
			break
		}
	}

	return allTuples, nil
}

// Scan reads all tuples in the store using ReadRequest with no tuple key.
// It sends HIGHER_CONSISTENCY and pages at 100 tuples per call.
func (c *Checker) Scan(ctx context.Context, fn func(authz.Tuple) error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if err := c.verify(ctx); err != nil {
		return err
	}

	var continuationToken string

	for {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}

		req := &openfgav1.ReadRequest{
			StoreId:           c.storeID,
			PageSize:          wrapperspb.Int32(100),
			ContinuationToken: continuationToken,
			Consistency:       openfgav1.ConsistencyPreference_HIGHER_CONSISTENCY,
		}

		callCtx, cancel := context.WithTimeoutCause(ctx, c.timeout, errCallTimeout)
		resp, err := c.client.Read(callCtx, req)
		cancel()
		if err != nil {
			return c.handleError(ctx, err)
		}

		for _, t := range resp.GetTuples() {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			k := t.GetKey()
			if k != nil {
				tuple := authz.Tuple{
					Object:   k.GetObject(),
					Relation: k.GetRelation(),
					User:     k.GetUser(),
				}
				if fn != nil {
					if err := fn(tuple); err != nil {
						return err
					}
				}
			}
		}

		continuationToken = resp.GetContinuationToken()
		if continuationToken == "" {
			break
		}
	}

	return nil
}
