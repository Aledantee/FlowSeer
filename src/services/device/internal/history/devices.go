package history

import (
	"context"
	"time"

	"github.com/google/uuid"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/tenant"
)

// Device is one registry device as the dimension table stores it.
type Device struct {
	// ID is the device's UUID.
	ID string
	// Name is the operator-assigned display name, empty when none was set.
	Name string
	// LocationID is the UUID of the location the operator placed the device
	// in, empty when it is unplaced.
	LocationID string
}

// MirrorDevices upserts devices of one tenant into device_dim. Each row
// carries the current time as its version, so the newest mirror of a device
// wins at merge and a read takes it with FINAL. It returns after the server
// holds the rows. A device with an invalid ID or location rejects the whole
// call before anything is sent.
func (s *Store) MirrorDevices(ctx context.Context, tenantID string, devices []Device) error {
	if err := tenant.Validate(tenantID); err != nil {
		return errs.From(err).Code(ErrCodeInsert).Attr("tenant", tenantID).Msg("mirror devices")
	}
	if len(devices) == 0 {
		return nil
	}

	type row struct {
		id, location uuid.UUID
		name         string
	}
	rows := make([]row, 0, len(devices))
	for _, d := range devices {
		id, err := uuid.Parse(d.ID)
		if err != nil {
			return errs.From(err).Code(ErrCodeInsert).Attr("device", d.ID).Msg("invalid device id")
		}
		var location uuid.UUID
		if d.LocationID != "" {
			if location, err = uuid.Parse(d.LocationID); err != nil {
				return errs.From(err).Code(ErrCodeInsert).Attr("device", d.ID).Attr("location", d.LocationID).Msg("invalid location id")
			}
		}
		rows = append(rows, row{id: id, location: location, name: d.Name})
	}

	batch, err := s.writer.PrepareBatch(ctx, "INSERT INTO "+quote(s.cfg.Database)+".device_dim (tenant_id, device_id, name, location_id, site_id, ver)")
	if err != nil {
		return errs.From(err).Code(ErrCodeInsert).Msg("prepare device mirror batch")
	}
	defer func() { _ = batch.Close() }()

	version := uint64(time.Now().UnixNano())
	for _, r := range rows {
		if err := batch.Append(tenantID, r.id, r.name, r.location, "", version); err != nil {
			return errs.From(err).Code(ErrCodeInsert).Msg("append device row")
		}
	}
	if err := batch.Send(); err != nil {
		return errs.From(err).Code(ErrCodeInsert).Msg("send device mirror batch")
	}
	return nil
}
