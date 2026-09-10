package host

import (
	"context"

	integrationv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1"
)

// Outbound is where a report goes on its way to central. The agent's re-send
// queue implements it, and it must not block: the lane calls the reporter on
// the goroutine that did the work, while holding the device's drain lock.
type Outbound interface {
	Report(ctx context.Context, report *integrationv1.ReportRequest)
}

// laneReporter turns what the lane reports into what central is owed. It is
// the whole of the adapter: the lane names the device, this addresses the
// report to it, and the queue re-sends it until central confirms.
//
// The device is why this cannot be done from the messages alone. None of the
// three carries one — a sequence is unique per device and the execution
// envelope leaves addressing to the transport — while ReportRequest.device_id
// is required. A reporter that had to guess would guess wrong the first time
// an edge served two devices.
type laneReporter struct{ out Outbound }

// Reported carries one operation's progress, observation, error or terminal
// phase.
func (r laneReporter) Reported(ctx context.Context, deviceKey string, result *integrationv1.ExecuteResult) {
	r.send(ctx, deviceKey, func(report *integrationv1.ReportRequest) { report.SetResult(result) })
}

// CheckpointAcked carries the acknowledgement of central's checkpoint. It is
// the one report central waits on: without it the mutation's row stays owed
// and central re-dispatches the checkpoint for as long as the edge lives.
func (r laneReporter) CheckpointAcked(ctx context.Context, deviceKey string, ack *integrationv1.CheckpointAck) {
	r.send(ctx, deviceKey, func(report *integrationv1.ReportRequest) { report.SetCheckpointAck(ack) })
}

// HoldResolvedAcked carries the acknowledgement of a hold central cleared.
func (r laneReporter) HoldResolvedAcked(ctx context.Context, deviceKey string, ack *integrationv1.HoldResolvedAck) {
	r.send(ctx, deviceKey, func(report *integrationv1.ReportRequest) { report.SetHoldResolvedAck(ack) })
}

// Onboarded carries the device's firmware epoch, and the fact that this edge
// has just onboarded it. Central sends nothing that asks for this: the edge
// makes it at start, which is why the queue never drops one.
func (r laneReporter) Onboarded(ctx context.Context, deviceKey, fingerprint string) {
	onboarded := &integrationv1.Onboarded{}
	onboarded.SetFirmwareFingerprint(fingerprint)
	r.send(ctx, deviceKey, func(report *integrationv1.ReportRequest) { report.SetOnboarded(onboarded) })
}

func (r laneReporter) send(ctx context.Context, deviceKey string, set func(*integrationv1.ReportRequest)) {
	report := &integrationv1.ReportRequest{}
	report.SetDeviceId(deviceKey)
	set(report)
	r.out.Report(ctx, report)
}
