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
	Tenants         int
	Roles           int
	Platforms       int
}

// Total returns the total number of objects repaired.
func (c RepairedCounts) Total() int {
	return c.Edges + c.Devices + c.CaptureSessions + c.Tenants + c.Roles + c.Platforms
}

// Each value says whether ownership requires the access source.
var ownedRelations = map[string]map[string]bool{
	"edge":            {"tenant": false, "administer": true, "operate": true, "capture": true, "view": true},
	"device":          {"tenant": false, "operate": true, "view": true},
	"capture_session": {"tenant": false, "edge": false, "requester": false},
	"tenant":          {"platform": true, "enrolled": true, "partner": true, "admin": true, "operator": true, "capturer": true, "viewer": true, "full_payload": true},
	"role":            {"assignee": true},
	"platform":        {"enrolled": true},
}

func (p *Projector) isOwnedRelation(objType, relation string) bool {
	requiresAccess, owned := ownedRelations[objType][relation]
	return owned && (!requiresAccess || p.access != nil)
}

func (p *Projector) filterOwnedTuples(objType string, tuples []authz.Tuple) []authz.Tuple {
	var filtered []authz.Tuple
	for _, t := range tuples {
		if p.isOwnedRelation(objType, t.Relation) {
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

type accessSnapshot struct {
	tuples  map[string][]authz.Tuple
	members map[string]map[string]bool
	roles   map[string]string
}

func (p *Projector) snapshotAccess(ctx context.Context) (accessSnapshot, error) {
	snapshot := accessSnapshot{
		tuples:  make(map[string][]authz.Tuple),
		members: make(map[string]map[string]bool),
		roles:   make(map[string]string),
	}
	committed := make(map[string]bool)
	if p.tenants != nil {
		records, err := p.tenants.List(ctx)
		if err != nil {
			return accessSnapshot{}, err
		}
		for _, rec := range records {
			committed[rec.GetConfig().GetRef().GetTenant().GetId()] = true
		}
	}
	ids, err := p.access.TenantIDs(ctx)
	if err != nil {
		return accessSnapshot{}, err
	}
	for _, id := range ids {
		if _, ok := committed[id]; !ok {
			committed[id] = false
		}
	}
	for id, hasRecord := range committed {
		members, err := p.access.Members(ctx, id)
		if err != nil {
			return accessSnapshot{}, err
		}
		roles, err := p.access.Roles(ctx, id)
		if err != nil {
			return accessSnapshot{}, err
		}
		partners, err := p.access.Partners(ctx, id)
		if err != nil {
			return accessSnapshot{}, err
		}
		snapshot.tuples["tenant:"+id] = tenantTuples(id, hasRecord, members, roles, partners, p.now())
		snapshot.members[id] = make(map[string]bool, len(members))
		for _, member := range members {
			operator := member.GetOperator()
			snapshot.members[id][authn.ComputePrincipalID(operator.GetIssuer(), operator.GetSubject())] = true
		}
		for _, role := range roles {
			roleID := role.GetRef().GetRole().GetId()
			snapshot.roles[roleID] = id
			snapshot.tuples["role:"+roleID] = roleTuples(roleID, roles, members)
		}
	}
	return snapshot, nil
}

// Reconcile performs a single reconciliation pass against the engine. It
// continues independent repairs after an object or source fails and returns
// the collected errors. The [Projector]'s Reconciled callback fires only when
// this method returns no error.
func (p *Projector) Reconcile(ctx context.Context) (RepairedCounts, error) {
	var counts RepairedCounts
	var reconcileErrs []error
	appendError := func(err error) bool {
		if err == nil {
			return false
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			reconcileErrs = append(reconcileErrs, ctxErr)
			return true
		}
		reconcileErrs = append(reconcileErrs, err)
		return false
	}
	checkContext := func() bool {
		if ctxErr := ctx.Err(); ctxErr != nil {
			reconcileErrs = append(reconcileErrs, ctxErr)
			return true
		}
		return false
	}

	if checkContext() {
		return counts, errors.Join(reconcileErrs...)
	}
	edgeMap, err := p.edges.All(ctx)
	if err != nil {
		if appendError(err) {
			return counts, errors.Join(reconcileErrs...)
		}
	}
	edgesComplete := err == nil
	if checkContext() {
		return counts, errors.Join(reconcileErrs...)
	}
	var access accessSnapshot
	accessComplete := p.access == nil
	if p.access != nil {
		access, err = p.snapshotAccess(ctx)
		if err != nil {
			if appendError(err) {
				return counts, errors.Join(reconcileErrs...)
			}
		} else {
			accessComplete = true
		}
	}
	if checkContext() {
		return counts, errors.Join(reconcileErrs...)
	}

	type sessionInfo struct {
		tenantID string
		tuples   []authz.Tuple
	}
	sessionSnapshot := make(map[string]sessionInfo)
	sessionErr := p.captures.EachSession(ctx, func(tenantID string, rec *modelcapturev1.CaptureSessionRecord) error {
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
		if reqBy != nil && reqBy.GetIssuer() != "" && (p.access == nil || access.members[tenantID][authn.ComputePrincipalID(reqBy.GetIssuer(), reqBy.GetSubject())]) {
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
	})
	if sessionErr != nil {
		if appendError(sessionErr) {
			return counts, errors.Join(reconcileErrs...)
		}
	}
	sessionsComplete := sessionErr == nil && accessComplete
	if checkContext() {
		return counts, errors.Join(reconcileErrs...)
	}

	deviceSnapshot := make(map[string]string)
	deviceUnsafe := make(map[string]bool)
	devicesComplete := false
	if p.registry != nil {
		regEdgeID := p.registry.EdgeID()
		if regEdgeID != "" {
			deviceIDs, err := p.registry.Devices(ctx, regEdgeID)
			if err != nil {
				if appendError(err) {
					return counts, errors.Join(reconcileErrs...)
				}
			} else {
				devicesComplete = true
				for _, id := range deviceIDs {
					deviceSnapshot[id] = regEdgeID
				}
			}
		} else {
			devicesComplete = true
		}
	}
	if checkContext() {
		return counts, errors.Join(reconcileErrs...)
	}

	expectedPerObject := make(map[string][]authz.Tuple)
	if edgesComplete {
		for edgeID, tenantID := range edgeMap {
			expectedPerObject["edge:"+edgeID] = []authz.Tuple{
				{Object: "edge:" + edgeID, Relation: "tenant", User: "tenant:" + tenantID},
			}
		}
	}
	for devID, edgeID := range deviceSnapshot {
		tenantID := edgeMap[edgeID]
		if tenantID == "" {
			tenantID, err = p.edges.TenantForEdge(ctx, edgeID)
			if err != nil {
				deviceUnsafe[devID] = true
				if appendError(err) {
					return counts, errors.Join(reconcileErrs...)
				}
				continue
			}
		}
		if tenantID != "" {
			expectedPerObject["device:"+devID] = []authz.Tuple{
				{Object: "device:" + devID, Relation: "tenant", User: "tenant:" + tenantID},
			}
		}
	}
	if accessComplete {
		for sessionID, sInfo := range sessionSnapshot {
			expectedPerObject["capture_session:"+sessionID] = sInfo.tuples
		}
	}
	if p.access != nil && accessComplete {
		for object, tuples := range access.tuples {
			expectedPerObject[object] = tuples
		}
	}
	if p.access != nil {
		var platform []authz.Tuple
		for _, id := range p.platformPrincipals {
			platform = append(platform, authz.Tuple{Object: "platform:flowseer", Relation: "enrolled", User: "user:" + id})
		}
		expectedPerObject["platform:flowseer"] = platform
	}

	scannedOwned := make(map[string][]authz.Tuple)
	err = p.relations.Scan(ctx, func(t authz.Tuple) error {
		objType, _, hasSep := strings.Cut(t.Object, ":")
		if !hasSep {
			return nil
		}
		if p.isOwnedRelation(objType, t.Relation) {
			scannedOwned[t.Object] = append(scannedOwned[t.Object], t)
		}
		return nil
	})
	if err != nil {
		if appendError(err) {
			return counts, errors.Join(reconcileErrs...)
		}
	}
	scanComplete := err == nil
	if checkContext() {
		return counts, errors.Join(reconcileErrs...)
	}

	objectsToSync := make(map[string]Object)

	for objKey, expTuples := range expectedPerObject {
		scanTuples := scannedOwned[objKey]
		if !sameTuples(expTuples, scanTuples) {
			objType, objID, _ := strings.Cut(objKey, ":")
			var tenant string
			switch objType {
			case "capture_session":
				tenant = sessionSnapshot[objID].tenantID
			case "role":
				tenant = access.roles[objID]
			}
			objectsToSync[objKey] = Object{Type: objType, ID: objID, Tenant: tenant}
		}
	}

	for objKey, scanTuples := range scannedOwned {
		if _, already := objectsToSync[objKey]; already {
			continue
		}
		if !scanComplete {
			continue
		}
		expTuples, inSnapshot := expectedPerObject[objKey]
		objType, objID, _ := strings.Cut(objKey, ":")
		switch objType {
		case "edge":
			if !edgesComplete {
				continue
			}
		case "device":
			if !devicesComplete || deviceUnsafe[objID] {
				continue
			}
		case "capture_session":
			if !sessionsComplete {
				continue
			}
		case "tenant", "role":
			if !accessComplete {
				continue
			}
		}
		if !inSnapshot || !sameTuples(expTuples, scanTuples) {
			var tenant string
			switch objType {
			case "capture_session":
				tenant = sessionSnapshot[objID].tenantID
			case "role":
				tenant = access.roles[objID]
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
		if checkContext() {
			return counts, errors.Join(reconcileErrs...)
		}
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
			case "tenant":
				counts.Tenants++
			case "role":
				counts.Roles++
			case "platform":
				counts.Platforms++
			}
		}
	}

	return counts, errors.Join(reconcileErrs...)
}
