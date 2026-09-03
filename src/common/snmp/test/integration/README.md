# SNMP integration testing

Four independent tiers exercise the FlowSeer SNMP library against
real SNMP agents on the wire. Tests in this package and tier-tagged
subpackages reach only through the public `snmp.Session` /
`snmp.TrapStream` / `snmp.Walker` surface; no fake `Session` is
constructed in tier code.

| Tier | Build tag | Agent | Owns |
|------|-----------|-------|------|
| T1 | `snmp_integration_t1` | Net-SNMP `snmpd` in Docker | Full USM auth/priv matrix; forged-edge wire shapes (NoSuchObject, EndOfMibView, oversized OCTET STRINGs, malformed `DateAndTime`, mid-table truncation). |
| T2 | `snmp_integration_t2` | Nokia SR Linux via `containerlab` | End-to-end dense-row collector flow and real-NOS trap reception (`coldStart`, `linkUp`/`linkDown`). |
| T3 | `snmp_integration_t3` | `lextudio/snmpsim` replay | Vendor regression coverage — adding a vendor is one `.snmprec` plus one manifest entry, no Go code. |
| T4 | `snmp_integration_t4` | Operator-supplied live device | Manual acceptance gate for the decoder-leniency + walker-buffer fixes. Pins each prior bug class (`MtxrHlFanSpeed1/2`, `MtxrOpticalSupplyVoltage`, `sysServices`, `ipAddrTable` composite-index walk, table row counts) as a named subtest. |

## Quick start

```sh
task --dir src/common/snmp/test/integration t1   # Net-SNMP snmpd smoke / USM matrix / forged-edge wire
task --dir src/common/snmp/test/integration t2   # Nokia SR Linux end-to-end (requires containerlab)
task --dir src/common/snmp/test/integration t3   # snmpsim replay against committed .snmprec baselines
SNMP_T4_TARGETS=host[:port]@community,... task --dir src/common/snmp/test/integration t4   # live MikroTik / vendor device
```

Each `task --dir src/common/snmp/test/integration t<N>` invocation builds a Docker image or deploys
a containerlab topology, runs the tier's tests, and tears
everything down. Container lifecycle is owned by each tier's
`TestMain` — there is no separate `task lab-up`.

Bare `go test ./...` runs zero integration tests; selecting exactly
one tier tag is the contract. Selecting two tier tags simultaneously
produces a compile error (`multiple definitions of TestMain`) — by
design.

## Prerequisites

| Tier | Tooling |
|------|---------|
| T1 | Docker (Docker Desktop or Engine 20.10+) |
| T2 | Docker, plus `containerlab` from <https://containerlab.dev/install/>. The Nokia SR Linux image `ghcr.io/nokia/srlinux` is publicly pullable (no vendor account). |
| T3 | Docker |
| T4 | Reachable SNMP-enabled device, plus `SNMP_T4_TARGETS` set to `host[:port]@community[,…]`. No containerisation. |

When the required tooling is missing the tier skips cleanly with a
clear message; no diagnostic noise.

### macOS / Docker Desktop

T2's trap-reception test (`TestT2_Trap_LinkDownLinkUp`) needs SR
Linux to send UDP traps back to the host. On Docker Desktop the
container-perspective host address `host.docker.internal` resolves
automatically. On Linux Docker Engine it does not; set
`FLOWSEER_T2_HOST_FROM_CONTAINER` to the docker0 bridge gateway
(typically `172.17.0.1`) or to your host's LAN IP before running
`task --dir src/common/snmp/test/integration t2`.

The trap-listener port defaults to `12162`. Override via
`FLOWSEER_T2_TRAP_PORT` if that port is busy on your machine.

## Tier walkthroughs

### T1 — Net-SNMP `snmpd`

```sh
task --dir src/common/snmp/test/integration t1
```

The harness:

1. Builds the Docker image from
   `src/common/snmp/test/integration/testdata/snmpd/Dockerfile` (debian + snmpd
   + per-tier `snmpd.conf` + forged-edge pass-scripts).
2. Runs the container with UDP/161 mapped to a random host port via
   testcontainers-go.
3. Polls `Get sysUpTime.0` via `snmp.NewSession` until ready.
4. Runs the tests in
   `src/common/snmp/test/integration/t1_*_test.go`:
   - **Wire smoke**: `Get`, `GetNext`, `GetBulk` against seeded MIBs.
   - **USM matrix**: every supported `(AuthProtocol, PrivProtocol)`
     pair Dials and runs a `Get sysUpTime.0`. `Priv3DES` asserts a
     typed `ErrUSMProtocolUnsupported` rejection.
   - **Dense-row walk**: the shared `AssertIfTableDenseRows` helper.
   - **Forged-edge cases**: `NoSuchInstance`, `NoSuchObject`,
     `EndOfMibView`, Counter32 boundary, malformed `DateAndTime`,
     oversized `OCTET STRING`, mid-table truncation.
5. `docker rm -f`'s the container on exit.

### T2 — Nokia SR Linux via `containerlab`

```sh
task --dir src/common/snmp/test/integration t2
```

The harness shells out to:

```sh
containerlab deploy -t src/common/snmp/test/integration/testdata/containerlab/srlinux.clab.yaml --reconfigure
containerlab inspect -t … --format json
containerlab destroy -t … --cleanup
```

SR Linux's startup-config provisions:

- SNMPv2c community `public` (used for v2c traps).
- SNMPv3 USM user `flowseer` with SHA-256 / AES-256 (used for Walk
  and the dense-row collector flow).

Tests in `src/common/snmp/test/integration/t2_*_test.go` exercise:

- **Dense-row walk** via the shared `AssertIfTableDenseRows` helper
  against SR Linux's IF-MIB.
- **`linkUp` / `linkDown` traps** triggered by an admin-state toggle
  through the `containerlab exec` callback. The trap listener is
  bound on the host port and SR Linux is configured (via CLI exec)
  to send v2c traps to that address.
- **`coldStart`** is currently skipped — it fires at SR Linux boot
  before tests set up the listener; capturing it requires moving
  trap-listener setup into T2's `TestMain` (planned follow-up).
- **v3 trap reception** is skipped per `common/snmp/trap.go`'s note
  that the upstream Backend's v3-trap path is currently unreliable.

### T3 — `lextudio/snmpsim` replay

```sh
task --dir src/common/snmp/test/integration t3
```

The harness:

1. Builds a small Python image (`testdata/snmpsim/Dockerfile`) with
   `snmpsim-lextudio` installed.
2. Runs the container with `testdata/snmprec/` bind-mounted as the
   replay data dir.
3. Iterates every entry in `testdata/snmprec/manifest.yaml` and
   runs `AssertIfTableDenseRows` against the entry's replayed
   context.

#### F3 — Adding a new vendor capture

```sh
# 1. From a workstation that can reach the device:
src/common/snmp/test/integration/scripts/capture-snmprec.sh \
    --target 10.0.0.5 \
    --community public \
    > src/common/snmp/test/integration/testdata/snmprec/cisco/ios-xe.snmprec

# 2. Append one entry to src/common/snmp/test/integration/testdata/snmprec/manifest.yaml:
#
#    - vendor: cisco
#      device: ios-xe-17
#      captured_on: 2026-06-01
#      snmpsim_context: cisco/ios-xe   # must match the .snmprec path
#      snmprec: cisco/ios-xe.snmprec
#
# 3. Re-run:
task --dir src/common/snmp/test/integration t3
```

The new entry runs as its own `t.Run` subtest — no Go code change is
required. `snmpsim_context` MUST equal the `.snmprec` file's path
relative to the data dir (without the `.snmprec` extension); snmpsim
derives the v2c community routing from that name.

### T4 — Operator-supplied live device

```sh
SNMP_T4_TARGETS="10.20.0.219@tegi,10.20.0.1@tegi" task --dir src/common/snmp/test/integration t4
```

T4 is the manual acceptance gate for the SNMP library decoder and
walker bug fixes: verification against a MikroTik CRS317
(SwOS 2.18, `10.20.0.219`) and a hAP ax³ (RouterOS 7.20.6,
`10.20.0.1`) surfaced four decoder / walker bugs. Each prior bug
class is a named subtest here so a future regression names itself:

- `sysServices_Integer32_into_Uinteger32` — agent emits Integer32
  for an `INTEGER (0..127)` scalar; previously the strict
  `Uinteger32Var` type-assert rejected it.
- `MtxrHlFanSpeed1_Integer32_into_Gauge32`,
  `MtxrHlFanSpeed2_Integer32_into_Gauge32` — agent emits Integer32
  for a Gauge32-declared column.
- `ifTable_walk_one_row_per_interface` — column-major BulkWalk on a
  14-port router previously inflated to 308 rows.
- `ipAddrTable_composite_index_decodes_per_row` — composite-index
  table walk previously halted on the first row's column-major
  emission.
- `hrStorageTable_walk_one_row_per_storage`,
  `lldpLocPortTable_walk_one_row_per_port`,
  `mtxrOpticalTable_walk_with_lenient_decoders` — additional walks
  exercising row-presence + leniency across four additional MIB
  packages.

Unlike T1/T2/T3, T4 owns no container lifecycle — the operator
supplies the device address(es) via `SNMP_T4_TARGETS`. When the env
var is unset, T4 skips with a single-line diagnostic and exits 0, so
the build tag is safe to include in opportunistic CI sweeps. Bad
target syntax exits 1 with a parser diagnostic; a misconfiguration
should not silently skip.

`SNMP_T4_TARGETS` grammar:

```
SNMP_T4_TARGETS = entry ("," entry)*
entry           = host [":" port] "@" community
```

Each prior bug-class assertion accepts `ErrException`
(NoSuchObject / NoSuchInstance — the device legitimately doesn't
expose that OID, e.g. a router lacking `mtxrHlFanSpeed1`) but fails
on `ErrTypeMismatch` or `ErrLossyConversion`, which would mean the
decoder leniency policy regressed.

The device classes T4 was built against:

- MikroTik SwOS 2.18 (CRS-series industrial switches)
- MikroTik RouterOS 7.20.6 (hAP / CCR / CRS-routers)

A future contributor touching `common/snmp/decode.go` or
`common/snmp/cmd/mibgen/emit_table.go` should re-run T4 against a
device of each class.

## Dialing

Every tier reaches the wire through the public `snmp.NewSession` /
`snmp.ListenTraps` constructors. There is no backend-swap seam — the
SNMP implementation lives in package `snmp` itself. Tiers point it at
their agent via `testenv.SetTarget` / `testenv.Target`.

## CI promotion

Promoting a tier to CI is a workflow-file PR that runs the same
`task --dir src/common/snmp/test/integration t<N>` target inside a runner with Docker (and `containerlab` for
T2) installed. The build-tag layout — independent gates per tier — is
the API for that future PR; nothing in the tier code needs to change.
