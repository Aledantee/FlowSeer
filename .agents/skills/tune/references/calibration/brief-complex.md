Ensure the Go package `src/common/netsim/fabric` (module `go.aledante.io/FlowSeer`) has a `Fabric.Configure` method meeting the contract below. If `configure.go` already exists, verify it against the contract and the package's tests, change only what fails, and report; do not rewrite working code. If it does not exist, add `configure.go` (and any test file the package needs to cover the contract). Either way the package's tests must cover every clause of the contract. Done means: `go test -race -count=3 ./src/common/netsim/fabric/` passes from the repository root, `gofumpt -l src/common/netsim/fabric` prints nothing, and any change is committed on the current branch with a short message. If nothing needed changing, say so and name the commit that holds the existing implementation.

Read first: `src/common/netsim/fabric/fabric.go` (the whole file; its `Fabric` state, `build`, `Fork`, `SetFault`, `Metadata`, and the port and link helpers define the model you must respect), the package's existing `*_test.go` files (test style, the fabric-construction helpers, `Snapshot`, `Fingerprint`, `Report`, and how a scenario is run), and `docs/code-style.md`.

Contract:

```go
// Configure reconfigures the switch named node to cfg at the fabric's current
// simulation clock.
func (f *Fabric) Configure(node string, cfg vswitch.Config) error
```

- `node` must name a switch already in the fabric. Configure applies `cfg` as that switch's new configuration at the fabric's current clock; from that point the run behaves as the new configuration dictates, and frames already in flight are evaluated against it when they reach the switch.
- Configure re-derives the switch through the same normalization and construction path as a build: it validates the resulting specification. If `node` is not a switch (unknown name, or a host) or the new configuration fails validation (for example a port that a cable names is absent), Configure returns an error naming the offending node or port and leaves the fabric entirely unchanged. No partial mutation, and the fabric's `Fingerprint` and `Spec` are identical to before the failed call.
- Each port's operational state follows its cable, not the configured value: the switch's port table is rebuilt so a cabled port takes its link's oper state and an unlinked port takes its unlinked default. Operational link states are re-resolved on every cable that touches `node`, and a link whose resolved state changed is relinked so its peer observes the new state.
- Retained layer state that stays valid under the new configuration is carried forward through the same derivation the vswitch layer uses; state the new configuration invalidates (an entry on a port moved to another VLAN, for example) is dropped. Statically seeded state is preserved; dynamically learned seeds are re-derived rather than replayed.
- Protocol layers for the switch restart from the new state: peers are notified of changed links, protocol timers are restarted, and any emissions or neighbor failures the reconfiguration produces enter the run at the fabric's current clock, not at construction time.
- Configure invalidates the fabric's cached metadata so the next observation reflects the new configuration.
- Configure mutates only the receiver. A fork taken before or after the call is independent: configuring one never changes the other's configuration, links, or spec.

Return, outcome first and nothing else: the changed paths (or "no change"), the exact test command with its last lines of output, and the commit hash. Do not edit files outside `src/common/netsim/fabric/`, do not touch `AGENTS.md`, `buf.yaml`, `tools/hooks/`, or `.claude/`, and do not cite plan or ticket identifiers in code or comments. Run only the package's own tests; the coordinator runs the repository verifier afterward, so skip it even where repository guidance asks for it before handoff. Do not ask questions; if something blocks you, state the blocker and stop. Do not spawn subagents that edit files; read-only subagents are fine. No narration while working; terse register in the report: fragments fine, identifiers and errors exact, code unchanged.
