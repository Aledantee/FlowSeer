---
title: Schema Building Blocks Phase 4, Endpoints and Port Access - Plan
type: feat
date: 2026-09-25
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: code
parent: docs/plans/2026-09-25-1713-feat-schema-building-blocks-plan.md
---

# Schema Building Blocks Phase 4, Endpoints and Port Access - Plan

> Implemented. 5 units, 2026-09-26T09:34:11Z to 2026-09-26T10:27:10Z. Targeted verification run over union of changed paths per directive (replacing --full).

## Goal

FlowSeer's protobuf schema gains the building blocks for hosts seen through the
network and access-layer port sessions. `net/endpoint/v1` holds wired and
wireless attachment primitives, client fingerprints, counters, connection
failure stages, and roam reasons; `net/portaccess/v1` holds port-access
sessions populated by 802.1X, MAC authentication bypass (MAB), and web
authentication; `model/endpoint/v1` holds the top-level `Endpoint` entity
(`EndpointState`, `EndpointEvent`, and the ref pair). A conformance fixture
proves a UniFi wireless client report maps into `EndpointState` with a wireless
attachment. The means is five units: endpoint primitives, port-access
primitives, the endpoint entity family, the UniFi fixture test, and
documentation synchronization.

Stop if a targeted provider reports an endpoint attachment or identifier that
cannot be expressed as either a wired port or wireless BSS attachment, or if
port-access sessions require per-session configuration intent.

## Decisions

The parent's decisions, the
[schema building blocks record](../architecture/2026-09-25-schema-building-blocks-direction.md)
(Endpoint decision, rule 1 units, rule 2 counters, rule 3 keys, rule 4
network instances, rule 6 functional packages, rule 7 enums, rule 8 SSID bytes),
and the conventions doc govern. The research dossiers are
[01](../research/schema-building-blocks/01-wifi-technology.md),
[03](../research/schema-building-blocks/03-clients-endpoints.md),
[05](../research/schema-building-blocks/05-l2-and-instances.md),
[07](../research/schema-building-blocks/07-qos-security-ops-wan.md), and
[09](../research/schema-building-blocks/09-wifi-registries.md).

- `Endpoint` (`model/endpoint/v1`) is top-level and UUID-keyed
  (`EndpointLocalRef.id`, `EndpointGlobalRef`). MAC is correlation data rather
  than the entity key because randomized MACs rotate per network, boot, or day
  (IEEE 802c AAI quadrant, dossier 03 §2). The triad has `EndpointState` and
  `EndpointEvent` with deliberately absent `EndpointConfig` (named in the file
  comment per conventions doc and `tools/hooks/proto-check.sh` check);
  endpoints are machine-observed hosts discovered through the network that
  nobody configures directly (operator labels and tags use existing
  `model/inventory` entities). Provenance rides the envelope per conventions
  doc convention 9; conflicting sightings (DHCP versus mDNS) are merged by the
  service before writing State. Endpoint stays outside `EntityType` enum until
  its store lands.
- An Endpoint's randomized-MAC merge policy requires no schema support beyond
  the repeated observed-address list `repeated MacAddress observed_mac_addresses`
  (min_items: 1). Why: the direction record (Endpoint entity decision) and
  dossier 03 §5 establish that MAC rotation is correlation data and the merge
  policy across BSSIDs, time windows, and 802.1X identities is a service
  concern; the wire schema needs only to make the key an opaque UUID and
  observed MACs a repeated list.
- `EndpointState` address cardinality: IPv4 is an optional
  `flowseer.net.addr.v1.Ipv4Address ipv4_address`, while IPv6 is
  `repeated flowseer.net.addr.v1.Ipv6Address ipv6_addresses`. Why: Cisco
  `sisf-db-mac-entry` models IPv6 bindings as a list (max 8) while IPv4 is a
  single binding, and Meraki reports `ip6` alongside `ip6Local` (global and
  link-local simultaneously); under RFC 4861 SLAAC, a client commonly holds a
  privacy address, a stable address, and a link-local address at the same time
  (dossier 03 §2, §6).
- Ruled (drive, on the user's cross-plan ruling): requirement 1 asserts the
  enforceable invariant, not the impossible state of both arms set. A protobuf
  `oneof` holds at most one arm in memory and wire decoding is last-tag-wins, so
  `protovalidate` can reject an `EndpointState` only when no arm is set.
  `required` enforces at-least-one; the `oneof` enforces at-most-one. A comment
  on the `attachment` oneof records that both arms are structurally
  unrepresentable.
- A wired attachment is `WiredAttachment` (`net/endpoint/v1`): holds
  `string switch_name` (1..256), `string interface_name` (validated with
  `flowseer.net.key.v1.interface_name`), and `uint32 vlan_id` (validated with
  `flowseer.net.switching.v1.vlan_id`). Why: Ruckus `APWiredClientInfo` reports
  `ethIF` and `vlan`, Meraki reports `switchport` and `vlan`, and UniFi reports
  `uplinkDeviceId` (dossier 03 §2, §4). Primitives name peers by device-local
  key, not refs.
- A wireless attachment is `WirelessAttachment` (`net/endpoint/v1`): holds
  `string ap_name` (1..256), a required `oneof serving_bss` holding either
  `flowseer.net.addr.v1.MacAddress bssid = 2` or `string vendor_bss_id = 12`
  (1..256, the source's own AP/BSS id, scoped by the envelope's provenance),
  `bytes ssid` (max_len 32, Rule 8), `flowseer.net.wlan.v1.WifiBand band`,
  `uint32 channel` (validated with `flowseer.net.wlan.v1.wifi_channel`),
  `sint32 rssi_millidbm` (Rule 1), `sint32 snr_millidb` (Rule 1),
  `uint64 negotiated_rate_bps` (Rule 1), `uint32 mcs` (0..31), `uint32 nss`
  (1..8), `google.protobuf.Duration guard_interval` (Rule 1), with CEL rule
  `wireless_attachment.channel_needs_band`
  (`!has(this.channel) || (has(this.band) && this.band != 0)`). Why: reuses
  `WifiBand` and `wifi_channel` from phase 3 (`net/wlan/v1`); 2.4 GHz and 6 GHz
  channel numbers overlap so channel requires band (dossier 09 §2); RSSI is
  absolute power in milli-dBm and SNR is a ratio in milli-dB per Rule 1;
  negotiated rate is in `_bps` per Rule 1; MCS/NSS/guard interval are
  association-level facts (dossier 01 §2, dossier 03 §4); guard interval is a
  time duration (400 ns, 800 ns, 1600 ns, 3200 ns) converting exactly to
  `Duration` per Rule 1.
- `EndpointFingerprint` (`net/endpoint/v1`): holds
  `repeated uint32 dhcp_parameter_request_list` (Option 55, RFC 2132 §9.8,
  validated with predefined rule `dhcp_option_code = 50006`),
  `string dhcp_vendor_class` (Option 60, RFC 2132 §9.13, max_len 255), and
  `string http_user_agent` (max_len 1024). Why: these are raw observed facts
  client stacks emit; classification into an OS or device guess is a service
  concern and stays out of the schema (dossier 03 §4, doc-style contract-only
  rule).
- Predefined rule `dhcp_option_code = 50006` on `buf.validate.UInt32Rules`:
  validates `!rule || (this >= 1u && this <= 254u)` with id
  `uint32.dhcp_option_code`. Why: 50005 is the last `UInt32Rules` number in
  `docs/code-style-proto.md` (from phase 3's `wifi_channel_width_mhz`), and
  BOOTP/DHCP option codes are IANA-assigned in 1..254 (RFC 2132).
- `EndpointCounters` (`net/endpoint/v1`): traffic counters `uint64 in_bytes`,
  `uint64 out_bytes`, `uint64 in_frames`, `uint64 out_frames`,
  `uint64 tx_retry_frames`, and `google.protobuf.Timestamp last_discontinuity`.
  Why: Rule 2 governs naming (`in_bytes`, `in_frames`) and requires
  `last_discontinuity` (enforced by `TestCountersCarryDiscontinuity`). Ruckus
  `APClientInfo` and Cisco ewlc client statistics report these traffic and retry
  metrics (dossier 03 §4).
- `ConnectionFailureStage` normalized enum (`net/endpoint/v1`):
  `CONNECTION_FAILURE_STAGE_UNSPECIFIED = 0`, `_ASSOCIATION = 1`,
  `_AUTHENTICATION = 2`, `_DHCP = 3`, `_DNS = 4`. Why: normalized from Cisco
  Meraki Wireless Connection Stats API
  (`/networks/{networkId}/wireless/clients/connectionStats` returning failure
  counts by stage: `assoc`, `auth`, `dhcp`, `dns`, dossier 03 §2, §4).
- `RoamReason` normalized enum (`net/endpoint/v1`):
  `ROAM_REASON_UNSPECIFIED = 0`, `_STANDARD = 1` (802.11 reassociation without
  fast transition), `_FAST_BSS_TRANSITION = 2` (IEEE 802.11r FT),
  `_OPPORTUNISTIC_KEY_CACHING = 3` (OKC / PMKID caching),
  `_BSS_TRANSITION_MANAGEMENT = 4` (IEEE 802.11v BTM), `_BAND_STEERED = 5`
  (controller or AP band steering), `_LOAD_BALANCED = 6` (AP or controller load
  balancing). Why: normalized from IEEE Std 802.11-2020 clauses 13.11 and 11.24.8,
  Cisco `dot11-client-roam-type`
  (`Cisco-IOS-XE-wireless-mobility-types.yang:107-137`), and wireless network
  domain atlas 08 §roaming.
- `EndpointEvent` (`model/endpoint/v1`): holds required `EndpointGlobalRef ref`
  and a required `oneof event` with arms starting at 10:
  `EndpointLifecycleTransition lifecycle = 10`, `EndpointRoam roam = 11`,
  `EndpointConnectionFailure connection_failure = 12`.
  `EndpointLifecycleTransition` enforces `from != to` via CEL rule
  `endpoint_lifecycle_transition.changes`. `EndpointRoam` holds optional
  `WirelessAttachment from_attachment`, required
  `WirelessAttachment to_attachment`, and `RoamReason reason`.
  `EndpointConnectionFailure` holds required `ConnectionFailureStage stage`
  (rejecting unspecified), optional `string detail` (max_len 1024), and an
  optional `oneof attempted_attachment` (`wired = 10`, `wireless = 11`). Why:
  captures the three distinct transition categories called out in the direction
  record and dossier 03 §5.
- `net/portaccess/v1.Session`: device-scoped table row keyed by
  `string interface_name` (validated with
  `flowseer.net.key.v1.interface_name`) and `MacAddress mac`. Fields:
  `PortAccessMethod method` (`_UNSPECIFIED = 0`, `_DOT1X = 1`, `_MAB = 2`,
  `_WEB_AUTH = 3`), `PortAccessAuthState auth_state` (`_UNSPECIFIED = 0`,
  `_AUTHORIZED = 1`, `_UNAUTHORIZED = 2`), `uint32 assigned_vlan_id` (validated
  with `flowseer.net.switching.v1.vlan_id`), `string role_name` (1..256),
  `string user_name` (1..256). Why: functional package per direction record
  rule 6 (port-access sessions are filled by 802.1X, MAB, and web auth alike, so
  they live in `net/portaccess`, not `protocol/dot1x`); keyed by interface and
  MAC because multi-supplicant switches allow multiple sessions per port
  (IEEE8021-PAE-MIB, Aruba CX `arubaWiredPortAccessClientTable`, dossier 05
  §4.3, dossier 07 §4); interface name scopes the key so network-instance is
  inherited and omitted per Rule 4.
- UniFi fixture test shape: `test/conformance/proto/endpoint_unifi_fixture_test.go`
  holds a sample JSON string conforming to `Wireless client details` from
  `spec/openapi/ubiquiti/unifi-network-openapi-v10.4.57.json:28283-28363`
  (`id`, `name`, `type`, `macAddress`, `ipAddress`, `uplinkDeviceId`,
  `connectedAt`, `access`). A test-local mapper `endpointStateFromUniFi`
  converts the JSON into an `EndpointState` with `EndpointLocalRef.id` from `id`,
  `hostname` from `name`, `observed_mac_addresses` from `macAddress`,
  `ipv4_address` from `ipAddress`, and `WirelessAttachment` from `uplinkDeviceId`
  (`ap_name`) and `type`. The test asserts mapped fields and runs
  `protovalidate.Validate` to prove representability. Why: matches the fixture
  precedent established by `wlan_ruckus_fixture_test.go` in phase 3; proves
  external vendor representability without adding an unmounted production
  mapper.

## Requirements

1. An `EndpointState` with no attachment arm set fails validation; an
   `EndpointState` with only `wired` or only `wireless` set passes. Example:
   `EndpointState_builder{Ref: ref, Lifecycle: ACTIVE.Enum(), ObservedMacAddresses: []*MacAddress{mac}}`
   without an attachment fails `protovalidate.Validate` with violation path
   `attachment` and message `exactly one field is required`; setting `Wired:
   wired` passes. Both arms are structurally unrepresentable in protobuf memory
   (decoding wire bytes carrying both tags leaves only the last arm set); a
   comment on `EndpointState.attachment` states this.
2. A UniFi wireless-client fixture from
   `spec/openapi/ubiquiti/unifi-network-openapi-v10.4.57.json`'s schema maps to
   an `EndpointState` with a wireless attachment. Example: JSON with `id:
   "11111111-2222-3333-4444-555555555555"`, `name: "Alice-Laptop"`, `type:
   "WIRELESS"`, `macAddress: "00:11:22:33:44:55"`, `ipAddress: "192.0.2.42"`,
   and `uplinkDeviceId: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"` maps to a
   valid `EndpointState` with `ref.endpoint.id =
   "11111111-2222-3333-4444-555555555555"`, `hostname = "Alice-Laptop"`,
   `observed_mac_addresses[0]` matching the MAC, `ipv4_address` matching
   `192.0.2.42`, `wireless.ap_name = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"`,
   and `wireless.vendor_bss_id` equal to the same `uplinkDeviceId` with no
   `bssid` set. The UniFi schema reports no BSSID, so the fixture maps only
   fields the schema has.
3. A port-access session without `interface_name` or without `mac` fails
   validation. Example: `Session_builder{Mac: mac}.Build()` fails with
   violation path `interface_name` and message `value is required`;
   `Session_builder{InterfaceName: proto.String("ethernet1/1")}.Build()` fails
   with violation path `mac` and message `value is required`; setting both with
   a valid interface name and MAC passes.
4. `EndpointState` and `EndpointEvent` pass the proto hook's family check with
   the absent Config named in the file comment. Example: feeding
   `tools/hooks/proto-check.sh` the JSON
   `{"cwd":"<repo>","tool_input":{"file_path":"<repo>/spec/proto/flowseer/model/endpoint/v1/endpoint.proto"}}`
   prints no "Message sync" line.
5. An `EndpointState` with an empty `observed_mac_addresses` list fails
   validation. Example: `EndpointState_builder{Ref: ref, Lifecycle:
   ACTIVE.Enum(), Wired: wired}.Build()` without observed MACs fails with
   `observed_mac_addresses: value must contain at least 1 item(s)`; providing
   multiple MAC addresses passes.
6. A `WirelessAttachment` with `channel` set without a valid `band` fails
   validation. Example: `WirelessAttachment_builder{Bssid: bssid, Channel:
   proto.Uint32(6)}.Build()` fails CEL rule
   `wireless_attachment.channel_needs_band`; setting `Band:
   WIFI_BAND_GHZ2P4.Enum()` passes; `channel = 0` or `234` fails
   `uint32.wifi_channel`.
7. `WirelessAttachment` bounds and parameters: an SSID longer than 32 octets
   fails `bytes.max_len`; non-UTF-8 SSID bytes pass. `mcs = 32` fails
   `uint32.lte = 31`; `nss = 0` and `nss = 9` fail `{gte: 1, lte: 8}`;
   `guard_interval` of 400 ns (`{nanos: 400}`) passes; `rssi_millidbm = -65000`
   and `snr_millidb = 30000` pass.
8. A DHCP option code outside 1..254 fails validation. Example:
   `EndpointFingerprint_builder{DhcpParameterRequestList: []uint32{0}}.Build()`
   and with `255` fail predefined rule `uint32.dhcp_option_code`; option code
   `55` passes; `dhcp_vendor_class` longer than 255 bytes fails `string.max_len`;
   `http_user_agent` longer than 1024 characters fails `string.max_len`.
9. `EndpointCounters` carries `last_discontinuity` timestamp and traffic
   counters. Example: `EndpointCounters_builder{InBytes: proto.Uint64(1024),
   OutBytes: proto.Uint64(2048), InFrames: proto.Uint64(10), OutFrames:
   proto.Uint64(20), TxRetryFrames: proto.Uint64(2), LastDiscontinuity:
   timestamppb.Now()}.Build()` passes `protovalidate.Validate` and passes
   `TestCountersCarryDiscontinuity`.
10. `EndpointEvent` validates event transitions: an `EndpointLifecycleTransition`
    with `from == to` fails CEL rule `endpoint_lifecycle_transition.changes`; an
    `EndpointRoam` without `to_attachment` fails `to_attachment: value is
    required`; an `EndpointConnectionFailure` with `stage =
    CONNECTION_FAILURE_STAGE_UNSPECIFIED` fails validation.
11. `PortAccessMethod` and `PortAccessAuthState` enums reject undeclared values;
    `assigned_vlan_id = 0` and `4095` fail `uint32.vlan_id`.
12. `go build ./...` and `go test -race ./...` pass over changed packages,
    including `TestNoFloatingPointFields`, `TestCanonicalUnitSuffixes`,
    `TestCountersCarryDiscontinuity`, `TestKeyFieldsUseKeyRules`,
    `TestProtoReadmeCoverage`, and `TestProtoReadmeImports`.

## Out of scope

- Production mappers from UniFi, Meraki, Ruckus, or Cisco; the UniFi fixture is
  a conformance test proving representability.
- Identity classification algorithms (heuristics mapping Option 55/60 and
  user-agent strings to OS/device names); classification is a service concern.
- Endpoint randomized-MAC merge policies (matching sightings by BSSID window,
  DHCP lease, or 802.1X username); the merge policy is a service concern.
- Multi-link device (MLD) link-level attachment lists for 802.11be; per-link
  attachments wait for multi-link producers.
- Service, store, or `EntityType` admission for Endpoint; deferred until its
  store exists per conventions doc.
- Interface-level 802.1X configuration (`dot1xAuthAuthControlledPortControl`);
  port-access configuration belongs to interface configuration in a future plan.

## Units

### U1. The `net/endpoint/v1` package

Files:
`spec/proto/flowseer/net/endpoint/v1/connection_failure_stage.proto`,
`spec/proto/flowseer/net/endpoint/v1/roam_reason.proto`,
`spec/proto/flowseer/net/endpoint/v1/dhcp.proto`,
`spec/proto/flowseer/net/endpoint/v1/wired_attachment.proto`,
`spec/proto/flowseer/net/endpoint/v1/wireless_attachment.proto`,
`spec/proto/flowseer/net/endpoint/v1/endpoint_fingerprint.proto`,
`spec/proto/flowseer/net/endpoint/v1/endpoint_counters.proto`,
`spec/proto/flowseer/net/endpoint/v1/README.md`,
`docs/code-style-proto.md`,
`test/conformance/proto/endpoint_rules_test.go`,
`generated/go/proto/flowseer/net/endpoint/v1/` (by `buf generate`)
After: none
Change: defines `flowseer.net.endpoint.v1` with one top-level declaration per
file. `dhcp.proto` extends `buf.validate.UInt32Rules` with
`dhcp_option_code = 50006` (CEL `!rule || (this >= 1u && this <= 254u)`) with
id `uint32.dhcp_option_code` and message "value must be a valid DHCP option
code in 1..254". `connection_failure_stage.proto` defines
`ConnectionFailureStage` with values `_UNSPECIFIED = 0`, `_ASSOCIATION = 1`,
`_AUTHENTICATION = 2`, `_DHCP = 3`, `_DNS = 4`. `roam_reason.proto` defines
`RoamReason` with values `_UNSPECIFIED = 0`, `_STANDARD = 1`,
`_FAST_BSS_TRANSITION = 2`, `_OPPORTUNISTIC_KEY_CACHING = 3`,
`_BSS_TRANSITION_MANAGEMENT = 4`, `_BAND_STEERED = 5`, `_LOAD_BALANCED = 6`.
`wired_attachment.proto` defines `WiredAttachment` with `string switch_name`
(1..256), `string interface_name` using `flowseer.net.key.v1.interface_name`,
and `uint32 vlan_id` using `flowseer.net.switching.v1.vlan_id`.
`wireless_attachment.proto` defines `WirelessAttachment` with `string ap_name`
(1..256), a required `oneof serving_bss` of `flowseer.net.addr.v1.MacAddress
bssid = 2` or `string vendor_bss_id = 12` (1..256), `bytes ssid` with
`max_len: 32`, `flowseer.net.wlan.v1.WifiBand band`, `uint32 channel` with
`flowseer.net.wlan.v1.wifi_channel`, `sint32 rssi_millidbm`,
`sint32 snr_millidb`, `uint64 negotiated_rate_bps`, `uint32 mcs` with
`uint32.lte = 31`, `uint32 nss` with `uint32 = {gte: 1, lte: 8}`,
`google.protobuf.Duration guard_interval`, and CEL rule
`wireless_attachment.channel_needs_band`
(`!has(this.channel) || (has(this.band) && this.band != 0)`).
`endpoint_fingerprint.proto` defines `EndpointFingerprint` with
`repeated uint32 dhcp_parameter_request_list` using `dhcp_option_code`,
`string dhcp_vendor_class` with `string.max_len = 255`, and
`string http_user_agent` with `string.max_len = 1024`. `endpoint_counters.proto`
defines `EndpointCounters` with `uint64 in_bytes = 1`, `uint64 out_bytes = 2`,
`uint64 in_frames = 3`, `uint64 out_frames = 4`, `uint64 tx_retry_frames = 5`,
and `google.protobuf.Timestamp last_discontinuity = 6`. Package README states
admission, boundaries, and standards grounding (RFC 2132, IEEE 802.11-2020,
Meraki Connection Stats). `docs/code-style-proto.md` registers `UInt32Rules`
50006 `dhcp_option_code`.
Tests: `test/conformance/proto/endpoint_rules_test.go` covers Requirements 6, 7,
8, and 9: channel needs band; channels 1 and 233 pass, 0 and 234 fail; SSID 32
octets and non-UTF-8 pass, 33 octets fails; MCS 0..31 passes, 32 fails; NSS 1..8
passes, 0 and 9 fail; guard interval 400 ns passes; RSSI and SNR milli-units
pass; DHCP option codes 1..254 pass, 0 and 255 fail; option 60 255 bytes passes,
256 fails; user agent 1024 chars passes, 1025 fails; counters validate
discontinuity and traffic counter fields.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/endpoint/v1 docs/code-style-proto.md test/conformance/proto/endpoint_rules_test.go generated/go/proto/flowseer/net/endpoint/v1`

### U2. The `net/portaccess/v1` package

Files:
`spec/proto/flowseer/net/portaccess/v1/port_access_method.proto`,
`spec/proto/flowseer/net/portaccess/v1/port_access_auth_state.proto`,
`spec/proto/flowseer/net/portaccess/v1/session.proto`,
`spec/proto/flowseer/net/portaccess/v1/README.md`,
`test/conformance/proto/portaccess_rules_test.go`,
`generated/go/proto/flowseer/net/portaccess/v1/` (by `buf generate`)
After: none
Change: defines `flowseer.net.portaccess.v1`. `port_access_method.proto` defines
`PortAccessMethod` with values `_UNSPECIFIED = 0`, `_DOT1X = 1`, `_MAB = 2`,
`_WEB_AUTH = 3`. `port_access_auth_state.proto` defines `PortAccessAuthState`
with values `_UNSPECIFIED = 0`, `_AUTHORIZED = 1`, `_UNAUTHORIZED = 2`.
`session.proto` defines `Session` table row with required `string interface_name`
validated by `flowseer.net.key.v1.interface_name`, required
`flowseer.net.addr.v1.MacAddress mac`, optional `PortAccessMethod method`,
optional `PortAccessAuthState auth_state`, optional `uint32 assigned_vlan_id`
validated by `flowseer.net.switching.v1.vlan_id`, optional `string role_name`
with `string.max_len = 256`, and optional `string user_name` with
`string.max_len = 256`. Package README documents functional package boundaries,
standards grounding (IEEE 802.1X-2004, IEEE8021-PAE-MIB, Aruba CX port-access),
and why `network_instance` is omitted (scoped by interface name per Rule 4).
Tests: `test/conformance/proto/portaccess_rules_test.go` covers Requirements 3
and 11: session requires `interface_name` and `mac`; invalid interface name
fails; `assigned_vlan_id` 1..4094 passes, 0 and 4095 fail; undefined enum
values fail validation.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/portaccess/v1 test/conformance/proto/portaccess_rules_test.go generated/go/proto/flowseer/net/portaccess/v1`

### U3. The `model/endpoint/v1` package

Files:
`spec/proto/flowseer/model/endpoint/v1/endpoint.proto`,
`spec/proto/flowseer/model/endpoint/v1/README.md`,
`test/conformance/proto/model_endpoint_rules_test.go`,
`generated/go/proto/flowseer/model/endpoint/v1/` (by `buf generate`)
After: U1
Change: defines `flowseer.model.endpoint.v1` in `endpoint.proto` holding
`EndpointLocalRef`, `EndpointGlobalRef`, `EndpointLifecycle`
(`_UNSPECIFIED = 0`, `_ACTIVE = 1`, `_STALE = 2`, `_RETIRED = 3`),
`EndpointState`, `EndpointLifecycleTransition`, `EndpointRoam`,
`EndpointConnectionFailure`, and `EndpointEvent`. `EndpointLocalRef` has
required UUID `id`. `EndpointGlobalRef` wraps `EndpointLocalRef`.
`EndpointState` has required `ref`, required defined `lifecycle`, optional
`string hostname` (max_len 255), required
`repeated flowseer.net.addr.v1.MacAddress observed_mac_addresses` (min_items 1),
optional `flowseer.net.addr.v1.Ipv4Address ipv4_address`,
`repeated flowseer.net.addr.v1.Ipv6Address ipv6_addresses`, optional
`flowseer.net.endpoint.v1.EndpointFingerprint fingerprint`, optional
`flowseer.net.endpoint.v1.EndpointCounters counters`, and required
`oneof attachment` (`wired = 10`, `wireless = 11`). A comment on the attachment
oneof explains that both arms are structurally unrepresentable in protobuf
memory. `EndpointEvent` has required `ref` and required `oneof event`
(`lifecycle = 10`, `roam = 11`, `connection_failure = 12`).
`EndpointLifecycleTransition` enforces `from != to` via CEL rule
`endpoint_lifecycle_transition.changes`. `EndpointRoam` holds optional
`WirelessAttachment from_attachment`, required
`WirelessAttachment to_attachment`, and defined `RoamReason reason`.
`EndpointConnectionFailure` holds required defined `ConnectionFailureStage stage`
(rejecting 0), optional `string detail` (max_len 1024), and an optional
`oneof attempted_attachment` (`wired = 10`, `wireless = 11`). The file comment
explicitly names `EndpointConfig` as deliberately absent. Package README
documents the family, imports (`net/addr`, `net/endpoint`), and admission
outside `EntityType`.
Tests: `test/conformance/proto/model_endpoint_rules_test.go` covers Requirements
1, 4, 5, and 10: `EndpointState` without attachment fails; with only wired or
only wireless passes; empty observed MACs fails; multiple IPv6 addresses pass;
`EndpointEvent` requires ref and an event arm; lifecycle transition from == to
fails; roam without `to_attachment` fails; connection failure without stage
fails; hook family check verification command passes with no Message sync lines.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/model/endpoint/v1 test/conformance/proto/model_endpoint_rules_test.go generated/go/proto/flowseer/model/endpoint/v1`

### U4. UniFi wireless-client fixture test

Files:
`test/conformance/proto/endpoint_unifi_fixture_test.go`
After: U1, U3
Change: creates `endpoint_unifi_fixture_test.go` with a JSON fixture conforming
to `Wireless client details` from
`spec/openapi/ubiquiti/unifi-network-openapi-v10.4.57.json:28283-28363`.
Implements test-local `endpointStateFromUniFi` parsing the JSON and converting it
to `EndpointState`: UUID `id` mapped to `EndpointLocalRef.id`, `name` mapped to
`hostname`, `macAddress` mapped to `observed_mac_addresses`, `ipAddress` mapped
to `ipv4_address`, `uplinkDeviceId` mapped to `WirelessAttachment.ap_name`, and
`type: "WIRELESS"` selecting the `wireless` arm.
Tests: the fixture unmarshals the JSON sample, asserts each converted field
against expected values, and verifies `protovalidate.Validate(state)` passes
(Requirement 2).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- test/conformance/proto/endpoint_unifi_fixture_test.go`

### U5. Conventions, concepts, and package README sync

Files:
`spec/proto/flowseer/net/README.md`,
`spec/proto/flowseer/model/README.md`,
`spec/proto/flowseer/net/addr/v1/README.md`,
`spec/proto/flowseer/net/key/v1/README.md`,
`spec/proto/flowseer/net/switching/v1/README.md`,
`spec/proto/flowseer/net/wlan/v1/README.md`,
`docs/conventions/protobuf.md`,
`CONCEPTS.md`
After: U1, U2, U3
Change:
`spec/proto/flowseer/net/README.md` drops planned markers on `endpoint/v1/` and
`portaccess/v1/`, adds `model/endpoint` to Imported by, and adds RFC 2132 (DHCP
options), RFC 4702 (DHCP FQDN), IEEE 802.1X / IEEE8021-PAE-MIB, and Meraki
Connection Stats to Standards grounding. `spec/proto/flowseer/model/README.md`
adds `endpoint/v1/` to Packages and `net/endpoint` to Imports. Leaf READMEs
`net/addr/v1/README.md`, `net/key/v1/README.md`, `net/switching/v1/README.md`,
and `net/wlan/v1/README.md` update `Imported by:` to reflect `net/endpoint`,
`net/portaccess`, and `model/endpoint`. `docs/conventions/protobuf.md` records
`Endpoint` in `model/endpoint/v1` in the landed entities outside `EntityType`
list (alongside `Wlan`, `Location`, `Cable`), documents the `Session` table row
in `net/portaccess/v1`, and documents the `EndpointState` / `EndpointEvent` triad
shape. `CONCEPTS.md` adds `Endpoint` and `Port-access session` entries under
Network model vocabulary.
Tests: `test/conformance/proto/layout_test.go` (`TestProtoReadmeCoverage` and
`TestProtoReadmeImports`) passes.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/README.md spec/proto/flowseer/model/README.md spec/proto/flowseer/net/addr/v1/README.md spec/proto/flowseer/net/key/v1/README.md spec/proto/flowseer/net/switching/v1/README.md spec/proto/flowseer/net/wlan/v1/README.md docs/conventions/protobuf.md CONCEPTS.md`

Waves: U1 U2 | U3 | U4 U5

U1 and U2 touch disjoint packages and run in parallel in Wave 1. U3 imports U1's
primitives and runs in Wave 2. U4 and U5 touch disjoint files and run in parallel
in Wave 3.

## Verification

```bash
buf lint
buf generate && git status --porcelain generated/   # empty after generation
go build ./... && go vet ./test/conformance/...
go test -race ./test/conformance/proto/...
printf '{"cwd":"%s","tool_input":{"file_path":"%s/spec/proto/flowseer/model/endpoint/v1/endpoint.proto"}}' "$PWD" "$PWD" \
  | tools/hooks/proto-check.sh 2>&1 | grep -c 'Message sync' || true   # prints 0
.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/endpoint spec/proto/flowseer/net/portaccess spec/proto/flowseer/model/endpoint spec/proto/flowseer/net spec/proto/flowseer/model docs/code-style-proto.md docs/conventions/protobuf.md CONCEPTS.md test/conformance/proto generated/go/proto/flowseer/net/endpoint/v1 generated/go/proto/flowseer/net/portaccess/v1 generated/go/proto/flowseer/model/endpoint/v1
```

The verifier runs over the union of changed paths, never `--full`: a full run
builds and race-tests `generated/go/yang` and exhausts host memory. The parent's
`--full` line is replaced by this targeted run for the same reason recorded in
phases 1 and 3.

## Definition of done

- [x] Verifier green for every changed path (targeted, as above).
- [x] `spec/proto/flowseer/net/endpoint/v1/`,
      `spec/proto/flowseer/net/portaccess/v1/`, and
      `spec/proto/flowseer/model/endpoint/v1/` exist with `.proto` files and a
      README; `buf lint` passes; `generated/` matches.
- [x] Requirements 1 to 12 pass in `test/conformance/proto`.
- [x] The hook command prints no "Message sync" line for `endpoint.proto`.
- [x] `docs/code-style-proto.md` registers `UInt32Rules` 50006 `dhcp_option_code`.
- [x] Package READMEs, root `net/` and `model/` READMEs,
      `docs/conventions/protobuf.md`, and `CONCEPTS.md` match the tree.
- [x] This plan's `status` is `implemented` with an outcome note under its
      title, the parent's U4 `Landed:` line carries the commit range, and no
      plan label appears in code, comments, or commit messages.

## Open questions

None.
