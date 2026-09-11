package lag

import (
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

func effectiveMode(m Mode) Mode {
	if m == "" {
		return ActiveBackup
	}

	return m
}

func effectiveLACPMode(m LACPMode) LACPMode {
	if m == "" {
		return Off
	}

	return m
}

func effectiveSystemPriority(p uint16) uint16 {
	if p == 0 {
		return DefaultSystemPriority
	}

	return p
}

func effectivePortPriority(p uint16) uint16 {
	if p == 0 {
		return DefaultPortPriority
	}

	return p
}

func effectiveMemberKey(memKey, lagKey uint16) uint16 {
	if memKey == 0 {
		return lagKey
	}

	return memKey
}

// Diff computes the difference between two link aggregation configurations,
// reporting changes to LAG settings and per-member administrative parameters.
func Diff(a, b Config) []trace.Change {
	var changes []trace.Change
	layer := port.LayerLag

	for _, lagName := range sortedKeys(a.LAGs) {
		aLag := a.LAGs[lagName]
		bLag, exists := b.LAGs[lagName]
		if !exists {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "",
				From:    aLag,
				To:      nil,
			})

			continue
		}

		if from, to := effectiveMode(aLag.Mode), effectiveMode(bLag.Mode); from != to {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "mode",
				From:    from,
				To:      to,
			})
		}

		if aLag.Primary != bLag.Primary {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "primary",
				From:    aLag.Primary,
				To:      bLag.Primary,
			})
		}

		if aLag.UpDelay != bLag.UpDelay {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "up_delay",
				From:    aLag.UpDelay,
				To:      bLag.UpDelay,
			})
		}

		if aLag.DownDelay != bLag.DownDelay {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "down_delay",
				From:    aLag.DownDelay,
				To:      bLag.DownDelay,
			})
		}

		if aLag.HashBasis != bLag.HashBasis {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "hash_basis",
				From:    aLag.HashBasis,
				To:      bLag.HashBasis,
			})
		}

		if aLag.MinLinks != bLag.MinLinks {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "min_links",
				From:    aLag.MinLinks,
				To:      bLag.MinLinks,
			})
		}

		if from, to := effectiveLACPMode(aLag.LACP.Mode), effectiveLACPMode(bLag.LACP.Mode); from != to {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "lacp_mode",
				From:    from,
				To:      to,
			})
		}

		if aLag.LACP.Fast != bLag.LACP.Fast {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "lacp_fast",
				From:    aLag.LACP.Fast,
				To:      bLag.LACP.Fast,
			})
		}

		if from, to := effectiveSystemPriority(aLag.LACP.SystemPriority), effectiveSystemPriority(bLag.LACP.SystemPriority); from != to {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "lacp_system_priority",
				From:    from,
				To:      to,
			})
		}

		if aLag.LACP.SystemID != bLag.LACP.SystemID {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "lacp_system_id",
				From:    aLag.LACP.SystemID,
				To:      bLag.LACP.SystemID,
			})
		}

		if aLag.LACP.Key != bLag.LACP.Key {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "lacp_key",
				From:    aLag.LACP.Key,
				To:      bLag.LACP.Key,
			})
		}

		if aLag.LACP.Fallback != bLag.LACP.Fallback {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "lacp_fallback",
				From:    aLag.LACP.Fallback,
				To:      bLag.LACP.Fallback,
			})
		}

		for _, memName := range sortedKeys(aLag.Members) {
			am := aLag.Members[memName]
			bm, memExists := bLag.Members[memName]
			if !memExists {
				changes = append(changes, trace.Change{
					Layer:   layer,
					Subject: trace.Subject{Kind: "port", Key: memName},
					Field:   "",
					From:    am,
					To:      nil,
				})

				continue
			}

			if from, to := effectivePortPriority(am.Priority), effectivePortPriority(bm.Priority); from != to {
				changes = append(changes, trace.Change{
					Layer:   layer,
					Subject: trace.Subject{Kind: "port", Key: memName},
					Field:   "priority",
					From:    from,
					To:      to,
				})
			}

			if from, to := effectiveMemberKey(am.Key, aLag.LACP.Key), effectiveMemberKey(bm.Key, bLag.LACP.Key); from != to {
				changes = append(changes, trace.Change{
					Layer:   layer,
					Subject: trace.Subject{Kind: "port", Key: memName},
					Field:   "key",
					From:    from,
					To:      to,
				})
			}
		}

		for _, memName := range sortedKeys(bLag.Members) {
			if _, memExists := aLag.Members[memName]; !memExists {
				changes = append(changes, trace.Change{
					Layer:   layer,
					Subject: trace.Subject{Kind: "port", Key: memName},
					Field:   "",
					From:    nil,
					To:      bLag.Members[memName],
				})
			}
		}
	}

	for _, lagName := range sortedKeys(b.LAGs) {
		if _, exists := a.LAGs[lagName]; !exists {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "",
				From:    nil,
				To:      b.LAGs[lagName],
			})
		}
	}

	return changes
}
