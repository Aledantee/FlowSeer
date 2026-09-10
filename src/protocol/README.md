# protocol

Libraries that speak a management protocol on the wire, plus the schema
languages those protocols are described in.

| Package    | What it does                                            |
| ---------- | ------------------------------------------------------- |
| `snmp`     | SNMP v1/v2c/v3 client, table streams, trap reception     |
| `netconf`  | NETCONF over SSH: datastores, edits, notifications       |
| `ssh`      | SSH interactive shell: prompts, pagination, evidence     |
| `restconf` | RESTCONF over HTTP: resources, subscriptions             |
| `gnmi`     | gNMI: Get, Set, Subscribe                                |
| `syslog`   | RFC 3164/5424 parsing, encoding, and receivers           |
| `smi`      | SMIv1/SMIv2 MIB parser behind `snmp/cmd/mibgen`          |
| `yang`     | YANG parser and data trees behind `yang/cmd/yanggen`     |

`smi` and `yang` are compilers, not protocols. They live here because each one
exists to serve the protocol next to it, and splitting them into a third tree
would separate a parser from its only consumer for the sake of a word.

## What belongs here

A package qualifies when it speaks a wire protocol or reads its schema
language, and when it depends on nothing in `generated/go/proto`. That second
half is the load-bearing one: the moment a package here imports a FlowSeer
protobuf message, it has stopped being a protocol library and become a piece of
the domain model, and it belongs in `src/modules/` instead.

The rule exists because this tree was called `src/common/` and had no admission
test at all, so anything shared landed in it by default. `src/common/` now holds
only what is genuinely cross-cutting and domain-free — `errs` and `pump`.

Two counterexamples worth knowing about:

- `src/modules/localnet/snmpmap` walks SNMP tables and returns FlowSeer
  protobuf messages. It is domain code, so it lives under `src/modules/` even
  though it is built entirely on the libraries here.
- Anything under `generated/go/mib` and `generated/go/yang` is *output* of the
  generators here, not a peer of them. Never edit it by hand.

## Layout inside a package

Each package owns its own tests, `test/integration/` suite, and where the
dependency would otherwise leak into the main module, a nested `bench/` or
`differential/` module with its own `go.mod`. Those nested modules exist to keep
comparison dependencies (gosnmp, gosmi) out of the main module's graph, so run
them with `go -C <dir> test ./...`.
