# RF Information and Radio/AP Operational Telemetry — Research Dossier

Scope: per-radio state, channel utilization, noise/RSSI/SNR family, retry/error
rates, airtime, DFS/RRM, spectrum analysis, neighbor/rogue scan results, AP
identity and status. Excludes 802.11 standards enums (separate dossier) and
client/station records (separate dossier).

## 1. Scope and sources

Repo conventions read: `docs/conventions/protobuf.md`,
`docs/code-style-proto.md`,
`docs/architecture/2026-08-20-network-model-structure-direction.md` (fixes
`net/wlan/v1` as a reserved peer of `net/switching`, holding `Radio`, `Bss`,
`WirelessClient`, importing `net/addr` and `net/switching`, never the
reverse). Existing `net/phy/v1` precedent read directly:
`spec/proto/flowseer/net/phy/v1/optical_power.proto` (linear-unit-on-wire
pattern with ordered thresholds), `poe_status.proto` (registry-worded,
FlowSeer-numbered enum pattern).

Vendored corpus fetched this session (repo paths, with line numbers where a
fact is cited below):
- `spec/proto/ruckus/ap/ap_status.proto` (APStatusRadio, lines 929–1330+)
- `spec/proto/ruckus/ap/ap_report.proto` (APReportBinRadio, lines 730–1000;
  per-client radio fields 1356–2060)
- `spec/proto/ruckus/ap/ap_rogue.proto` (ReportType/RogueType, lines 1–230)
- `spec/proto/ruckus/ap/ap_mesh.proto` (mesh uplink/downlink RSSI, lines 41,
  107, 166, 358–368)
- `spec/proto/ruckus/ap/ap_common.proto` (checked for a shared band/radio
  enum: none exists — confirmed empty grep)
- `spec/mib/ieee/IEEE802dot11-MIB` (`dot11PhyTxPowerTable`, lines 1634–1736)
- `spec/mib/ietf/CAPWAP-BASE-MIB` (`CapwapBaseRadioIdTC`, lines 99–104;
  WTP/radio structure, lines 900–1260)
- `spec/yang/cisco/iosxe/2611/Cisco-IOS-XE-wireless-rrm-oper.yang` (RSSI/SNR/
  Tx-power/utilization/noise leaves, lines 386–1130)
- `spec/yang/cisco/iosxe/2611/Cisco-IOS-XE-wireless-radio-cfg.yang`,
  `Cisco-IOS-XE-wireless-rf-cfg.yang`, `Cisco-IOS-XE-wireless-dot11-cfg.yang`
  (units statements, grepped)
- `spec/yang/cisco/iosxe/2611/Cisco-IOS-XE-wireless-rrm-types.yang`
  (`pmac-dev-id-*` CleanAir interferer classification enum, lines 173–293;
  `rrm-phy-*` band enum, lines 92–107)
- `spec/openapi/ubiquiti/unifi-network-openapi-v10.4.57.json` (schemas
  "Wireless radio overview", "Latest statistics for wireless radio",
  "Latest statistics for device interfaces")
- `spec/openapi/ruckus/vsz/vsz-7.1.1-v13_1-openapi.json` (`ap_apOperationalSummary`,
  `ap_neighborAPList` definitions)

Web sources fetched this session:
- Cisco Meraki Dashboard API v1 docs: `get-network-wireless-channel-utilization-history`,
  `get-device-wireless-radio-settings`, `get-network-wireless-air-marshal`,
  `get-network-wireless-rf-profile` — https://developer.cisco.com/meraki/api-v1/
- OpenConfig `openconfig-wifi-phy.yang` v1.4.1, raw file —
  https://raw.githubusercontent.com/openconfig/public/master/release/models/wifi/openconfig-wifi-phy.yang
- Aruba Central / Central on-prem docs (RF tab, noise/utilization/SNR
  definitions) — https://help.centralon-prem.arubanetworks.com/2.5.4/...ap-rf.htm,
  https://arubanetworking.hpe.com/techdocs/central/latest/...
- Juniper Mist docs and API-class reference (radio_stat shape, indirect —
  **unverified in detail**, see Traps) —
  https://api-class.mist.com/rest/read/monitoring/get_devices/,
  https://www.juniper.net/documentation/us/en/software/mist/mist-analytics/...

Not reached this session (mark **unverified**, flagged for the planner):
Juniper Mist exact `radio_stat` JSON field list (site search only, no
authoritative page fetched with full schema); TP-Link Omada OpenAPI radio
schema (no public machine-readable spec found, only support-doc mentions of
`txPower`/`channelWidth`); RUCKUS One (cloud) API exact JSON schema (only
narrative docs found, no OpenAPI); HPE Aruba Central/New Central exact REST
JSON field names (only the web-UI doc's metric definitions, not the API
response shape).

## 2. Standards facts

- **IEEE802dot11-MIB** `dot11PhyTxPowerTable` (`spec/mib/ieee/IEEE802dot11-MIB:1634-1736`):
  `dot11TxPowerLevel1..8` are `INTEGER (0..10000)`, described as "transmit
  output power for LEVELn **in mW**" — not dBm. `dot11CurrentTxPowerLevel`
  indexes into this table. This MIB predates 802.11k/n and has no RCPI/RSNI,
  no per-radio channel-utilization objects, and no chain/spatial-stream
  counts.
- **CAPWAP-BASE-MIB** (`spec/mib/ietf/CAPWAP-BASE-MIB`): `CapwapBaseRadioIdTC`
  is `Unsigned32 (1..31)` (line 99-104) — the WTP-local radio index CAPWAP
  uses to key everything else (`capwapBaseWirelessBindingRadioId`,
  `capwapBaseStationEntry`). `capwapBaseWtpRadiosInUseNum` /
  `capwapBaseWtpRadioNumLimit` are `Unsigned32 (0..255)` (lines 1016-1031).
  No RF measurement objects (no RSSI/power/noise) in this base MIB — those
  live in vendor WTP MIBs (Aruba WLSX-WLAN-MIB, Ruckus RUCKUS-*-WLAN-MIB,
  Huawei HUAWEI-WLAN-AP-RADIO-MIB — present in the vendored corpus but not
  read this session; flagged for a follow-up pass if the planner wants
  MIB-sourced RF values beyond what Cisco YANG and Ruckus GPB already cover).
- **OpenConfig `openconfig-wifi-phy.yang` v1.4.1**
  (fetched from openconfig/public master):
  - `channel`: `uint8`, range 1..233 (covers 6 GHz channel numbering), no
    `units` statement.
  - `channel-width` (deprecated) and `channel-bandwidth` (current): both
    with `units "MHz"`.
  - `transmit-power`: `int8`, `units "dBm"`.
  - `transmit-eirp`, `allowed-max-eirp`, `allowed-max-txpower`: `units "dBm"`.
  - `rssi`, `noise-floor`: `int8`, **no explicit `units` statement** in the
    leaf (dBm is implied by convention and by every consumer's use, but the
    schema itself does not say so — the same silent-convention gap FlowSeer
    should not repeat).
  - `total-channel-utilization`, `rx-dot11-channel-utilization`,
    `tx-dot11-channel-utilization`, `obss-rx`: OpenConfig `percentage` type
    (0–100 scale).
  - `antenna-gain`: `int8`, no unit stated (industry convention dBi).
  - No spatial-stream/chain-count leaf found in this pass.
- **Cisco IOS-XE wireless YANG** (`Cisco-IOS-XE-wireless-{rf,radio,dot11,rrm}-cfg/oper.yang`):
  every power leaf states `units "dBm"` explicitly (`tx-power-min`,
  `tx-power-max`, `transmit-power-level`, `trap-threshold-noise`,
  `coverage-data/voice-packet-rssi-threshold`, `band-select-client-rssi`,
  `cell-handoff-rssi-thold`, `opt-roam-rssi-treshold`, `max-tx-power-level`,
  OBSS-PD thresholds) — always plain integer dBm, never mW, never a
  fractional/milli-dBm unit. Every utilization leaf states
  `units "percentage"` (foreign-AP interference utilization, RX/TX
  utilization, noise-channel-utilization, FRA utilization thresholds).
  `antenna-gain` uses `units "dBi"` (`Cisco-IOS-XE-wireless-radio-cfg.yang:259`).
  CleanAir spectrum-analysis interferer classification is a closed enum
  `pmac-dev-id-*` in `Cisco-IOS-XE-wireless-rrm-types.yang:173-293`: `bt`
  (Bluetooth), `mwave` (microwave oven), `fh` (frequency hopper), `bti`,
  `gtdd`, `jam` (jammer), `wform` (generic waveform), `dect`, `video`,
  `zigbee`, `wifi-norm`/`wifi-iq`/`wifi-chan`/`wifi-sup-g` (non-standard
  Wi-Fi variants), `radar`, `canopy`, `xbox`, `wmxm`/`wmxf` (WiMAX mobile/
  fixed), `exalt`, `ibeacon`, `aci` (adjacent-channel interference),
  `undef`/`unknown`. This is the standards-adjacent (Cisco-registry, not
  IEEE) vocabulary for "interferer type" in the domain brief.
- **DFS/radar**: Cisco YANG models it as a list of radar-detection events per
  radio/channel (`st-rrm-radio-radar-info`: `channel`,
  `radar-detected-timestamp`), not as a channel-change-reason enum; no
  channel-change-reason enum was found in the vendored 2611 YANG set in this
  pass (**unverified** whether one exists elsewhere in the module — the
  general RRM oper model instead exposes `radar-info` lists plus separate
  channel/txpower leaves that a consumer diffs over time to infer a change).
- **RCPI / RSNI (802.11k)**: not present in the vendored IEEE802dot11-MIB
  (too old) nor found in the OpenConfig or Cisco YANG leaves read this
  session. **Unverified** in this corpus; the domain brief's RCPI/RSNI ask is
  not grounded in a vendored or fetched primary source this session — flag
  as an open question rather than fabricate a shape.

## 3. Provider data matrix

| Concept | Meraki Dashboard v1 | Mist (unverified detail) | Aruba Central (web-doc only) | UniFi Network Integration API | RUCKUS SmartZone (vsz OpenAPI) | Ruckus GPB (`ap_status`/`ap_report`) | Cisco IOS-XE YANG | OpenConfig wifi-phy |
|---|---|---|---|---|---|---|---|---|
| Channel | `twoFourGhzSettings.channel` int (1-14), `fiveGhzSettings.channel` int (36-165 enumerated) | present, exact field name unverified | shown in RF tab, API shape unverified | `channel` int32 (radio overview) | `wifi24Channel`/`wifi50Channel`/`wifi6gChannel` — **strings**, not ints | `channel` int32 (`APStatusRadio.channel`, `ap_status.proto:943`) | `channel-number` leaf (RRM oper) | `channel` uint8 1..233 |
| Channel width | `fiveGhzSettings.channelWidth` int (0/20/40/80/160, "presumed MHz") | unverified | shown in RF tab | `channelWidthMHz` int32 | not modeled explicitly in the summary schema read | `channelWidth` uint32 (`ap_status.proto:1111`), `channelWidthGroup` int32 | `channel-width`/`channel-bandwidth`, units MHz | `channel-bandwidth`, units MHz |
| Tx power | RF profile `min/maxPower` int, **units "dBm"**, range 2–30; per-device `targetPower` int (unit unstated in schema text, same profile convention) | present, unverified units | not confirmed | not present in Integration API schema read (only channel/width/standard/retry-pct) | not in summary schema | `txPower` **string** (`ap_status.proto:971`), `actualTxPower`/`calibrationTxPower`/`maxTxPower` int32 (`:1300-1314`), `hdTxPower` string, "Absolute management TX Power in dBm" (comment only, `:1327`) | `transmit-power-level`, `tx-power-min/max`, units dBm | `transmit-power` int8, units dBm |
| EIRP | not found in fetched pages | unverified | not confirmed | not present | not in summary schema | `eirp` int32 = "tx_power + antenna gain" (`:1220-1223`) | `transmit-eirp`, units dBm (openconfig deviation import present but not this file) | `transmit-eirp`, units dBm |
| Antenna gain | not found | unverified | not confirmed | not present | not present | not present (no antenna-gain field in the radio message read) | `antenna-gain`, units dBi | `antenna-gain`, no stated unit |
| Chains / spatial streams | not found | unverified | not confirmed | not present | not present | `chainmask` **string** (`:1258`, bitmask-as-text, not a count) | not found in this pass | not found in this pass |
| Noise floor | not found in fetched pages (present in RF-profile-adjacent "signal quality" reports, not confirmed) | "average noise floor... per band" (narrative only) | "Noise Floor (dBm)" (web-doc metric name) | not present | not present | `noiseFloor` int32 (`ap_status.proto:992`, `ap_report.proto:807`), AVG-aggregated | `noise` leaf, units dBm (`rrm-oper.yang:603-605`) | `noise-floor` int8, no stated unit |
| Channel utilization split | `utilizationTotal`/`utilization80211`/`utilizationNon80211`, floats, 0-100% | "traffic, background noise, interference sources" (narrative) | "Utilization (%)" and "Interference (%)" separately | `txRetriesPct` only (no busy/rx/tx/self split) | not present | `total`/`busy`/`rx`/`tx` uint32 "exponential average" (`:1080-1104`); `APReportBinRadio.airtime`/`airtimeB`/`airtimeRx`/`airtimeTx` uint32 (`ap_report.proto:741-765`) | `rx-utilization`/`tx-utilization`/`rx-noise-channel-utilization`/foreign-AP-interference-utilization, all `units "percentage"` | `total-channel-utilization`/`rx-`/`tx-dot11-channel-utilization`/`obss-rx`, `percentage` type |
| RSSI | Air Marshal `detectedBy[].rssi` (unit unstated in fetched text, industry dBm) | present, unverified | "coverage RSSI in dBm" | not present | mesh-neighbor `signal` — **string** | `rssi` **int32** on client/report rows (`ap_report.proto:1457`) but **`rssi` `uint32`** on rogue peer rows (`ap_rogue.proto:55`) — inconsistent sign convention across the same vendor's own schemas | `rssi` leaf, units dBm (`rrm-oper.yang:386-388`) | `rssi` int8, no stated unit |
| SNR | not found | unverified | "SNR... delta between noise dBm and RSSI dBm", units dB | not present | not present | `snr` float (`ap_report.proto:2053`, "not implemented yet") | `snr` leaf, units dB (`rrm-oper.yang:392-396`) | not found |
| Retry rate | not found in fetched pages | unverified | not confirmed | `txRetriesPct` double (percent, unbounded format) | not present | `retry` uint64 count (`ap_status.proto:1055`), `txRetryRate` float (`ap_report.proto:989`) | not found explicitly as a leaf in this pass | not found |
| Radio admin/oper state | `enabled` boolean on radio settings (not confirmed in this pass) | unverified | unverified | not present in radio-overview schema (device-level status elsewhere) | AP-level `administrativeState` enum `Locked`/`Unlocked` (`vsz` def), `connectionState` free string ("Discovery","Connect","Rebooting","Disconnect","Provisioned") | `isRadioEnabled` bool (`ap_status.proto:1209`) | admin/oper leaves exist in `ap-cfg`/`ap-oper` (not read field-by-field this pass) | not found in this pass |
| Radio mode (access/monitor/sensor/mesh) | not confirmed | unverified | unverified | `wlanStandard` enum is the 802.11 PHY generation, not the radio role | `meshRole` enum `Disabled`/`Root`/`Map`/`eMap`/`Down`/`Undefined` | `radioMode`/`ap80211RadioMode` **strings** ("b/g/n" etc — PHY generation, not role); no access/monitor/sensor role enum found | `rrm-phy-80211b/a/xor/6ghz` are PHY-band enums, not a role enum | not found |
| Mesh role/hop | not confirmed | unverified | unverified | not present | `meshRole` enum, `meshHop` integer | `ap_mesh.proto` carries uplink/downlink RSSI and channel per mesh link, no role enum | dedicated `mesh-*` YANG modules exist (not read field-by-field) | not present |
| Neighbor/rogue scan | Air Marshal: `ssid`, `bssids[]`, `channels[]`, `detectedBy[].{device,rssi}`, `types` (`rogue`\|`spoof`), `encryption` (WEP/WPA/open/null), `contained` bool, `firstSeen`/`lastSeen` epoch, `manufacturers` (OUI-resolved) | unverified | unverified | not present | `ap_neighborAPList`: `mac`,`name`,`zoneName`,`ip`,`channel` (string), `signal` (string), `connectionState` (string) — mesh-neighbor only, not a general rogue/air scan | `ap_rogue.proto` `ReportType`: `rogueMac`, `rssi` (uint32), `encryption`/`encrypt_type`/`auth_type` (strings), `radio` (string), `channel` (uint32), `ssid`, `type` (**free string** classification, not the closed `RogueType` enum — that enum is only DISCOVERY/UPDATE/DISAPPEAR, i.e. the *event* kind, not the *classification*) | Cisco has a dedicated `rogue-cfg`/`rogue-oper`/`rogue-types` YANG triad (not read field-by-field this pass) | not present |
| AP identity | `serial` (path param, not a body field in pages read) | unverified | unverified | UniFi has separate device schemas (not read this pass) | `serial`, `model`, `name`, `mac`, `version` (firmware), `countryCode`, `zoneId`/`apGroupId`, `cpId`/`dpId` (control/data-plane cluster), lat/long/altitude/location | AP mac carried per-row (`ap` field, string MAC) rather than a separate identity message in these files | Cisco AP identity lives in `ap-global-oper`/`ap-cfg` (wtp-mac, ethernet-mac, model, serial fields present per earlier grep) | not present (out of scope for this module) |
| PoE draw | not found | unverified | unverified | not present in radio schema (device-level power elsewhere) | not in AP operational summary read | not found in `ap_status`/`ap_report` radio messages | AP PoE is a separate `Cisco-IOS-XE-wireless-ap-cfg` concern, not read this pass | not present |

**Core across providers** (near-universal, safe to normalize): channel
number, channel width (MHz), tx power in dBm (Meraki, Cisco YANG, OpenConfig
all agree on dBm as the *documented* unit even where Ruckus GPB is
inconsistent), noise floor (dBm), RSSI (dBm, sign convention aside),
channel-utilization-as-percentage (every provider that reports it uses
0–100), AP identity by serial+MAC+model+firmware version.

**Niche / vendor-specific**: EIRP as a derived field (only Ruckus GPB and
OpenConfig expose it explicitly as its own field rather than "power + gain
computed downstream"), antenna gain as its own field (OpenConfig, Cisco YANG
only), chain/spatial-stream counts (not found as a first-class field
anywhere in this pass — likely derived from the negotiated PHY rate/MCS, not
reported standalone), CleanAir-style interferer classification (Cisco-only
vocabulary; other vendors report "spectrum" as a boolean feature or a
severity score, not a typed interferer enum, in what was fetched).

## 4. Proposed primitives

Package: `flowseer/net/wlan/v1` (already reserved by the 2026-08-20
direction; imports `net/addr` for BSSID/MAC and `net/switching` for the VLAN
a BSS maps to; never imported back by either).

- **`RadioBand`** (normalized enum, `_UNSPECIFIED = 0`): `2_4GHZ`, `5GHZ`,
  `6GHZ`, `60GHZ` — grounded in UniFi's `frequencyGHz` enum `{2.4,5,6,60}`
  and Cisco's `rrm-phy-80211b/a/6ghz` band split. Frequency-in-GHz-as-enum
  (UniFi) is rejected as a wire type; band is a closed normalized set,
  frequency in MHz is a separate scalar (see unit recommendation).
- **`RadioMode`** (normalized enum): `ACCESS`, `MONITOR`, `SENSOR`, `MESH` —
  **no provider in this corpus modeled this as a field this session**
  (Ruckus/Meraki/Cisco all conflate "radio mode" with the negotiated 802.11
  PHY generation string, e.g. "b/g/n"). This enum is FlowSeer-normalized
  from the domain brief, not vendor-sourced; flag to the planner as
  needing corroboration from a live device or a provider page not reached
  this session (Meraki has separate `monitorMode`-style settings on some
  endpoints not fetched).
- **`RadioAdminState`** / **`RadioOperState`**: reuse the shape of
  `flowseer.net.interface.v1.AdminStatus`/`OperStatus` rather than inventing
  new enums — a radio's admin/oper split is structurally identical to an
  interface's. Ruckus's `isRadioEnabled` bool and SmartZone's
  `administrativeState` (`Locked`/`Unlocked`) both collapse to this pair.
- **`Radio`** message (primitive, embedded by a device/component's State,
  not a ref-carrying entity): `radio_id` (device-local index — CAPWAP's
  `CapwapBaseRadioIdTC` is `1..31`, so a `uint32` with a predefined-rule
  bound, not a raw `uint32`), `band` (`RadioBand`), `channel_number`
  (`uint32`, OpenConfig bounds it 1..233 to cover 6 GHz — reuse that bound
  as a predefined rule), `channel_width_mhz` (`uint32`; every provider that
  states a unit says MHz), `admin_state`/`oper_state`, `mode` (`RadioMode`),
  `tx_power_dbm` (`sint32` — **not** a wrapper; dBm is legitimately negative
  for some reported quantities and the *setting* is always given as a plain
  signed integer by every provider that documents a unit), `eirp_dbm`
  (`sint32`, absent when not reported — only Ruckus and OpenConfig expose
  it), `antenna_gain_dbi` (`sint32`, absent when not reported), `noise_floor_dbm`
  (`sint32`), `chains` (absent from every provider read this session — do
  not add a field with no grounded source; open question below).
- **`ChannelUtilization`** message: `total_percent`, `self_tx_percent`,
  `self_rx_percent`, `other_bss_percent` (OpenConfig's `obss-rx` +
  Cisco's foreign-AP-interference-utilization), `non_wifi_percent` (Cisco's
  `rx-noise-channel-utilization`) — all `uint32` 0–100 with a shared
  predefined percent rule (see unit recommendation), because every provider
  that reports this splits it as a percentage, never a raw busy-time
  duration.
- **`RfSignalQuality`** message, embedded wherever a peer signal is
  reported (mesh link, neighbor scan, client association — this dossier
  covers the AP/scan side; the client dossier owns the client-association
  use): `rssi_dbm` (`sint32`), `snr_db` (`sint32`, absent when not derived —
  Cisco states `snr = noise(dBm) - rssi(dBm)`, so a consumer can always
  derive it if both operands are present; storing it too is still
  worthwhile because several providers report it directly and the raw pair
  is not always both present). RCPI/RSNI are **not** added — no grounded
  source this session (see Traps/Open questions); do not invent a
  0.5 dB-per-step field without a cited clause.
- **`SpectrumInterfererType`** (registry pass-through enum numbered by
  Cisco's `pmac-dev-id-*` bit position, the way `MauLinkMode` follows
  IANA-MAU-MIB): `SPECTRUM_INTERFERER_TYPE_UNKNOWN = 0` matching
  `pmac-dev-unknown`, then `BLUETOOTH`, `MICROWAVE_OVEN`, `FREQUENCY_HOPPER`,
  `CONTINUOUS_TRANSMITTER` (`bti`/`gtdd`/`jam`/`wform` family — needs the
  planner to decide how finely to split these four Cisco-specific
  sub-kinds), `DECT_PHONE`, `VIDEO_CAMERA`, `ZIGBEE`, `NON_STANDARD_WIFI`,
  `RADAR`, `CANOPY`, `XBOX`, `WIMAX`, `ANALOG_PHONE` (`exalt`/other), `BLE`
  (`ibeacon`), `ADJACENT_CHANNEL_INTERFERENCE`, and an `OTHER` catch-all —
  this is a normalized enum (FlowSeer's own numbering), not a registry
  pass-through, since Cisco's own numbering is proto2-legacy internal and
  not a public registry; mark it FlowSeer-normalized, `_UNSPECIFIED = 0`.
- **`DfsRadarEvent`** message (state row, not a primitive struct on Radio):
  `channel_number`, `detected_at` (timestamp) — Cisco's shape
  (`st-rrm-radio-radar-info`) is a list of these per radio, not a single
  "last radar" scalar plus a boolean; follow that shape rather than
  flattening to `last_radar_detected_at`.
- **`Bss`** (already named in the reserved package; not re-specified here —
  owned by the peer dossier covering clients/BSS if the split assigns it
  there, otherwise: `bssid` (MAC), `ssid`, `vlan_id` (uses `net/switching`'s
  vlan_id predefined rule), `security` — the RF dossier's contribution is
  that a BSS's `security` needs a **typed-variant oneof** (open/PSK/802.1X/
  SAE/OWE), not the free-text `encryption`/`auth_type` strings every vendor
  API in this corpus actually returns; the mapper does the string→enum
  classification, the schema stays closed.
- **`NeighborScanResult`** / **`RogueApSighting`** table row (device-scoped
  table, like `FdbEntry`, hanging off the AP's State — not embedded in
  `Radio`): `bssid`, `ssid` (absent when hidden), `channel_number`,
  `rssi_dbm`, `security` (same typed variant as `Bss`, best-effort from the
  scan), `classification` (normalized enum `ROGUE`/`NEIGHBOR`/`FRIENDLY`/
  `UNCLASSIFIED` — grounded in Meraki's `rogue`/`spoof` two-value set plus
  the narrative "known neighbor vs malicious rogue" language found for
  RUCKUS and SmartZone's `modifyRogueType` endpoint, but note every vendor's
  *wire* representation in this corpus is a free string or a two-value
  set, never this four-value taxonomy — the planner should decide whether
  four values earns its keep over reusing Meraki's two plus an
  "unclassified" default), `first_seen_at`/`last_seen_at`.

**Reuse, not new**: `AdminStatus`/`OperStatus` from `net/interface/v1` for
radio admin/oper (structurally identical, see above); the `vlan_id`
predefined rule from `net/switching/v1` for a BSS's VLAN; the
typed-variant-oneof pattern from `net/addr/v1` for `Bss.security`;
`OpticalPower`'s ordered-thresholds CEL pattern as the template for any
future `ChannelUtilization` cross-field rule (e.g. `self_tx + self_rx +
other_bss + non_wifi <= total`, if the planner wants it validated — Ruckus's
own `total`/`busy`/`rx`/`tx` are independent exponential averages that do
not sum cleanly, so **do not** add a strict-sum CEL rule without checking
live data first).

## 5. Entity candidates (`model/`)

RF telemetry itself is state, not an entity — it hangs off whatever entity
owns the radio. The open architectural question (not resolved by this
research) is *what that entity is*:

- **If an AP is modeled as a `model/inventory/v1` `Device`** (the current
  landed shape for every other network element), then `Radio` is a
  repeated field or table on `DeviceState`, keyed by `radio_id`, and AP
  identity fields (serial, model, firmware, uplink, mesh role, cluster)
  reuse `Device`'s existing identity/placement fields rather than
  duplicating them — RUCKUS SmartZone's `ap_apOperationalSummary`
  (`serial`, `model`, `version`, `zoneId`/`apGroupId`, `cpId`/`dpId`) maps
  almost one-to-one onto `Device` + `IntegrationScope` + a cluster
  reference, which argues for reuse over a new `AccessPoint` entity.
- **If an AP needs its own entity** (because mesh role, PoE draw, and
  radio count are meaningfully different from a switch's Device shape),
  that is a new `model/` family this dossier does not have grounds to
  design — no primary source in this corpus draws that line for FlowSeer
  specifically; it is a planner decision, not a research finding.
- **`Radio`, `Bss`, `NeighborScanResult`/`RogueApSighting`**: primitives and
  tables, ref-free, living in `net/wlan/v1` per the accepted direction —
  no entity, no Config/State/Event triad of their own. A `RadioConfig`/
  `RadioState`/`RadioEvent` triad only makes sense once the owning entity
  (Device today, or a future AP entity) is settled, because the triad's
  "owning parent" is that entity, not the radio itself.
- **DFS radar and rogue/neighbor sightings as events**: a radar detection
  or a rogue-AP discovery/disappearance (Ruckus's own `RogueType`
  DISCOVERY/UPDATE/DISAPPEAR enum already models this as a transition) is a
  natural `<Something>Event` on whichever entity owns radio state — this
  reinforces putting them on the AP-owning entity's Event stream rather
  than inventing a standalone event package.

## 6. Traps

- **dBm vs mW**: IEEE802dot11-MIB's own `dot11TxPowerLevel*` is in **mW**,
  while every modern schema fetched (Meraki RF profile, Cisco YANG,
  OpenConfig) documents **dBm**. A mapper reading the legacy MIB and a
  mapper reading Cisco YANG for the same physical quantity must not be
  merged blindly — mirrors the `net/phy` optical-power precedent
  (`optical_power.proto`'s own comment: SFF-8472 is linear, dBm is
  derived) and argues for the same policy here: **carry dBm as the wire
  unit for RF power fields** (every current-generation source already
  speaks dBm; mW only appears in one 2002-era MIB), and if a mapper ever
  has to convert from a raw mW value, do the mW→dBm conversion once in
  that mapper, not in the schema.
- **RSSI sign inconsistency inside one vendor**: Ruckus's own GPB has
  `rssi` as `int32` in `ap_report.proto` client rows but `uint32` in
  `ap_rogue.proto` peer rows (`ap_rogue.proto:55`) — the same physical
  quantity, two wire types, in the same vendor's schema family. A FlowSeer
  primitive must pick one signed type (`sint32`) and let the mapper handle
  whichever convention (magnitude-only vs. signed dBm) the source uses;
  never let the wire type vary by which endpoint the value came from.
- **String-typed enums where the vendor clearly has a closed set**: Ruckus
  GPB's `radioMode`, `ap80211RadioMode`, `radio` (band), `type`
  (rogue classification), `encryption`/`encrypt_type`/`auth_type`, and
  SmartZone's `channel` (per-band, as a string) and `signal` (as a string)
  are all free-text strings on the wire despite having small, effectively
  closed value sets. Do not let the vendor's laziness leak into FlowSeer's
  schema as a `string` field "because that's what the source sends" — the
  mapper classifies into a normalized enum, exactly as
  `docs/conventions/protobuf.md`'s registry-pass-through-vs-normalized
  split intends. But also do not invent enum members with no
  corroborating value list — several of the exact string values (e.g. what
  SmartZone's `channel` string actually contains beyond a bare channel
  number) were not enumerated in the corpus reached this session.
- **`RogueType` names an event kind, not a classification**: it is tempting
  to reuse Ruckus's `RogueType` enum name for the rogue/neighbor/friendly
  *classification* the domain brief asks for — its three values
  (DISCOVERY/UPDATE/DISAPPEAR) are actually the sighting's lifecycle
  state, unrelated to threat classification. Do not conflate the two; the
  classification concept needs its own enum, sourced from Meraki's
  `types: rogue|spoof` and vendor UI language, not from this Ruckus name.
- **Percent vs "exponential average" are not the same measurement
  semantics**: Ruckus's `APStatusRadio.total`/`busy`/`rx`/`tx` are
  explicitly commented "Exponential average of..." (`ap_status.proto:1080-1104`)
  — a smoothed value, not an instantaneous sample like Meraki's
  `utilizationTotal`/`utilization80211`/`utilizationNon80211` for a fixed
  time window. Both are legitimately "percent 0-100", but a consumer
  comparing them across providers without knowing the averaging window
  will draw wrong conclusions. The schema should carry the value; whether
  to also carry an "averaging window" or "sample kind" hint is an open
  question (see §7) rather than something this dossier can settle from the
  corpus alone.
- **EIRP is derived, not always separately reported**: Ruckus's own comment
  defines `eirp = tx_power + antenna_gain` (`ap_status.proto:1220-1223`).
  Storing `eirp_dbm` as an independent field (rather than always computing
  it downstream) is only worth doing because some providers *report* it
  directly without separately reporting antenna gain (so it cannot always
  be recomputed) — but a consumer must not assume
  `eirp_dbm == tx_power_dbm + antenna_gain_dbi` holds byte-for-byte across
  every source; some regulatory-domain caps clip it.
- **Frequency vs channel vs band are three different axes** that UniFi's
  schema (`frequencyGHz` as an enum of `{2.4,5,6,60}`, separate from
  `channel` as an integer, separate from `wlanStandard` as the PHY
  generation) keeps orthogonal; Ruckus's `radioMode` string conflates band
  and PHY generation ("b/g/n" says both "2.4 GHz" and "supports 802.11b/g/n"
  in one token). FlowSeer's `RadioBand` enum plus a separate `channel_number`
  plus (out of this dossier's scope) an 802.11-standard enum from the
  standards dossier must not be collapsed back into one Ruckus-style
  string field.

## 7. Open questions for the planner

1. **AP as Device vs. a new entity.** Does FlowSeer model an access point as
   a `model/inventory/v1` `Device` (reusing identity/placement/PoE) or does
   it need its own entity family? This dossier found strong field-level
   overlap (serial, model, firmware, PoE, uplink) but no source that
   settles the FlowSeer-specific design choice.
2. **`RadioMode` (access/monitor/sensor/mesh) has no vendor-sourced field in
   this corpus.** Confirm against a live device or a not-yet-fetched
   provider page before landing it, or drop it from v1 and add it when a
   source is found.
3. **RCPI/RSNI (802.11k)**: no grounded source in the vendored corpus or
   the pages fetched this session. Either scope them out of v1 RF
   primitives or assign a follow-up fetch of the 802.11k amendment / a
   provider that reports them (Cisco 9800 client-oper YANG is the most
   likely candidate and was not read field-by-field this session).
4. **Chains / spatial streams**: no provider in this pass reports a
   standalone chain or spatial-stream count field. Confirm whether any
   fetched-but-not-read page (Cisco `ap-oper.yang`, Aruba Central RF tab)
   actually has one before adding the field the domain brief asks for.
5. **Channel-utilization averaging semantics**: should `ChannelUtilization`
   carry a "sample kind" (instantaneous vs. exponential-average) hint, given
   Ruckus and Meraki measure genuinely different things under the same
   percent-0-100 shape? Affects whether cross-provider utilization values
   are ever safely comparable.
6. **Rogue/neighbor classification granularity**: two values (Meraki-style
   `rogue`/`spoof`) vs. four (`ROGUE`/`NEIGHBOR`/`FRIENDLY`/`UNCLASSIFIED`,
   closer to RUCKUS's admin-facing language) — no single provider's wire
   schema in this corpus settles it; every vendor's classification logic
   lives behind a policy engine, not a fixed enum on the wire.
7. **DFS channel-change reason**: Cisco's RRM oper model exposes radar
   *events* per channel but no explicit "why did the channel change" reason
   enum was found in the 2611 YANG set this session. If AI-RRM/ARM change
   reasons are wanted, they likely need a targeted fetch of
   `Cisco-IOS-XE-wireless-rrm-rpc.yang` / `-rrm-global-oper.yang` beyond
   what this pass grepped, or Mist's documented RRM "reason codes" (Mist
   product docs mention `radio_config_reason` narratively but no schema was
   fetched with authority this session).
