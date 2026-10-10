# flow

`flow` owns traffic stream identities and statistics independently of the
fabric executor. [`flow.go`](flow.go) declares `FlowID`, `FlowStats`, and
`LatencyStats`. Fabric folds journeys and reads held-frame gauges through
[`Fabric.Flows`](../fabric/flow.go).

```go
stats := flow.FlowStats{
    Delivered: map[string]uint64{"host": 1},
    Copies: map[string]uint64{"mirror": 1},
}
clone := stats.Clone()
clone.Delivered["host"] = 2
// stats.Delivered["host"] remains 1.
```

`Clone` copies the Delivered, Drops, and Copies maps. Immutable analysis
metadata stays shared. [`TestFlowStatsCloneKeepsIndependentMaps`](flow_test.go)
mutates each cloned map and checks the original counts.
