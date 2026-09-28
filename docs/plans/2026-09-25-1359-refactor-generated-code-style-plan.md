---
title: Generated Code Style Conformance - Plan
type: refactor
date: 2026-09-25
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept after fixes
execution: code
compound: docs/solutions/architecture-patterns/claim-companion-symbols-in-scope-before-child-nodes.md
---

# Generated Code Style Conformance - Plan

Outcome: implemented 2026-09-25 on this branch (1d982d63..ba082819). All four units passed; the verifier is green on the union of changed paths. The wide bench case now walks the 18 current ifTable columns. t4_manual_verify_test.go still walks the deprecated ipAddrTable; it builds only under its lab tag, so SA1019 does not fire today, and the open question below stands.

Reviewed 2026-09-25: accept after fixes. One correctness defect and four smaller
ones were found and fixed in two rounds (cd4b6527, ace96ff7, dcb2a076, 946446b6,
3b4219cd). The correctness one: yanggen claimed the schema variable's name but
emitted the unclaimed spelling, so a sibling node named after another node's
schema companion produced a `type X` and a `var X` in one package — output that
does not compile. No vendored module has that shape, so nothing was broken in
the tree; a fixture node now covers it. Requirement 12 was not met on the first
measurement either: three `deprecatedComment` hits survived, because a wrapped
line of copied MIB prose can open with the word "deprecated". `splitDoc` now
keeps that word off a line start, and the probe reads zero for all four
checkers. Two findings stand open, both pre-existing and outside this change:
yanggen's output is not reproducible run to run, and `moduleScopedName`'s
empty-name fallback became unreachable when `goname.Exported("")` was fixed.

## Goal

The Go that mibgen writes to `generated/go/mib/` and yanggen writes to
`generated/go/yang/` follows `docs/code-style.md`: MixedCaps identifiers
with initialisms in their conventional case, `// Deprecated:` paragraphs on
deprecated MIB objects, and none of the mechanical smells the repository's
linters flag in hand-written code. The means is changing the two generators
and regenerating, never editing `generated/`. Stop if a generated Go
identifier turns out to be a wire or storage contract (a name read by
reflection into an encoding, or persisted), because then the rename is a
data migration rather than a refactor.

## Evidence

The probe ran `golangci-lint` with the repository's `.golangci.yml`, with
`exclusions.generated` set to `disable` and the `zz_generated_` path
exclusion removed. For `./generated/go/mib/...` the results were:

| Finding | Count | Emitter |
| --- | --- | --- |
| `gocritic unlambda`: `func(vb snmp.VarBind) (T, error) { return snmp.DecodeT(vb) }` | 11,817 | `mustDecodeNatural`, `src/protocol/snmp/cmd/mibgen/emit_tc.go:722` |
| `unconvert`: `row.Dot1dBasePort = int32(v)` after `snmp.RawInteger32` | 4,196 | fused arm, `src/protocol/snmp/cmd/mibgen/emit_table.go:315` |
| `gocritic singleCaseSwitch` on one-column tables | 119 | `emit_table.go:167,239,297` |
| `gocritic deprecatedComment` on copied "is deprecated in favour of" text | 6 | copied MIB text, see Decisions |
| `misspell` in copied MIB DESCRIPTION text | 6 | spec text, out of scope |

For YANG, lint runs only on a sample. The full `./generated/go/yang/...`
tree (208 MB) is not linted, because on this host it exhausts memory.
The sample (`ruckus-icx/openconfigvlan`,
`ruckus-icx/openconfigsystem`, `aruba-cx/openconfignetworkinstance`)
returned 266 `unlambda` hits on
`Equal: func(a, b XFlatRow) bool { return yang.EqualStructs(a, b) }`,
emitted at `src/protocol/yang/cmd/yanggen/emit_module.go:479`. It also
returned 2 `misspell` hits on the YANG node name
`flood-unknown-unicast-supression`, which is a spec identifier.

The judgement rules were read against sampled output:

- yanggen joins ancestry with `_`:
  `Native_Aaa_Accounting_CommandsConfig_GroupConfig_Group1`
  (`generated/go/yang/cisco-iosxe/ciscoiosxenative/ciscoiosxenative_p000.go:2074`),
  about 144,000 type declarations and 14,000 `…Descriptor` functions.
  `structName` (`emit_module.go:495`) produces them. Key-struct fields
  carry `_` too (`NetworkInstance_Name`), and so does the collision suffix
  in `nameScope.claim` (`src/protocol/yang/cmd/yanggen/naming.go:67`).
- `camel` (`naming.go:22`) has no initialism handling: `VlanId`, `Ip`,
  `Ipv6`. mibgen's `camelCase` (`emit_tc.go:785`) keeps the MIB spelling
  (`Dot1qFdbId`, `LldpRemChassisId`), about 950 exported names, and
  prefixes a digit-leading name with `_`.
- mibgen never reads `smi.Node.Status` (`src/protocol/smi/model.go:500`):
  IF-MIB's `ifInNUcastPkts` is `STATUS deprecated`, and its binding
  `IfInNUcastPkts` has no `// Deprecated:` paragraph
  (`generated/go/mib/ifmib/mib.go:470`). `docs/code-style.md` Doc comments
  requires one.
- mibgen doc comments start with the symbol name, state a contract, and use
  `[Symbol]` links. The samples had no panics, TODOs, planning
  identifiers, or `Get`-prefixed accessors. yanggen's doc comments are
  formulaic but well-formed.

`protoc-gen-go` and `protoc-gen-connect-go` output under
`generated/go/proto/` comes from pinned third-party buf plugins; it has no
generator here to adjust.

## Decisions

- yanggen joins ancestry names in MixedCaps with no separator:
  `NativeAaaAccountingCommandsConfig`. A clash created by the dropped
  separator goes through the existing `nameScope.claim`, whose suffix
  becomes `X` plus six hex digits (`FooX1a2b3c`). Why:
  `docs/code-style.md` Naming says "MixedCaps, never underscores", and the
  user chose this over documenting an exception or naming by the node
  alone (the latter would let one added sibling rename existing types).
- Every Go name yanggen derives from a struct name is claimed in the
  package `nameScope` right after the struct's own claim and before its
  children: the schema var (`…Schema`), the list key type (`…Key`), the
  descriptor function (`…Descriptor`), and the flat row (`…FlatRow`). If a
  child node's joined name collides with one of these companions, the
  child gets the hash suffix. Why: without the `_` separator,
  `schemaVarName` (`emit_module.go:505`, which never goes through
  `claim`) produces `YangLibrarySchema` twice in `ietfyanglibrary`. The
  list key `KeyChains_Key` also collides with a child container named
  `key`. The companions are the list's API, and callers name them more
  often than an oddly named child struct.
- One initialism table serves both generators. It lives in a new package,
  `src/protocol/internal/goname`, with `goname.Exported(name string) string`
  and `goname.Unexported(name string) string`. `Unexported` lower-cases
  the whole leading word when that word is an initialism (`LLDPPortConfigTable`
  → `lldpPortConfigTable`, `ID` → `id`). Why: one shared
  list serves both generators, and both consumers sit under `src/protocol/`. `src/common/README.md`
  sends a package whose importers are all in one tree to that tree's
  `internal/`. `Unexported` exists because mibgen lower-cases the first
  rune for its unexported names (`emit_table.go:49`, `emit_watch.go:304`,
  `emit_key.go:516`, `emit_dispatch.go:82`). Once the rename lands, that
  would turn `LLDPPortConfigTable` into `lLDPPortConfigTable`.
- The table is staticcheck ST1003's default list (ACL API ASCII CPU CSS
  DNS EOF GUID HTML HTTP HTTPS ID IP JSON LHS QPS RAM RHS RPC SLA SMTP SQL
  SSH TCP TLS TTL UDP UI UID UUID URI URL UTF8 VM XML XMPP XSRF XSS), plus
  MAC, VLAN, MTU, VRF, OID, SNMP, BGP, OSPF, LLDP, and the spellings IPv4
  and IPv6. It adds no other protocol names. Why: code-style.md names
  `ID`, `OID`, `URL`, `SNMP`. Whoever reads the generated code already
  knows the staticcheck list, and each added entry is a network term that
  already appears in FlowSeer's hand-written identifiers. A longer list
  renames more identifiers and gives little in return.
- `goname.Exported` splits words at `-`, `_`, `.`, space, `/`, `:`, and at
  camelCase boundaries. A run of capitals followed by a lowercase letter
  ends one letter early (`ifHCInOctets` → `If HC In Octets`), and digits
  stay attached to the word before them (`dot1q`). Each word whose
  lowercase form is in the table takes the table spelling. Every other word
  gets an upper-case first letter and keeps the rest as written. A result
  that starts with a digit gets an `X` prefix. Why: this one splitter
  handles both MIB camelCase and YANG kebab-case. Keeping the rest of the
  word as written leaves MIB mixed case such as `HC` intact.
- mibgen fails generation when two SMI names in one package map to the
  same Go name. The error names both SMI names. Why: mibgen output is a
  public API that callers type by hand, so a hash suffix would hide the
  clash and leave a name nobody can predict. yanggen keeps its hash suffix
  because its names are already path-derived and not typed from memory.
- A decoder that only forwards to an `snmp.Decode*` helper is emitted as a
  reference to the helper. The scalar accessor calls the helper directly
  (`return snmp.DecodeMacAddress(vbs[0])`), and the column passes it
  (`snmp.DecodeUint32` is the last argument to `snmp.NewColumn`). This covers
  `mustDecodeNatural` (`emit_tc.go:722`) and `tcDelegate`
  (`emit_tc.go:673`, the TC path for MacAddress, PhysAddress,
  DateAndTime, TruthValue, RowStatus, DisplayString, and BITS).
  `mustDecodeCast` also emits the helper reference when the target type is
  the helper's own result type. `mustDecodeNatural` loses its `goType`
  parameter, because `unparam` would flag it. Why: this is the `unlambda`
  finding, and the forwarding closure only adds indirection. Every
  `mustDecodeNatural` caller passes exactly the helper's result type
  (`src/protocol/snmp/decode.go:68-196`), and `snmp.NewColumn[T]` takes a
  `func(VarBind) (T, error)` (`src/protocol/snmp/column.go:43`), so a
  bare reference type-checks.
- The fused raw arm emits `row.F = v` when `GoType` is the `snmp.Raw*`
  result type, and keeps the conversion for a named type. Why: `unconvert`.
  A named enum or alias type still needs the conversion.
- A `switch` with one case and no `default` is emitted as an `if`. The
  sites are `emit_table.go:167` and `:297` (the `Observed` method and the
  walk loop), `emit_watch.go:162` (two per watched one-column table), and
  `emit_enum.go:169` (`String()` on a one-member enum; 17 exist today).
  `emit_table.go:239` has a `default` arm, so the linter does not flag it
  and it stays a switch. Why: the `singleCaseSwitch` finding.
- A node with `STATUS deprecated` or `STATUS obsolete` gets a final
  doc-comment paragraph:
  `Deprecated: <smiName> is STATUS deprecated in <MODULE>.` (or
  `obsolete`). Why: code-style.md Doc comments says "Mark deprecations with
  a `// Deprecated:` paragraph". The six `deprecatedComment` hits come from
  copied MIB prose ("This object is deprecated in favour of …") and stop
  mattering once the real paragraph exists, so the copied text stays as it
  is.
- Hand-written code that reads a now-deprecated binding moves to a
  current object when one gives the same measurement. It does not get an
  SA1019 suppression. `src/protocol/snmp/bench/tablewalk_test.go:104`
  walks `IfInNUcastPkts`, `IfOutNUcastPkts`, and `IfOutQLen` to measure a
  wide ifTable walk. Only 18 current ifTable columns exist, so the wide
  case walks all 18 once, and its sub-benchmark becomes `cols=18`, with
  `src/protocol/snmp/bench/doc.go` updated to match. No gate compares the wide-case numbers; `bench-gate.sh`
  reads only `baseline-micro.txt`.
  `src/protocol/snmp/test/integration/t4_manual_verify_test.go:137`
  walks the deprecated `ipAddrTable` against lab devices, and it has no
  drop-in replacement: `ipAddressTable` is another table, and the lab
  devices may not serve it. That one is an open question. Why:
  `AGENTS.md` Hard boundaries forbids adding a suppression to make our own
  artifacts pass.
- yanggen's `generatorVersion` moves from `yanggen-3` to `yanggen-4`. Why:
  `src/protocol/yang/cmd/yanggen/lockfile.go:14` requires a bump whenever
  output changes shape for unchanged input.
- Breaking every generated name is intended. Why: `AGENTS.md` Agent
  behavior asks for breaking changes that improve the design before the
  first stable release. Hand-written consumers change in the same unit.

## Requirements

1. `goname.Exported` produces:
   `lldpRemChassisId` → `LLDPRemChassisID`, `dot1qFdbId` → `Dot1qFdbID`,
   `ifHCInOctets` → `IfHCInOctets`, `vlan-id` → `VLANID`,
   `ipv6-address` → `IPv6Address`, `mac-address` → `MACAddress`,
   `if-gsn` → `IfGsn`, `802dot3` → `X802dot3`, `ip` → `IP`,
   `ipAdEntAddr` → `IPAdEntAddr`, `idle-timeout` → `IdleTimeout`.
2. No identifier declared under `generated/go/yang/` or
   `generated/go/mib/` contains `_`. That covers types, functions, vars,
   consts (including those inside `const (` blocks), and struct fields.
   Example: the generator tests' AST walk finds none (U2, U3), and the
   IOS-XE struct above is declared as
   `NativeAaaAccountingCommandsConfigGroupConfigGroup1`.
3. yanggen output compiles when a child's joined name equals a
   companion name. Example: `ietfyanglibrary` declares
   `var YangLibrarySchema` once. The `yang-library/schema` struct gets
   the hash-suffixed name, and `KeyChainsKey` is the key-chain list's key
   type.
4. YANG key-struct fields are MixedCaps. Example: the OpenConfig
   network-instance MAC-entry key has a field `NetworkInstanceName`, and
   its `Key` function assigns `k.NetworkInstanceName = r.NetworkInstanceName`.
5. The YANG table descriptor's `Equal` is `yang.EqualStructs[<FlatRow>]`,
   not a closure.
6. A mibgen scalar accessor with a natural decoder returns the helper call.
   Example: `Dot1dBaseBridgeAddressGet` ends with
   `return snmp.DecodeMacAddress(vbs[0])`.
7. A mibgen column with a natural decoder passes the helper. Example:
   `IfInNUcastPkts` is built by `snmp.NewColumn` with `snmp.KindCounter32`
   and `snmp.DecodeUint32` as its last two arguments.
8. The fused arm converts only when the types differ. Example: bridgemib's
   walk sets `row.Dot1dBasePort = v`, and a column of an enum type keeps
   `row.X = XType(v)`.
9. A table with one readable column emits `if col.Key() == X.Key()` rather
   than a `switch`. Example: `EntLPMappingTableRow.Observed` in entitymib.
10. A deprecated or obsolete MIB object carries the paragraph. Example: the
    `IfInNUcastPkts` doc comment ends with
    `// Deprecated: ifInNUcastPkts is STATUS deprecated in IF-MIB.`, and a
    `STATUS current` column has no `Deprecated:` line.
11. Two SMI names in one module that map to the same Go name fail mibgen,
    and the error message contains both SMI names.
12. The probe's lint command, rerun over `./generated/go/mib/...` and over
    the three sampled YANG packages, reports zero `unlambda`, `unconvert`,
    `singleCaseSwitch`, and `deprecatedComment` issues.
13. `go build ./...` and `go test -race ./...` pass with every hand-written
    consumer moved to the new names.

## Out of scope

- `generated/go/proto/`: its output comes from pinned third-party plugins.
- Spelling errors inside copied MIB DESCRIPTION text and inside YANG node
  names (`supression`, `favour`, `sattelite`). These are spec text and spec
  identifiers.
- mibgen's reflow of preformatted DESCRIPTION tables and banners
  (`splitDoc`, `emit_scalar.go:73`). The doc-comment form survives, and the
  mangled layout is spec text.
- The `SysDescrGet` suffix: the rule bans a `Get` prefix, not a suffix.
- yanggen's formulaic doc comments ("X is the Y node Z."), which are
  well-formed. YANG `description` text is not carried into doc comments.
- Lifting the `generated: strict` exclusion in `.golangci.yml`. That file
  is a policy surface, so the change is requested separately (Open
  questions).
- Planning identifiers (KD5, R8, R11) in `src/protocol/yang/cmd/yanggen/yanggen.yaml`
  comments. That is hand-written config, and the request scoped this work
  to generated code.

## Units

### U1. Shared Go identifier builder

Files: `src/protocol/internal/goname/goname.go`,
`src/protocol/internal/goname/goname_test.go`, `src/protocol/README.md`
After: none
Change: `goname.Exported` and `goname.Unexported` implement the splitting
and initialism rules in Decisions. The package comment states the rule set
and the table's origin (staticcheck ST1003 plus the listed network terms),
and says both generators use it. `src/protocol/README.md` lists the
package.
Tests: `TestExported`, table-driven, with every example in requirement 1.
It also covers the empty string (`""`), a name that is all separators
(`"--"` → `"X"`), and words that contain an initialism without being one
(`Idle` does not become `IDle`, and `Vlans` does not become `VLANs`).
`TestUnexported` covers `LLDPPortConfigTable` → `lldpPortConfigTable`,
`IfTable` → `ifTable`, `ID` → `id`, and `IPv6Address` → `ipv6Address`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/internal/goname src/protocol/README.md`

### U2. yanggen MixedCaps names

Files: `src/protocol/yang/cmd/yanggen/naming.go`,
`src/protocol/yang/cmd/yanggen/emit_module.go`,
`src/protocol/yang/cmd/yanggen/lockfile.go`,
`src/protocol/yang/cmd/yanggen/doc.go`,
`src/protocol/yang/cmd/yanggen/*_test.go`,
`src/protocol/yang/cmd/yanggen/testdata/modules/**` (a new fixture child
node named `schema` under a container, and one named `key` under a list),
`src/protocol/yang/cmd/yanggen/testdata/golden/**`, the hand-written files
that import the golden fixture or `generated/go/yang/`
(`src/protocol/netconf/watch_test.go`,
`src/protocol/netconf/test/integration/t1_smoke_test.go`,
`src/protocol/netconf/test/integration/t4_lab_test.go`,
`src/protocol/netconf/test/integration/t4_manual_verify_test.go`,
`src/protocol/restconf/watch_test.go`,
`src/protocol/restconf/test/integration/t4_lab_test.go`,
`src/protocol/gnmi/watch_test.go`,
`src/protocol/gnmi/test/integration/t1_smoke_test.go`), and
`generated/go/yang/**` (regenerated)
After: U1
Change: `camel` becomes a call to `goname.Exported`. `structName` joins
`parent + want` with no separator. The key-struct field names and every
other `_` join in `emit_module.go` use the same MixedCaps join. The
companion names (`…Schema`, `…Key`, `…Descriptor`, `…FlatRow`) are
claimed in `em.scope` right after their struct, before its children.
`nameScope.claim` suffixes a clash with `X` plus six hex digits. `Equal` is
emitted as `jen.Qual(yangPkg, "EqualStructs").Types(jen.Id(flatName))`.
`generatorVersion` is `yanggen-4`, and `doc.go` describes the naming rule.
The golden fixtures are refreshed with
`go test ./src/protocol/yang/cmd/yanggen -run TestEmitFixtureGolden -update-golden`,
the output is regenerated with
`go run ./src/protocol/yang/cmd/yanggen -update`, and the consumers are
moved to the new names.
Tests: an emit test parses the golden fixture output with `go/parser` and
walks every declaration. It asserts that no identifier or struct field
contains `_`, that `Servers_Server` is now `ServersServer`, and that the
package compiles with the new `schema` and `key` children: the
companion names keep their plain spelling, and the children carry the
suffix. A `nameScope` test forces a clash between `a-b` + `c` and `a` +
`b-c`, which both join to `ABC`. It asserts that the second claim gets the
`X`-hex suffix and that the result is the same on every run.
`golden_roundtrip_test.go` passes on the refreshed fixture, and
`go run ./src/protocol/yang/cmd/yanggen -check` passes after
regeneration. The codec looks up a field by reflection on the schema's
`GoName` (`src/protocol/yang/schema.go:98`) and puts the schema `Name` on
the wire. So the renamed field and its `GoName` string must come from the
same emitter variable, and the round-trip test catches a drift between
them. If the round trip fails for any other reason, that is the stop
condition in Goal.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/yang src/protocol/netconf src/protocol/restconf src/protocol/gnmi generated/go/yang`

### U3. mibgen emission cleanups and deprecation paragraphs

Files: `src/protocol/snmp/cmd/mibgen/emit_tc.go`,
`src/protocol/snmp/cmd/mibgen/emit_key.go`,
`src/protocol/snmp/cmd/mibgen/emit_scalar.go`,
`src/protocol/snmp/cmd/mibgen/emit_table.go`,
`src/protocol/snmp/cmd/mibgen/emit_watch.go`,
`src/protocol/snmp/cmd/mibgen/emit_enum.go`,
`src/protocol/snmp/cmd/mibgen/emit_bits.go`,
`src/protocol/snmp/cmd/mibgen/*_test.go`,
`src/protocol/snmp/cmd/mibgen/testdata/**` (fixture MIB and golden),
`src/protocol/snmp/bench/tablewalk_test.go`,
`generated/go/mib/**` (regenerated)
After: none
Change:
- `mustDecodeNatural` and `tcDelegate` return `jen.Qual(snmpImport, helper)`,
  and `mustDecodeNatural` loses its `goType` parameter at every caller.
  The scalar emitter calls the reference on `vbs[0]`. `mustDecodeCast`
  returns the same reference when the target renders identically to the
  helper's result type, and the fused arm drops the conversion under the
  same condition.
- The four one-case switch sites named in Decisions emit an `if`.
- The doc-comment builders append the `Deprecated:` paragraph for
  `smi.StatusDeprecated` and `smi.StatusObsolete` on scalars, columns,
  and tables. mibgen emits no notification bindings.
- `tablewalk_test.go` swaps its three deprecated columns for current
  ifTable columns of the same wire types.
- The golden output is refreshed with
  `go test ./src/protocol/snmp/cmd/mibgen -run TestEmit_FakeMIB_Golden -update-golden`,
  and `generated/go/mib` is regenerated with `go generate .`.
Tests: emit tests over the fixture MIB in `testdata`, which gains a
deprecated column, an obsolete scalar, a one-column table, a one-member
enum, a natural `Counter32` column, a MacAddress TC column, and an enum
column. They assert the rendered source for requirements 6 through 10,
including that the enum column keeps its conversion. The same AST walk as
U2 asserts no `_` identifier in the fixture output. Both
`go run ./src/protocol/snmp/cmd/mibgen -check` and the bench module's
tests pass. `t4_manual_verify_test.go` still uses `ipAddrTable` until the
open question is settled; this unit does not touch it.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/snmp/cmd/mibgen src/protocol/snmp/bench generated/go/mib`

### U4. mibgen initialism names

Files: every `src/protocol/snmp/cmd/mibgen/emit*.go` that calls
`camelCase` or lower-cases a name's first rune (`emit_bits.go`,
`emit_dispatch.go`, `emit_enum.go`, `emit_indicator.go`, `emit_key.go`,
`emit_scalar.go`, `emit_table.go`, `emit_tc.go`, `emit_watch.go`),
`src/protocol/snmp/cmd/mibgen/emit.go`,
`src/protocol/snmp/cmd/mibgen/doc.go`,
`src/protocol/snmp/cmd/mibgen/*_test.go`,
`src/protocol/snmp/cmd/mibgen/testdata/golden/**`,
`src/protocol/snmp/cmd/mibgen/goldentest/*.go`, the hand-written files
that import `generated/go/mib/` (23 today; list them with
`grep -rl 'FlowSeer/generated/go/mib/' src test --include='*.go'`),
`docs/code-style.md`, and `generated/go/mib/**` (regenerated)
After: U1, U3
Change: `camelCase`'s body becomes a call to `goname.Exported`. Its 26
call sites across eight files keep calling it, so the swap itself
touches only `emit_tc.go`. The `_` prefix for a digit-leading name becomes
`X`. The first-rune lower-casing in `emit_table.go:49`,
`emit_watch.go:304`, `emit_key.go:516`, and `emit_dispatch.go:82` becomes
`goname.Unexported`. Before emitting, mibgen collects each module's Go
names and fails when two SMI names map to one Go name, naming both.
`doc.go` states the naming rule. `docs/code-style.md` Naming gains one
sentence: generated bindings follow the same MixedCaps and initialism
rules, with the table in `src/protocol/internal/goname`. The golden
output is refreshed, the tree is regenerated, and consumers move to the
new names.
Tests: a unit test gives two synthetic SMI names that both map to `FooID`
(`fooId`, `fooID`) and asserts the requirement 11 error. An emit test
asserts that `lldpRemChassisId` renders as `LLDPRemChassisID` and that
`lldpPortConfigTable` produces the unexported `lldpPortConfigTableT`.
`goldentest` passes on the renamed API, and `go test -race ./...` passes
repo-wide.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/snmp docs/code-style.md generated/go/mib <the consumer paths>`

Waves: U1 U3 | U2 U4

## Verification

- `.claude/skills/verify-change/scripts/verify-change.sh --full`
- `go run ./src/protocol/snmp/cmd/mibgen -check` and
  `go run ./src/protocol/yang/cmd/yanggen -check`
- The probe lint from Evidence, with a scratch copy of `.golangci.yml` that
  sets `exclusions.generated: disable`, run over
  `./generated/go/mib/...` and over the three sampled YANG packages. It must
  report zero `unlambda`, `unconvert`, `singleCaseSwitch`, and
  `deprecatedComment` issues. Do not lint the full YANG tree.
- The generator tests' AST walk (U2, U3) finds no `_` identifier, and
  `grep -rnE '^(type|func|var) [A-Za-z0-9]*_' generated/go/yang generated/go/mib`
  prints nothing as a quick spot check.
- Regeneration and the httptest-based suites may need unsandboxed runs (see
  the repository's verify environment notes).

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `src/protocol/README.md`, the yanggen and mibgen `doc.go` files, and
      `docs/code-style.md` Naming updated in the units that change them.
- [ ] Both generator drift checks pass, and `generated/` contains only
      generator output.
- [ ] This plan's `status` is set, with an outcome note under its title.
- [ ] No plan labels in code, comments, or commit messages.

## Open questions

- Should `.golangci.yml` stop excluding `generated/go/mib` and
  `generated/go/yang` from lint once this lands, so the generators stay
  held to the same rules? It is a policy surface and a separate request.
  Lint time over the 208 MB YANG tree is the cost to weigh.
- `src/protocol/snmp/test/integration/t4_manual_verify_test.go:137`
  walks the deprecated `ipAddrTable` on lab switches. It builds only under
  its lab tag, so SA1019 does not fire today. It moves to `ipAddressTable` (RFC 4293). That is follow-up
  work outside this plan: first confirm the lab switches serve the table,
  which needs the lab powered on, with advance notice to the user.
