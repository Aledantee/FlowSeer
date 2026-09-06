# snmp

FlowSeer's SNMP client supports polling, table change streams, and trap
reception. `NewSession` creates a session for one target; `ListenTraps` creates
a listener. The package owns the wire implementation and supports concurrent
requests on one session.

For example, this function reads a device description through a generated MIB
binding and closes the session before returning:

```go
package example

import (
	"context"
	"errors"

	"go.aledante.io/FlowSeer/generated/go/mib/snmpv2mib"
	"go.aledante.io/FlowSeer/src/common/secret"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

func description(ctx context.Context, target string, community secret.Value) (string, error) {
	sess, err := snmp.NewSession(ctx, target, snmp.V2c,
		snmp.WithCommunity(community))
	if err != nil {
		return "", err
	}

	value, readErr := snmpv2mib.SysDescrGet(ctx, sess)
	return value, errors.Join(readErr, sess.Close())
}
```

Pass a target such as `udp://10.0.0.1:161`. The version is a required argument;
SNMPv3 uses `snmp.V3` with `snmp.WithUSM`. Construction does not exchange SNMP
messages; USM discovery happens on the first operation.

## Reading and streaming

`Session` exposes Get, GetNext, GetBulk, Set, and subtree walks. Its reactor
matches replies by request ID, so concurrent operations do not require a
separate connection for each request. Close the session when its owner stops.

A `Walker` offers range-over-function iteration and Scanner-style
`Next`/`Current` methods. Use one iteration form and check `Err` after it ends.
Generated table walkers decode only the requested columns. A row's `Observed`
method distinguishes a reported zero from a column the device did not return.
A column decode error terminates the generated walk; check `Err` even when
some rows were returned.

`DecodeIndex` turns a row's index suffix into typed parts from the row's
`IndexShape` list. A suffix that does not fit the shapes yields `ok=false` and
zero parts instead of an error, so a bad index on one row never ends the walk.
`TableDescriptor` names a table by root OID, change indicator, and key type;
its `Present` method issues one GetNext and reports whether the agent holds an
instance under the root (an empty table reads as absent). `OIDSet` answers
longest-prefix lookups, such as resolving a sysObjectID to the deepest known
product node.

A `Watcher` retains a table snapshot and uses a change indicator to decide when
to fetch updates. It emits added, modified, and removed rows. Transient tick
errors are available through `LastTickErr`; `Err` reports the terminal cause.
Close a watcher when finished so its producer can stop.

`TrapStream` drops the oldest buffered trap when a consumer falls behind.
`Dropped` reports the loss. Source filtering and rate limiting are advisory;
use network controls when reception requires a stricter boundary.

The [package documentation](doc.go) describes the API and lifecycle contracts.

## Generating MIB bindings

`mibgen` reads the repository-root `mibgen.yaml` and writes one package per
configured module under `generated/go/mib/`. Bindings use the public SNMP API
and the repository's `errs` package. Run these commands from the repository
root:

```sh
go generate .
go run ./src/protocol/snmp/cmd/mibgen -verify
go run ./src/protocol/snmp/cmd/mibgen -check
```

`-verify` resolves the configured MIBs without writing output. `-check`
regenerates into a temporary directory and compares the result with the
committed bindings. Change the emitter or schema source, then regenerate;
hand edits to generated files are not retained.

The [generator documentation](cmd/mibgen/doc.go) explains the diagnostic
baseline and golden-fixture update command.

## Tests and conventions

Run the unit and conformance tests from the repository root:

```sh
go test -race ./src/protocol/snmp/...
```

Integration tiers require an explicit build tag. For example,
`task --dir src/protocol/snmp/test/integration t1` runs the containerized
Net-SNMP suite. The [integration guide](test/integration/README.md) covers the
other tiers and their prerequisites.

[Go style](../../../docs/code-style.md) and
[documentation style](../../../docs/doc-style.md) govern this package.
Generated output is excluded from normal lint findings, so emitter tests and
generation drift checks must cover it directly.
