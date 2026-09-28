# Wi-Fi (IEEE 802.11) technology building blocks — research dossier

## 1. Scope and sources

Domain: 802.11 PHY/MAC building blocks (bands, channels, operating classes,
width, PHY generations, MCS/NSS/GI/RU, rates, regulatory domain, DFS/TPC,
BSS/SSID/BSSID, beacon/DTIM, AKM/cipher suites, PMF, 802.11k/v/r, MLO, WFA
generation names) plus OpenConfig wifi models and vendor API naming. Client
detail and RF telemetry are covered by sibling dossiers; only load-bearing
overlap (RSSI/SNR field names as they appear on Radio/Bss primitives) is
noted here.

**Vendored corpus read for this dossier:**
- `spec/mib/ieee/IEEE802dot11-MIB:1-2977` — IEEE 802.11 MIB (2002, Cisco
  submission; pre-11n). `dot11StationConfigEntry`, `dot11DesiredBSSType`
  (line 304), `dot11CountryString` (475), `dot11PhyOperationEntry` PHY-type
  enum (1511), `dot11RegDomainsSupportedValue` (2179), `dot11PhyOFDMEntry`
  frequency/band (2419-2457), `dot11ChannelAgility` (2493).
- `spec/mib/ietf/CAPWAP-BASE-MIB:1-2502` — CAPWAP (RFC 5834 MIB); channel
  type/keepalive/notification objects only, no radio/band/PHY table (the
  radio detail lives in `CAPWAP-DOT11-MIB`, which is **not vendored here** —
  confirmed absent from `spec/mib/`).
- `spec/proto/ruckus/ap/ap_status.proto:929-1330` (`APStatusRadio`),
  `spec/proto/ruckus/ap/ap_client.proto:1-500` (`APClientWlan`/client MCS
  fields), `spec/proto/ruckus/ap/ap_common.proto` (shared attribute-map
  types only, no wireless enums), `spec/proto/ruckus/ap/ap_report.proto`
  (grepped, no independent security enum — encryption lives in the VSZ
  OpenAPI, not this proto).
- `spec/openapi/ruckus/vsz/vsz-7.1.1-v13_1-openapi.json` — searched via
  `grep`/`python3 json.load`; `wlan_wlanEncryption.method` enum at line
  ~78372, `rfBand`/`radioBand`/`bandBalancing` fields (~line 58859, 82527).
- `spec/openapi/ubiquiti/unifi-network-openapi-v10.4.57.json:700-760,
  2857-2891, 14228-19237` — radio overview object (`channel`,
  `channelWidthMHz`, `frequencyGHz` enum `[2.4, 5, 6, 60]`, `wlanStandard`
  enum), WLAN security `securityType` enum (`WPA2_ENTERPRISE`,
  `WPA2_PERSONAL`, `WPA2_WPA3_ENTERPRISE`, `WPA2_WPA3_PERSONAL`,
  `WPA3_ENTERPRISE`, `WPA3_PERSONAL`), `dtimPeriod2gLockedTo3`,
  `dtimPeriodByFrequencyGHzOverride`.
- `docs/conventions/protobuf.md`, `docs/code-style-proto.md`,
  `docs/architecture/2026-08-20-network-model-structure-direction.md` —
  FlowSeer schema conventions; the direction record already reserves
  `net/wlan/v1` for `Radio`, `Bss`, `WirelessClient` as "a peer of
  switching", importing `addr` and `switching` (a BSS maps to a VLAN id),
  never the reverse (direction doc, "Wireless is a peer of L2" section).
- `spec/proto/flowseer/net/phy/v1/ethernet_facet.proto`,
  `mau_type.proto`, `poe_facet.proto`/`poe_settings.proto`,
  `net/switching/v1/vlan_id.proto` — read as the worked pattern for
  Facet/Settings split, registry pass-through enums, and typed variants.

**Web sources fetched for this dossier:**
- `https://raw.githubusercontent.com/openconfig/public/master/release/models/wifi/openconfig-wifi-types.yang`
  — identities `OPERATING_FREQUENCY` (`FREQ_2GHZ`, `FREQ_5GHZ`, `FREQ_6GHZ`,
  and dual/tri-band composites `FREQ_2_5_GHZ`, `FREQ_5_6_GHZ`,
  `FREQ_2_5_6_GHZ`, `FREQ_2_6_GHZ`), `WIFI_PROTOCOL` (`WIFI_80211_A/B/G/N/AC/AX/BE`),
  `CLIENT_STATE`, `AP_STATE`, `DATA_RATE` (`RATE_1MB`..`RATE_54MB`),
  `CLIENT_CAPABILITIES` (`MU_BEAMFORMER`, `MU_BEAMFORMEE`, `OFDMA`,
  `DOT_11R`, `DOT_11V`, `MFP`), `CHANGE_REASON_TYPE` (`DFS`, `NOISE`,
  `ERRORS`, `BETTER_CHANNEL`), typedef `channels-type` (uint8, "superset of
  what may be allowed by any one particular regulatory domain").
- `https://raw.githubusercontent.com/openconfig/public/master/release/models/wifi/openconfig-wifi-phy.yang`
  — `channel` (uint8 1-233, "Primary 20MHz channel" under bonding),
  `channel-width` (uint8, MHz, **deprecated**), `channel-bandwidth` (uint16,
  MHz, "supports channel bandwidths of 320Mhz and greater" — the
  replacement), `operating-frequency` (identityref), `transmit-power` (int8,
  dBm), `transmit-eirp` (uint8, dBm), `wifi-protocol` (identityref),
  `ofdma`/`mru` (bool), `allowed-max-txpower`/`allowed-max-eirp` (state,
  dBm). No MCS/NSS/GI/RU leaves in this module.
- `https://raw.githubusercontent.com/openconfig/public/master/release/models/wifi/openconfig-wifi-mac.yang`
  — `bssids`/`bss-common-state` (`bssid` mac-address, `radio-id` uint8,
  `mld-address` mac-address, `num-associated-clients`), `ssids` (`name`,
  `enabled`, `hidden`, `default-vlan`, `vlan-list`, `operating-frequency`),
  `opmode` enumeration (`OPEN`, `WPA2_PERSONAL`, `WPA2_ENTERPRISE`,
  `ENHANCED_OPEN`, `ENHANCED_OPEN_TRANSITION`, `WPA3_SAE`,
  `WPA3_2_SAE_TRANSITION`, `WPA3_ENTERPRISE`,
  `WPA3_2_ENTERPRISE_TRANSITION`, `WPA3_ENTERPRISE_192_BIT`), `mfp`
  (boolean, "mandatory for WPA3 and OWE"), `ptk-timeout`/`gtk-timeout`,
  `dot11k`/`dot11k-neighbors`, `dot11r`/`dot11r-domainid`/`dot11r-method`
  (OVA|ODS)/`dot11r-r1key-timeout`, `dot11v`/`dot11v-dms`/`dot11v-bssidle`/
  `dot11v-bssidle-timeout`/`dot11v-bsstransition`, client `mac`,
  `mld-address`, `client-state` (identityref), `rssi` (int8, dBm), `snr`
  (uint8, dB), `ss` (spatial streams, uint8), `rx-phy-rate`/`tx-phy-rate`
  (Mbps), `connection-mode` (A|B|G|N|AC|AX|BE), `client-capabilities`
  (identityref list), band-steering, `mlo-enable`, FTM/RTT `enabled`.
- `https://raw.githubusercontent.com/openconfig/public/master/release/models/wifi/openconfig-access-points.yang`
  — single `access-points/access-point` list keyed by hostname, composing
  `wifi-phy:radio-top` and `wifi-mac:ssid-top` groupings plus
  `assigned-ap-managers` (ties to `openconfig-ap-manager`).
- IEEE 802.11 AKM/cipher suite selector table (00-0F-AC:N): confirmed via
  `https://mrncciew.com/2014/08/21/cwsp-rsn-information-elements/` — AKM
  00-0F-AC-1 = 802.1X, 00-0F-AC-2 = PSK, 00-0F-AC-3 = FT-802.1X; cipher
  00-0F-AC-1 = WEP-40, 00-0F-AC-2 = TKIP, 00-0F-AC-4 = CCMP (default),
  00-0F-AC-5 = WEP-104. **The full current tables (802.11-2020 Table 9-151
  cipher suite selectors / Table 9-152 AKM suite selectors, which add SAE
  (00-0F-AC-8), OWE (00-0F-AC-18), 802.1X-SUITE-B-192 (00-0F-AC-12), GCMP-256
  (00-0F-AC-9), etc.) were searched but not found in a fetchable
  non-paywalled page for this dossier** — the 2020 standard PDF sits behind
  IEEE/USPTO viewers that did not yield table text. Mark the post-2014
  values (SAE, OWE, GCMP, Suite B) **unverified**; the planner should budget
  a follow-up fetch of the actual 802.11-2020 clause 9.4.2.24
  cipher-suite-selector / 9.4.2.25 AKM-suite-selector tables, or a IEEE
  mentor tutorial PDF, before finalizing the registry pass-through enum
  values.
- `https://mentor.ieee.org/802.11/dcn/20/11-20-0646-00-00ax-update-to-6ghz-operating-classes.docx`
  and search snippets — Annex E Table E-4 "Global operating classes" has
  columns Operating class / Nonglobal operating class(es) / Channel starting
  frequency (GHz) / Channel spacing (MHz) / Channel set / Channel center
  frequency index / Behavior limits set. Example rows found in snippets:
  class 125 = 5 GHz start, 20 MHz spacing, channels {149,153,157,161,165,
  169,173,177}; 6 GHz classes (131-135) start at 5,950 MHz. **The complete
  table (all ~136+ rows spanning 2.4/5/6/60 GHz) was not fully retrieved —
  only fetchable as snippets, not the full table text.** Mark the exhaustive
  operating-class → channel-set mapping unverified; recommend the planner
  treat operating class as an opaque registry pass-through integer rather
  than re-deriving the table by hand.
- Wi-Fi Alliance generation naming, cross-checked via search results citing
  `electronics-notes.com` and `wifivitae.com`: 802.11n → Wi-Fi 4 (2009),
  802.11ac → Wi-Fi 5 (2014), 802.11ax (5 GHz+2.4 GHz) → Wi-Fi 6 (2020),
  802.11ax with 6 GHz → Wi-Fi 6E (2021), 802.11be → Wi-Fi 7 (2024). Wi-Fi
  Alliance introduced the numbered scheme in 2018; names are a marketing
  layer over the IEEE amendment, not a standards concept.
- Cisco Meraki Dashboard API (`developer.cisco.com/meraki/api-v1/...`,
  search-result summaries of "Get/Update Device Wireless Radio
  Settings/Status/Overrides" and "RF Profiles" pages): band is one of
  `'2.4'`, `'5'`, `'6'` (string); 5 GHz channel width one of `'auto'`,
  `'20'`, `'40'`, `'80'`; 6 GHz channel width adds `'160'`, `'320'`; 2.4 GHz
  channels 1-14, 5 GHz channels the standard UNII set (36..177); radio
  status endpoint returns band, channel, channel width, and DFS status.
- Juniper Mist API (search-result summaries of Mist docs and the
  `/sites/{site_id}/stats/devices` and "Get Org Other Device Stats" pages):
  per-radio `radio_stat` block carries channel, power (dBm), and byte
  counters per band; RSSI documented on a -100..0 dBm scale, typical client
  range -90..-25 dBm.
- HPE Aruba Central / AOS (search-result summaries of
  `arubanetworking.hpe.com/techdocs` "Configuring Radio Parameters" and
  "rf dot11a/dot11g/dot11-60GHz-radio-profile" CLI-bank pages): Aruba names
  **radio profiles by PHY letter, not by GHz** — `dot11a-radio-profile` (5
  GHz), `dot11g-radio-profile` (2.4 GHz), `dot11-60GHz-radio-profile` (60
  GHz) — each with its own primary/secondary channel and 40 MHz bonding
  fields; ARM (Adaptive Radio Management) is Aruba's RRM feature choosing
  channel and TX power dynamically.

## 2. Standards facts

- **Bands and channel numbering.** IEEE 802.11 channelization is band-specific
  and not derivable from a single formula across bands; FlowSeer sources
  disagree on how many bands to enumerate as a closed set — OpenConfig's
  `OPERATING_FREQUENCY` identity models *simultaneous multi-band radios*
  (`FREQ_2_5_6_GHZ` etc.) as their own identities rather than a repeated
  field of single bands (`openconfig-wifi-types.yang`, `OPERATING_FREQUENCY`
  base). This is a real API-shape decision: FlowSeer's own `Radio` normally
  operates on **one** band at a time (a tri-radio AP is three `Radio` rows),
  so a single-valued band enum is the natural normalized shape; the
  composite identities are OpenConfig's answer to a radio capable of
  simultaneous dual/tri-band operation and do not need to be copied.
- **Channel width vs. channel bandwidth.** OpenConfig's own module marks its
  `channel-width` leaf (uint8, MHz) **deprecated** in favor of
  `channel-bandwidth` (uint16, MHz) specifically because widths above 255
  MHz (320 MHz, EHT) do not fit a `uint8`
  (`openconfig-wifi-phy.yang`). This is a direct trap for a schema author
  copying the "obvious" field width from an older reference.
  Domain-standard widths per the dossier's scope: 20, 40, 80, 80+80, 160, 320 MHz (S1G
  also has 1/2/4/8/16 MHz widths at 900 MHz, not covered by any vendored or
  fetched source for this dossier — mark unverified/niche).
- **Primary channel / secondary channel / secondary offset.** Not explicitly
  in the fetched OpenConfig PHY module (only `channel` = "Primary 20MHz
  channel... [when] using channel-bonding"). Ruckus's `APStatusRadio`
  carries this concretely: `channel` (int32, primary) plus
  `secondaryChannel` (uint32, "second channel value for 80_80MHz channel
  width", `ap_status.proto:1216`) — i.e. Ruckus only needs a second explicit
  channel number for the non-contiguous 80+80 case; contiguous 40/80/160
  MHz widths are inferred from primary channel + width by the receiver, per
  the IEEE channelization tables (Annex E), not carried as a separate field.
- **Operating classes (Annex E).** A registry: `(operating class, channel
  number)` pair maps to a channel center frequency, independent of country
  — this is IEEE's own normalization of "which channel numbering table
  applies", used in 802.11k neighbor reports and 802.11v BSS transition
  candidate lists to name a channel unambiguously across regulatory
  domains. Table E-4 ("Global operating classes") is confirmed to exist
  with the columns listed in section 1, but the full row set was not
  retrieved for this dossier (unverified in detail; see traps).
- **Regulatory domain / country code.** `dot11CountryString` in the vendored
  IEEE MIB (`IEEE802dot11-MIB:475`) is an `OCTET STRING`: this is the ISO
  3166-1 alpha-2 country code plus an "environment" octet ('I'/'O'/'X'), the
  encoding used in the 802.11 Country Information Element. The MIB also
  carries a much older, US-Cisco-specific `dot11RegDomainsSupportedValue`
  enumeration (`fcc(16)`, `doc(32)`, `etsi(48)`, `spain(49)`, `france(50)`,
  ... `IEEE802dot11-MIB:2179`) — this predates the 3-letter country-code
  convention and should **not** be reused; it is included only as a trap
  (see section 6).
- **DFS / TPC.** OpenConfig's `CHANGE_REASON_TYPE` identity
  (`openconfig-wifi-types.yang`) includes `DFS` as one reason a channel
  changed, alongside `NOISE`, `ERRORS`, `BETTER_CHANNEL` — DFS is modeled as
  an *event cause*, not a channel property, in that schema. Meraki's radio
  status endpoint instead exposes DFS as a per-radio/per-channel state field
  (channel is or is not DFS-restricted). TPC (Transmit Power Control) is not
  named explicitly in any fetched source; `transmit-power` /
  `allowed-max-txpower` / `transmit-eirp` / `allowed-max-eirp` in
  `openconfig-wifi-phy.yang` are the closest analogs (actual vs.
  regulatory/hardware ceiling).
- **BSS/ESS/SSID.** SSID is 0-32 octets per the 802.11 standard (not
  independently re-verified for this dossier against the standard text, but
  consistent with every vendored/fetched source's string-typed `ssid`
  field and matches the OpenConfig `ssids/name` container's use as the
  keying attribute). BSSID is a MAC address
  (`openconfig-wifi-mac.yang bss-common-state.bssid`). `dot11DesiredBSSType`
  in the IEEE MIB fixes the type enum: `infrastructure(1)`,
  `independent(2)` (IBSS), `any(3)` (`IEEE802dot11-MIB:303-317`) — this is
  the authoritative source for infrastructure vs. IBSS vs. mesh BSS typing;
  MBSS (mesh) is a later 802.11s addition not present in this 2002 MIB and
  not independently confirmed by any other fetched source for this dossier
  (unverified — treat mesh as a third value to add, not one to copy from a
  source).
- **Beacon interval / DTIM.** `dot11BeaconPeriod` and `dot11DTIMPeriod` are
  both plain `INTEGER` in the IEEE MIB (`IEEE802dot11-MIB:187-188`), i.e.
  the beacon interval's canonical unit is TU (Time Units, 1024 µs) per the
  802.11 standard convention (not independently re-verified against
  standard text for this dossier; consistent with every vendor field being an
  integer "period" with no unit suffix — vendors assume the reader knows
  TU). DTIM period is a *multiplier* of beacon intervals (every Nth beacon
  is a DTIM), not an independent time value — UniFi's
  `dtimPeriod2gLockedTo3` / `dtimPeriodByFrequencyGHzOverride` fields
  confirm DTIM is configured as a small integer count, per-band-overridable,
  not a duration.
- **AKM suite selectors / cipher suite selectors.** IEEE 802.11 Table
  9.4.2.24/9.4.2.25 (numbering varies by edition; commonly cited as Table
  9-151/9-152 in 802.11-2020) define both as `(OUI, suite type)` pairs. The
  standards OUI is `00-0F-AC`; a non-`00-0F-AC` OUI means vendor-specific.
  Confirmed suite-type values (both tables, for this dossier):
  - AKM: `1` = 802.1X (RSNA/dot1x), `2` = PSK, `3` = FT-over-802.1X.
  - Cipher: `1` = WEP-40, `2` = TKIP, `4` = CCMP-128 (the default/mandatory
    cipher), `5` = WEP-104.
  **Not independently re-verified against the 802.11-2020 clause text this
  session** (fetch attempts against IEEE/USPTO PDF viewers did not return
  table text): AKM suite type `8` = SAE (WPA3-Personal), `18` = OWE, and the
  higher-numbered Suite-B / GCMP-256 / FT-SAE values that UniFi's and VSZ's
  security enums (`WPA3_PERSONAL`, `WPA3_ENTERPRISE`, `OWE`,
  `OWE_Transition`) clearly correspond to at the marketing layer. Recommend
  the planner fetch clause 9.4.2.24/9.4.2.25 directly from a IEEE Xplore or
  purchased-standard source before fixing the pass-through enum's numeric
  domain; do not guess the numbers from vendor marketing names.
- **PMF / 802.11w.** `mfp` in `openconfig-wifi-mac.yang` is a boolean,
  described as "mandatory for WPA3 and OWE" — OpenConfig models PMF as a
  binary required/not-required rather than the standard's three-state
  disabled/optional/required. The standard actually has three states
  (0/1/2, sometimes named "Disabled"/"Optional"/"Required" or "Capable"/
  "Required" depending on vendor UI) but no fetched source for this dossier
  reproduces the numeric encoding; unverified in detail.
- **802.11k/v/r.** Confirmed shape from `openconfig-wifi-mac.yang`:
  - 802.11k: neighbor reports keyed by BSSID with channel + RSSI
    (`dot11k-neighbors`: `neighbor-bssid`, `neighbor-channel`,
    `neighbor-rssi`, `channel-load-report`), gated by a per-SSID `dot11k`
    boolean.
  - 802.11r: fast transition, with a mobility-domain id
    (`dot11r-domainid`), a method choice `OVA`/`ODS` (over-the-air vs.
    over-the-DS), and a PMK-R1 key timeout in seconds.
  - 802.11v: BSS transition management with sub-features — directed
    multicast service (`dot11v-dms`), BSS max idle period
    (`dot11v-bssidle`/`dot11v-bssidle-timeout`, seconds), and BSS
    transition itself (`dot11v-bsstransition`) — each independently
    togglable, not one monolithic "802.11v enabled" flag.
- **MLO (802.11be multi-link).** Confirmed from `openconfig-wifi-mac.yang`:
  an MLD (multi-link device) has its own `mld-address` distinct from any
  single link's BSSID/client MAC, present on both the AP side
  (`bss-common-state.mld-address`) and the client side
  (`clients.mld-address`); `mlo-enable` is a boolean toggle at the SSID
  level. The per-link structure (which `Radio`/`Bss` rows belong to one MLD)
  is not detailed in the fetched module — the module exposes MLD identity
  but not an explicit link-membership list; the planner will need to derive
  that from `Bss` rows that share an `mld_address` rather than expecting a
  dedicated "links" container.
- **PHY generations / Wi-Fi Alliance names.** IEEE amendments (802.11a, b,
  g, n, ac, ax, be) are the wire-relevant `WIFI_PROTOCOL` identity/
  `wifi-protocol` in OpenConfig and the `wlanStandard` enum in UniFi
  (`802.11a`..`802.11be`, exact string match between the two independent
  sources). Wi-Fi Alliance generation numbers (Wi-Fi 4/5/6/6E/7) are a
  **derived marketing label**, not a wire value: Wi-Fi 6 and 6E share the
  same IEEE amendment (802.11ax) and are distinguished only by which band
  the radio uses (6E = ax on 6 GHz), so "generation name" cannot be a field
  independent of (amendment, band) — it is a display-layer computation, not
  a primitive.
- **MCS / NSS / GI / RU / data rates.** No fetched OpenConfig module exposes
  MCS index, NSS, guard interval, or RU allocation as radio/BSS-level
  fields; `openconfig-wifi-mac.yang`'s client-level fields
  (`rx-phy-rate`/`tx-phy-rate` in Mbps, `ss` = spatial stream count, and
  `connection-mode` = A/B/G/N/AC/AX/BE) are the closest OpenConfig gets, and
  they are **client** state, not `Radio`/`Bss` primitives. Ruckus's
  `APClientWlan` mirrors this: `medianTxMCSRate`/`medianRxMCSRate` (uint32)
  are per-client observed values, not radio capabilities. This confirms the
  brief's own framing: MCS/NSS/GI/RU/rates are association-level (client or
  per-link) facts, not `Radio` facts — `Radio` carries capability
  (supported PHY generations, channel, width, power); actual negotiated
  MCS/NSS/rate is a `WirelessClient`/link-state fact. `DATA_RATE` in
  OpenConfig's types module (`RATE_1MB` .. `RATE_54MB`) is a **legacy** (pre-11n)
  fixed-rate identity set with no HT/VHT/HE/EHT MCS equivalent — it will not
  represent modern rates and should not be extended; a numeric Mbps field is
  the better shape for negotiated rate, matching `rx-phy-rate`/
  `tx-phy-rate`.

## 3. Provider data matrix

| Concept | UniFi (Network OpenAPI) | Ruckus (VSZ OpenAPI / AP proto) | Meraki (Dashboard API, from docs) | Mist (API, from docs) | Aruba Central/AOS (from docs) | OpenConfig wifi | Core or niche |
|---|---|---|---|---|---|---|---|
| Band | `frequencyGHz` enum `[2.4, 5, 6, 60]` (number) | `band` (string, free-form, `APStatusRadio.band`) | `band` string `'2.4'`/`'5'`/`'6'` | per-radio `radio_stat` keyed by band | **radio profile per band by PHY letter**: `dot11g`=2.4GHz, `dot11a`=5GHz, `dot11-60GHz`=60GHz (no unified "band" field — band is which profile type you use) | `operating-frequency` identityref (`FREQ_2GHZ`/`FREQ_5GHZ`/`FREQ_6GHZ` + composites) | **Core**, but representation varies: numeric GHz (UniFi/OpenConfig) vs. profile-type-as-band (Aruba) vs. plain string (Ruckus/Meraki) |
| Channel | `channel` int | `channel` int32 + `secondaryChannel` uint32 (80+80 only) | `channel` int, band-scoped valid sets | `radio_stat.channel` | primary + secondary channel per radio profile (40 MHz bonding) | `channel` uint8 1-233 ("primary 20MHz channel") | **Core** |
| Channel width | `channelWidthMHz` int | `channelWidth` uint32 + `channelWidthGroup` int32 (7.0+) | `channelWidth` string enum, band-gated valid sets (`auto`/20/40/80[/160/320 on 6GHz]) | bandwidth field on `radio_stat` | 40 MHz bonding flag on radio profile | `channel-bandwidth` uint16 MHz (replaces deprecated uint8 `channel-width`) | **Core**; width-as-string-enum (Meraki) vs. width-as-int-MHz (everyone else) is a real divergence |
| PHY standard/generation | `wlanStandard` enum `802.11a..be` (string, exact IEEE names) | `radioMode` string, vendor's own slash-joined format (e.g. `"11bgn"`, explicitly flagged by Ruckus's own comment as bad for machine parsing) | not directly fetched; RF profile implies band+width only | not fetched in detail | dot11a/dot11g/dot11-60GHz profile *type* implies generation indirectly | `wifi-protocol` identityref, same `WIFI_80211_A..BE` set as UniFi | **Core** for the plain amendment letter; Ruckus's compound string is a **named trap** (see section 6) |
| Tx power | `txPower` (Ruckus: string!), `actualTxPower`/`calibrationTxPower`/`maxTxPower`/`eirp` (int32, dBm, Ruckus 7.1+) | same as above (Ruckus is the richest source: actual vs. calibration vs. max vs. EIRP, all dBm) | radio settings support explicit power in dBm | power in dBm on `radio_stat` | ARM dynamically manages power; profile carries min/max | `transmit-power`/`transmit-eirp` (config, int8/uint8 dBm) + `allowed-max-txpower`/`allowed-max-eirp` (state ceiling) | **Core** concept, but Ruckus alone distinguishes 4-5 power values (requested/actual/calibration/max/EIRP) that other sources collapse into one — a modeling trap (section 6) |
| Country / reg domain | not directly located for this dossier | not located | present (Meraki networks have a country/reg-domain setting, not directly fetched) | not fetched | `ap regulatory-domain-profile` (named CLI object) | not modeled in fetched files | **Core** but under-verified for this dossier; needs a follow-up fetch |
| DFS | radio status implies channel legality | Ruckus radio has channel blacklist (`channelBlacklist`) but no explicit DFS flag found | explicit DFS status field on radio status endpoint | not fetched | ARM avoids DFS channels dynamically (behavioral, not necessarily a field) | `CHANGE_REASON_TYPE.DFS` (event cause, not a channel property) | **Core**, but "is this channel DFS" (channel property) vs. "did we vacate due to DFS" (event) are two different facts — trap |
| SSID / BSSID | present (WLAN objects) | `ssid`, `bssid` string fields on `APClientWlan`/`APStatusWlan` | present | present | present | `ssids.name`, `bss-common-state.bssid` (mac-address) | **Core** |
| Security/AKM | `securityType` enum: `WPA2_ENTERPRISE`, `WPA2_PERSONAL`, `WPA2_WPA3_ENTERPRISE`, `WPA2_WPA3_PERSONAL`, `WPA3_ENTERPRISE`, `WPA3_PERSONAL` (transition modes as their own named values, not a flag) | `wlan_wlanEncryption.method` enum: `WPA2`, `WPA_Mixed`, `WEP_64`, `WEP_128`, `None`, `WPA3`, `WPA23_Mixed`, `OWE`, `OWE_Transition` | not fetched in detail (Meraki has its own PSK/8021x/OPEN model) | not fetched | AOS security modes doc references WPA3 config (not itemized for this dossier) | `opmode` enumeration: `OPEN`, `WPA2_PERSONAL`, `WPA2_ENTERPRISE`, `ENHANCED_OPEN`, `ENHANCED_OPEN_TRANSITION`, `WPA3_SAE`, `WPA3_2_SAE_TRANSITION`, `WPA3_ENTERPRISE`, `WPA3_2_ENTERPRISE_TRANSITION`, `WPA3_ENTERPRISE_192_BIT` | **Core**, but every vendor enumerates a *different* flattening of (AKM × cipher × transition-mode) as one string enum — none matches the IEEE AKM/cipher suite-selector model directly (trap, section 6) |
| PMF | `mfp` boolean (mandatory for WPA3/OWE) — OpenConfig | not located as a distinct field in fetched Ruckus/UniFi | Meraki has PMF disabled/optional/required (not itemized for this dossier) | not fetched | not itemized for this dossier | `mfp` boolean | **Core**, three-state in the standard, boolean in the one schema fetched — verify before assuming binary |
| DTIM | `dtimPeriod2gLockedTo3` + `dtimPeriodByFrequencyGHzOverride` (per-band override) | not located in fetched proto | present, per-SSID | not fetched | not itemized | not in fetched modules | **Core**, band-scoped override in UniFi is notable |
| Beacon interval | not located for this dossier | not located | present (RF profile settings) | not fetched | not itemized | not in fetched modules | Likely core; under-verified |
| RSSI / SNR (client) | n/a here (client dossier) | `Rssi` int, radio `noiseFloor` int32 | n/a here | RSSI documented -100..0 dBm scale | n/a here | client `rssi` int8 dBm, `snr` uint8 dB | Cross-referenced only — owned by the client/telemetry dossier |
| 802.11k/v/r | not itemized for this dossier | not located | Meraki RF profile has 802.11k/v/r toggles (not itemized) | not fetched | ARM/AOS supports 802.11k/v/r (not itemized) | full k/v/r leaf set (section 2) | **Core** feature-toggle set; OpenConfig is the richest fetched source |
| MLO | not itemized for this dossier | not located (VSZ 7.1.1 predates or omits MLO in fetched sections) | not itemized | not fetched | not itemized | `mld-address` (AP + client), `mlo-enable` | **Niche/emerging** — only OpenConfig models it among fetched sources; expected, since MLO is new with 802.11be/Wi-Fi 7 |
| 900 MHz S1G | not found in any fetched source | not found | not found | not found | not found | not found | **Not covered by any vendored or fetched source for this dossier** — treat as out of scope for the first cut of `net/wlan/v1`, flag as an open question |
| 60 GHz (802.11ad/ay) | `frequencyGHz` enum includes `60` | Aruba has a distinct `dot11-60GHz-radio-profile` | not found | not found | confirmed (see previous) | not modeled (fetched types module has no 60 GHz identity) | **Niche**; two independent sources (UniFi, Aruba) confirm it exists as a real vendor concept even though OpenConfig's fetched module doesn't carry it |

## 4. Proposed primitives

All under `spec/proto/flowseer/net/wlan/v1/`, edition 2024, ref-free
(peers named by key: interface `name`, VLAN `id` per the direction record's
own note that "a BSS maps to a VLAN id"). Facet/Settings split per
convention: `Radio` and `Bss` bundle what a source observed; a
`RadioSettings`/`BssSettings` sibling (not detailed here — flag as an open
question, see section 7) would carry what was requested, mirroring
`EthernetFacet`/`EthernetSettings`.

- **`WifiBand` enum** — FlowSeer-normalized, `WIFI_BAND_UNSPECIFIED = 0`,
  then `WIFI_BAND_2_4GHZ`, `WIFI_BAND_5GHZ`, `WIFI_BAND_6GHZ`,
  `WIFI_BAND_60GHZ`. Do not add composite dual/tri-band values the way
  OpenConfig's `OPERATING_FREQUENCY` does (section 2) — FlowSeer's `Radio`
  is one band per row, so composites would never be assigned. S1G (900 MHz)
  omitted pending the open question in section 7 (no vendored or fetched
  evidence it is in scope for named providers).
- **`Dot11Standard` enum** — FlowSeer-normalized (not a registry: IEEE does
  not assign these letters a stable integer table the way it does AKM/cipher
  suites), `DOT11_STANDARD_UNSPECIFIED = 0`, then `DOT11_STANDARD_A`,
  `_B`, `_G`, `_N`, `_AC`, `_AX`, `_BE`. This is the wire-relevant fact
  (matches OpenConfig `WIFI_PROTOCOL` and UniFi `wlanStandard` exactly,
  section 2/3); Wi-Fi generation names (Wi-Fi 4/5/6/6E/7) are **not** a
  schema field — they are `(standard, band)` derived and belong in a
  frontend/display layer, never stored (section 2, PHY generations note).
- **`ChannelWidthMhz`** — plain `uint32` field on `Radio`, not a wrapper
  message (VLAN-id precedent, protobuf.md "Field numbering"/typed-variant
  section: "a scalar with a range... gets a protovalidate predefined rule,
  not a wrapper message"). Validate `in [20, 40, 80, 160, 320]` — 80+80 is
  *not* a width value, it needs a second channel number (see
  `secondary_channel` below), matching Ruckus's shape exactly (section 2).
  Do not copy OpenConfig's now-deprecated `uint8 channel-width` — go
  straight to a wide-enough integer type, since the deprecation reason
  (320 MHz doesn't fit uint8) is dated and load-bearing evidence
  (`openconfig-wifi-phy.yang`).
- **`Radio` message** (per-radio facet, one row per physical radio, keyed by
  `radio_id` + owning interface name — no ref):
  - `radio_id` uint32 — vendor-local radio index (Ruckus: `radioId`,
    OpenConfig: `radio-id`). Comment: unset means not reported.
  - `band` `WifiBand`.
  - `channel` uint32 — primary channel number. Comment states it is a
    channel *number*, not a frequency; resolving number→frequency needs
    band + (eventually) operating class, deliberately not embedded here
    (see open question on operating classes, section 7).
  - `secondary_channel` uint32 — set only for 80+80 MHz non-contiguous
    bonding (Ruckus `secondaryChannel`, section 2/3). Comment: unset means
    either not 80+80 or not reported.
  - `channel_width_mhz` uint32, validated against the discrete set above.
  - `standard` `Dot11Standard` — highest amendment the radio currently runs;
    do NOT try to encode "which amendments this radio *supports*" as a
    repeated field yet (no fetched source needed it as a `Radio`-level
    fact; every source that had a capability list had it at the client
    level).
  - `tx_power_dbm` int32 (dBm) — Ruckus distinguishes actual vs. calibration
    vs. max vs. EIRP (section 3); recommend **one field per Ruckus-confirmed
    concept** rather than collapsing them, since they are genuinely
    different physical facts a consumer must not conflate (trap, section
    6): `tx_power_dbm` (actual), `max_tx_power_dbm` (regulatory/hardware
    ceiling — matches OpenConfig `allowed-max-txpower` too), `eirp_dbm`
    (tx power + antenna gain, Ruckus's own definition,
    `ap_status.proto:965`). Whether `calibration_tx_power` earns a field is
    an open question (section 7) — it looks Ruckus-specific.
  - `noise_floor_dbm` int32 — Ruckus `noiseFloor` (section: vendored corpus).
  - `dfs_channel` — presence-only bool is a trap per direction doc's own
    rule ("no `is_routed` booleans... may grow more states"); given DFS
    channel status genuinely is binary per-channel (is-DFS or not, distinct
    from the *event* of vacating one), a plain optional bool with a comment
    stating absence means "not reported" is acceptable here, unlike the
    routed/switched interface case, because there is no second signal it
    could contradict.
  - `country_code` — **open question**, no field proposed yet; needs the
    follow-up fetch noted in section 7 to decide format (ISO 3166-1 alpha-2
    string vs. the IEEE Country IE's environment-octet-inclusive encoding).
- **`Bss` message** (one row per BSSID, keyed by `bssid`, referencing its
  `Radio` by `radio_id` and the VLAN by `vlan_id` per the direction record —
  no refs):
  - `bssid` — reuse `flowseer.net.addr.v1.MacAddress` (typed variant,
    already landed), not a raw string.
  - `ssid` bytes — **bytes, not string**: SSID is defined as 0-32 arbitrary
    octets, not guaranteed valid UTF-8 (802.11 standard; consistent
    treatment needed since every fetched source used a naive string type,
    which is itself a trap, section 6). Validate `max_len = 32`.
  - `bss_type` — normalized enum from `dot11DesiredBSSType`
    (`IEEE802dot11-MIB:304`): `BSS_TYPE_UNSPECIFIED = 0`,
    `BSS_TYPE_INFRASTRUCTURE`, `BSS_TYPE_INDEPENDENT` (IBSS),
    `BSS_TYPE_MESH` (802.11s — not in the 2002 MIB source, added because
    the dossier's scope includes MBSS; flag as unverified against a primary 802.11s
    source, section 6/7).
  - `radio_id` uint32 — bare key into the owning `Radio`, ref-free per
    convention.
  - `vlan_id` — reuse `flowseer.net.switching.v1.VlanId` per the direction
    record's explicit statement that a BSS maps to a VLAN id.
  - `beacon_interval_tu` uint32 — name carries the unit (TU) per
    `docs/code-style-proto.md`'s "units in field names" rule, since TU
    (1024 µs) is not an obviously-inferable unit like "seconds".
  - `dtim_period` uint32 — a beacon multiplier, not a duration (section 2).
  - `hidden` bool — matches OpenConfig `ssids.hidden`.
  - `akm` — **typed variant candidate, not yet finalized** (open question,
    section 7): the AKM/cipher suite selectors are individually a registry
    pass-through pair (`(OUI, suite type)`), but every vendor collapses
    (AKM × cipher × PMF-mode × transition) into one flattened security enum
    (section 3). Recommend **not** modeling raw AKM/cipher suite selector
    numbers as the primary `Bss` field — no fetched vendor source exposes
    them at that granularity — and instead defining a FlowSeer-normalized
    `WlanSecurity` enum shaped like the union of UniFi's `securityType` and
    the VSZ `wlan_wlanEncryption.method` (both fetched, section 3), reserving
    raw AKM/cipher-suite-selector pass-through enums (`net/packet`-style,
    `<ENUM>_<REGISTRY-VALUE>` at zero) as a *second*, deeper field for
    sources (like a live 802.11 frame capture, `net/capture` correlation)
    that do report suite selectors directly. Needs the planner's decision;
    do not build the registry enum until the AKM/cipher table gap (section
    1) is closed.
  - `pmf` — three-state normalized enum (`PMF_UNSPECIFIED`, `PMF_DISABLED`,
    `PMF_OPTIONAL`, `PMF_REQUIRED`), not a bool, despite OpenConfig's own
    boolean shape — the standard genuinely has three states and FlowSeer
    should not inherit OpenConfig's collapse (trap, section 6).
  - `dot11k_enabled`, `dot11v_bss_transition_enabled`,
    `dot11r_enabled` bools — each an independently-togglable feature per
    OpenConfig's own per-feature booleans (section 2); do not collapse into
    one "fast-roaming-features" bitmask.
  - `mld_address` — optional `flowseer.net.addr.v1.MacAddress`; presence
    means this BSS is one link of an MLD (802.11be MLO, section 2). Two
    `Bss` rows sharing the same `mld_address` are the same MLD's links —
    document this relationship in the comment since there is no separate
    "links" container in any fetched source (section 2, MLO note).
- **`WirelessClient` message** — out of full scope for this dossier per the
  brief ("not clients — other agents cover those"); noted here only because
  the direction record names it as a `net/wlan/v1` peer of `Radio`/`Bss`.
  The one load-bearing fact from this dossier's research that the client
  dossier will need: negotiated MCS/NSS/rate/guard-interval are
  **client-association** facts (section 2, MCS/NSS/GI/RU note), never
  `Radio` or `Bss` fields — do not let a later pass push them up into
  `Radio` by mistake.

## 5. Entity candidates (model/)

No UUID entity is proposed from this dossier. Per the direction record,
`Radio`, `Bss`, `WirelessClient` are named as **primitives** (peer of
`switching`), not entities — consistent with the "Primitives versus
entities" rule (a primitive doesn't know which device/interface it came
from; a `Radio` embeds by value into whatever interface or component
entity eventually models an AP's hardware). If FlowSeer later needs an
"Access Point" entity distinct from `Device`/`Component` (e.g. because an
AP's radios have independent lifecycle from the device housing them), that
is an `model/inventory/v1` (or a new `model/` package) decision outside
this dossier's scope — flagged as an open question, not designed here.

## 6. Traps

- **SSID as `string` instead of `bytes`.** Every fetched vendor API types
  SSID as a string (UniFi, Ruckus, OpenConfig `ssids.name`), but the 802.11
  standard defines it as 0-32 arbitrary octets. A `bytes` field with a
  `max_len = 32` protovalidate rule is correct; a `string` field would
  either reject or silently mangle a non-UTF-8 SSID a real deployment can
  emit.
- **TX power is not one number.** Ruckus alone (because it is the deepest
  fetched source) distinguishes requested/actual/calibration/max/EIRP power,
  all in dBm but physically different (section 3/4). Collapsing these into
  a single `tx_power_dbm` field the way UniFi and Meraki appear to
  (unverified in full depth for those two) would lose the "how close to the
  regulatory ceiling are we" question that DFS/TPC monitoring needs.
- **PMF is not boolean.** OpenConfig's `mfp` leaf is a boolean, but the
  802.11 standard's PMF has (at minimum) disabled/optional/required states,
  and vendor UIs commonly expose three settings. Do not copy OpenConfig's
  collapse (see section 4, `pmf` field).
- **Channel width as string enum (Meraki) vs. integer MHz (everyone else).**
  A FlowSeer normalization must pick the integer-MHz shape; do not let a
  Meraki-shaped mapper coerce `'auto'` into a numeric width without a
  presence rule for "let the AP choose" (absence, not a sentinel value, per
  the direction record's own "no sentinel values" rule).
- **`dot11RegDomainsSupportedValue`'s vendor-numbered enum
  (`fcc(16)`/`doc(32)`/`etsi(48)`/`spain(49)`/`france(50)`...) is a
  historical Cisco-submitted MIB artifact, not a current IEEE or ISO
  registry** (`IEEE802dot11-MIB:2179`). Do not use these integers as a
  registry pass-through enum for regulatory domain / country code; they
  predate ISO 3166 country-code-based regulatory modeling and would
  misrepresent modern deployments (e.g. Spain and France as separate
  numbered domains from the rest of ETSI Europe).
- **Ruckus's `radioMode` string (`"11bgn"`) is a slash-joinable multi-value
  string the vendor's own proto comment admits is bad for parsing**
  (`ap_status.proto:959-961`, "The 11bgn is not a good formate if machine
  wants to parse... we use '/' to seperate each capability"). Do not treat
  this as a model for a FlowSeer field; it is exactly the anti-pattern
  `Dot11Standard` (a single closed enum per generation) exists to avoid.
- **AKM/cipher suite-selector numbers are unverified past the pre-2014
  values fetched for this dossier** (section 1/2). Do not hand the planner a
  numeric registry pass-through enum built from vendor marketing names
  (`WPA3_SAE`, `OWE`, `SAE`) mapped by guesswork to suite-type integers —
  fetch the actual 802.11-2020 clause text first.
- **The Annex E operating-class table is not fully retrieved.** Do not
  attempt to reconstruct the full channel-number-to-frequency mapping by
  hand from the fragments in section 1; if FlowSeer needs it (e.g. for
  802.11k/v candidate channel lists), treat "operating class" as an opaque
  registry integer carried alongside "channel number", not a table
  FlowSeer re-derives.
- **Do not conflate a BSS's `vlan_id` field with a full `Vlan` primitive.**
  Per the direction record, `net/wlan` imports `net/switching` for the VLAN
  id type only — a `Bss` names its VLAN by id, it does not embed or own the
  `Vlan` row (same pattern as `SwitchportFacet`).
- **60 GHz and 900 MHz S1G are asymmetrically covered.** 60 GHz has two
  independent confirming sources (UniFi, Aruba); 900 MHz S1G has zero
  vendored or fetched confirmation for this dossier. Do not add an S1G band
  value to `WifiBand` on the strength of the dossier's scope line alone
  without a source (open question, section 7).

## 7. Open questions for the planner

1. **AKM/cipher suite selectors**: fetch IEEE 802.11-2020 §9.4.2.24/9.4.2.25
   (or equivalent current clause numbering) directly before deciding
   whether `Bss` carries a raw registry pass-through pair, a
   FlowSeer-normalized `WlanSecurity` enum (recommended shape sketched in
   section 4), or both. This dossier could not retrieve the full tables
   from a fetchable source.
2. **Operating classes (Annex E)**: same gap — is a `Radio`/`Bss` expected
   to carry an operating-class number at all in v1, or is channel number +
   band sufficient until 802.11k/v candidate-list modeling is scoped?
3. **Country code / regulatory domain field**: no fetched source this
   session pinned down whether FlowSeer should store the raw 802.11 Country
   IE encoding (alpha-2 + environment octet) or a plain ISO 3166-1 alpha-2
   string with DFS/TPC-relevant regulatory facts computed downstream.
4. **900 MHz S1G**: in scope per the dossier's scope, but zero vendored or
   fetched-web evidence surfaced for this dossier (no MIB, no OpenAPI, no
   OpenConfig identity). Confirm whether any FlowSeer-targeted provider
   (Ruckus, UniFi, Meraki, Mist, Aruba) actually ships S1G hardware before
   spending a `WifiBand` value on it.
5. **`RadioSettings`/`BssSettings` (the intended-vs-observed split)**: this
   dossier only detailed the observed-side `Radio`/`Bss` facets. The
   Facet/Settings convention (section 4 intro) implies a sibling
   `RadioSettings` (requested channel/width/power) the same way
   `PoeSettings` sits beside `PoeFacet` — not designed here; flag for the
   planner to decide whether v1 needs it immediately or can defer.
6. **MLD/link modeling**: is `mld_address` on `Bss` (this dossier's
   proposal, matching OpenConfig) sufficient, or does 802.11be MLO need its
   own primitive (e.g. an explicit `Mld` grouping message) once more than
   one provider's MLO API shape is surveyed? Only OpenConfig's fetched
   module modeled MLO for this dossier — a second confirming source is
   recommended before treating the shape as settled.
7. **Access Point as a future entity**: out of scope here (section 5), but
   the planner should note that `Radio`/`Bss` as primitives will need to be
   embedded somewhere once an AP hardware entity is designed — likely
   `model/inventory/v1` `Component`, mirroring how `net/phy` values embed
   into `EthernetFacet` today without an entity yet existing.
