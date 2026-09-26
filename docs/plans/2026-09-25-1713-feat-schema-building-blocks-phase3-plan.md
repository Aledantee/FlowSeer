---
title: Schema Building Blocks Phase 3, Wireless and RF - Plan
type: feat
date: 2026-09-25
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept
execution: code
parent: docs/plans/2026-09-25-1713-feat-schema-building-blocks-plan.md
---

# Schema Building Blocks Phase 3, Wireless and RF - Plan

> Implemented. 4 units, 2026-09-26T08:16Z to 2026-09-26T08:38Z. The
> Verification and Definition-of-done lines naming `--full` are replaced by
> targeted verifier runs over the union of paths changed in each unit,
> because `--full` builds and race-tests `generated/` and exhausts host
> memory; all targeted runs ended `FlowSeer verification passed.`

## Goal

FlowSeer can hold what a controller or cloud API reports about radios,
BSSs, and the RF environment. `net/wlan/v1` holds the radio facet, the BSS
row nested in it, channel utilization, the neighbor-scan row, and the
enums and predefined rules they share; `Component` gains the radio kind
and carries the facet; `model/wireless/v1` holds the `Wlan` family
(`WlanConfig`, `WlanState`, `WlanEvent`, and the ref pair). A conformance
fixture proves a Ruckus `APStatusRadio` report fits the facet in canonical
units. The means is four units: the primitive package, the component
change, the fixture, and the entity family.

Stop if a targeted provider reports a radio quantity the canonical units
cannot hold (the parent's stop condition), or if a provider reports a
channel width that is not in the width rule's set.

## Decisions

The parent's decisions, the
[schema building blocks record](../architecture/2026-09-25-schema-building-blocks-direction.md)
(entity list, rule 1 units, rule 5 facets, rule 7 enums, rule 8 SSID
bytes), and the conventions doc govern. The dossiers are
[01](../research/schema-building-blocks/01-wifi-technology.md),
[02](../research/schema-building-blocks/02-rf-and-ap-telemetry.md), and
[09](../research/schema-building-blocks/09-wifi-registries.md).

- The tree differs from the stub's assumption in one place: `net/measure`
  has no milli-dBm carrier. It holds `basis_points` and the sensor
  readings; power levels are plain `sint32` fields whose suffix names the
  unit (`_millidbm`, `_millidbi`), which `TestCanonicalUnitSuffixes`
  already accepts. Why: record rule 2 adds no shared message until two
  packages need the same shape, and this phase is the first RF user. The
  layering rows for `net/wlan` and `model/wireless` already exist
  (`test/conformance/proto/layering_test.go:53,128`), so this phase does
  not edit that file.
- `WifiBand` is normalized: `WIFI_BAND_UNSPECIFIED = 0`, `_GHZ2P4 = 1`,
  `_GHZ5 = 2`, `_GHZ6 = 3`, `_GHZ60 = 4`. Why: UniFi's `frequencyGHz` is
  `[2.4, 5, 6, 60]` (`spec/openapi/ubiquiti/unifi-network-openapi-v10.4.57.json`),
  a radio is on one band at a time so OpenConfig's composite
  `FREQ_2_5_GHZ` identities never apply (dossier 01 §2), and 900 MHz S1G
  has no targeted producer (dossier 01 §3). The spelling is forced:
  edition 2024's STYLE2024 rejects an enum value segment that starts with
  a digit (`WIFI_BAND_2_4_GHZ` and `WIFI_BAND_BAND_2400_MHZ` both fail
  `buf build` with "enum value name should be SCREAMING_SNAKE_CASE",
  reproduced 2026-09-26), so the unit leads the segment.
- `Dot11Standard` is normalized: `DOT11_STANDARD_UNSPECIFIED = 0`, `_A = 1`,
  `_B = 2`, `_G = 3`, `_N = 4`, `_AC = 5`, `_AX = 6`, `_BE = 7`. Why: the
  same seven letters are UniFi's `wlanStandard` enum and OpenConfig's
  `WIFI_PROTOCOL` (dossier 01 §2). Wi-Fi 4 through 7 are derived from
  standard and band (6E is `_AX` on `_GHZ6`), so they are a display
  concern and no field. A 60 GHz radio leaves `standard` absent: no
  targeted source reports 802.11ad or 802.11ay, and a value is added with
  the first one that does.
- A radio facet is `RadioFacet` with these fields: `radio_index`, `band`,
  `primary_channel`, `secondary_channel`, `channel_width_mhz`,
  `operating_class`, `standard`, `admin_status`, `oper_status`,
  `tx_power_millidbm`, `max_tx_power_millidbm`, `eirp_millidbm`,
  `antenna_gain_millidbi`, `noise_floor_millidbm`, `country_code`,
  `country_environment`, `channel_utilization`, and `repeated Bss bsses`.
  Why: each is reported by at least one targeted source with a stated
  unit (dossier 02 §3: Ruckus `actualTxPower`/`maxTxPower`/`eirp`/
  `noiseFloor`, OpenConfig `transmit-power`/`allowed-max-txpower`/
  `transmit-eirp`/`antenna-gain`, Cisco `antenna-gain` in dBi). The tx
  power values stay separate because Ruckus defines EIRP as tx power plus
  antenna gain (`spec/proto/ruckus/ap/ap_status.proto`, `eirp` field 42)
  and a regulatory cap can clip it (dossier 02 §6).
  Ruckus `calibrationTxPower` gets no field: it is a per-model target that
  no second source reports.
- A BSS nests in the radio that serves it, so `Bss` carries no radio key.
  Why: Ruckus nests `repeated APStatusWlan wlans` in `APStatusRadio`
  (field 27), and a BSSID belongs to exactly one radio; an 802.11be
  multi-link device is one BSS per link, each on its own radio.
- Admin and oper status are radio-local enums, `RadioAdminStatus`
  (`_UNSPECIFIED = 0`, `_UP = 1`, `_DOWN = 2`) and `RadioOperStatus`
  (`_UNSPECIFIED = 0`, `_UP = 1`, `_DOWN = 2`), not `net/interface`'s
  `AdminStatus`/`OperStatus`. Why: the record's amended decision limits
  `net/wlan` to `addr`, `key`, `measure`, and `switching`, and the
  allowlist row enforces it, so importing `net/interface` would change an
  accepted record for two enums. The radio sources also report two states,
  not RFC 2863's seven: Cisco's `enum-radio-admin-state` is enabled or
  disabled and `enum-radio-oper-state` is radio-up or radio-down
  (`spec/yang/cisco/iosxe/2611/Cisco-IOS-XE-wireless-access-point-oper.yang:1460-1491`).
- Two predefined rules on `buf.validate.UInt32Rules`, in
  `net/wlan/v1/channel.proto`: `wifi_channel = 50004` (1 to 233) and
  `wifi_channel_width_mhz = 50005` (in 20, 40, 80, 160, 320, 2160, 4320,
  6480, 8640). Why: 50003 is the last `UInt32Rules` number in the
  registry (`docs/code-style-proto.md`, Predefined-rule extension numbers),
  and both rules are used by `RadioFacet` and `NeighborBss` here and by
  the endpoint wireless attachment in phase 4. 233 is the highest channel
  number in any band (6 GHz, Annex E classes 131 to 137; dossier 09 §2).
  The 60 GHz widths are the channel widths of classes 180 to 183 (dossier
  09 §2); without them a `_GHZ60` radio could not report its width. 80+80
  is not a width: an 80+80 radio reports `channel_width_mhz = 160` with
  `secondary_channel` set to the second segment's channel, the shape
  Ruckus uses (`secondaryChannel`, field 41, "second channel value for
  80_80MHz channel width"), and a CEL rule allows `secondary_channel` only
  at 160. If phase 6 or phase 8 lands a `UInt32Rules` number first, this
  phase takes the next two free numbers at merge; the registry row is
  where the collision shows.
- A channel number needs its band: a CEL rule on `RadioFacet` and on
  `NeighborBss` rejects a channel without a non-zero `band`; a set
  `WIFI_BAND_UNSPECIFIED` does not count, because under explicit presence
  it is set and still names no band. Why: 2.4 GHz channels 1
  to 13 and 6 GHz channels 1 to 233 overlap (dossier 09 §2), and band is
  what disambiguates them in FlowSeer's shape.
- `operating_class` is a `uint32` bounded 1 to 255, not an enum. Why: the
  class is one octet on the wire, the Annex E tables number classes per
  region and reach 183 in the global table alone (dossier 09 §4), and
  channel plus band already disambiguate the channel; the class serves
  802.11k/v candidate lists.
- `WlanSecurity` is normalized, one value per mode the Wi-Fi Alliance
  WPA3 Specification v3.5 defines, plus the pre-RSN modes:
  `WLAN_SECURITY_UNSPECIFIED = 0`, `_OPEN = 1`, `_WEP = 2`, `_OWE = 3`,
  `_WPA2_PERSONAL = 4`, `_WPA3_PERSONAL = 5`, `_WPA3_PERSONAL_TRANSITION = 6`,
  `_WPA2_ENTERPRISE = 7`, `_WPA3_ENTERPRISE = 8`,
  `_WPA3_ENTERPRISE_TRANSITION = 9`, `_WPA3_ENTERPRISE192 = 10`. Why:
  WPA3 v3.5 §2.2, §2.3, §3.2, §3.3, and §3.5 each define an AP BSS
  configuration by its AKM set and PMF bits (read from
  <https://www.wi-fi.org/system/files/WPA3%20Specification%20v3.5.pdf>,
  fetched 2026-09-26). The stub's list lacked Enterprise transition; §3.3
  defines it ("shall enable at least AKM suite selectors 00-0F-AC:1 ... and
  00-0F-AC:5 ... shall be PMF Capable, i.e., AP sets MFPC to 1 and MFPR to
  0"), and UniFi's `WPA2_WPA3_ENTERPRISE` and OpenConfig's
  `WPA3_2_ENTERPRISE_TRANSITION` report it, so without the value a mapper
  would have to misreport it. OWE transition is two BSSs, one open and one
  OWE, linked by the OWE Transition Mode element (dossier 09 §2), so it
  is no value: each BSS reports its own mode. The 192-bit value ends in
  `ENTERPRISE192` because STYLE2024 rejects the segment `192`. WPA3 v3.5's
  Personal Compatibility Mode (RSN overriding) gets no value: none of
  UniFi's, SmartZone's, or OpenConfig's enums names it. Raw AKM and cipher
  suite pass-through enums wait for a producer that reports suite
  selectors (dossier 09 §7.1); no vendor API does.
- `PmfMode` is normalized: `PMF_MODE_UNSPECIFIED = 0`, `_DISABLED = 1`
  (MFPC 0), `_OPTIONAL = 2` (MFPC 1, MFPR 0), `_REQUIRED = 3` (MFPC 1,
  MFPR 1). Why: WPA3 v3.5 distinguishes Transition Mode (PMF Capable) from
  Only Mode (PMF Required) by exactly these bits; OpenConfig's boolean
  `mfp` cannot (dossier 09 §4).
- `WlanConfig` rejects a PMF mode its security mode forbids: when `pmf` is
  set, WPA3-Personal, WPA3-Enterprise, and WPA3-Enterprise 192-bit require
  `_REQUIRED` and the two transition modes require `_OPTIONAL` (WPA3 v3.5
  §2.2, §2.3, §3.2, §3.3, §3.5). `pmf` is not required: unset means the
  controller applies the security mode's own PMF setting, which for the
  WPA3 modes is the one the rule enforces. The rule is on intent only. `Bss` and `WlanState` carry what the
  device reports, because rejecting an observed row loses data the
  operator needs to see (the reasoning of record rule 3 for observed
  interface names).
- Enum fields on observed messages (`RadioFacet`, `Bss`, `NeighborBss`,
  `ChannelUtilization`, and `WlanState`'s mirrored fields) carry no enum
  rule: zero is the source reporting an unknown value and an unknown
  non-zero value stays valid. Why: that is `Interface.admin_status` and
  `oper_status` today (`spec/proto/flowseer/net/interface/v1/interface.proto:36-43`).
  Intent and service-derived enums (`WlanConfig.security`, `WlanConfig.pmf`,
  `WlanConfig.bands` items, `WlanState.status`, `WlanEvent.from`/`to`) are
  `defined_only` with `not_in: [0]`. One observed enum is bounded:
  `RadioFacet.country_environment` carries the field CEL rule
  `radio_facet.country_environment_octet` (`this >= 0 && this <= 255`),
  because an open pass-through enum does not enforce its registry's width
  and the conventions doc bounds every such field at the use site
  (`docs/conventions/protobuf.md`, Enums); the Country element's third
  field is one octet.
- Country is two fields: `country_code`, a string matching `^[A-Z]{2}$`
  (ISO 3166-1 alpha-2, the two octets of the Country element), and
  `country_environment`, the third octet, as `CountryEnvironment`. The
  enum is a registry pass-through keeping the octet values:
  `COUNTRY_ENVIRONMENT_UNSPECIFIED = 0`, `_US_OPERATING_CLASSES = 1`,
  `_EUROPE_OPERATING_CLASSES = 2`, `_JAPAN_OPERATING_CLASSES = 3`,
  `_GLOBAL_OPERATING_CLASSES = 4`, `_S1G_OPERATING_CLASSES = 5`,
  `_CHINA_OPERATING_CLASSES = 6`, `_ALL = 32`, `_INDOOR = 73`,
  `_OUTDOOR = 79`, `_NON_COUNTRY_ENTITY = 88`. Why: the octet is a
  ten-value registry, not indoor or outdoor (Wireshark
  `packet-ieee80211.c:31988-31999` as dossier 09 §2 quotes it, citing
  802.11 clause 7.3.2.9 and `dot11CountryString`,
  `spec/mib/ieee/IEEE802dot11-MIB:475`). The registry does not own zero,
  so `_UNSPECIFIED = 0` stays. A CEL rule requires code `XX` with
  `_NON_COUNTRY_ENTITY` (the dissector's rule). The fields sit on
  `RadioFacet` because each radio beacons its own Country element.
- `ChannelUtilization` carries `total_avg_basis_points`,
  `tx_dot11_avg_basis_points`, `rx_dot11_avg_basis_points`,
  `obss_rx_avg_basis_points`, `non_dot11_avg_basis_points`, and
  `google.protobuf.Duration window`. Why: the first four are OpenConfig's
  `total-channel-utilization`, `tx-dot11-channel-utilization`,
  `rx-dot11-channel-utilization`, and `obss-rx`; non-802.11 is Meraki's
  `utilizationNon80211` and Cisco's `rx-noise-channel-utilization`
  (dossier 02 §3). Utilization is a busy-time fraction over some interval,
  so every source's value is an average, and record rule 2 names a
  windowed statistic for the statistic and the unit
  (`utilization_avg_basis_points`) beside a `window`. Absent `window` means
  the source states no interval, as with Ruckus's exponential averages. No
  sum rule, because Ruckus's values do not sum (dossier 02 §4).
- `NeighborBss` is the neighbor-scan and rogue row: `bssid`, `ssid`,
  `band`, `primary_channel`, `channel_width_mhz`, `rssi_millidbm`,
  `security`, `classification`, and `radio_name`, the `ComponentLocalRef`
  name of the radio that heard it (bounded 1 to 256 like that ref).
  Absent `radio_name` means the source reports sightings per device
  (Meraki Air Marshal's `detectedBy[].device`). First and last seen are
  not on the row: `net/` primitives carry no observation time
  (`spec/proto/flowseer/net/README.md`, Identity), so the entity or event
  that stores sightings carries them.
- `NeighborClassification` is normalized: `_UNSPECIFIED = 0` (the source
  reported that it does not know), `_UNCLASSIFIED = 1`, `_FRIENDLY = 2`,
  `_MALICIOUS = 3`, `_ROGUE = 4`, `_SPOOF = 5`. Why: Cisco's
  `rogue-class-type` has unclassified, friendly, and malicious
  (`spec/yang/cisco/iosxe/2611/Cisco-IOS-XE-wireless-enum-types.yang:915-953`),
  and Meraki Air Marshal's `types` are `rogue` (seen on the operator's
  wired network) and `spoof` (impersonating the operator's SSID) (dossier
  02 §3). Cisco's `custom` is operator-defined and maps to whichever value
  its severity names, or stays absent. Ruckus's `RogueType`
  (DISCOVERY/UPDATE/DISAPPEAR) is a sighting event kind and is not this
  enum (dossier 02 §6).
- `Bss.ssid` is `bytes` with `max_len = 32` and no `min_len`: an empty SSID
  is what a hidden BSS beacons. `WlanConfig.ssid` is required with
  `min_len = 1`, because the zero-length SSID is the wildcard and names no
  network. Why: record rule 8 (`dot11DesiredSSID`,
  `spec/mib/ieee/IEEE802dot11-MIB:290`).
- `Bss.beacon_interval` is a `google.protobuf.Duration` bounded 1024 µs to
  65535 × 1024 µs (67.10784 s), and `dtim_period` is a `uint32` count
  bounded 1 to 255. Why: record rule 1 converts TU exactly into a
  Duration; the bounds are `dot11BeaconPeriod INTEGER (1..65535)` and
  `dot11DTIMPeriod INTEGER (1..255)` (`spec/mib/ieee/IEEE802dot11-MIB:344,355`).
- The radio kind is `COMPONENT_KIND_RADIO = 14`, and `ComponentState`
  carries `flowseer.net.wlan.v1.RadioFacet radio = 21` beside `module = 20`,
  with a CEL rule that sets it only on a radio. Why: the record's Radio
  entity decision copies the transceiver pattern (`module_only_transceiver`
  in `spec/proto/flowseer/model/inventory/v1/component.proto`). The
  record's rule 5 says a facet is per-interface while its entity list puts
  the radio facet on a component; the component unit amends rule 5's
  sentence to "per interface, or per component for a radio" so the two
  passages agree, and changes no decision.
- No `RadioSettings`. Why: rule 5 makes a Settings that no facet carries a
  review finding, and nothing writes radio channel or power yet. The
  first writer adds it.
- `Wlan` is top-level and UUID-keyed: `WlanLocalRef{id}` and
  `WlanGlobalRef{wlan}` in the Tag shape. `WlanConfig` and `WlanState`
  share field numbers 3 to 9 (`ssid`, `security`, `pmf`, `vlan_id`,
  `hidden`, `enabled`, `bands`) so intent and observation diff field for
  field; Config has `name` at 2, State has `status` at 2 and
  `repeated WlanBroadcast broadcasts` at 10. `WlanBroadcast` names a
  radio by `ComponentGlobalRef` and the BSSID it broadcasts on. Why: the
  record makes Wlan a full triad because "an SSID can be configured and
  broadcast by no AP"; the broadcast list is where that shows.
- `WlanStatus` is normalized: `_UNSPECIFIED = 0`, `_BROADCASTING = 1` (at
  least one broadcast), `_NOT_BROADCASTING = 2` (the source reports the
  WLAN and no radio broadcasts it), `_MISSING = 3` (the source no longer
  reports a configured WLAN). CEL ties `_BROADCASTING` to a non-empty
  broadcast list and the other two to an empty one. `WlanEvent` carries
  `from` and `to` of that status. Why: every landed full triad (Device,
  Edge, CaptureSession) makes its Event a status transition; Tag's
  before/after-Config shape belongs to a family with no observed side.
- `Wlan` does not join `EntityType`. Why: the record admits none of its
  four new entities until a store answers the existence check; the
  conventions doc's list of landed, outside-the-enum entities gains Wlan.
- No production mapper. The Ruckus fixture is a conformance test that
  decodes known wire bytes with the generated vendor type and converts them
  with a test-local function. Why: the parent keeps mappers from live
  sources out of scope, and `src/modules/README.md` admits no package that
  no host wires; the test proves representability, which is what the
  parent's stop condition asks.

## Requirements

1. A `Bss` with a 33-octet SSID fails; one with the non-UTF-8 SSID
   `0xff 0xfe` passes. Example: `wlanv1.Bss_builder{Bssid: <a valid MacAddress>,
   Ssid: bytes.Repeat([]byte{'a'}, 33)}.Build()` fails with rule
   `bytes.max_len`; the same with `Ssid: []byte{0xff, 0xfe}` passes.
2. A radio facet with `channel_width_mhz = 320` passes and `60` fails.
   Example: `RadioFacet_builder{ChannelWidthMhz: proto.Uint32(320)}` passes;
   `60` fails with rule id `uint32.wifi_channel_width_mhz`; `2160` passes.
3. A Ruckus `APStatusRadio` report maps to a radio facet with tx power in
   milli-dBm. Example: the 37 bytes
   `08 01 10 24 22 02 35 47 48 a1 ff ff ff ff ff ff ff ff 01 b0 01 25 d0 01 50 c0 02 01 d0 02 14 a8 03 11 b8 03 17`
   decode (checked with `buf convert`) to `radioId 1, channel 36, band "5G",
   noiseFloor -95, total 37, channelWidth 80, isRadioEnabled true, eirp 20,
   actualTxPower 17, maxTxPower 23`, and convert to a valid `RadioFacet`
   with `tx_power_millidbm = 17000`, `max_tx_power_millidbm = 23000`,
   `eirp_millidbm = 20000`, `noise_floor_millidbm = -95000`,
   `band = WIFI_BAND_GHZ5`, `primary_channel = 36`,
   `channel_width_mhz = 80`, `oper_status = RADIO_OPER_STATUS_UP`,
   `channel_utilization.total_avg_basis_points = 3700`, `radio_index = 1`.
4. `WlanConfig`, `WlanState`, and `WlanEvent` pass the proto hook's family
   check. Example: feeding `tools/hooks/proto-check.sh` the JSON
   `{"cwd":"<repo>","tool_input":{"file_path":"<repo>/spec/proto/flowseer/model/wireless/v1/wlan.proto"}}`
   prints no "Message sync" line.
5. A channel without a band fails. Example: `RadioFacet_builder{PrimaryChannel:
   proto.Uint32(6)}` fails `radio_facet.channel_needs_band`, and so does the
   same with `Band: wlanv1.WifiBand_WIFI_BAND_UNSPECIFIED.Enum()`; with
   `Band: wlanv1.WifiBand_WIFI_BAND_GHZ6.Enum()` it passes. Channel `0` and
   `234` fail `uint32.wifi_channel`.
6. `secondary_channel` is set only at width 160. Example: width 80 with
   `secondary_channel = 155` fails `radio_facet.secondary_needs_160`;
   width 160 with it passes.
7. `country_environment = _NON_COUNTRY_ENTITY` requires `country_code =
   "XX"`. Example: with `"US"` it fails
   `radio_facet.non_country_entity_code`; `"us"` fails the pattern;
   `country_environment` of 256 fails
   `radio_facet.country_environment_octet` and 73 (`_INDOOR`) passes.
8. A `ComponentState` of kind port carrying `radio` fails
   `component_state.radio_only_radio`; kind radio with a valid facet
   passes.
9. `WlanConfig` with `security = WPA3_PERSONAL` and `pmf = OPTIONAL` fails
   `wlan_config.pmf_matches_security`; with `REQUIRED` it passes, and
   `WPA3_PERSONAL_TRANSITION` with `OPTIONAL` passes; `WPA3_PERSONAL` with
   `pmf` absent passes, because an unset PMF defers to the mode.
10. `WlanState` with `status = BROADCASTING` and no broadcasts fails
    `wlan_state.status_matches_broadcasts`; `NOT_BROADCASTING` with one
    broadcast fails the same rule.
11. `go build ./...` and `go test -race` over the changed packages pass,
    including `TestCanonicalUnitSuffixes`, `TestNoFloatingPointFields`,
    `TestProtoReadmeCoverage`, and `TestProtoReadmeImports`.

## Out of scope

- Production mappers from Ruckus, Meraki, UniFi, Cisco, or OpenConfig;
  which vendor security string maps to which `WlanSecurity` value (for
  example SmartZone's `WPA_Mixed`) is the first mapper's decision.
- Radio and BSS counters, radio mode (access, monitor, sensor, mesh), mesh
  role, chains and spatial streams, RCPI and RSNI, spectrum interferer
  types, DFS radar events, MLO link membership (`mld_address`), 802.11k/v/r
  toggles, BSS type: no requirement asks for them, and dossier 02 §7 found
  no grounded source for several.
- Raw AKM and cipher suite pass-through enums.
- Wireless client facts; phase 4 puts them in `net/endpoint`.
- A service, store, or `EntityType` value for Wlan.
- `DeviceState` fields and sensor rows on `Component`; phase 5 edits
  `component.proto` after this phase.

## Units

### U1. The `net/wlan/v1` package

Files: spec/proto/flowseer/net/wlan/v1/{channel.proto,wifi_band.proto,dot11_standard.proto,wlan_security.proto,pmf_mode.proto,radio_admin_status.proto,radio_oper_status.proto,country_environment.proto,channel_utilization.proto,bss.proto,radio_facet.proto,neighbor_classification.proto,neighbor_bss.proto,README.md},
spec/proto/flowseer/net/{addr,measure,switching}/v1/README.md,
spec/proto/flowseer/net/README.md,
docs/code-style-proto.md,
test/conformance/proto/wlan_rules_test.go,
generated/go/proto/flowseer/net/wlan/v1/ (by `buf generate`)
After: none
Change: `flowseer.net.wlan.v1` exists with one top-level declaration per
file. `channel.proto` extends `buf.validate.UInt32Rules` with
`wifi_channel = 50004` (CEL `!rule || (this >= 1u && this <= 233u)`) and
`wifi_channel_width_mhz = 50005` (CEL `!rule || this in [20u, 40u, 80u,
160u, 320u, 2160u, 4320u, 6480u, 8640u]`), each with a stable id
(`uint32.wifi_channel`, `uint32.wifi_channel_width_mhz`) and a `message:`
string written for the API consumer. The enum files hold `WifiBand`,
`Dot11Standard`, `WlanSecurity`, `PmfMode`, `RadioAdminStatus`, `RadioOperStatus`, `CountryEnvironment`,
and `NeighborClassification` with the values in Decisions; each enum
comment names its source (the WPA3 v3.5 section per `WlanSecurity` value,
the MFPC/MFPR bits per `PmfMode` value, the octet per
`CountryEnvironment` value). `ChannelUtilization` has the five `uint32`
`_avg_basis_points` fields (each with
`(flowseer.net.measure.v1.basis_points)`) at 1 to 5 and `window` at 6. `Bss` has `bssid`
(`flowseer.net.addr.v1.MacAddress`, required) = 1, `ssid` (bytes,
`max_len: 32`) = 2, `security` = 3, `pmf` = 4, `vlan_id` (with
`(flowseer.net.switching.v1.vlan_id)`) = 5, `hidden` = 6,
`beacon_interval` (Duration with the bounds in Decisions) = 7,
`dtim_period` (`uint32`, 1 to 255) = 8, and `uint32
associated_client_count` (OpenConfig `num-associated-clients`, Ruckus
`totalClientCnts`) = 9. `RadioFacet` has the Decisions' fields numbered 1
to 18 in the listed order: `uint32 radio_index` (no bound; Ruckus numbers
from 0, CAPWAP from 1), the `uint32` channel fields using `wifi_channel`, the `uint32` width using `wifi_channel_width_mhz`, `uint32
operating_class` using `{gte: 1, lte: 255}`, the five power fields as
`sint32`, `country_code` using `string.pattern: "^[A-Z]{2}$"`, no rule on
its enum fields except the field CEL `radio_facet.country_environment_octet`
on `country_environment`, and three message CEL rules:
`radio_facet.channel_needs_band`
(`(!has(this.primary_channel) && !has(this.secondary_channel)) || (has(this.band) && this.band != 0)`),
`radio_facet.secondary_needs_160`
(`!has(this.secondary_channel) || (has(this.channel_width_mhz) && this.channel_width_mhz == 160u)`),
and `radio_facet.non_country_entity_code`
(`!has(this.country_environment) || this.country_environment != 88 || (has(this.country_code) && this.country_code == 'XX')`).
`NeighborBss` has the Decisions' fields numbered 1 to 9: `bssid` required,
`bytes ssid` with `max_len: 32`, `band`, the `uint32` channel and width
with their predefined rules, `sint32 rssi_millidbm`, `security`,
`classification`, and `string radio_name` (1 to 256), with the rule
`neighbor_bss.channel_needs_band` written as `RadioFacet`'s over
`primary_channel` alone. Every comment
states units and what absence means, and no message name ends in
`Config`, `State`, or `Event`. The package README states imports
(`net/addr`, `net/measure`, `net/switching`), `Imported by: nothing`,
contents, what is deliberately absent (per Out of scope), and sources
(IEEE802dot11-MIB, WPA3 v3.5, OpenConfig wifi, dossiers 01, 02, 09). The
`addr`, `measure`, and `switching` READMEs add `net/wlan` to Imported by;
`net/README.md` drops the planned marker on `wlan/v1/`;
`docs/code-style-proto.md` adds the two `UInt32Rules` registry rows.
Tests: `test/conformance/proto/wlan_rules_test.go` covers Requirements 1,
2, 5, 6, and 7: SSID 32 octets passes and 33 fails, `0xff 0xfe` passes,
empty SSID passes; widths 20, 320, and 2160 pass and 60 and 0 fail;
channels 1 and 233 pass, 0 and 234 fail; channel without band fails;
a channel with an explicitly unspecified band fails; secondary channel at
80 fails and at 160 passes; `XX` with non-country-entity passes and `US`
fails; `us` fails the pattern; `country_environment` 256 fails;
`beacon_interval` of 1023 µs fails and 102400 µs passes; `dtim_period` 0
fails; a basis-points field of 10001 fails; `Bss.security` of 0 and of
an undeclared value 99 both pass; `NeighborBss` without `bssid` fails. The
schema-language tests in `schema_language_test.go` walk the new package
through this file's import.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/wlan/v1 spec/proto/flowseer/net/addr/v1/README.md spec/proto/flowseer/net/measure/v1/README.md spec/proto/flowseer/net/switching/v1/README.md spec/proto/flowseer/net/README.md docs/code-style-proto.md test/conformance/proto/wlan_rules_test.go generated/go/proto/flowseer/net/wlan/v1`

### U2. Ruckus radio fixture

Files: test/conformance/proto/wlan_ruckus_fixture_test.go
After: U1
Change: the test holds Requirement 3's 37 bytes as a hex constant with a
comment naming each field's tag and value, unmarshals them into
`ap.APStatusRadio` from `generated/go/proto/ruckus/ap`, and converts with
a test-local `radioFacetFromRuckus`: dBm fields times 1000 into the
`_millidbm` fields, read only when `Has*` reports them (the vendor file
keeps explicit presence); `channelWidth` into `channel_width_mhz`;
`secondaryChannel` only when non-zero; `isRadioEnabled` into
`oper_status` (the vendor comment reads "is wfii interface up or not");
`total` times 100 into `channel_utilization.total_avg_basis_points`; `band`
through a classifier accepting `"2.4G"`, `"5G"`, and `"6G"` (the
vocabulary of `ap_report.proto:734`, "0: 2.4G 1: 5G"; the `6G` spelling
is unverified), which leaves `band` and both channels absent for any
other string. The vendor's `txPower` string and `calibrationTxPower` are
not read.
Tests: the fixture asserts the decoded vendor values, then the converted
facet's fields exactly as Requirement 3 lists them, then
`protovalidate.Validate` on the facet. A second case feeds the band
string `"60G"` and asserts `band` and `primary_channel` are both absent and
the facet still validates. Nothing here checks that Ruckus's `total` is a
percentage; dossier 02 reads it as one, and the first production mapper
confirms it on a live report.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- test/conformance/proto/wlan_ruckus_fixture_test.go`

### U3. The radio component

Files: spec/proto/flowseer/model/inventory/v1/{component.proto,README.md},
spec/proto/flowseer/model/README.md,
spec/proto/flowseer/net/wlan/v1/README.md,
test/conformance/proto/model_inventory_topology_rules_test.go,
docs/architecture/2026-09-25-schema-building-blocks-direction.md,
docs/conventions/protobuf.md,
CONCEPTS.md,
generated/go/proto/flowseer/model/inventory/v1/ (by `buf generate`)
After: U1
Change: `ComponentKind` gains `COMPONENT_KIND_RADIO = 14` (comment: an
802.11 radio; `radio` carries its facet), and the enum comment names the
radio beside the transceiver as FlowSeer's additions to RFC 6933.
`ComponentState` gains `flowseer.net.wlan.v1.RadioFacet radio = 21`
("For a radio, the facet read from it. Set only on a radio; unset there
means the radio was not read.") and the CEL rule
`component_state.radio_only_radio` (`!has(this.radio) || this.kind == 14`).
The inventory README adds `net/wlan` to Imports; the `model/` README adds
`net/wlan` to Imports; the `net/wlan` README names `model/inventory` under
Imported by. The inventory README's Components prose names the radio
beside the transceiver as a kind added to RFC 6933 and `radio` beside
`module` as a facet a component carries. The record's rule 5 first
sentence reads "A facet is per-interface, or per-component for a radio,
and named `<Name>Facet`"; in the conventions doc, the "Primitives refer to
peers by key" paragraph ("what a source reports about one interface for
one layer") and the "Facets, settings, and rows" bullet say the same;
`CONCEPTS.md` widens Facet from "embedded by value in the interface
message" to "embedded by value in the interface or component it
describes", and adds a Radio entry under Network model (a component of
kind radio carrying a radio facet with its BSSs; an AP is a Device).
Tests: `model_inventory_topology_rules_test.go` gains Requirement 8's two
cases, and a radio component without a facet passes.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/model/inventory/v1 spec/proto/flowseer/model/README.md spec/proto/flowseer/net/wlan/v1/README.md test/conformance/proto/model_inventory_topology_rules_test.go docs/architecture/2026-09-25-schema-building-blocks-direction.md docs/conventions/protobuf.md CONCEPTS.md generated/go/proto/flowseer/model/inventory/v1`

### U4. The Wlan family

Files: spec/proto/flowseer/model/wireless/v1/{wlan.proto,README.md},
spec/proto/flowseer/model/README.md,
spec/proto/flowseer/model/inventory/v1/README.md,
spec/proto/flowseer/net/{addr,switching,wlan}/v1/README.md,
spec/proto/flowseer/net/README.md,
docs/conventions/protobuf.md,
CONCEPTS.md,
test/conformance/proto/model_wireless_rules_test.go,
generated/go/proto/flowseer/model/wireless/v1/ (by `buf generate`)
After: U1, U3 (both edit the `model/` and inventory READMEs,
`docs/conventions/protobuf.md`, and `CONCEPTS.md`)
Change: `wlan.proto` in `flowseer.model.wireless.v1` holds, in this order,
`WlanLocalRef` (`id`, required UUID), `WlanGlobalRef` (`wlan`, required),
`WlanStatus`, `WlanBroadcast` (`radio` =
`flowseer.model.inventory.v1.ComponentGlobalRef`, required, = 1; `bssid`
= `flowseer.net.addr.v1.MacAddress`, required, = 2), `WlanConfig`,
`WlanState`, and `WlanEvent`. `WlanConfig`: `ref` = 1 required, `name` = 2
(1 to 1024 characters, free text with no standard size per record rule
3), `ssid` = 3 (bytes, required, 1 to 32), `security` = 4 (required,
`defined_only`, `not_in: [0]`), `pmf` = 5 (`defined_only`, `not_in: [0]`,
not required; comment: unset means the controller applies the security
mode's own PMF setting), `vlan_id` = 6, `hidden` = 7, `enabled` = 8
(required), `repeated WifiBand bands` = 9 (items `defined_only`,
`not_in: [0]`, `unique`; empty means every band the serving radios run),
and the CEL rule `wlan_config.pmf_matches_security`
(`!has(this.pmf) || ((this.security in [5, 8, 10]) ? this.pmf == 3 : ((this.security in [6, 9]) ? this.pmf == 2 : true))`).
`WlanState`: `ref` = 1, `status` = 2 (required, `defined_only`,
`not_in: [0]`), fields 3 to 9 as in Config without `required`, without
enum rules or `unique` on `bands`, without the PMF rule, and with `ssid`
bounded 0 to 32,
`repeated WlanBroadcast broadcasts` = 10, and the CEL rule
`wlan_state.status_matches_broadcasts`
(`!has(this.status) || (this.status == 1 ? this.broadcasts.size() > 0 : this.broadcasts.size() == 0)`).
`WlanEvent`: `ref` = 1, `from` = 2 (`defined_only`, `not_in: [0]`), `to`
= 3 (required, same), CEL `wlan_event.status_changes`
(`!has(this.from) || this.from != this.to`). Comments follow the
contract-only rule, refs get one line, required fields say "Must be
present.", and nothing names a tenant. The file-level comment names none
of the triad as absent, because all five members exist. The new README
states the family, imports (`model/inventory`, `net/addr`,
`net/switching`, `net/wlan`), and that Wlan stays out of `EntityType`
until its store lands. READMEs: `model/` adds `net/switching` to Imports
and lists `wireless/v1/`; inventory, `addr`, `switching`, and `wlan` add
`model/wireless` to Imported by; `net/README.md` adds `model/wireless` to
Imported by. The conventions doc adds `Wlan` in `model/wireless/v1` to
the landed entities outside the enum; `CONCEPTS.md` adds a Wlan entry
under Inventory.
Tests: `model_wireless_rules_test.go` covers Requirements 9 and 10, plus:
`WPA3_PERSONAL` with `pmf` absent passes; `pmf` of 0 or 99 fails;
`WlanConfig` without `ssid`, with an empty `ssid`, with `security`
unspecified, or with a duplicate band fails; `WlanEvent` with `from ==
to` fails and with `to` absent fails; `WlanBroadcast` without `bssid`
fails; a valid Config, State (broadcasting, one broadcast), and Event
pass. Requirement 4 is proven by the hook command under Verification,
which the implementer runs once `wlan.proto` exists.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/model/wireless/v1 spec/proto/flowseer/model/README.md spec/proto/flowseer/model/inventory/v1/README.md spec/proto/flowseer/net/addr/v1/README.md spec/proto/flowseer/net/switching/v1/README.md spec/proto/flowseer/net/wlan/v1/README.md spec/proto/flowseer/net/README.md docs/conventions/protobuf.md CONCEPTS.md test/conformance/proto/model_wireless_rules_test.go generated/go/proto/flowseer/model/wireless/v1`

Waves: U1 | U2 U3 | U4

U4 waits for U3 because both edit the same README lines and docs; its
code needs only U1 and the landed `ComponentGlobalRef`.

## Verification

```bash
buf lint
buf generate && git status --porcelain generated/   # empty after the commit
go build ./... && go vet ./test/conformance/...
go test -race ./test/conformance/proto/...
printf '{"cwd":"%s","tool_input":{"file_path":"%s/spec/proto/flowseer/model/wireless/v1/wlan.proto"}}' "$PWD" "$PWD" \
  | tools/hooks/proto-check.sh 2>&1 | grep -c 'Message sync' || true   # prints 0
.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net spec/proto/flowseer/model docs/code-style-proto.md docs/conventions/protobuf.md docs/architecture/2026-09-25-schema-building-blocks-direction.md CONCEPTS.md test/conformance/proto generated/go/proto/flowseer/net/wlan/v1 generated/go/proto/flowseer/model/inventory/v1 generated/go/proto/flowseer/model/wireless/v1
```

The verifier runs over the union of changed paths, never `--full`: a full
run builds and race-tests `generated/go/yang` and exhausts host memory.
The parent's `--full` line is replaced by this targeted run for the same
reason phase 1 recorded.

## Definition of done

- [ ] Verifier green for every changed path (targeted, as above).
- [ ] `spec/proto/flowseer/net/wlan/v1/` and
      `spec/proto/flowseer/model/wireless/v1/` exist with `.proto` files
      and a README; `buf lint` passes; `generated/` matches.
- [ ] Requirements 1 to 11 pass in `test/conformance/proto`.
- [ ] The hook command prints no "Message sync" line for `wlan.proto`.
- [ ] Package READMEs, the `net/` and `model/` root READMEs,
      `docs/code-style-proto.md`, `docs/conventions/protobuf.md`, the
      record's rule 5 sentence, and `CONCEPTS.md` match the tree.
- [ ] This plan's `status` is `implemented` with an outcome note under its
      title, the parent's U3 `Landed:` line carries the commit range, and
      no plan label appears in code, comments, or commit messages.

## Open questions

None.

- Review (accept) left two style-only notes, no defect: the
  `net/wlan/v1/README.md` "Deliberately absent" line says "bitmasks" where the
  design calls the deferred raw AKM/cipher pass-throughs "enums" (trivial
  wording drift), and the Ruckus fixture maps `isRadioEnabled` to `oper_status`
  (an explicit U2 decision quoting the vendor comment; test-local). Non-blocking
  coverage gap: no conformance case exercises `beacon_interval` at its upper
  bound, `antenna_gain_millidbi`, `standard`/`admin_status`, or a `RadioFacet`
  carrying `bsses` — none is a plan requirement. For compound to weigh.
