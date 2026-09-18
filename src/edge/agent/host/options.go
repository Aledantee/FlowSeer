package host

import (
	"context"
	"log/slog"
	"time"

	"go.aledante.io/FlowSeer/src/edge/agent/internal/lanehost"
	"go.aledante.io/FlowSeer/src/modules/capture"
)

// Endpoint is where one device answers: its address and, when they are not
// the defaults, its SNMP and SSH ports.
//
// Aliased from the package that computes it so a caller outside this
// application can write a factory taking one. A field typed with a name it
// cannot spell is a field it cannot fill, which is how the access module came
// to export its own seam types.
type Endpoint = lanehost.Endpoint

// SNMPFactoryFor builds a device's SNMP session factory from where that
// device answers. [ShellFactoryFor] is its counterpart for the shell.
//
// What they substitute is the transport and nothing else. The factory is
// handed the endpoint the agent computed from central's listing and, on each
// call, the credential the agent acquired for that operation — so the address
// resolution, the credential acquisition and the host-key pin all still
// happen, and what changes is only how the connection is made. It is not a
// way to run an operation without them, and widening it into one would make
// every test above it pass with the checks it is meant to exercise switched
// off.
//
// A deployment reaching its devices through a bastion, or a device family
// that answers over something else, is the production shape of the same seam.
type SNMPFactoryFor = lanehost.SNMPFactoryFor

// ShellFactoryFor builds a device's shell session factory from where that
// device answers. See [SNMPFactoryFor] for what it may and may not stand in
// for.
type ShellFactoryFor = lanehost.ShellFactoryFor

// CaptureSourceOpener opens the packet source one capture session reads from.
// The boolean return says whether that source's Stats reports a real kernel
// drop count; a source that counts nothing answers false, so the session
// reports no drop counter rather than a zero it cannot stand behind.
//
// What it stands in for is the whole of [capture.New], not its socket step.
// New validates the budget, refuses a snap length over 65535, and compiles
// cfg.Filter into the cBPF program it attaches to the socket it opens; an
// opener reaches the engine through [capture.NewWithSource], which does none
// of that and has no userspace filter stage to do it in. So an opener owns
// cfg.Filter: a session's filter is applied where the source is opened or it
// is not applied at all, and a capture wider than the operator authorized is
// what that costs.
type CaptureSourceOpener func(ctx context.Context, cfg capture.Config) (capture.Source, bool, error)

// Options are what a caller assembling this agent in its own process can
// substitute. The zero value is the packaged deployment: real dialers and the
// wall clock.
type Options struct {
	// OpenSNMP and OpenShell replace the dialers the lane opens device
	// sessions with. Nil means the real ones. See [SNMPFactoryFor] for what
	// they may and may not stand in for.
	OpenSNMP  SNMPFactoryFor
	OpenShell ShellFactoryFor
	// OpenCaptureSource replaces how a capture session's packet source is
	// opened. Nil is the packaged deployment: [capture.New], with its
	// validation and its compiled filter. A non-nil opener takes both on
	// itself, for every session this agent runs; see [CaptureSourceOpener].
	OpenCaptureSource CaptureSourceOpener
	// Logger overrides the base logger the agent logs to. Nil means stderr with
	// [Config.LogLevel].
	Logger *slog.Logger
	// CaptureInactivityTimeout overrides the idle duration before an in-flight
	// capture session aborts. Zero uses the default (60 seconds).
	CaptureInactivityTimeout time.Duration
	// Clock is what the lane reads the time from: every audit record's
	// timestamp, the operation-duration measurement, the moment a mutation
	// was submitted, and the recovery runner's own waiting. Nil means
	// [time.Now].
	//
	// Substituting it changes what the agent believes the time is and
	// nothing else. It skips no check and takes no branch away, which is why
	// it is a plain field rather than something more careful: an operation
	// that has to wait out a delayed-apply horizon is otherwise reachable
	// only by waiting out a delayed-apply horizon.
	Clock func() time.Time
}
