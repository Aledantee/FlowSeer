package collect

import (
	"context"
	"errors"

	"go.aledante.io/FlowSeer/generated/go/mib/snmpv2mib"
	"go.aledante.io/FlowSeer/generated/go/mib/sysobjectid"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// ErrCodeIdentity identifies a failed sysObjectID read. The cycle goes on
// without an identity; mappers restricted to sysObjectID prefixes cannot
// apply to it.
var ErrCodeIdentity = errs.NewCode("collect/identity")

// Identity is what one cycle learned about the device's make and model.
type Identity struct {
	// SysObjectID is the OID the agent reported, kept even when no MIB
	// names it so a caller can report the unresolved OID. The zero OID
	// means the read failed.
	SysObjectID snmp.OID
	// Entry is the deepest naming node the vendored MIBs declare above
	// SysObjectID. Meaningful only when Known is true.
	Entry sysobjectid.Entry
	// Known reports whether Entry resolved.
	Known bool
}

// Result is one mapper's outcome for a cycle.
type Result struct {
	// Mapper is the mapper's [Spec.Name].
	Mapper string
	// Output is what the mapper returned, of the type the mapper documents.
	// A mapper that declined returns its zero output; one that degraded
	// returns a partial output beside Err.
	Output any
	// Err is the mapper's error, nil when it mapped cleanly.
	Err error
}

// Cycle is the outcome of one collection cycle.
type Cycle struct {
	// Identity is the device's identity as read this cycle.
	Identity Identity
	// Results hold one entry per applicable mapper, in registration order.
	// A mapper that did not apply has no entry.
	Results []Result
	// Snapshot is what the cycle read, for a caller that wants rows a
	// mapper did not turn into messages.
	Snapshot *Snapshot
}

// Collector runs collection cycles for a fixed set of mappers. The zero
// value has no mappers and collects nothing but the identity. A Collector
// is safe for concurrent use; each cycle is independent.
type Collector struct {
	mappers []Mapper
}

// New returns a collector over mappers, which apply in the order given.
func New(mappers ...Mapper) *Collector {
	return &Collector{mappers: mappers}
}

// Collect runs one cycle against sess: it reads sysObjectID once, decides
// which mappers apply, reads the union of their tables and scalars through
// [Read], and maps the snapshot with each applicable mapper.
//
// The returned error joins the identity read failure, the presence probes
// that failed, and every mapper's error; the Cycle beside it carries
// whatever was still collected. A caller therefore cannot read the error
// as "no data" and must inspect the results too.
func (c *Collector) Collect(ctx context.Context, sess snmp.Session) (Cycle, error) {
	var errList []error

	identity, err := readIdentity(ctx, sess)
	if err != nil {
		errList = append(errList, err)
	}

	applicable, err := detect(ctx, sess, c.mappers, identity.SysObjectID)
	if err != nil {
		errList = append(errList, err)
	}

	snap := Read(ctx, sess, applicable...)

	results := make([]Result, 0, len(applicable))

	for _, m := range applicable {
		out, err := m.Map(snap)
		if err != nil {
			errList = append(errList, errs.Wrap(err, "map "+m.Spec().Name))
		}

		results = append(results, Result{Mapper: m.Spec().Name, Output: out, Err: err})
	}

	return Cycle{Identity: identity, Results: results, Snapshot: snap}, errors.Join(errList...)
}

// readIdentity reads sysObjectID and resolves it. An OID no vendored MIB
// declares is kept as read, with Known false.
func readIdentity(ctx context.Context, sess snmp.Session) (Identity, error) {
	oid, err := snmpv2mib.SysObjectIDGet(ctx, sess)
	if err != nil {
		return Identity{}, errs.From(err).Code(ErrCodeIdentity).Msg("read sysObjectID")
	}

	entry, known := sysobjectid.Lookup(oid)

	return Identity{SysObjectID: oid, Entry: entry, Known: known}, nil
}
