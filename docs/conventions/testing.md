# Test layout

Place tests with the code whose contract they check. Repository-wide checks belong
under the root `test/` directory because no single implementation package owns them.

Run the default checks from the repository root:

```sh
go test -race ./...
go -C src/edge/netpen test -race ./...
```

Netpen has its own Go module to isolate its dependencies. Its integration suite
stays inside that module, so the second command includes its harness checks.
Live integration tiers require an explicit build tag and their lab prerequisites.

## Placement

| Test scope | Location | Reason |
| --- | --- | --- |
| Unit tests | Beside the implementation as `*_test.go` | Tests can exercise unexported behavior when necessary. |
| Package-specific conformance tests | Beside the implementation as `*_test.go` | The package owns the protocol or API contract and its conformance corpus. |
| Integration suite for a package | `<package>/test/integration/` | The suite stays with its owner and exercises the public API or binary. |
| Repository-wide protobuf conformance | `test/conformance/proto/` | Schema validation, import layering, and source layout span several packages. |

```text
src/common/snmp/
  session.go
  session_test.go
  conformance_corpus_test.go
  test/integration/
    testenv/
    testdata/

src/common/yang/
  test/integration/testenv/

src/edge/netpen/
  go.mod
  test/integration/

test/conformance/proto/
  layout_test.go
  layering_test.go
  validation_rules_test.go
```

A Go subdirectory is a separate package. Tests under `test/integration/` cannot
access their parent's unexported identifiers; use its public API or executable.
Keep tests requiring private access beside the implementation. A directory named
`test` does not create a module boundary, and ordinary `go test ./...` includes
its packages within the current module.

## Fixtures and shared helpers

Keep fixtures in the suite's `testdata/` and environment helpers in its `testenv/`.
Existing tier-specific fixtures may live under `<tier>/testdata/`. Go starts tests
in the package directory, so fixture paths should resolve from there.

Share helpers through the narrowest common domain owner. The gNMI, NETCONF, and
RESTCONF suites use `src/common/yang/test/integration/testenv/` for their container
environments. Helpers used only by SNMP remain in its own suite.

Executable protobuf checks and their fixtures stay outside `spec/proto/` and
`generated/`. Future repository-wide conformance suites belong under
`test/conformance/<domain>/`; package-owned suites stay with their package.

## Running a suite

These commands run from the repository root:

```sh
go test -race ./test/conformance/proto
go test -race ./src/common/snmp/test/integration/...
go test -tags=snmp_integration_t1 ./src/common/snmp/test/integration
go test -tags=yang_integration_t1 ./src/common/{gnmi,netconf,restconf}/test/integration
go -C src/edge/netpen test -tags=netpen_t1 ./test/integration/...
```

Choose one tier tag per invocation. Default tests cover the corpus, fixtures, and
harness; tagged tiers connect to containers or configured lab devices. See the
[SNMP integration guide](../../src/common/snmp/test/integration/README.md) and
[netpen validation matrix](../../src/edge/netpen/test/integration/VALIDATION_MATRIX.md)
for their prerequisites.
