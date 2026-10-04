---
title: A Refusal Test Needs an Input Only the Refusal Rejects
date: 2026-09-16
last_verified: 2026-10-03
category: conventions
module: src/common/net/udp
problem_type: convention
component: sim
severity: high
applies_when:
  - "Writing a test that asserts a function refuses, returning false, nil, or an error, where the function has more than one way to produce that outcome"
  - "Choosing a test input by running the function under test until it returns the wanted result"
  - "Reviewing a codec or a validator whose tests are refusals, or judging whether a passing suite would notice a guard being deleted"
  - "Adding a rule to a gate that already refuses, such as a second condition in a layering or validation table, where the cases you write may be ones the old rule rejects anyway"
  - "Writing a table-driven refusal gate where each row names a fixture and a test drops each row to prove it is needed"
related_components: [codec, packet_capture, conformance-gates]
tags: [testing, codec, refusal, mutation-testing, conformance-gate]
---

# A refusal test needs an input only the refusal rejects

## The situation

`udp.Verify` can return false three ways: the addresses are a mixed family, the
datagram is one `Decode` would refuse, or the checksum does not match
(`src/common/net/udp/udp.go:113-124`). Two subtests were written to pin the
first two, each feeding an input that trips the guard it names. Both assertions
pass. Neither notices when the guard it was written for is deleted, because the
input trips the checksum as well, and the checksum alone is enough to return
false.

This is not the round-trip problem that
[a codec round trip cannot locate a field on the wire](a-codec-round-trip-cannot-locate-a-field-on-the-wire.md)
describes. There the expected value came out of the code under test. Here the
expected values are literals written from the specification, and the tests still
cannot fail. What is overdetermined is the *input*: it satisfies the property
under test and a second property that produces the same answer.

## What to do instead

Choose an input for which the named property is the only thing that can produce
the outcome. For the refusal case that means a datagram which is well formed in
every respect except the one under test:

```go
// Length is 4, so Decode refuses. The eight-octet prefix checksums correctly
// for 10.0.10.7 -> 224.0.0.251, so the refusal is the only thing that can
// make Verify return false. Derived by hand from RFC 768.
wire := []byte{0x14, 0xe9, 0x14, 0xe9, 0x00, 0x04, 0xe1, 0x0d}

if udp.Verify(wire, src, dst) {
    t.Error("Verify() = true, want false for a datagram Decode itself would refuse")
}
```

The test that was there used `0xaa, 0x94` for the last two octets, carried over
from a decode test where the checksum does not matter. Under that input the sum
folds to `0xc986`, so the checksum fails too and the refusal never has to.

The same rule has a second shape: never let the code under test pick the input.
A test that searches for a payload by calling the encoder and stopping when the
output looks right has made the behavior under test into its own search
predicate, and its assertion restates the condition that ended the loop. Pin the
input as a literal and derive it from the specification instead.

## A second rule in a gate that already refuses

The same trap catches a gate gaining a rule. `layeringViolation` refuses an
import for two reasons: the importer's allowlist does not name it, or the
imported package declares a Connect service and is therefore a sink
(`test/conformance/proto/layering_test.go:412-424`). Every case written for the
sink rule named an import the allowlist already rejected, and the table lists no
service package as a permitted import of anything, so the whole gate stayed
green with the sink branch deleted.

The discriminating case has to make the old rule say yes:

```go
// model/access declares model/inventory, so only a service declaration in
// model/inventory can stand in the way.
{
    name:       "declared import of a package that declares a service",
    importer:   "model/access",
    imported:   "model/inventory",
    services:   map[string]bool{"model/inventory": true},
    wantReason: "model/inventory declares a service and is imported by nothing",
},
```

Two things make it work: a substituted set of service-declaring packages, so the
case does not depend on which packages happen to declare one today, and an
assertion on the reason rather than on the boolean, so the case pins which rule
refused. A gate whose test asserts only that something refused cannot tell its
rules apart.

## How to tell, without waiting for a reviewer

Delete the guard the test names and run the package. If it stays green, the test
does not hold that guard. This is what mutation testing automates, and it is the
only cheap check that distinguishes a test which passes from a test which would
fail. Three review rounds over this one package each found another test that
passed for the wrong reason. The ones that survived a deleted guard were found
by trying it, not by reading.

## Dropping a table row proves it is needed, not that its guard is pinned

When a refusal gate holds its rules in a table and pairs each row with a fixture
(`test/conformance/sim/layer_contract_test.go:70-90`), a test may drop each row
in turn (`slices.DeleteFunc`) and require zero findings without it
(`layer_contract_test.go:274-283`). That proves the row is necessary to reject
that fixture. It does not prove the row's predicate or scope is pinned.

If the fixture is overdetermined, a mutated or weakened guard still matches.
In `test/conformance/sim`, five rows required contract members on any layer
package. Each was tested against a stateful layer fixture. When the checker was
mutated to require those members on stateful packages only, every fixture still
failed with the exact expected finding string, and dropping each row still
cleared the finding. The gate passed with every member unheld for stateless
packages until stateless fixtures were added add-only (`1b6ff63b`).
Similarly, `import_device` refuses imports below `sim/device`, but its fixture
imports `sim/device/vswitch` (`testdata/import_device/fixture.go:4`). Mutating the
guard root to `sim/device/vswitch` still triggers the fixture and passes the
drop check (`2db760b7`), leaving `sim/device` unpinned.

A row-dropping test checks only whether a row was dead code on its fixture. To pin
table guards, fixtures must be minimal along each classification axis the gate
divides, and guard predicates must be mutated directly. Editing existing fixtures
in place risks silently dropping existing pins (`03246c8e` lost three pins that
`3247dcf7` had to restore).

## Evidence

Mutations run on 2026-09-16, darwin, against
`go test -count=1 ./src/common/net/udp/`:

- Replacing `Verify`'s decode guard (`src/common/net/udp/udp.go:118-121`) with
  `_, payload, _ := Decode(b)` leaves the package green, including the subtest
  written for it (`src/common/net/udp/udp_test.go:284-292`).
- Replacing its family guard (`udp.go:114-117`) with `v4, _ := addressFamily(src, dst)`
  also leaves the package green, including
  `udp_test.go:274-282`.
- The arithmetic behind the replacement input: for `10.0.10.7 -> 224.0.0.251`
  with a UDP length word of 8, the octets `14e9 14e9 0004 e10d` fold to
  `0xffff`, so `checksum` returns zero, while `14e9 14e9 0004 aa94` fold to
  `0xc986`.
- The searched-input shape, since replaced by two pinned payloads
  (`udp_test.go:159-170`): while the test chose its payload by calling `Encode`
  and breaking on a `0xffff` result, changing the substitution guard from
  `sum == 0` to `sum == 1` left the package green.

Mutations run on 2026-10-03, darwin, against
`go test -count=1 ./test/conformance/sim/`:

- In `test/conformance/sim/layer_contract_test.go:70-90`, requiring members only on
  stateful packages left all tests green until stateless fixtures were added
  (`1b6ff63b`).
- Mutating `import_device` (`layer_contract_test.go:88`) from `simImportPath + "/device"`
  to `simImportPath + "/device/vswitch"` leaves the gate green because
  `testdata/import_device/fixture.go:4` imports `sim/device/vswitch` (`2db760b7`).
- Mutating `stateful_retention_key` (`layer_contract_test.go:79`) to require the full
  signature of `retention_key` leaves the gate green because its fixture declares
  the contract's `RetentionKey`.

## What this does not cover

It says nothing about whether the guard is the right guard, only that a test
names one and holds it. It also does not ask for a separate input per reason on
a function with one refusal path, where any rejected input discriminates fine.
The cost is real: each such input has to be constructed so that everything
except the property under test is valid, which for a checksummed format means
computing the checksum by hand.
