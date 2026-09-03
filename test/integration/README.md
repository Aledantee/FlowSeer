# Integration tests

Run the default fixture and harness checks from the repository root:

```sh
go test -race ./test/integration/...
go -C test/integration/netpen test -race ./...
```

Each component has its own directory. `yang/testenv` supplies the shared
container helpers for gNMI, NETCONF, and RESTCONF. Fixtures move with their
suite so Go tests can resolve them from the package directory.

Netpen has a separate test module with local replacements for its source
module and the repository root. This preserves netpen's dependency isolation;
the root module's `./...` command does not include it.

Live tiers require an explicit build tag and the relevant lab prerequisites:

```sh
go test -tags=snmp_integration_t1 ./test/integration/snmp
go test -tags=yang_integration_t1 ./test/integration/{gnmi,netconf,restconf}
go -C test/integration/netpen test -tags=netpen_t1 ./...
```

See the [SNMP guide](snmp/README.md) and
[netpen validation matrix](netpen/VALIDATION_MATRIX.md) for tier details.
