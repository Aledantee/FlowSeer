# FlowSeer

FlowSeer is a network management codebase built around typed device models and
protocol libraries. The repository currently contains Go libraries for SNMP,
NETCONF, RESTCONF, gNMI, and syslog; SMI and YANG tooling; protobuf schemas; and
the `netpen` edge application.

The control-plane services described in the architecture documents have not
been implemented yet. This is a development repository rather than a released,
end-to-end FlowSeer distribution.

## Get the repository green

FlowSeer requires Go 1.27. From the repository root, run the main module checks:

```bash
go build ./...
go vet ./...
go test -race ./...
```

`netpen` is a separate Go module so its packet and terminal dependencies do not
enter the control-plane dependency graph. Test it separately:

```bash
go -C src/edge/netpen test -race ./...
```

Go and protobuf changes also use golangci-lint v2 and Buf. Run the command for
the area you changed:

```bash
golangci-lint run
buf lint
```

Docker is needed only for integration suites that start real protocol targets.
Those tests honor `testing.Short()`.

## Try the edge application

The safe way to confirm that `netpen` builds is to print its version:

```bash
go -C src/edge/netpen run ./cmd/netpen version
```

Most other `netpen` commands open raw network sockets. Run them only in an
authorized lab after reading the command help and the integration validation
matrix in
[`src/edge/netpen/test/integration/VALIDATION_MATRIX.md`](src/edge/netpen/test/integration/VALIDATION_MATRIX.md).

## Repository tour

| Path | Purpose |
| --- | --- |
| [`src/protocol/`](src/protocol/README.md) | Device protocol clients and the SMI/YANG libraries behind their generators. |
| [`src/common/`](src/common/README.md) | Small foundations with no FlowSeer domain knowledge. |
| [`src/modules/`](src/modules/README.md) | Reusable host-assembled behavior; currently empty by design. |
| [`src/services/`](src/services/README.md) | Future control-plane services. |
| [`src/edge/`](src/edge/README.md) | Applications designed to run near managed networks. |
| [`spec/`](spec/README.md) | Owned schemas and vendored protocol specifications. |
| `generated/` | Generated Go bindings and model code. Never edit these files by hand. |
| [`docs/`](docs/README.md) | Architecture, conventions, plans, research, and captured solutions. |

[`CONCEPTS.md`](CONCEPTS.md) defines the domain vocabulary used across schemas,
code, and architecture documents.

## Contributing

Read [`AGENTS.md`](AGENTS.md) before changing the repository. Despite its name,
the linked engineering and documentation conventions bind human contributors as
well as coding agents. In particular:

- edit schemas and generator inputs instead of files under `generated/`;
- keep protocol libraries free of FlowSeer protobuf domain types;
- update documentation in the same change that makes an existing claim stale;
- run focused checks while working, then use the diff-aware verifier before
  handoff:

  ```bash
  .claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>
  ```

Start with the [documentation map](docs/README.md) when you need architecture or
workflow details. Coding-agent setup notes, including Serena configuration, live
under [`tools/serena/`](tools/serena/README.md) rather than in this human quick
start.
