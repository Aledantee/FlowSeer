---
title: Schema Building Blocks Phase 2, Network Instances and Routing - Plan
type: feat
date: 2026-09-25
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: partially-implemented
execution: code
parent: docs/plans/2026-09-25-1713-feat-schema-building-blocks-plan.md
---

# Schema Building Blocks Phase 2, Network Instances and Routing - Plan

> Partially implemented: U1 landed (683c171f); requirement 4 corrected on the user's ruling; resume from U2.

## Goal

Every forwarding table names the network instance it belongs to, and a routing
table exists. `net/instance/v1` holds the `NetworkInstance` row,
`NetworkInstanceKind`, and `RouteDistinguisher`; `Vlan`, `FdbEntry`, and
`IpFacet` require `network_instance`; `net/routing/v1` holds `Route`,
`NextHop`, `NextHopGroup`, `RouteSourceProtocol`, and the `RouteTableType`
RIB/FIB discriminator. Netsim and the SNMP mappers fill the key with the
device's default instance. The means is five units: new instance schema, new
routing schema, reshaping existing switching and IP schemas, updating Go
consumers in netsim and snmpmap, and updating conventions and concepts
documentation.

Stop if a targeted device or standard requires a network-instance key that
cannot be represented as a 1-to-255-character name, or if an external routing
table cannot be partitioned into forwarding next hops and RFC 8349 special next
hops.

## Decisions

The parent plan's decisions, the
[schema building blocks record](../architecture/2026-09-25-schema-building-blocks-direction.md),
and the conventions doc govern. The decisions below resolve the open calls
from dossiers 05 and 06:

- `NetworkInstanceKind` is normalized from
  `spec/yang/openconfig/openconfig-network-instance-types.yang:144-176`:
  `NETWORK_INSTANCE_KIND_UNSPECIFIED = 0`, `_DEFAULT = 1` (DEFAULT_INSTANCE),
  `_L3VRF = 2` (L3VRF), `_L2VSI = 3` (L2VSI), `_L2P2P = 4` (L2P2P), `_L2L3 = 5`
  (L2L3). Why: OpenConfig identities are open strings without integer
  assignments, so FlowSeer defines a normalized enum with `_UNSPECIFIED = 0` per
  `docs/code-style-proto.md` (Naming) and `docs/conventions/protobuf.md` (Enums).
  `DEFAULT = 1` mirrors the global routing instance on standard switches.
- `NetworkInstance.route_distinguisher` is a typed variant (`RouteDistinguisher`
  with required `oneof format`), not a string. Format messages are
  `As2RouteDistinguisher` (`as2 = 1`: 2-octet ASN uint32 <= 65535, 4-octet
  assigned number uint32), `Ipv4RouteDistinguisher` (`ipv4 = 2`:
  `flowseer.net.addr.v1.Ipv4Address` ip, 2-octet assigned number uint32 <=
  65535), and `As4RouteDistinguisher` (`as4 = 3`: 4-octet ASN uint32, 2-octet
  assigned number uint32 <= 65535). Why: RFC 4364 §4.2 strictly defines three
  distinct binary formats (Type 0, Type 1, Type 2) with different field widths
  and semantics. A string (such as `"65000:100"` or `"192.0.2.1:1"`) would push
  parsing, regex matching, and range validation into every client and consumer.
  Per `docs/conventions/protobuf.md` (Typed variants), a closed set of variants
  that validate differently gets a typed variant oneof, providing declarative
  protovalidate bounds and type-safe generated accessors without custom CEL.
  `net/instance` imports `net/addr` and `net/key` to support this type, and
  `test/conformance/proto/layering_test.go` records that import.
- `RouteSourceProtocol` is a registry pass-through enum keeping
  `spec/mib/ietf/IANA-RTPROTO-MIB:48-79` (`IANAipRouteProtocol`) and RFC 4292
  `inetCidrRouteProto` integers: `ROUTE_SOURCE_PROTOCOL_UNSPECIFIED = 0`,
  `_OTHER = 1`, `_LOCAL = 2`, `_NETMGMT = 3`, `_ICMP = 4`, `_EGP = 5`,
  `_GGP = 6`, `_HELLO = 7`, `_RIP = 8`, `_ISIS = 9`, `_ESIS = 10`,
  `_CISCO_IGRP = 11`, `_BBN_SPF_IGP = 12`, `_OSPF = 13`, `_BGP = 14`,
  `_IDPR = 15`, `_CISCO_EIGRP = 16`, `_DVMRP = 17`, `_RPL = 18`, `_DHCP = 19`,
  `_TTDP = 20`. Why: Registry pass-through enums preserve external integers
  verbatim per `docs/conventions/protobuf.md` (Enums). Static routes use
  `_NETMGMT = 3`. Multicast route sources (`IANAipMRouteProtocol`, e.g.
  `pimSparseMode = 8`, `pimDenseMode = 9`) belong to a separate registry and do
  not share this enum per direction record rule 7 and dossier 06.
- `NextHop` is a typed variant with a required `oneof target` between forwarding
  and special next hops: `forwarding = 1` (`ForwardingNextHop`: `string
  interface_name` validated with `flowseer.net.key.v1.interface_name`, optional
  gateway `flowseer.net.addr.v1.IpAddress address`, and message-level CEL
  requiring at least one target), and `special = 2` (`SpecialNextHop` enum:
  `_BLACKHOLE = 1`, `_UNREACHABLE = 2`, `_PROHIBIT = 3`, `_RECEIVE = 4`).
  `NextHopGroup` holds `repeated NextHop next_hops` (min_items = 1). Why: RFC
  8349 (`ietf-routing.yang` §5.2) cleanly separates forwarding next hops from
  the four special next-hop actions (`blackhole`, `unreachable`, `prohibit`,
  `receive`). An egress forwarding target and a discard/receive
  action are mutually exclusive by construction: the `oneof` holds at most one
  arm, so both cannot be represented at once, and `required` fails the empty case.
  `NextHopGroup` models multi-path ECMP groups as an ordered set of next hops
  (RFC 8349 `next-hop-list`).
- Ruled (drive, on the user's decision): requirement 4 asserts the enforceable
  invariant, not the impossible "both arms set" state. A protobuf `oneof` holds
  at most one arm in memory, and decoding wire bytes carrying both tags keeps
  only the last (last-tag-wins), so `protovalidate` can reject a `NextHop` only
  when no arm is set. `required` enforces at-least-one; the oneof enforces
  at-most-one. A comment on `NextHop` records that both arms are structurally
  unrepresentable.
- `Route` carries `RouteTableType table_type = 8` (`ROUTE_TABLE_TYPE_UNSPECIFIED = 0`,
  `_RIB = 1`, `_FIB = 2`) as an explicit discriminator. Why: Network domain atlas
  04 §3.7 and dossier 06 note that standard SNMP routing tables (RFC 1213
  `ipRouteTable`, RFC 4292 `inetCidrRouteTable`) do not indicate whether the
  agent exposed the control-plane Routing Information Base or the hardware
  Forwarding Information Base. Without this discriminator, routing data ingested
  via SNMP is ambiguous by construction.
- `Vlan` (field 4) and `FdbEntry` (field 6) require `string network_instance`
  validated by `(flowseer.net.key.v1.network_instance_name) = true`. `FdbEntry`
  keeps `(vlan_id, mac)` and updates its file-level doc comment to state that its
  key assumes independent VLAN learning (IVL) and bridge-domain scoping is
  provided by `network_instance` (direction rule 4, dossier 05, atlas 04 §3.2).
- `IpFacet` requires `string network_instance = 3` when present. Why: A routed
  interface names its VRF once, and rows keyed by that interface inherit it
  without repeating the instance key (direction record rule 4).
- Netsim and SNMP mappers report a default instance: devices without
  multi-instance concepts report one `NetworkInstance` of kind `DEFAULT` named
  `"default"`, and all rows cite `"default"` (direction record rule 4).

## Requirements

1. An `FdbEntry` without `network_instance` fails validation.
   Example: `switchingv1.FdbEntry_builder{ VlanId: proto.Uint32(10), Mac: addrv1.Eui48Address_builder{Octets: []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}}.Build(), InterfaceName: proto.String("ethernet1/1") }.Build()`
   fails `protovalidate.Validate` with violation path `network_instance` and
   message `value is required`.
2. A netsim export of a single-bridge switch reports one `NetworkInstance` of kind
   `DEFAULT` named `default`, and every `Vlan` and `FdbEntry` row names it.
   Example: `netmodel.NetworkInstances(sw)` on a single-bridge switch export
   returns a single `NetworkInstance` with `name = "default"` and `kind = DEFAULT`,
   and every row returned by `netmodel.Vlans(sw)` and `netmodel.FdbEntries(...)`
   has `GetNetworkInstance() == "default"`.
3. A static default route `0.0.0.0/0 via 192.0.2.1` in instance `default`
   round-trips through `Route` with `source_protocol = NETMGMT(3)`.
   Example: Constructing a `routingv1.Route` with `network_instance = "default"`,
   `destination_prefix = 0.0.0.0/0`, `source_protocol = ROUTE_SOURCE_PROTOCOL_NETMGMT`,
   and `next_hop_group` containing a forwarding next hop with gateway `192.0.2.1`
   passes `protovalidate.Validate`, marshals to protobuf wire bytes, unmarshals
   back, and preserves `GetSourceProtocol() == routingv1.RouteSourceProtocol_ROUTE_SOURCE_PROTOCOL_NETMGMT`.
4. A `NextHop` with no arm set fails the required `oneof`; a `NextHop` with
   exactly one arm passes. Example: `NextHop_builder{}.Build()` fails
   `protovalidate.Validate` with `oneof: exactly one field is required`, and a
   `NextHop` with only `forwarding` (or only `special`) set passes. The `oneof`
   makes both arms structurally unrepresentable in memory (proto decoding is
   last-tag-wins, so wire bytes carrying both tags leave only the last arm set);
   a comment on `NextHop` states this.
5. An `IpFacet` without `network_instance` fails validation.
   Example: `ipv1.IpFacet_builder{ Ipv4: ipv1.Ipv4Facet_builder{Enabled: proto.Bool(true)}.Build() }.Build()`
   fails `protovalidate.Validate` with violation path `network_instance` and
   message `value is required`.
6. A `Vlan` without `network_instance` fails validation.
   Example: `switchingv1.Vlan_builder{ Id: proto.Uint32(10) }.Build()` fails
   `protovalidate.Validate` with violation path `network_instance` and message
   `value is required`.
7. `NetworkInstance` requires a valid name and defined kind.
   Example: `instancev1.NetworkInstance_builder{ Name: proto.String(""), Kind: instancev1.NetworkInstanceKind_NETWORK_INSTANCE_KIND_DEFAULT.Enum() }.Build()`
   fails key validation; `NetworkInstance` with `kind = NETWORK_INSTANCE_KIND_UNSPECIFIED`
   fails validation.
8. `RouteDistinguisher` validates format-specific ASN and assigned number bounds
   per RFC 4364 §4.2.
   Example: `As2RouteDistinguisher` with `asn = 65536` fails `uint32.lte = 65535`;
   `Ipv4RouteDistinguisher` with `assigned_number = 65536` fails `uint32.lte = 65535`;
   `As4RouteDistinguisher` with `assigned_number = 65536` fails `uint32.lte = 65535`;
   `RouteDistinguisher` with no arm set fails required oneof.
9. `ForwardingNextHop` requires at least an interface name or a gateway address.
   Example: `ForwardingNextHop_builder{}.Build()` fails message CEL
   `forwarding_next_hop.target_present`.
10. `SpecialNextHop` rejects `SPECIAL_NEXT_HOP_UNSPECIFIED`.
    Example: `NextHop_builder{ Target: NextHop_Special_builder{ Special: routingv1.SpecialNextHop_SPECIAL_NEXT_HOP_UNSPECIFIED.Enum() }.Build() }.Build()`
    fails validation.
11. `go build ./...` and `go test -race ./...` pass.
    Example: Conformance gates, netsim suites, and snmpmap mappers compile and
    pass tests under `-race`.

## Out of scope

- L3 routing protocols (BGP, OSPF, IS-IS, VRRP, BFD); phase 7 adds them under
  `net/protocol/`.
- IP services (DHCP, DNS, NAT, tunnels); phase 7 adds them.
- EVPN/VXLAN route types and VNI mappings (atlas 05).
- Shared VLAN learning (SVL) FID-to-VID maps and bridge component identifiers
  (dossier 05).
- Service-level projections that union routing tables across instances.

## Units

### U1. Network instance schema and conformance tests

Files: `spec/proto/flowseer/net/instance/v1/{network_instance_kind.proto,route_distinguisher.proto,network_instance.proto,README.md}`,
`spec/proto/flowseer/net/addr/v1/README.md`,
`spec/proto/flowseer/net/key/v1/README.md`,
`test/conformance/proto/layering_test.go`,
`test/conformance/proto/instance_rules_test.go`,
`generated/go/proto/flowseer/net/instance/v1/`
After: none
Change: defines the `flowseer.net.instance.v1` package under `spec/proto/flowseer/net/instance/v1/`.
`network_instance_kind.proto` declares `NetworkInstanceKind` with values `_UNSPECIFIED = 0`,
`_DEFAULT = 1`, `_L3VRF = 2`, `_L2VSI = 3`, `_L2P2P = 4`, `_L2L3 = 5` citing
`openconfig-network-instance-types.yang:144-176`.
`route_distinguisher.proto` declares `RouteDistinguisher` as a typed variant with a required
`format` oneof (`as2 = 1`, `ipv4 = 2`, `as4 = 3`) and format messages `As2RouteDistinguisher`
(ASN uint32 <= 65535, assigned_number uint32), `Ipv4RouteDistinguisher`
(`flowseer.net.addr.v1.Ipv4Address` ip, assigned_number uint32 <= 65535), and
`As4RouteDistinguisher` (ASN uint32, assigned_number uint32 <= 65535) citing RFC 4364 §4.2.
`network_instance.proto` declares `NetworkInstance` table row with required `name`
validated by `(flowseer.net.key.v1.network_instance_name) = true`, required `kind`
rejecting unspecified (value 0), optional `route_distinguisher`, and optional
`description`. Every doc comment states field contracts, and new required fields
state "Must be present."
`README.md` documents package admission, boundaries, and standards grounding.
`test/conformance/proto/layering_test.go` updates `net/instance` imports to
`{"net/addr", "net/key"}`.
`test/conformance/proto/instance_rules_test.go` tests validation rules for
`NetworkInstance`, `NetworkInstanceKind`, and `RouteDistinguisher`.
`buf generate` emits generated Go bindings under `generated/go/proto/flowseer/net/instance/v1/`.
Tests: `test/conformance/proto/instance_rules_test.go` (verifies requirements 7 and 8:
name required and key-rule bounded, kind required and defined-only, RD required oneof,
and range bounds for each RD format).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/instance/v1 test/conformance/proto/layering_test.go test/conformance/proto/instance_rules_test.go generated/go/proto/flowseer/net/instance/v1`

### U2. Routing schema and conformance tests

Files: `spec/proto/flowseer/net/routing/v1/{route_source_protocol.proto,route_table_type.proto,special_next_hop.proto,next_hop.proto,next_hop_group.proto,route.proto,README.md}`,
`spec/proto/flowseer/net/addr/v1/README.md`,
`spec/proto/flowseer/net/key/v1/README.md`,
`test/conformance/proto/routing_rules_test.go`,
`generated/go/proto/flowseer/net/routing/v1/`
After: none
Change: defines the `flowseer.net.routing.v1` package under `spec/proto/flowseer/net/routing/v1/`.
route_source_protocol.proto declares `RouteSourceProtocol` keeping IANA integers
`_UNSPECIFIED = 0`, `_OTHER = 1` through `_TTDP = 20` citing
`spec/mib/ietf/IANA-RTPROTO-MIB:48-79` and RFC 4292.
route_table_type.proto declares `RouteTableType` discriminator enum with values
`_UNSPECIFIED = 0`, `_RIB = 1`, `_FIB = 2` citing RFC 8349 and atlas 04 §3.7.
special_next_hop.proto declares `SpecialNextHop` enum with `_UNSPECIFIED = 0`,
`_BLACKHOLE = 1`, `_UNREACHABLE = 2`, `_PROHIBIT = 3`, `_RECEIVE = 4` citing RFC 8349 §5.2.
next_hop.proto declares `NextHop` as a typed variant with a required `target` oneof
containing `forwarding = 1` (`ForwardingNextHop` with `interface_name` validated by
`net/key/v1` rule, optional `flowseer.net.addr.v1.IpAddress address`, and message CEL
requiring at least one target) and `special = 2` (`SpecialNextHop` with defined_only and
rejecting unspecified).
next_hop_group.proto declares `NextHopGroup` with required `repeated NextHop next_hops`
containing at least 1 item.
route.proto declares `Route` table row with required `network_instance` validated by
`net/key/v1` rule, required `destination_prefix` (`flowseer.net.addr.v1.IpPrefix`), optional
`source_protocol`, optional `preference`, optional `metric`, optional `next_hop_group`,
optional `active`, and optional `table_type`. Every doc comment states field contracts,
and new required fields state "Must be present."
README.md documents package admission, boundaries, and standards grounding.
test/conformance/proto/routing_rules_test.go tests validation rules for `Route`,
`NextHop`, `ForwardingNextHop`, and `NextHopGroup`.
buf generate emits generated Go bindings under `generated/go/proto/flowseer/net/routing/v1/`.
Tests: `test/conformance/proto/routing_rules_test.go` (verifies requirements 3, 4, 9,
and 10: static default route round-trip with NETMGMT(3), NextHop missing arms, NextHop
with both arms set, ForwardingNextHop without targets, SpecialNextHop undefined/unspecified,
and Route network_instance / destination_prefix required).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/routing/v1 test/conformance/proto/routing_rules_test.go generated/go/proto/flowseer/net/routing/v1`

### U3. Reshape existing schemas with required network instance

Files: `spec/proto/flowseer/net/switching/v1/{vlan.proto,fdb_entry.proto,README.md}`,
`spec/proto/flowseer/net/ip/v1/{ip_facet.proto,README.md}`,
`test/conformance/proto/{switching_rules_test.go,interface_rules_test.go}`,
`generated/go/proto/flowseer/net/switching/v1/`,
`generated/go/proto/flowseer/net/ip/v1/`
After: none
Change: adds `string network_instance = 4 [(buf.validate.field).required = true, (buf.validate.field).string.(flowseer.net.key.v1.network_instance_name) = true];`
to `Vlan`.
Adds `string network_instance = 6 [(buf.validate.field).required = true, (buf.validate.field).string.(flowseer.net.key.v1.network_instance_name) = true];`
to `FdbEntry`, and updates the file-level doc comment stating that its key assumes
independent VLAN learning (IVL) and bridge-domain scoping is provided by `network_instance`
(IEEE 802.1Q).
Adds `string network_instance = 3 [(buf.validate.field).required = true, (buf.validate.field).string.(flowseer.net.key.v1.network_instance_name) = true];`
to `IpFacet`.
Updates `spec/proto/flowseer/net/switching/v1/README.md` and `spec/proto/flowseer/net/ip/v1/README.md`
to describe the network-instance key scoping.
Updates `test/conformance/proto/switching_rules_test.go` to provide `network_instance = "default"`
on valid `Vlan` and `FdbEntry` cases and adds test cases proving that `FdbEntry` without
`network_instance` fails validation and `Vlan` without `network_instance` fails validation.
Updates `test/conformance/proto/interface_rules_test.go` to provide `network_instance = "default"`
on `IpFacet` and adds a test case proving that `IpFacet` without `network_instance` fails validation.
buf generate regenerates Go bindings for `net/switching/v1` and `net/ip/v1`.
Tests: `test/conformance/proto/switching_rules_test.go` (verifies requirements 1 and 6:
FdbEntry and Vlan fail when network_instance is omitted; valid cases pass when set),
`test/conformance/proto/interface_rules_test.go` (verifies requirement 5: IpFacet fails
when network_instance is omitted; passes when set).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/switching/v1 spec/proto/flowseer/net/ip/v1 test/conformance/proto/switching_rules_test.go test/conformance/proto/interface_rules_test.go generated/go/proto/flowseer/net/switching/v1 generated/go/proto/flowseer/net/ip/v1`

### U4. Go consumers and netsim export

Files: `src/modules/localnet/snmpmap/{ifmib.go,ifmib_test.go}`,
`src/common/netsim/vswitch/netmodel/{export.go,export_test.go,acceptance_pass10_test.go,conformance_test.go,forward_metadata_test.go,netmodel_test.go,report_test.go,routing_test.go,testdata_icx7150_test.go,trust_test.go}`,
`src/common/netsim/internal/netsimtest/cases.go`
After: U1, U2, U3
Change: `src/modules/localnet/snmpmap/ifmib.go` populates `NetworkInstance: proto.String("default")`
on `IpFacet` when creating routed VLAN interfaces.
`src/common/netsim/vswitch/netmodel/export.go`:
- `FdbEntries` sets `NetworkInstance: proto.String("default")` on all exported `FdbEntry` messages.
- Adds `NetworkInstances(sw *vswitch.Switch) []*instancev1.NetworkInstance` exporting a single
  `DEFAULT` instance named `"default"`.
- Adds `Vlans(sw *vswitch.Switch) []*switchingv1.Vlan` exporting VLAN entries from switch bridge
  configuration with `NetworkInstance: proto.String("default")`.
- Updates all netsim fixtures and test suites in `netmodel` and `netsimtest` that construct
  `Vlan_builder`, `FdbEntry_builder`, and `IpFacet_builder` to set
  `NetworkInstance: proto.String("default")`.
Tests: `src/common/netsim/vswitch/netmodel/export_test.go` (verifies requirement 2: single-bridge
switch netsim export reports one DEFAULT instance named "default", and every Vlan and FdbEntry
row names it), `src/modules/localnet/snmpmap/ifmib_test.go`, and full package test suite:
`go test -race ./src/modules/localnet/snmpmap/... ./src/common/netsim/...`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/localnet/snmpmap src/common/netsim`

### U5. Conventions, concepts, and net package README documentation

Files: `docs/conventions/protobuf.md`,
`./CONCEPTS.md`,
`spec/proto/flowseer/net/README.md`
After: U1, U2, U3
Change: `docs/conventions/protobuf.md` records `NetworkInstance` and `Route` table row shapes,
updates the "Units and keys" section to state the required `network_instance` key rule for
tables and facets (`Vlan`, `FdbEntry`, `Route`, `IpFacet`), and documents `RouteDistinguisher`
under "Typed variants".
`CONCEPTS.md` records `Network instance` and `Route` concepts under Network model vocabulary.
`spec/proto/flowseer/net/README.md` updates the package list entries for `instance/v1/` and
`routing/v1/` from planned to active packages, and adds RFC 4364 (BGP/MPLS IP VPNs),
RFC 8349 (YANG routing), RFC 4292 (IP Forwarding MIB), and IANA-RTPROTO-MIB to Standards
grounding.
Tests: `test/conformance/proto/layout_test.go` (`TestProtoReadmeCoverage` ensures package
README coverage matches directories).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/conventions/protobuf.md CONCEPTS.md spec/proto/flowseer/net/README.md`

Waves: U1 U2 U3 | U4 U5

## Verification

```bash
buf lint
buf generate && git status --porcelain generated/
go build ./... && go vet ./...
go test -race ./test/conformance/... ./src/modules/localnet/snmpmap/... ./src/common/netsim/...
.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/instance spec/proto/flowseer/net/routing spec/proto/flowseer/net/switching spec/proto/flowseer/net/ip test/conformance/proto src/modules/localnet/snmpmap src/common/netsim docs/conventions/protobuf.md CONCEPTS.md spec/proto/flowseer/net/README.md
```

## Definition of done

- [ ] Verifier green for every changed path with targeted paths argument.
- [ ] `spec/proto/flowseer/net/instance/v1/` and `spec/proto/flowseer/net/routing/v1/` exist with `.proto` files and `README.md` passing `buf lint`.
- [ ] `generated/` in sync with `spec/proto/` (`git status --porcelain generated/` empty).
- [ ] Requirements 1 through 11 pass in conformance and netsim test suites.
- [ ] `docs/conventions/protobuf.md`, `CONCEPTS.md`, and `spec/proto/flowseer/net/README.md` updated in the same change.
- [ ] This plan's `status` set to `implemented` with an outcome note under its title upon completion, and no plan labels in code.

## Open questions

None.
