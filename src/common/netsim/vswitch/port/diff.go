package port

import (
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

// Diff computes the difference between two port tables, reporting added and removed
// ports as well as changes to admin_status, mtu, and lag_parent.
func Diff(a, b Table) []trace.Change {
	var changes []trace.Change

	for _, ap := range a.ports {
		bp, exists := b.Port(ap.Name)
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

		if ap.MTU != bp.MTU {
			changes = append(changes, trace.Change{
				Layer: LayerPort,
				Subject: trace.Subject{
					Kind: "port",
					Key:  ap.Name,
				},
				Field: "mtu",
				From:  ap.MTU,
				To:    bp.MTU,
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
				From:  ap.LagParent,
				To:    bp.LagParent,
			})
		}
	}

	for _, bp := range b.ports {
		if _, exists := a.Port(bp.Name); !exists {
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
