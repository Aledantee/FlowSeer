---
title: An unexported method leaves an interface open to embedding
date: 2026-10-07
last_verified: 2026-10-07
category: conventions
module: src/common/sim/stream
problem_type: convention
component: sim
severity: high
applies_when:
  - "Treating a Go interface with an unexported method as a closed set of concrete implementations."
  - "Validating interface values with a type switch before invoking a promoted method that relies on concrete-type checks."
related_components: [testing, validation]
tags: [go, embedding, validation, type-switch]
---

# An unexported method leaves an interface open to embedding

An external struct embedding `stream.SizeVariation` implements `stream.Variation`.
Its dynamic type is the wrapper, so the concrete `SizeVariation` cases in
`Spec.Validate` do not inspect its sizes. The promoted `apply` still runs and
assumes those template-dependent checks passed.

The [Go specification's struct rules](https://go.dev/ref/spec#Struct_types)
include promoted methods in the embedding struct's method set. Go 1.27.1's
`doc/go_spec.html:1126-1136` contains the same rule. An unexported method prevents
an external caller from declaring that package's method directly, but embedding
an implementing type supplies it.

## Reproduce the validation gap

Run this from the repository with `go run`:

```go
package main

import (
    "fmt"

    "go.aledante.io/FlowSeer/src/common/net/ethernet"
    "go.aledante.io/FlowSeer/src/common/net/vlan"
    "go.aledante.io/FlowSeer/src/common/sim/stream"
)

type wrappedSize struct{ stream.SizeVariation }

var _ stream.Variation = wrappedSize{}

func main() {
    spec := stream.Spec{
        Frame: ethernet.Frame{Tags: []vlan.Tag{{VID: 1}}},
        Rate: stream.Rate{FramesPerSecond: 1},
        Count: 1,
        Variations: []stream.Variation{
            wrappedSize{stream.SizeVariation{Sizes: []int{20}}},
        },
    }
    fmt.Println(spec.Validate())
    source, err := spec.Source()
    if err != nil {
        fmt.Println(err)
        return
    }
    source.Next()
}
```

Validation returns nil. `Next` panics because a one-tag header plus FCS needs
22 octets, and the inherited size operation allocates a negative payload length.
`src/common/sim/stream/variation.go:99-100` computes:

```go
length := v.Sizes[n%len(v.Sizes)] - 18 - 4*len(frame.Tags)
payload := make([]byte, length)
```

The review gaps that commit `7d149d7b` added to the plan of the analysis,
trace, stream, and search work (2026-10-07) record both the missing default
case and the inaccurate closure claim:

> a type embedding a variation implements the interface through the promoted `apply`

## Apply the lesson

When a validator's type switch checks concrete implementations, include an
embedding wrapper in the inputs used to judge its coverage. A switch with no
default leaves wrapper values unchecked even when interface method dispatch
succeeds. Document the set as closed only when the boundary enforces that claim.

This entry records an open validation gap. It does not change the stream API or
resolve the plan's review gaps, and it does not address typed-nil implementations.
