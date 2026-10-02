package port

import (
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

// Diff computes the difference between two port tables, reporting added and removed
// ports as well as changes to ifindex, kind, admin_status, oper_status, mtu, and lag_parent.
func Diff(a, b Table) []trace.Change {
	na := a.Normalize()
	nb := b.Normalize()
	var changes []trace.Change

	for _, ap := range na.ports {
		bp, exists := nb.Port(ap.Name)
		if !exists {
			changes = append(changes, trace.Change{
				Layer: LayerPort,
				Subject: trace.Subject{
					Kind: "port",
					Key:  ap.Name,
				},
				Field: "",
				From:  ap,
				To:    nil,
			})

			continue
		}

		if ap.IfIndex != bp.IfIndex {
			changes = append(changes, trace.Change{
				Layer: LayerPort,
				Subject: trace.Subject{
					Kind: "port",
					Key:  ap.Name,
				},
				Field: "ifindex",
				From:  IfIndexFact(ap.IfIndex),
				To:    IfIndexFact(bp.IfIndex),
			})
		}

		if ap.Kind != bp.Kind {
			changes = append(changes, trace.Change{
				Layer: LayerPort,
				Subject: trace.Subject{
					Kind: "port",
					Key:  ap.Name,
				},
				Field: "kind",
				From:  ap.Kind,
				To:    bp.Kind,
			})
		}

		if ap.AdminStatus != bp.AdminStatus {
			changes = append(changes, trace.Change{
				Layer: LayerPort,
				Subject: trace.Subject{
					Kind: "port",
					Key:  ap.Name,
				},
				Field: "admin_status",
				From:  ap.AdminStatus,
				To:    bp.AdminStatus,
			})
		}

		if ap.OperStatus != bp.OperStatus {
			changes = append(changes, trace.Change{
				Layer: LayerPort,
				Subject: trace.Subject{
					Kind: "port",
					Key:  ap.Name,
				},
				Field: "oper_status",
				From:  ap.OperStatus,
				To:    bp.OperStatus,
			})
		}

		if ap.MTU != bp.MTU {
			changes = append(changes, trace.Change{
				Layer: LayerPort,
				Subject: trace.Subject{
					Kind: "port",
					Key:  ap.Name,
				},
				Field: "mtu",
				From:  MTUFact(ap.MTU),
				To:    MTUFact(bp.MTU),
			})
		}

		if ap.LagParent != bp.LagParent {
			changes = append(changes, trace.Change{
				Layer: LayerPort,
				Subject: trace.Subject{
					Kind: "port",
					Key:  ap.Name,
				},
				Field: "lag_parent",
				From:  LagParentFact(ap.LagParent),
				To:    LagParentFact(bp.LagParent),
			})
		}
	}

	for _, bp := range nb.ports {
		if _, exists := na.Port(bp.Name); !exists {
			changes = append(changes, trace.Change{
				Layer: LayerPort,
				Subject: trace.Subject{
					Kind: "port",
					Key:  bp.Name,
				},
				Field: "",
				From:  nil,
				To:    bp,
			})
		}
	}

	return changes
}
