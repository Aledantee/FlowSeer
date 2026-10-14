package netmodel

import (
	"cmp"
	"slices"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func (c *loadContext) finishLoad(normCfg vswitch.Config, seeds []bridge.Seed, want []trace.Layer) (Result, error) {
	c.reconcileCapabilities(normCfg.Capabilities(), want)
	slices.Sort(c.report.Capabilities)
	slices.Sort(c.report.NoSwitchport)
	slices.SortFunc(c.report.Skipped, func(a, b Skipped) int {
		if r := cmp.Compare(a.Port, b.Port); r != 0 {
			return r
		}
		if r := cmp.Compare(a.What, b.What); r != 0 {
			return r
		}
		return cmp.Compare(a.Why, b.Why)
	})
	slices.SortFunc(c.report.Defaults, func(a, b Default) int {
		if r := cmp.Compare(a.Port, b.Port); r != 0 {
			return r
		}
		if r := cmp.Compare(a.Field, b.Field); r != 0 {
			return r
		}
		return cmp.Compare(a.Value, b.Value)
	})
	slices.SortFunc(c.report.Conflicts, func(a, b Conflict) int {
		if r := cmp.Compare(a.What, b.What); r != 0 {
			return r
		}
		if r := cmp.Compare(a.Key, b.Key); r != 0 {
			return r
		}
		return cmp.Compare(a.Detail, b.Detail)
	})

	slices.SortFunc(seeds, func(a, b bridge.Seed) int {
		if r := cmp.Compare(a.FID, b.FID); r != 0 {
			return r
		}
		if r := cmp.Compare(a.MAC.String(), b.MAC.String()); r != 0 {
			return r
		}
		if r := cmp.Compare(a.Port, b.Port); r != 0 {
			return r
		}
		if a.Lifetime != b.Lifetime {
			if a.Lifetime == bridge.Static {
				return -1
			}
			return 1
		}
		return a.LearnedAt.Compare(b.LearnedAt)
	})

	metadata := analysis.NewMetadata(c.rootScope, c.issues, c.catalog, c.assumptions)
	spec, err := (vswitch.ConstructionSpec{
		Config:   normCfg,
		Seeds:    seeds,
		NodeID:   c.src.DeviceID,
		Metadata: metadata,
	}).Normalize()
	if err != nil {
		return Result{}, errs.Wrap(err, "normalize switch construction specification")
	}

	return Result{
		Spec:     spec,
		Report:   c.report.Clone(),
		Metadata: metadata,
	}, nil
}
