package projector

import (
	"context"
	"errors"
	"slices"
	"strings"

	modelcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz"
)

// RepairedCounts records the number of objects repaired during a reconciliation pass.
type RepairedCounts struct {
	Edges           int
	Devices         int
	CaptureSessions int
}

// Total returns the total number of objects repaired.
func (c RepairedCounts) Total() int {
	return c.Edges + c.Devices + c.CaptureSessions
}

func isOwnedRelation(objType, relation string) bool {
	switch objType {
	case "edge", "device":
		return relation == "tenant"
	case "capture_session":
		return relation == "tenant" || relation == "edge" || relation == "requester"
	default:
		return false
	}
}

func filterOwnedTuples(objType string, tuples []authz.Tuple) []authz.Tuple {
	var filtered []authz.Tuple
	for _, t := range tuples {
		if isOwnedRelation(objType, t.Relation) {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

func containsTuple(tuples []authz.Tuple, target authz.Tuple) bool {
	for _, t := range tuples {
		if t == target {
			return true
		}
	}
	return false
}

func diffTuples(existing, desired []authz.Tuple) (writes, deletes []authz.Tuple) {
	for _, d := range desired {
		if !containsTuple(existing, d) {
			writes = append(writes, d)
		}
	}
	for _, e := range existing {
		if !containsTuple(desired, e) {
			deletes = append(deletes, e)
		}
	}
	return writes, deletes
}

func sameTuples(a, b []authz.Tuple) bool {
	if len(a) != len(b) {
		return false
	}
	for _, t := range a {
		if !containsTuple(b, t) {
			return false
		}
	}
	return true
}

// Reconcile performs a single reconciliation pass against the engine.
func (p *Projector) Reconcile(ctx context.Context) (RepairedCounts, error) {
	var counts RepairedCounts
	var reconcileErrs []error

	edgeMap, err := p.edges.All(ctx)
	if err != nil {
		return counts, err
	}

	type sessionInfo struct {
		tenantID string
		tuples   []authz.Tuple
	}
	sessionSnapshot := make(map[string]sessionInfo)
	if err := p.captures.EachSession(ctx, func(tenantID string, rec *modelcapturev1.CaptureSessionRecord) error {
		sessionID := rec.GetConfig().GetRef().GetCaptureSession().GetId()
		objectKey := "capture_session:" + sessionID
		tuples := []authz.Tuple{
			{Object: objectKey, Relation: "tenant", User: "tenant:" + tenantID},
		}
		if edgeID := rec.GetConfig().GetRef().GetEdge().GetEdge().GetId(); edgeID != "" {
			tuples = append(tuples, authz.Tuple{
				Object:   objectKey,
				Relation: "edge",
				User:     "edge:" + edgeID,
			})
		}
		reqBy := rec.GetConfig().GetAuthorization().GetRequestedBy()
		if reqBy != nil && reqBy.GetIssuer() != "" {
			principalID := authn.ComputePrincipalID(reqBy.GetIssuer(), reqBy.GetSubject())
			tuples = append(tuples, authz.Tuple{
				Object:   objectKey,
				Relation: "requester",
				User:     "user:" + principalID,
			})
		}
		sessionSnapshot[sessionID] = sessionInfo{
			tenantID: tenantID,
			tuples:   tuples,
		}
		return nil
	}); err != nil {
		reconcileErrs = append(reconcileErrs, err)
	}

	deviceSnapshot := make(map[string]string)
	if p.registry != nil {
		regEdgeID := p.registry.EdgeID()
		if regEdgeID != "" {
			deviceIDs, err := p.registry.Devices(ctx, regEdgeID)
			if err != nil {
				return counts, err
			}
			for _, id := range deviceIDs {
				deviceSnapshot[id] = regEdgeID
			}
		}
	}

	expectedPerObject := make(map[string][]authz.Tuple)
	for edgeID, tenantID := range edgeMap {
		expectedPerObject["edge:"+edgeID] = []authz.Tuple{
			{Object: "edge:" + edgeID, Relation: "tenant", User: "tenant:" + tenantID},
		}
	}
	for devID, edgeID := range deviceSnapshot {
		tenantID := edgeMap[edgeID]
		if tenantID == "" {
			tenantID, err = p.edges.TenantForEdge(ctx, edgeID)
			if err != nil {
				return counts, err
			}
		}
		if tenantID != "" {
			expectedPerObject["device:"+devID] = []authz.Tuple{
				{Object: "device:" + devID, Relation: "tenant", User: "tenant:" + tenantID},
			}
		}
	}
	for sessionID, sInfo := range sessionSnapshot {
		expectedPerObject["capture_session:"+sessionID] = sInfo.tuples
	}

	scannedOwned := make(map[string][]authz.Tuple)
	err = p.relations.Scan(ctx, func(t authz.Tuple) error {
		objType, _, hasSep := strings.Cut(t.Object, ":")
		if !hasSep {
			return nil
		}
		if isOwnedRelation(objType, t.Relation) {
			scannedOwned[t.Object] = append(scannedOwned[t.Object], t)
		}
		return nil
	})
	if err != nil {
		return counts, err
	}

	objectsToSync := make(map[string]Object)

	for objKey, expTuples := range expectedPerObject {
		scanTuples := scannedOwned[objKey]
		if !sameTuples(expTuples, scanTuples) {
			objType, objID, _ := strings.Cut(objKey, ":")
			var tenant string
			if objType == "capture_session" {
				tenant = sessionSnapshot[objID].tenantID
			}
			objectsToSync[objKey] = Object{Type: objType, ID: objID, Tenant: tenant}
		}
	}

	for objKey, scanTuples := range scannedOwned {
		if _, already := objectsToSync[objKey]; already {
			continue
		}
		expTuples, inSnapshot := expectedPerObject[objKey]
		if !inSnapshot || !sameTuples(expTuples, scanTuples) {
			objType, objID, _ := strings.Cut(objKey, ":")
			var tenant string
			if objType == "capture_session" {
				tenant = sessionSnapshot[objID].tenantID
			}
			objectsToSync[objKey] = Object{Type: objType, ID: objID, Tenant: tenant}
		}
	}

	keys := make([]string, 0, len(objectsToSync))
	for k := range objectsToSync {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	for _, k := range keys {
		obj := objectsToSync[k]
		repaired, err := p.syncObject(ctx, obj)
		if err != nil {
			reconcileErrs = append(reconcileErrs, err)
			continue
		}
		if repaired {
			switch obj.Type {
			case "edge":
				counts.Edges++
			case "device":
				counts.Devices++
			case "capture_session":
				counts.CaptureSessions++
			}
		}
	}

	return counts, errors.Join(reconcileErrs...)
}
