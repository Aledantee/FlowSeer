package telemetry

import (
	"context"
	"log/slog"

	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/inventory/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
)

// event writes an OpenTelemetry Event, per docs/conventions/observability.md's
// "Distinguish logs from named events" section: an INFO record carrying the
// stable otel.event.name bridge attribute, with no dynamic values in the
// message body itself.
func (v *View) event(ctx context.Context, name, message string, attrs ...slog.Attr) {
	if v == nil {
		return
	}

	args := make([]slog.Attr, 0, len(attrs)+1)
	args = append(args, slog.String("otel.event.name", name))
	args = append(args, attrs...)

	v.logger.LogAttrs(ctx, slog.LevelInfo, message, args...)
}

// eventAt is [View.event] for an event whose level is not INFO. Every event
// in this package is a lifecycle fact and INFO is right for almost all of
// them; one is a statement that the record of what happened is wrong, which
// is not a lifecycle fact.
func (v *View) eventAt(ctx context.Context, level slog.Level, name, message string, attrs ...slog.Attr) {
	if v == nil {
		return
	}

	args := make([]slog.Attr, 0, len(attrs)+1)
	args = append(args, slog.String("otel.event.name", name))
	args = append(args, attrs...)

	v.logger.LogAttrs(ctx, level, message, args...)
}

// RouteSelected emits flowseer.device.route.selected for every route
// resolution, and additionally emits flowseer.device.route.fallback when
// fellThrough is true — the direction record's decision 1 case where a
// valid-but-incomplete primary read fell through to a complete route.
func (v *View) RouteSelected(ctx context.Context, protocol inventoryv1.ManagementProtocol, fellThrough bool) {
	v.event(ctx, "flowseer.device.route.selected", "route selected",
		slog.String("flowseer.device.route", protocol.String()),
		slog.Bool("flowseer.device.route.fell_through", fellThrough),
	)
	if fellThrough {
		v.event(ctx, "flowseer.device.route.fallback", "route fell through to fallback",
			slog.String("flowseer.device.route", protocol.String()),
		)
	}
}

// DiscoveryCompleted emits flowseer.device.discovery.completed once identity
// and capability discovery finishes for a device.
func (v *View) DiscoveryCompleted(ctx context.Context, firmwareFingerprint string) {
	v.event(ctx, "flowseer.device.discovery.completed", "discovery completed",
		slog.String("flowseer.device.firmware_fingerprint", firmwareFingerprint),
	)
}

// FirmwareEpochChanged emits flowseer.device.firmware.epoch_changed when a
// device's firmware fingerprint differs from the one route evidence was
// learned under. Lane emits it after either of its mid-operation identity
// probes establishes a change.
func (v *View) FirmwareEpochChanged(ctx context.Context) {
	v.event(ctx, "flowseer.device.firmware.epoch_changed", "firmware epoch changed")
}

// RecoveryStarted emits flowseer.device.recovery.started when a mutation
// whose effect could not be established enters recovery.
func (v *View) RecoveryStarted(ctx context.Context) {
	v.event(ctx, "flowseer.device.recovery.started", "recovery started")
}

// LaneFrozen emits flowseer.device.lane.frozen when the lane pauses because
// the hosting edge's own contact could not be confirmed.
func (v *View) LaneFrozen(ctx context.Context) {
	v.event(ctx, "flowseer.device.lane.frozen", "lane frozen")
}

// LaneBlocked emits flowseer.device.lane.blocked when the lane stops
// admitting the next mutation.
func (v *View) LaneBlocked(ctx context.Context, reason accessv1.BlockReason) {
	v.event(ctx, "flowseer.device.lane.blocked", "lane blocked",
		slog.String("flowseer.device.reason", reason.String()),
	)
}

// AuditGap emits flowseer.device.audit.gap when a mutation ends still
// holding audit records the stream never took.
//
// At ERROR, because it is the account of a device being wrong rather than a
// device being slow: a reader of the stream cannot tell a gap from a device
// nothing happened to, and the mutation this befalls is the one an operator
// will later have to resolve. The event ids are named so the missing records
// can be identified as missing rather than merely absent.
func (v *View) AuditGap(ctx context.Context, deviceKey string, sequence uint64, eventIDs []string) {
	v.eventAt(ctx, slog.LevelError, "flowseer.device.audit.gap", "audit records were never delivered",
		slog.String("flowseer.device.id", deviceKey),
		slog.Uint64("flowseer.device.sequence", sequence),
		slog.Any("flowseer.device.audit.missing_event_ids", eventIDs),
	)
}

// LaneReleased emits flowseer.device.lane.released when the lane is free for
// the next mutation.
func (v *View) LaneReleased(ctx context.Context) {
	v.event(ctx, "flowseer.device.lane.released", "lane released")
}
