# assembly

Assembly builds devices for the [fabric runtime](../fabric/README.md).
It owns configuration normalization, construction specifications, derivation,
reconfiguration, and configuration diffs. The fabric imports the device seam
and the cable medium so another device kind can run without changing it.

```go
fab, err := assembly.New(cfg)
if err != nil {
    return err
}
next := assembly.Spec(fab)
next.Switches["sw1"] = target
candidate, err := assembly.Derive(fab, next)
if err != nil {
    return err
}
fmt.Println(candidate.Clock())
```

`Spec` reads the live devices' specifications and the configured port tables
from `Fabric.Config`. Cable-derived operational states belong to the runtime
ports. A no-op `Configure` preserves the configured table and its conflict
reporting (`TestConfigureWithoutChangeKeepsSpec` in [built_spec_test.go](built_spec_test.go)).

`DiffSpecs` compares construction inputs and configuration behavior. Evidence
catalogs and references produce no change, including evidence attached to switch
issues and assumptions. `ConstructionSpec.Equal` includes that evidence.
[construction_result_test.go](construction_result_test.go) checks both contracts.
Switch subjects and scenario operation subjects use quoted component keys from
`trace.CompositeKey`, so a slash within an identifier stays part of that identifier.

```mermaid
flowchart LR
    config[Config or ConstructionSpec] --> normalize[Normalize and validate]
    normalize --> resolve[Cable resolution]
    resolve --> build[Build or derive devices]
    build --> start[fabric.New or fabric.Replace]
```

`ConfigureAction(node, cfg)` copies the configuration into an immutable
`fabric.Operation`. `McheckAction(node, port)` builds migration checking
through `fabric.Operate`. Scenario execution and diffing stay in fabric,
which can also execute caller-supplied operations without importing assembly.
`TestOperationNeedsNoAssembly` in
[the fabric tests](../fabric/topology_internal_test.go) checks that seam.
