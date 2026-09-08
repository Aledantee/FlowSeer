package host

import (
	"time"

	"go.aledante.io/FlowSeer/src/edge/agent/internal/lanehost"
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

// Options are what a caller assembling this agent in its own process can
// substitute. The zero value is the packaged deployment: real dialers and the
// wall clock.
type Options struct {
	// OpenSNMP and OpenShell replace the dialers the lane opens device
	// sessions with. Nil means the real ones. See [SNMPFactoryFor] for what
	// they may and may not stand in for.
	OpenSNMP  SNMPFactoryFor
	OpenShell ShellFactoryFor
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
