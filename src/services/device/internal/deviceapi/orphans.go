package deviceapi

import (
	"context"
	"slices"

	connect "connectrpc.com/connect"

	devicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/device/v1"
)

// ListEdgeOpenMutations names the devices whose lanes an edge is still holding
// open work on.
//
// Retiring an edge ends the edge's standing and abandons nothing, because
// abandoning live work across a fleet of records is a decision only an
// operator makes. That leaves the work orphaned, and the operator has to learn
// which lanes they now have to end — from this call, at the moment they
// retire, rather than from a lane found stuck weeks later. So each row carries
// the sequence AbandonMutation takes and the phase the mutation stopped at,
// which is what makes it something to act on rather than a count.
func (s *Service) ListEdgeOpenMutations(ctx context.Context, req *connect.Request[devicev1.ListEdgeOpenMutationsRequest]) (*connect.Response[devicev1.ListEdgeOpenMutationsResponse], error) {
	edgeID := req.Msg.GetEdgeId()
	devices, err := s.cfg.Resolver.Devices(ctx, edgeID)
	if err != nil {
		return nil, connectErr(err)
	}
	slices.Sort(devices)

	rows := make([]*devicev1.OpenMutation, 0, len(devices))
	for _, deviceID := range devices {
		record, err := s.cfg.Journal.Record(ctx, deviceID)
		if err != nil {
			// One unreadable record must not hide the rest: an operator acting
			// on a short list would think the others were finished.
			return nil, connectErr(err)
		}
		mutation := record.GetMutation()
		if mutation == nil {
			continue
		}
		row := &devicev1.OpenMutation{}
		row.SetDevice(record.GetDevice())
		row.SetMutation(mutation)
		rows = append(rows, row)
	}

	resp := &devicev1.ListEdgeOpenMutationsResponse{}
	resp.SetOpen(rows)
	return connect.NewResponse(resp), nil
}
