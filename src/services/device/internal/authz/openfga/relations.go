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
		if _, ok := seenWrites[t]; ok {
			return errs.New().Code(ErrCodeInvalidTuple).Msg("tuple in both writes and deletes")
		}
		if _, ok := seenDeletes[t]; !ok {
			seenDeletes[t] = struct{}{}
			cleanDeletes = append(cleanDeletes, t)
		}
	}

	if len(cleanWrites) == 0 && len(cleanDeletes) == 0 {
		return nil
	}

	remDeletes := cleanDeletes
	remWrites := cleanWrites

	for len(remDeletes) > 0 || len(remWrites) > 0 {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}

		batchSize := maxTuplesPerWrite
		var nd, nw int
		if len(remDeletes) > 0 {
			nd = len(remDeletes)
			if nd > batchSize {
				nd = batchSize
			}
		}
		if remaining := batchSize - nd; remaining > 0 && len(remWrites) > 0 {
			nw = len(remWrites)
			if nw > remaining {
				nw = remaining
			}
		}

		chunkDeletes := remDeletes[:nd]
		chunkWrites := remWrites[:nw]
		remDeletes = remDeletes[nd:]
		remWrites = remWrites[nw:]

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

		callCtx, cancel := c.callContext(ctx)
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

		callCtx, cancel := c.callContext(ctx)
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

		callCtx, cancel := c.callContext(ctx)
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
