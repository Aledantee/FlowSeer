# Wi-Fi 802.11 registry gap-closure — AKM/cipher suites, operating classes, country element

Closes three specific gaps `01-wifi-technology.md` (sections 1, 2, 7) left
unverified: the full RSN AKM/cipher suite-selector tables past the pre-2014
values, the Annex E global-operating-class table plus channel→frequency
formulas, and the 802.11d Country element encoding. Read `01-wifi-technology.md`
first; this dossier only adds the load-bearing facts that close its open
questions 1–3 and its `pmf`/`akm`/`country_code` fields (section 4) and traps
(section 6).

## 1. Scope and sources

Web sources fetched for this dossier (all raw source text, read directly, not
summarized by a third party):

- `https://raw.githubusercontent.com/torvalds/linux/master/include/linux/ieee80211.h`
  (fetched twice, once via WebFetch decode, once via `curl` into
  `$TMPDIR/ieee80211.h` and grepped directly) — `WLAN_CIPHER_SUITE_*` and
  `WLAN_AKM_SUITE_*` `#define`s, lines ~2226–2264. Current mainline Linux; does
  **not** yet carry PASN, SAE-EXT-KEY/GROUP-DEPEND, or EPPKE suite numbers —
  itself a fact (see section 6, "Linux lags Wireshark/WFA").
- `https://raw.githubusercontent.com/wireshark/wireshark/master/epan/dissectors/packet-ieee80211.c`
  (fetched via `curl` into `$TMPDIR/pkt80211.c`, 64074 lines, grepped and
  read directly at the cited line numbers):
  - `ieee80211_rsn_cipher_vals[]`, lines 19487–19502 — the dissector's cipher
    suite-type value_string table, values 0–13.
  - `AKMS_*` `#define`s and `ieee80211_rsn_keymgmt_vals[]`, lines 19505–19553
    — the dissector's AKM suite-type value_string table, values 0–20 plus 21,
    24, 25, 29 (802.11be-era additions Linux does not have yet).
  - `environment_vals[]` and `ieee80211_tag_country_info()`, lines 31988–32032
    — Country information element parser, citing clause **7.3.2.9** and MIB
    clause **C.3** (`dot11CountryString`) directly in the source comments.
  - No `value_string` table exists for Annex E operating-class numbers in this
    file (grepped `global_op_class|operating_class`); every operating-class
    field is parsed as an opaque octet (`ENC_NA`) — corroborates
    `01-wifi-technology.md`'s recommendation to treat operating class as
    opaque (see section 6).
- `https://raw.githubusercontent.com/1g4-mirror/hostap/master/src/common/ieee802_11_common.c`
  (a GitHub mirror of `hostap.git`; the canonical `w1.fi/cgit/hostap` host
  returned an Anubis anti-bot challenge page to every fetch attempt for this dossier and could not be read directly — noted so the planner does not
  re-attempt the same URL expecting a different result):
  - `global_op_class[]` table, lines 2545–2591 — the full Annex E global
    operating-class table hostap ships, through class 183, with inline
    comments citing "IEEE Std 802.11ax-2021, Table E-4" and "IEEE Std
    802.11be-2024, Table E-4".
  - `ieee80211_chan_to_freq_global()`, lines 2047–2129 — the exact per-class
    channel-number → center-frequency formulas, comment-cited to "Table E-4 in
    IEEE Std 802.11-2020".
  - `is_6ghz_op_class()`, line 3101–3103 — `op_class >= 131 && op_class <= 137`.
- `https://www.wi-fi.org/system/files/WPA3%20Specification%20v3.5.pdf` (Wi-Fi
  Alliance, dated 2025 per its own footer; fetched as a binary PDF via
  WebFetch, then text-extracted for this dossier with a short ad hoc Python script
  — `zlib`-decompressing each PDF stream and concatenating `Tj`/`TJ` string
  operands, no third-party PDF library available in the sandbox — into
  `$TMPDIR/wpa3_joined.txt`, then read/grepped directly). This is the current
  published WPA3 Specification, not a secondary summary.
- Repo conventions re-read for this dossier: `docs/conventions/protobuf.md`
  lines 215–236 (Enums / registry pass-through rules, `IpDscp`/`IpEcn` worked
  example), `spec/proto/flowseer/net/packet/v1/ether_type.proto` (the
  registry-pass-through-enum shape to copy: named + open, `UNSPECIFIED = 0`
  even when the registry's own zero exists, protovalidate CEL rule for the
  numeric domain).
- Explicitly **not** re-verified for this dossier (see section 7): the 2020/2024
  IEEE clause numbering for Table 9-151/9-152 (cipher/AKM suite selectors)
  itself — no fetchable non-paywalled copy of the IEEE standard text was
  found; the Linux/Wireshark/hostap source tables are used as the machine-readable proxy, not as a
  replacement citation for the standard's own table.

## 2. Standards facts

**AKM and cipher suite selectors (OUI `00-0F-AC`).** Two independent
open-source implementations agree exactly on suite types 1–20 (Linux kernel
`WLAN_AKM_SUITE_*`/`WLAN_CIPHER_SUITE_*`, and Wireshark's `AKMS_*`
defines/`ieee80211_rsn_keymgmt_vals`/`ieee80211_rsn_cipher_vals`). Wireshark
additionally carries four newer AKM suite types (21, 24, 25, 29) that Linux's
mainline header does not yet define — these are 802.11be/2024-era additions,
confirmed independently by the WFA WPA3 Specification v3.5 quoting suite
`00-0F-AC:24` ("SAE using group-dependent hash") by name (see the WPA3 table
below). Cipher suite types are unchanged from `01-wifi-technology.md`'s
pre-2014 partial list, now completed 0–13.

**AKM suite selectors, `00-0F-AC:N`** (Linux `include/linux/ieee80211.h:2243-2264`,
cross-checked against Wireshark `packet-ieee80211.c:19505-19553`; name in
quotes is Wireshark's dissector label where it differs from the Linux
`#define` name):

| N | Linux define | Wireshark label |
|---|---|---|
| 0 | — | "NONE" |
| 1 | `WLAN_AKM_SUITE_8021X` | "WPA" (802.1X/RSNA) |
| 2 | `WLAN_AKM_SUITE_PSK` | "PSK" |
| 3 | `WLAN_AKM_SUITE_FT_8021X` | "FT over IEEE 802.1X" |
| 4 | `WLAN_AKM_SUITE_FT_PSK` | "FT using PSK" |
| 5 | `WLAN_AKM_SUITE_8021X_SHA256` | "WPA (SHA256)" |
| 6 | `WLAN_AKM_SUITE_PSK_SHA256` | "PSK (SHA256)" |
| 7 | `WLAN_AKM_SUITE_TDLS` | "TDLS / TPK Handshake (SHA256)" |
| 8 | `WLAN_AKM_SUITE_SAE` | "SAE (SHA256)" — WPA3-Personal |
| 9 | `WLAN_AKM_SUITE_FT_OVER_SAE` | "FT using SAE (SHA256)" |
| 10 | `WLAN_AKM_SUITE_AP_PEER_KEY` | "APPeerKey (SHA256)" |
| 11 | `WLAN_AKM_SUITE_8021X_SUITE_B` | "WPA (SHA256-SuiteB)" |
| 12 | `WLAN_AKM_SUITE_8021X_SUITE_B_192` | "WPA (SHA384-SuiteB)" — WPA3-Enterprise 192-bit |
| 13 | `WLAN_AKM_SUITE_FT_8021X_SHA384` | "FT over IEEE 802.1X (SHA384)" |
| 14 | `WLAN_AKM_SUITE_FILS_SHA256` | "FILS (SHA256 and AES-SIV-256)" |
| 15 | `WLAN_AKM_SUITE_FILS_SHA384` | "FILS (SHA384 and AES-SIV-512)" |
| 16 | `WLAN_AKM_SUITE_FT_FILS_SHA256` | "FT over FILS (SHA256 and AES-SIV-256)" |
| 17 | `WLAN_AKM_SUITE_FT_FILS_SHA384` | "FT over FILS (SHA384 and AES-SIV-512)" |
| 18 | `WLAN_AKM_SUITE_OWE` | "Opportunistic Wireless Encryption" — Enhanced Open |
| 19 | `WLAN_AKM_SUITE_FT_PSK_SHA384` | "FT using PSK (SHA384)" |
| 20 | `WLAN_AKM_SUITE_PSK_SHA384` | "PSK (SHA384)" |
| 21 | *(not in Linux)* | "PASN" |
| 24 | *(not in Linux)* | "SAE (GROUP-DEPEND)" — WFA spec calls this "SAE using group-dependent hash", **SAE-EXT-KEY** |
| 25 | *(not in Linux)* | "FT using SAE (GROUP-DEPEND)" — FT-SAE-EXT-KEY |
| 29 | *(not in Linux)* | "EPPKE" (Enhanced Privacy Protection Key Exchange — a newer 802.11 auth algorithm, `AUTH_ALG_EPPKE = 9`, `packet-ieee80211.c:2098/2111`) |
| — | `WLAN_AKM_SUITE_WFA_DPP` | OUI `50-6F-9A:2` (WFA vendor OUI, not `00-0F-AC`) — Wi-Fi Easy Connect/DPP, a distinct OUI from the IEEE standard AKM/cipher space |

Gaps in the numbering (22, 23, 26–28) are not assigned in either fetched
source — treat as reserved, not evidence of a missing fetch.

**Cipher suite selectors, `00-0F-AC:N`** (identical across Linux
`WLAN_CIPHER_SUITE_*` and Wireshark `ieee80211_rsn_cipher_vals`,
`packet-ieee80211.c:19487-19502`):

| N | Name |
|---|---|
| 0 | Use group cipher (`WLAN_CIPHER_SUITE_USE_GROUP`) / "NONE" |
| 1 | WEP-40 |
| 2 | TKIP |
| 3 | (reserved — Wireshark labels it "AES (OCB)", an early/unused assignment; not in Linux) |
| 4 | CCMP-128 (the default/mandatory cipher) |
| 5 | WEP-104 |
| 6 | BIP-CMAC-128 (`WLAN_CIPHER_SUITE_AES_CMAC`) — the original PMF integrity cipher |
| 7 | "Group addressed traffic not allowed" |
| 8 | GCMP-128 |
| 9 | GCMP-256 |
| 10 | CCMP-256 |
| 11 | BIP-GMAC-128 |
| 12 | BIP-GMAC-256 |
| 13 | BIP-CMAC-256 |

A `00-14-72:1` (non-`00-0F-AC` OUI) cipher, `WLAN_CIPHER_SUITE_SMS4`, also
exists in Linux (China's WAPI SMS4 cipher) — evidence that the OUI, not just
the suite-type integer, must be part of any raw pass-through representation
if FlowSeer ever stores one; see section 6.

**Operating classes (Annex E, global table).** hostap's `global_op_class[]`
(`ieee802_11_common.c:2545-2591`) is the complete, currently-maintained table,
through 802.11be-2024's addition of class 137. Full row set (fields:
class, channel-number range, channel spacing/`inc`, bandwidth):

| Class | Band/mode | Channels | Spacing | Width | Notes |
|---|---|---|---|---|---|
| 81 | 2.4 GHz | 1–13 | 1 | 20 MHz | |
| 82 | 2.4 GHz | 14 | 1 | 20 MHz | Japan-only channel 14 |
| 83 | 2.4 GHz | 1–9 | 1 | 40 MHz+ | HT40, primary-low |
| 84 | 2.4 GHz | 5–13 | 1 | 40 MHz− | HT40, primary-high |
| 115 | 5 GHz | 36–48 | 4 | 20 MHz | indoor only |
| 116 | 5 GHz | 36–44 | 8 | 40 MHz+ | indoor only |
| 117 | 5 GHz | 40–48 | 8 | 40 MHz− | indoor only |
| 118 | 5 GHz | 52–64 | 4 | 20 MHz | DFS |
| 119 | 5 GHz | 52–60 | 8 | 40 MHz+ | DFS |
| 120 | 5 GHz | 56–64 | 8 | 40 MHz− | DFS |
| 121 | 5 GHz | 100–144 | 4 | 20 MHz | |
| 122 | 5 GHz | 100–140 | 8 | 40 MHz+ | |
| 123 | 5 GHz | 104–144 | 8 | 40 MHz− | |
| 124 | 5 GHz | 149–161 | 4 | 20 MHz | |
| 125 | 5 GHz | 149–177 | 4 | 20 MHz | |
| 126 | 5 GHz | 149–173 | 8 | 40 MHz+ | |
| 127 | 5 GHz | 153–177 | 8 | 40 MHz− | |
| 128 | 5 GHz | 36–177 | 4 | 80 MHz | center-frequency-index encoded |
| 129 | 5 GHz | 36–177 | 4 | 160 MHz | center-frequency-index encoded |
| 130 | 5 GHz | 36–177 | 4 | 80+80 MHz | paired with 128 (130 = the "80+" half) |
| 131 | 6 GHz | 1–233 | 4 | 20 MHz | UHB |
| 132 | 6 GHz | 1–233 | 8 | 40 MHz | UHB |
| 133 | 6 GHz | 1–233 | 16 | 80 MHz | UHB |
| 134 | 6 GHz | 1–233 | 32 | 160 MHz | UHB |
| 135 | 6 GHz | 1–233 | 16 | 80+80 MHz | UHB, paired with 133 |
| 136 | 6 GHz | 2 only | — | 20 MHz | the PSC-adjacent single-channel class |
| 137 | 6 GHz | 31–191 | 32 | 320 MHz | 802.11be-2024 addition |
| 180 | 60 GHz | 1–6 | 1 | 2160 MHz | legacy (802.11ad) |
| 181 | 60 GHz | 9–15 | 1 | 4320 MHz | EDMG CB2 |
| 182 | 60 GHz | 17–22 | 1 | 6480 MHz | EDMG CB3 |
| 183 | 60 GHz | 25–29 | 1 | 8640 MHz | EDMG CB4 |

`is_6ghz_op_class()` in the same file (`ieee802_11_common.c:3101-3103`) defines
the 6 GHz range as classes 131–137 inclusive — this is the authoritative
disambiguator `01-wifi-technology.md` asked for: **channel number alone is
ambiguous between 2.4 GHz (class 81, channels 1–13) and 6 GHz (class 131,
channels 1–233, spaced by 4) — a receiver needs the operating class (or
equivalently, an explicit band) to resolve which table a channel number
belongs to.** A `Radio`/`Bss` primitive that carries `channel` + `WifiBand`
(FlowSeer's own enum, section 2 of `01-wifi-technology.md`) is already
unambiguous without needing the operating-class integer for this specific
purpose — band alone disambiguates 2.4 GHz from 6 GHz channel-number
collisions, since FlowSeer's `WifiBand` is a first-class field. Operating
class only adds value for 802.11k/v candidate-channel *reports*, where the
standard's own wire format names channels by `(operating class, channel)`
pairs without a redundant band field.

**Channel number → center frequency formulas**, `ieee80211_chan_to_freq_global()`
(`ieee802_11_common.c:2047-2129`, comment-cited to "Table E-4 in IEEE Std
802.11-2020"):

- 2.4 GHz, channels 1–13: `freq_MHz = 2407 + 5 * channel`.
- 2.4 GHz, channel 14 (Japan): `freq_MHz = 2414 + 5 * 14 = 2484` (the formula
  in source is written as `2414 + 5 * chan` but only ever evaluated at
  `chan == 14`, yielding the well-known 2484 MHz value — do not generalize
  the `2414 +` constant to other channel numbers).
- 5 GHz, channels 36–177 (classes 115–130): `freq_MHz = 5000 + 5 * channel`.
- 6 GHz, channels 1–233 (classes 131–135, 137): `freq_MHz = 5950 + 5 * channel`.
- 6 GHz, channel 2 (class 136): fixed `5935 MHz` (the special "channel 2"
  preferred scanning channel; does not fit the `5950 + 5*chan` formula).
- 60 GHz, channels 1–8 (class 180): `freq_MHz = 56160 + 2160 * channel`.
- 60 GHz, channels 9–15 (class 181, EDMG CB2): `freq_MHz = 56160 + 2160 * (channel - 8)`.
- 60 GHz, channels 17–22 (class 182, EDMG CB3): `freq_MHz = 56160 + 2160 * (channel - 16)`.
- 60 GHz, channels 25–29 (class 183, EDMG CB4): `freq_MHz = 56160 + 2160 * (channel - 24)`.

These formulas are per-band and closed-form; FlowSeer does not need to carry
a frequency field alongside channel number if band is already a field —
frequency is fully derivable downstream from `(band, channel)` for every band
except that the 5 GHz/6 GHz classes 128–135 use a "channel center frequency
index" convention for the wide-bandwidth classes (comment at
`ieee802_11_common.c:2517-2528`) rather than a literal channel number — hostap
itself notes this is a simplification ("currently use the lowest 20 MHz
channel for simplicity") and that the true Annex E semantics differ. Do not
treat the table above as license to reimplement wide-channel center-frequency
math; if FlowSeer needs it, use the same "lowest 20 MHz channel" approximation
hostap uses, or store the raw operating-class/channel pair unresolved (see
section 4).

**Country element (802.11d), clause 7.3.2.9.** Confirmed directly from the
Wireshark dissector source (`packet-ieee80211.c:31988-32032`, itself
comment-cited to "IEEE 802.11-2020, C.3 MIB detail, dot11CountryString" for
the environment octet and "7.3.2.9 Country information element (7)" for the
element itself):

- Bytes 0–1: 2-character country code (ASCII), e.g. `"US"`. The dissector
  reads exactly 2 octets (`ENC_ASCII`) — this is ISO 3166-1 alpha-2, **not**
  3 octets as `01-wifi-technology.md` section 4 assumed when scoping the
  `country_code` field (`IEEE802dot11-MIB:475`'s `dot11CountryString` MIB
  object pads to 3 octets, but the wire element itself is 2 code octets plus
  a separate 1-octet environment field — see trap in section 6).
- Byte 2: the "environment" octet. Confirmed full value set
  (`environment_vals[]`, `packet-ieee80211.c:31988-31999`):
  - `0x1` = "Operating classes in the United States" (Table E-1)
  - `0x2` = "Operating classes in Europe" (Table E-2)
  - `0x3` = "Operating classes in Japan" (Table E-3)
  - `0x4` = "Global operating classes" (Table E-4) — the table this dossier
    tabulates above
  - `0x5` = "S1G operating classes" (Table E-5)
  - `0x6` = "Operating classes in China" (Table E-6)
  - `' '` (space, 0x20) = "All" (all environments for this band)
  - `'I'` (0x49) = "Indoor"
  - `'O'` (0x4F) = "Outdoor"
  - `'X'` (0x58) = "Non Country Entity" — dissector comment: "If environment
    is 'X', the only allowed CC is \"XX\"".
  - This resolves `01-wifi-technology.md`'s open question 3 cleanly: the
    environment octet is **not** binary indoor/outdoor, it is an 8+ value
    registry that also names *which Annex E table* (E-1 through E-6,
    country-specific vs. global vs. S1G) governs the operating classes that
    follow in the rest of the element. A FlowSeer schema that only stores
    ISO alpha-2 and drops the environment octet loses this table-selector
    fact.
  - Following the environment octet: a variable-length sequence of 3-octet
    "first channel number / number of channels / max transmit power" triples
    (802.11d format, `tvb_get_uint8(tvb, offset) <= 200` branch,
    `packet-ieee80211.c:32036 ff.`), each such triple further disambiguated by
    which Annex E table the environment octet selected.

**WPA3 AKM combinations per mode** (Wi-Fi Alliance WPA3 Specification v3.5,
extracted text, all direct quotes unless noted; suite numbers cross-checked
against the table above):

- **WPA3-Personal**: AKM `00-0F-AC:8` (SAE) required, PMF required (MFPC=1,
  MFPR=1 implied by "PMF and one of the following AKMs was negotiated" —
  the spec's WPA3-class-AKM definition bundles SAE/FT-SAE/802.1X-SHA256/FT-
  802.1X/Suite-B-192 AKMs together as "used with PMF").
- **WPA3-Personal Transition Mode**: "The AP's BSS Configuration shall enable
  at least AKM suite selectors 00-0F-AC:2 (PSK) and 00-0F-AC:8 (SAE)... The
  AP's BSS Configuration shall be PMF Capable, i.e., AP sets MFPC to 1 and
  MFPR to 0 in beacons." — PMF **capable, not required**, is the defining
  trait of transition mode (a WPA3-only STA gets PMF; a WPA2-only STA can
  still associate without it).
- **WPA3-Personal, SAE-EXT-KEY variant**: "If the AP's BSS Configuration
  enables AKM suite selector 00-0F-AC:24, it should also enable 00-0F-AC:8
  for interoperability" — `:24` is additive to, not a replacement for, `:8`.
- **WPA2-Personal (for contrast, defining the pre-WPA3 AKM set the transition
  and "shall not enable" rules reference)**: "00-0F-AC:2 (PSK), 00-0F-AC:4
  (FT with PSK), 00-0F-AC:1 (802.1X), 00-0F-AC:3 (FT with 802.1X)... WPA2 STAs
  and APs might support the SHA-256 AKMs (00-0F-AC:3 and 00-0F-AC:5) with
  PMF for Enterprise".
- **WPA3-Personal Only Mode** (no transition): "shall not enable AKM suite
  selectors 00-0F-AC:2 (PSK), 00-0F-AC:4 (FT over PSK), 00-0F-AC:6 (PSK using
  SHA-256), 00-0F-AC:19 (FT over PSK using SHA-384) or 00-0F-AC:20 (PSK using
  SHA-384)" — i.e. Only Mode is defined by exclusion of every PSK-family AKM,
  not just enablement of SAE.
- **WPA3-Enterprise 192-bit mode**: AKM `00-0F-AC:12` ("802.1X in 192-bit
  mode" / "WPA (SHA384-SuiteB)"), defined by the spec as mutually exclusive
  with non-192-bit WPA3-Enterprise and WPA2-Enterprise on the same BSS
  ("operating a BSS configured to support WPA3-Enterprise 192-bit
  connections, but not support WPA3-Enterprise without 192-bit or
  WPA2-Enterprise connections").
- **WPA3-Enterprise (non-192-bit)**: `00-0F-AC:5` (802.1X-SHA256) primary, plus
  `00-0F-AC:3` (FT with 802.1X) and `00-0F-AC:13` (FT with 802.1X SHA384) for
  fast transition.
- **Wi-Fi Enhanced Open (OWE)**: AKM `00-0F-AC:18`. "Wi-Fi Enhanced Open Only
  Mode" = OWE-only BSS; **"Wi-Fi Enhanced Open Transition Mode"** = "operating
  multiple BSSs (using OWE SSID and Open SSID, advertised in OWE Transition
  Mode element) configured to support Wi-Fi Enhanced Open and non Wi-Fi
  Enhanced Open STAs to connect to the same distribution system" — this is
  architecturally different from WPA3-Personal Transition Mode: OWE
  transition is **two separate BSSIDs/SSIDs** (one open, one OWE, linked by an
  information element), not one BSS accepting two AKMs. A FlowSeer schema
  must not model OWE transition the same way as WPA3-Personal transition
  (see section 6).

## 3. Provider data matrix

Not re-surveyed in this dossier — `01-wifi-technology.md` section 3 already
covers per-provider security field shapes (UniFi `securityType`, Ruckus
`wlan_wlanEncryption.method`, OpenConfig `opmode`) and the finding stands:
**no fetched vendor API exposes raw AKM/cipher suite-selector numbers**; every
vendor collapses them into a flattened marketing-named enum. The tables in
section 2 above are the mapping key a FlowSeer mapper would need to translate
a captured/monitored AKM (e.g. from a frame capture or `net/capture`
correlation) into one of those vendor-style flattened categories, or into the
`WlanSecurity` enum `01-wifi-technology.md` section 4 proposed.

## 4. Proposed primitives (delta on `01-wifi-technology.md` section 4)

This dossier does not re-propose `Radio`/`Bss`; it resolves the three fields
that dossier left open and specifies exact enum values.

- **`WlanSecurity` enum** (FlowSeer-normalized, `net/wlan/v1`,
  `WLAN_SECURITY_UNSPECIFIED = 0`) — build from the WPA3 spec's own mode
  taxonomy (section 2 above), since it is the authoritative definition of
  each named mode's AKM set, not vendor marketing:
  `WLAN_SECURITY_OPEN`, `WLAN_SECURITY_WEP` (legacy, no AKM),
  `WLAN_SECURITY_OWE`, `WLAN_SECURITY_OWE_TRANSITION`,
  `WLAN_SECURITY_WPA2_PERSONAL`, `WLAN_SECURITY_WPA2_ENTERPRISE`,
  `WLAN_SECURITY_WPA3_PERSONAL`, `WLAN_SECURITY_WPA3_PERSONAL_TRANSITION`,
  `WLAN_SECURITY_WPA3_ENTERPRISE`, `WLAN_SECURITY_WPA3_ENTERPRISE_TRANSITION`,
  `WLAN_SECURITY_WPA3_ENTERPRISE_192`. Each value's precise AKM-set
  definition (for mapper authors and doc comments) is now pinned by the
  direct WFA quotes in section 2 — cite the spec quote in the enum value's
  proto comment rather than re-deriving it, since "shall not enable" /
  "shall enable at least" phrasing carries real interoperability meaning a
  paraphrase would blur (`docs/code-style-proto.md`, comment discipline).
  `WLAN_SECURITY_OWE_TRANSITION` needs a doc comment flagging that it names
  a *pair of BSSIDs*, not one BSS's AKM set (section 2, Enhanced Open
  Transition note) — a consumer joining `Bss` rows for OWE transition must
  match by the OWE Transition Mode element's referenced BSSID, a fact this
  dossier surfaces but does not design the join key for (open question,
  section 7).
- **`Dot11AkmSuite` / `Dot11CipherSuite` — registry pass-through enums**,
  shaped like `net/packet/v1`'s `EtherType`/`IpDscp` (open, numeric-domain
  validated by a `buf.validate.predefined` CEL rule, `_UNSPECIFIED = 0` even
  though the registry's own 0 is a real "NONE"/"use group cipher" value, per
  `docs/conventions/protobuf.md`'s pass-through-enum presence rule). Populate
  from the tables in section 2: `Dot11AkmSuite` values 0–20 from Linux/
  Wireshark agreement, plus 21/24/25/29 from Wireshark-only (comment each of
  those four as "not yet in the Linux kernel header as of this writing" so a
  future refresh knows to re-check, per the "cite your source" discipline).
  `Dot11CipherSuite` values 0–13. **Both enums must carry the OUI alongside
  the suite-type integer** if raw suite selectors are ever stored raw (not
  just the type byte) — `WLAN_CIPHER_SUITE_SMS4` (`00-14-72:1`) proves the
  suite-type integer alone collides across OUIs (section 2, section 6). If
  FlowSeer only ever needs the `00-0F-AC` OUI (the standard IEEE space,
  covering every AKM/cipher a Wi-Fi Alliance-certified device uses), a single
  pass-through enum without an explicit OUI field is defensible and should
  say so in a comment — this is the planner's call, not decided here.
  Recommend deferring these two enums until a concrete producer (frame
  capture correlation) needs suite-selector granularity, per
  `01-wifi-technology.md`'s own recommendation — the WPA3 spec quotes above
  are sufficient to build `WlanSecurity` without them.
- **`OperatingClass` — registry pass-through, opaque `uint32`, not an
  enum.** hostap's table (136+ rows after 802.11be) is too large and too
  entangled with per-country variants (Annex E has US/EU/Japan/China/global/
  S1G tables, only one of which — global, Table E-4 — is tabulated above) to
  enumerate as a closed proto enum. Store it as a validated integer
  (`in [1, 255]` roughly; hostap's table tops out at 183, but Annex E's other
  national tables use different, possibly higher, numbers not surveyed here)
  with a comment citing this dossier's table for the global-table subset and
  noting country-specific tables are out of scope. This directly answers
  `01-wifi-technology.md` open question 2: **yes, carry operating class in
  v1, but as an opaque pass-through integer, never a re-derived enum** — the
  channel-number-ambiguity problem it exists to solve is already solved for
  FlowSeer's own `Radio`/`Bss` by the `WifiBand` field (section 2 above);
  operating class only earns its keep on 802.11k/v candidate-channel report
  primitives, which are not designed in this dossier.
- **`country_code`**: use the Country element's actual 2-octet ISO 3166-1
  alpha-2 code as a `string` (not the MIB's 3-octet
  `dot11CountryString`, which pads/duplicates for the older US/UK-CLI use
  case — the wire element `01-wifi-technology.md` and this dossier both
  ultimately care about is 2 code octets, per clause 7.3.2.9). Add a sibling
  **`country_environment`** field — a registry pass-through enum or opaque
  `uint32`/`string`(single char) carrying the environment octet's full 10-value
  set from section 2 (0x1–0x6 table selectors plus space/I/O/X) — dropping it
  loses the "which Annex E table applies" fact. This resolves open question 3
  from `01-wifi-technology.md`: the answer is **both** fields, not a choice
  between them — alpha-2 alone cannot express "Non Country Entity" (`'X'`,
  where the 2-byte code is forced to literal `"XX"`) or which of the six
  Annex E tables a global/indoor/outdoor AP is using.
- **`pmf` three-state enum** — `01-wifi-technology.md` section 4 already
  proposed `PMF_UNSPECIFIED/DISABLED/OPTIONAL/REQUIRED`; this dossier confirms
  the three states are real and load-bearing, not a guess: the WPA3 spec's
  own PMF-Capable-not-Required (MFPC=1, MFPR=0) vs. PMF-Required (MFPR=1) vs.
  disabled (MFPC=0) distinction is exactly what separates WPA3-Personal
  Transition Mode from WPA3-Personal Only Mode in section 2 above — keep the
  three-state enum as designed, now with a citable source for each state's
  wire meaning (MFPC/MFPR bit pair, not a single boolean).

## 5. Entity candidates (model/)

None proposed — unchanged from `01-wifi-technology.md` section 5; this
dossier only fills in primitive-level enum/field detail, not entity shape.

## 6. Traps

- **Linux's kernel header undercounts current AKM suites.** `WLAN_AKM_SUITE_*`
  in mainline Linux stops at suite 20 (PSK-SHA384); it does not yet define
  PASN (21), SAE-EXT-KEY/GROUP-DEPEND (24/25), or EPPKE (29), all of which
  Wireshark's actively-maintained dissector already names and the WFA WPA3
  spec already references by number (`00-0F-AC:24`). Do not treat "not in the
  Linux header" as "not a real assignment" — cross-check against Wireshark
  and, where available, the WFA spec text before excluding a value.
- **The MIB's `dot11CountryString` (3 octets) is not the same as the wire
  Country element's country-code field (2 octets).** `01-wifi-technology.md`
  cited `IEEE802dot11-MIB:475` and described a 3-octet field; the actual
  802.11 Country information element (clause 7.3.2.9, confirmed directly in
  Wireshark's dissector) reads exactly 2 ASCII octets for the country code,
  then a **separate** 1-octet environment field. Do not concatenate them into
  one 3-byte FlowSeer field the way the MIB's naming might suggest — model
  them as two fields (`country_code` string, `country_environment` registry
  value), matching the wire element's actual byte layout.
- **The environment octet is not indoor/outdoor.** It is a 10-value registry:
  six numeric values (0x1–0x6) selecting *which Annex E table* applies
  (US/Europe/Japan/global/S1G/China), plus four ASCII values (space/I/O/X)
  for "all environments"/indoor/outdoor/non-country-entity. A schema that
  only stores a 2-state indoor/outdoor bool, or that conflates the numeric
  table-selector values with the ASCII environment values, cannot
  round-trip a real Country element.
- **Wide-bandwidth operating classes (128–135, 137) do not use a literal
  channel number.** hostap's own source comment
  (`ieee802_11_common.c:2517-2528`) admits its table entry for these classes
  is a simplification ("use the lowest 20 MHz channel for simplicity") of
  what Annex E actually specifies (a channel *center frequency index*). Do
  not build a FlowSeer channel↔frequency converter for 80/160/320 MHz classes
  from this dossier's table alone; either use hostap's same approximation
  explicitly, or store the operating-class/channel pair unresolved and defer
  frequency resolution.
- **Cipher/AKM suite-type integers are not globally unique without the
  OUI.** `WLAN_CIPHER_SUITE_SMS4` reuses suite-type `1` under OUI `00-14-72`
  (China's WAPI), the same integer TKIP uses under `00-0F-AC`. Any raw
  pass-through representation of a suite selector (section 4) that drops the
  OUI and keeps only the type byte will silently misidentify a non-`00-0F-AC`
  cipher as a standard one.
- **OWE Transition Mode is not the same shape as WPA3-Personal Transition
  Mode.** WPA3-Personal transition is one BSS enabling two AKMs
  (`00-0F-AC:2` and `:8`) on one BSSID. OWE transition is **two separate
  BSSIDs/SSIDs** (an open one and an OWE one) linked by an OWE Transition
  Mode information element, per the WFA spec's own definition quoted in
  section 2. A `WlanSecurity` value or a `Bss`-level boolean cannot express
  the OWE case the same way as the WPA3-Personal case — it needs a
  cross-`Bss` reference (the paired BSSID), which is out of this dossier's
  scope to design (see open question below).
- **`w1.fi`/`git.w1.fi` (the canonical hostap upstream) is not fetchable by
  an automated tool** because it serves an Anubis bot-challenge page
  to every request. The `1g4-mirror/hostap` GitHub mirror used instead is a
  third-party mirror, not upstream; a future session verifying this dossier
  should re-check the mirror is still in sync with upstream (or find a
  working path to `git.w1.fi`) rather than assuming permanence.

## 7. Open questions for the planner

1. **Do the `Dot11AkmSuite`/`Dot11CipherSuite` raw pass-through enums get
   built in v1 at all**, or does `WlanSecurity` (section 4, fully specifiable
   now from the WPA3 spec quotes) cover every producer FlowSeer has in the
   first cut? This dossier recommends deferring the raw enums until a
   frame-capture-level producer exists, matching `01-wifi-technology.md`'s
   original recommendation — now on firmer footing since the exact suite
   tables are no longer a blocker if the planner decides the raw enums are
   needed sooner.
2. **OWE Transition Mode's cross-`Bss` link**: does `Bss` need a
   `paired_bssid` (or similar) field for OWE transition symmetric to
   `mld_address` for MLO (`01-wifi-technology.md` section 4), or is this
   deferred like MLO's link-membership question? Not designed in either
   dossier.
3. **`OperatingClass` scope**: this dossier only tabulates Annex E Table E-4
   (global). If FlowSeer ever needs to represent a device reporting via a
   country-specific table (E-1/E-2/E-3/E-6), the numeric domain validated
   against the field must be widened accordingly — no fetched source enumerated the national tables' row counts or ranges.
4. **`country_environment`'s proto shape**: registry pass-through enum (10
   named values, closed set per clause 7.3.2.9) vs. a single-byte/rune field
   — this dossier surfaces the exact value set (section 2) but does not
   pick the representation; either is defensible, an enum matches the
   `IpDscp`-style convention better and gets protovalidate range checking for
   free.
