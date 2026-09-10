package deviceapi

import (
	"context"
	"slices"

	connect "connectrpc.com/connect"

	devicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/device/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
)

// GetDeviceAccessStatus reports the record: how far the device's sequence has
// run, the mutation still holding the lane if there is one, the firmware epoch
// central last learned, and the last complete observation of each interface.
//
// This is the call an operator polls after an apply, so it reads and answers
// and writes nothing. The interface rows come out in name order, because a map
// would otherwise hand the same device a different answer each time.
func (s *Service) GetDeviceAccessStatus(ctx context.Context, req *connect.Request[devicev1.GetDeviceAccessStatusRequest]) (*connect.Response[devicev1.GetDeviceAccessStatusResponse], error) {
	deviceID, _, err := s.device(req.Msg.GetDevice())
	if err != nil {
		return nil, connectErr(err)
	}
	record, err := s.cfg.Journal.Record(ctx, deviceID)
	if err != nil {
		return nil, connectErr(err)
	}

	resp := &devicev1.GetDeviceAccessStatusResponse{}
	resp.SetHighWatermark(record.GetHighWatermark())
	if m := record.GetMutation(); m != nil {
		resp.SetUnresolved(m)
	}
	if fingerprint := record.GetFirmwareFingerprint(); fingerprint != "" {
		resp.SetFirmwareFingerprint(fingerprint)
	}

	observed := record.GetLastObservations()
	names := make([]string, 0, len(observed))
	for name := range observed {
		names = append(names, name)
	}
	slices.Sort(names)
	rows := make([]*accessv1.InterfaceObservation, 0, len(names))
	for _, name := range names {
		rows = append(rows, observed[name])
	}
	resp.SetInterfaces(rows)

	return connect.NewResponse(resp), nil
}
