# Dossier: QoS, security/AAA, ops telemetry, WAN/access, tunnels

## 1. Scope and sources

Repo conventions read: `docs/conventions/protobuf.md`, `docs/code-style-proto.md`,
`docs/architecture/2026-08-20-network-model-structure-direction.md` (package tree,
import layering, Primitive/Entity split, facet/table rule).

Repo packages read: `spec/proto/flowseer/net/filter/v1/filter.proto` +
`README.md`, `spec/proto/flowseer/net/packet/v1/ip_dscp.proto`.

Vendored corpus read: `spec/yang/openconfig/openconfig-acl.yang`,
`spec/yang/cisco/iosxe/2611/openconfig-qos-elements.yang`,
`openconfig-qos-types.yang`, `spec/mib/ietf/RADIUS-AUTH-CLIENT-MIB`,
`spec/mib/ietf/VDSL2-LINE-MIB` + `VDSL2-LINE-TC-MIB`, `spec/proto/ruckus/ap/ap_avc.proto`.

Prior survey read: `docs/research/network-domain-atlas/entities/06-qos.md`,
`07-security.md`, `09-ops.md`, `10-wan-access.md`.

Web fetched for this dossier:
- RFC 5424 §6.2.1 — https://www.rfc-editor.org/rfc/rfc5424.html
- RFC 2865 §3, §5 — https://www.rfc-editor.org/rfc/rfc2865.html
- RFC 8907 §3.5, §4.1, §5.2 — https://www.rfc-editor.org/rfc/rfc8907.html
- IANA IPFIX registry — https://www.iana.org/assignments/ipfix/ipfix.xhtml
- sFlow v5 spec structure — https://sflow.org/sflow_version_5.txt
- RFC 8519 §3.1, §4.1 — https://www.rfc-editor.org/rfc/rfc8519.html
- 3GPP TS 36.133 v19.5.0 §9.1.4 (RSRP), §9.1.7 (RSRQ), via
  https://www.sharetechnote.com/html/Handbook_LTE_RSRP.html and
  .../Handbook_LTE_RSRQ.html (secondary source quoting the primary clause;
  the 3GPP FTP directory itself — https://www.3gpp.org/ftp/Specs/archive/36_series/36.133/
  — served only a file listing, no extractable table, so the clause numbers
  are attributed but not independently verified against the PDF for this dossier)
- Meraki Dashboard API v1 — `getOrganizationUplinksStatuses` and
  `getOrganizationDevicesUplinksLossAndLatency` —
  https://developer.cisco.com/meraki/api-v1/get-organization-uplinks-statuses/ ,
  .../get-organization-devices-uplinks-loss-and-latency/

Not fetched (time-boxed out; flagged as gaps in §7): RFC 2866 (RADIUS
accounting codes), RFC 3954 (NetFlow v9), RFC 7011 (IPFIX packet format),
RFC 8622/9956 already covered by existing `ip_dscp.proto`, ITU-T G.997.1 text
itself (used only via the vendored VDSL2-LINE-MIB, which already encodes its
clause references), Juniper Mist / Aruba Central / RUCKUS One / Omada API docs,
3GPP TS 36.101 band-number table, GSMA IMEI/ICCID format specs.

## 2. Standards facts

**Syslog (RFC 5424 §6.2.1).** PRI = facility×8 + severity. Facility 0–23
(kernel, user, mail, daemon, auth, syslog, lpr, news, uucp, cron, authpriv,
ftp, ntp, logaudit, logalert, cron2, local0–local7 at 16–23). Severity 0–7:
Emergency, Alert, Critical, Error, Warning, Notice, Informational, Debug.

**RADIUS (RFC 2865 §3, §5).** Header: Code(1)+Identifier(1)+Length(2)+
Authenticator(16) = 20 octets, then attributes. Code values: 1
Access-Request, 2 Access-Accept, 3 Access-Reject, 11 Access-Challenge.
Attribute is Type(1)+Length(1)+Value. Accounting codes (RFC 2866, not
fetched for this dossier — carried from established knowledge, mark
unverified): 4 Accounting-Request, 5 Accounting-Response.

**TACACS+ (RFC 8907 §3.5, §4.1, §5.2).** Three session kinds: authentication,
authorization, accounting — `TAC_PLUS_AUTHEN=0x01`, `TAC_PLUS_AUTHOR=0x02`,
`TAC_PLUS_ACCT=0x03` in the 12-byte header's `type` field, alongside
major/minor version, seq_no, flags (`TAC_PLUS_UNENCRYPTED_FLAG=0x01`,
`TAC_PLUS_SINGLE_CONNECT_FLAG=0x04`), session_id, length. Authentication
REPLY status: PASS=0x01, FAIL=0x02, GETDATA=0x03, GETUSER=0x04, GETPASS=0x05,
RESTART=0x06, ERROR=0x07, FOLLOW=0x21.

**ACL (RFC 8519 `ietf-access-control-list`, §3.1 tree, §4.1).**
`/acls/acl[name]/type` (an `acl-type` identityref) +
`/acls/acl/aces/ace[name]` with `matches` (choice of `eth`/`ipv4`/`ipv6` L3,
`tcp`/`udp`/`icmp` L4) and `actions` (mandatory `forwarding`, optional
`logging`, both identityrefs). Six `acl-type` identities: `ipv4-acl-type`,
`ipv6-acl-type`, `eth-acl-type`, `mixed-eth-ipv4-acl-type`,
`mixed-eth-ipv6-acl-type`, `mixed-eth-ipv4-ipv6-acl-type`.
`/acls/attachment-points/interface` binds `acl-sets` per ingress/egress
direction with optional per-interface statistics. This is the model FlowSeer's
existing `net/filter/v1` already approximates with `FilterRuleSet`/
`FilterRule`/`FilterMatch`/`FilterFacet` — see §4 for the gap.

**IPFIX (IANA registry).** Information Element numbers relevant to a flow-key
primitive: 1 octetDeltaCount (unsigned64), 2 packetDeltaCount (unsigned64),
4 protocolIdentifier (unsigned8), 5 ipClassOfService (unsigned8),
6 tcpControlBits (unsigned16), 7 sourceTransportPort (unsigned16),
8 sourceIPv4Address (ipv4Address), 10 ingressInterface (unsigned32),
11 destinationTransportPort (unsigned16), 12 destinationIPv4Address
(ipv4Address), 14 egressInterface (unsigned32), 58 vlanId (unsigned16),
152 flowStartMilliseconds / 153 flowEndMilliseconds (dateTimeMilliseconds).
These IE numbers are IPFIX's own registry (RFC 7011/7012 lineage); NetFlow v9
(RFC 3954) uses a *different*, older numbering for the same concepts (its
field type 1 is `IN_BYTES`, 2 `IN_PKTS`, 4 `PROTOCOL`, 7 `L4_SRC_PORT`, 8
`IPV4_SRC_ADDR`, 10 `INPUT_SNMP`, 11 `L4_DST_PORT`, 12 `IPV4_DST_ADDR`, 14
`OUTPUT_SNMP` — not independently re-verified for this dossier, flagged
unverified) — a flow-key primitive that says "IPFIX IE 8" must not silently
also mean "NetFlow v9 field 8"; the numbers coincide for several early fields
by lineage but are governed by separate registries and diverge higher up
(e.g. IPFIX 152/153 vs NetFlow v9's 21/22 `LAST_SWITCHED`/`FIRST_SWITCHED` in
seconds-since-boot, a different epoch entirely).

**sFlow v5** (sflow.org spec text). `sample_datagram_v5{agent_address,
sub_agent_id, sequence_number, uptime, sample_record[]}`. A `sample_record`
carries a `data_format` (enterprise<<12 | format) and opaque data; format 1 =
`flow_sample`, 2 = `counter_sample`, 3/4 = their "expanded" variants (larger
ifIndex space). A compact `flow_sample` carries `sequence_number, source_id,
sampling_rate, sample_pool, drops, input, output, flow_records[]` — the
1-in-N sampling ratio and the total eligible-packet pool are what let a
consumer reconstruct estimated volume from a sample.

**3GPP cellular signal quality (TS 36.133, cited via secondary source, clause
numbers not independently re-verified against the primary PDF for this dossier —
mark this whole entry lower-confidence than the RFC-sourced facts above).**
RSRP: −156 dBm to −44 dBm, 1 dB steps (§9.1.4, current spec; the classic
range widely quoted elsewhere is −140 to −44 dBm — the wider range is a
recent extension). RSRQ: −34 dB to +2.5 dB, 0.5 dB steps (§9.1.7; classic
range −19.5 to −3 dB). RSSI and SINR/RS-SINR are not in the fetched excerpts;
treat their ranges as unverified until a dedicated fetch. dBm/dB unit
distinction matters: RSRP and RSSI are absolute power (dBm); RSRQ and SINR
are ratios (dB) — never store one typed as the other.

**VDSL2-LINE-MIB (RFC 5650, ITU-T G.997.1 clause references embedded in the
vendored MIB text, `spec/mib/ietf/VDSL2-LINE-MIB` and
`VDSL2-LINE-TC-MIB`).** `xdsl2LineStatusXtur`/`xdsl2LineStatusXtuc` use the
`Xdsl2LineStatus` BITS textual convention: `noDefect(0)`,
`lossOfFraming(1)`, `lossOfSignal(2)`, `lossOfPower(3)`, `initFailure(4)` —
a bitmask, not a mutually-exclusive enum, and the near-end (xTU-C) / far-end
(xTU-R) split is doubled throughout the table (`...AttainableRateDs/Us`,
`...ActPsdDs/Us`, `...ActAtpDs/Us`). `xdsl2LineStatusXtuTransSys` names the
active transmission mode as a bitmap (ADSL/ADSL2/ADSL2+/VDSL2, one bit set).
The MIB's own `REFERENCE` clauses point at ITU-T G.997.1 §7.3.1.1.3 (PMSF)
and §7.5.1.1 (transmission system) — these clause numbers come from the
vendored MIB text itself, repo path
`spec/mib/ietf/VDSL2-LINE-MIB:572-587`, not an independent ITU-T fetch.

## 3. Provider data matrix

| Concept | Field (provider/spec) | Type/unit | Required in practice |
|---|---|---|---|
| DSCP trust boundary | `agentCosMapIntfTrustTable` (FASTPATH) — per-port trust/remark | enum | **core**: every switch vendor in the atlas has this somewhere, per §06-qos "if FlowSeer models one QoS thing, model this" |
| Classifier terms | `openconfig-qos` `classifiers/classifier/terms/term/{conditions,actions}` (verified in vendored `openconfig-qos-elements.yang:188-264`: `list term`, `container conditions`, `container actions`, `container remark`) | structured | core (class-map/policy-map family — Cisco MQC and its D-Link/Comware/Huawei clones) |
| Queue/scheduler | `openconfig-qos` `queues`, `scheduler-policies/scheduler/{inputs, one-rate-two-color, two-rate-three-color}` — types confirmed in `openconfig-qos-types.yang:56-137` (`QOS_QUEUE_TYPE`→`DROP_TAIL`/`RED`/`WRED`; `QOS_SCHEDULER_TYPE`→`ONE_RATE_TWO_COLOR`/`TWO_RATE_THREE_COLOR`; action enum `SHAPE`/`POLICE`) | structured | core on managed switches, niche on unmanaged/consumer gear |
| ACL rule | RFC 8519 `ace` match+action; FlowSeer's own `net/filter/v1.FilterRule` (name+match+action) already covers L3/L4 match via `net/packet` | structured | core |
| ACL binding | RFC 8519 `attachment-points/interface` + direction; FlowSeer's `FilterFacet.{in_set,out_set}` | structured | core — already landed, narrower than RFC 8519 (device-scoped name, not VLAN/control-plane binding) |
| RADIUS server | `RADIUS-AUTH-CLIENT-MIB radiusAuthServerTable[index]` (verified `spec/mib/ietf/RADIUS-AUTH-CLIENT-MIB:53-77`: index, address, port, ...) | address+port+counters | core (RFC 4668, near-universal) |
| RADIUS packet type | RFC 2865 Code field: Access-Request(1)/Accept(2)/Reject(3)/Challenge(11) | pass-through int | core for an AAA-event primitive if ever modelled |
| TACACS+ session type | RFC 8907 `type` = AUTHEN(1)/AUTHOR(2)/ACCT(3) | pass-through int | niche (much rarer than RADIUS per atlas 07-security) |
| Syslog severity/facility | RFC 5424 §6.2.1 | pass-through int, 0-7 / 0-23 | **core** — every device speaks syslog; this is FlowSeer's Event source |
| dot1x session | `IEEE8021X-PAE-MIB dot1xAuthSessionStatsTable` (username, octets, duration) + every vendor's private multi-session table keyed `(port,mac[,vlan])` | mixed | core-ish: near-universal presence, but shape diverges by vendor (single vs multi-session) — see Traps |
| Port security | `arubaWiredPortSecurityClientTable[portName,mac]` / FASTPATH `agentPortSecurityDynamicTable[ifIndex,vlanId,mac]` | (interface,mac,vlan) row | core on managed switches |
| Certificate | X.509 subject/issuer/not_before/not_after/fingerprint — no single vendored MIB table found in this pass; `CISCOSB-SSH-MIB` covers SSH host keys, not X.509 certs directly | structured | niche today, "worth carrying forward" per atlas 07-security (cert-expiry as a real fault) |
| Flow export config | `SFLOW-MIB sFlowFsTable`/`sFlowCpTable` (sampling rate, counter-poll interval) — cross-vendor standard, confirmed present across D-Link/FASTPATH/LANCOM/Foundry per atlas | structured | core for sFlow-capable switches; NetFlow/IPFIX is collector-config only, vendor-specific (Comware flow-template, Huawei NetStream, IOS-XE Flexible NetFlow) — no standard MIB in corpus |
| WAN uplink status | Meraki `getOrganizationUplinksStatuses`: `status`∈{active,connecting,failed,"not connected",ready}, `interface`∈{wan1,wan2,wan3,cellular}, `ip`,`gateway`,`publicIp`,`primaryDns`/`secondaryDns` (verified via fetch) | enum+string | core for gateway/appliance-class devices |
| WAN loss/latency | Meraki `getOrganizationDevicesUplinksLossAndLatency`: `timeSeries[].{ts, lossPercent, latencyMs}` (verified via fetch) | percent, ms | core, the basis of the shared perf primitive below |
| Cellular signal | Meraki uplink status `signalStat.{rsrp, rsrq}` for cellular (verified via fetch, field-name level only — value range not in that fetch) | dBm/dB | core for cellular-equipped gear |
| DSL line status | VDSL2-LINE-MIB `xdsl2LineStatusXtur/Xtuc` (BITS), `...AttainableRateDs/Us`, `...ActAtpDs/Us` (verified in vendored MIB) | bitmask / bps / dB | niche (LANCOM only, per atlas) |
| PON ONU state | ITU-T G.984.3 O1-O7 — not independently fetched for this dossier; carried from the atlas's `[ifIndex,onuIndex]` keying note | enum | niche, out of FlowSeer's device classes per atlas 10-wan-access |

## 4. Proposed primitives

All under `net/` per the primitive/entity split; ref-free, peers named by
interface `name` or device-scoped rule-set `name`, no triad suffixes (facet/
settings only). Package names below are `flowseer.net.<pkg>.v1`.

**`net/packet/v1` (extend, don't duplicate).** DSCP already landed
(`ip_dscp.proto`). Add a registry pass-through `IpPrecedence` only if a
producer needs the legacy 3-bit field distinct from DSCP CS values — skip
otherwise, DSCP subsumes it. Do **not** add an IEEE 802.1p PCP enum here:
PCP is a 3-bit VLAN-tag field, which is `net/switching` territory (it lives
in the tag stack), not a packet-header registry — flag as an open question
for the planner rather than deciding it in this dossier, since `net/switching` belongs to `05-l2-and-instances.md`.

**`net/qos/v1` — new package, core.** The direction record explicitly killed
an earlier unused `net/qos/v1` placeholder (2026-08-30 amendment) for being
empty, not for being the wrong idea. Propose:
- `TrustMode` enum (normalized, not pass-through — no registry owns
  "trust CoS vs DSCP vs untrusted"): `TRUST_MODE_UNSPECIFIED=0`,
  `TRUST_MODE_UNTRUSTED`, `TRUST_MODE_COS`, `TRUST_MODE_DSCP`. Per §06-qos
  this is the single highest-value QoS fact.
  `QosFacet.trust = TrustMode` on the interface.
- `QosClassifierTerm{name, match FilterMatch-like or reuse net/filter's
  FilterMatch, set_dscp IpDscp, set_cos uint32 (0-7, predefined rule),
  police_rate_bps uint64, police_burst_bytes uint64}` — reuses
  `net/packet` match atoms exactly as `net/filter` does; do not invent a
  second match type.
- `QosQueue{queue_id uint32, discipline QueueDiscipline (normalized:
  DROP_TAIL/RED/WRED, from openconfig-qos-types, verified enum names above),
  min_rate_bps uint64, max_rate_bps uint64}` — device-scoped table, keyed by
  interface name + queue id (table, not facet — a device reports N queues
  per port).
- Defer: DiffServ's functional-block pointer-chain model (rejected by nearly
  every vendor per atlas), full TSN suite (802.1Qav/Qbv/Qbu/CB, no consumer
  device class needs it today), WRED profile objects (bundle into
  `QosQueue` fields instead of a separate profile entity until a consumer
  needs sharing one profile across many queues).

**`net/filter/v1` (extend existing).** Landed shape already covers RFC 8519's
core (name+type-implicit L3/L4 match+action, direction, interface binding).
Gaps to flag for the planner, not silently fix here (owned by another
domain's landed package):
- No L2 match (RFC 8519 `eth`: src/dst MAC, mask, ethertype) — `FilterMatch`
  is IP/transport/ICMP only today.
- No `acl-type` discriminator (`ipv4`/`ipv6`/`eth`/mixed) — current
  `FilterMatch` mixes families in one flat message rather than a typed
  variant; this is the same shape RFC 8519's atlas note (§06-qos "exact
  observations and match expressions are different types") warns about,
  and may be worth a follow-up reshape under the pre-stability breaking-change
  license, but it is out of this dossier's scope to redesign a landed package.
- No time-range condition (`FASTPATH-TIMERANGE-MIB` pattern, §06-qos
  "cross-cutting: time ranges"). Propose a small `net/schedule/v1` (or fold
  into an existing package) holding `TimeRange{name, absolute windows,
  periodic weekday/time windows}` referenced by name from ACLs, QoS policers,
  and PoE schedules — model once, per the atlas's explicit recommendation.

**`net/perf/v1` — new package, core, one shared performance primitive.** One message covers WAN uplink health, cellular link quality, and any
future "how good is this path" question:
```
message PathQuality {
  google.protobuf.Duration latency = 1;   // round-trip, unset = not measured
  google.protobuf.Duration jitter = 2;    // unset = not measured
  float loss_percent = 3;                 // 0-100; unset field = not measured (presence, not sentinel -1)
}
```
Loss/latency/jitter is exactly the Meraki `lossPercent`/`latencyMs`
time-series shape (verified above) generalized with a jitter field the
atlas's cellular/WAN section implies but Meraki's fetched schema does not
carry on this endpoint — mark jitter as "reserved for a source that reports
it" rather than invented. `float loss_percent` needs a protovalidate range
rule `[0, 100]` — a percent is a domain scalar candidate for a predefined
rule (`(buf.validate.field).float.(percent)`) if reused across ≥2 messages;
with only this one user for now, plain `gte/lte` suffices, promote to a
predefined rule when a second consumer appears.

**`net/wlan/v1` extension (reserved package per direction record) —
`SignalQuality`, usable by both cellular and Wi-Fi.**
```
message SignalQuality {
  google.protobuf.FloatValue rssi_dbm = 1;   // wrapper rejected per convention 3 — use presence on a plain float instead: float rssi_dbm = 1;
  float rsrp_dbm = 1;   // absolute power, dBm; unset = not reported
  float rsrq_db = 2;    // ratio, dB; unset = not reported
  float sinr_db = 3;    // ratio, dB; unset = not reported — value range unverified for this dossier
}
```
(Correction inline: no wrapper messages per convention 3 — plain scalar
fields with presence.) This message is deliberately *not* WLAN-specific
despite living near `net/wlan`; RSRP/RSRQ/SINR are the LTE/cellular metrics
(3GPP TS 36.133, cited above) and RSSI/SNR the Wi-Fi ones (802.11), and one shape should serve both. Recommend the planner decide the
actual home package (`net/wlan` is wireless-Ethernet-adjacent; cellular is
WAN-adjacent) — flagged as an open question in §7 rather than decided here,
since `net/wlan` is reserved and this dossier does not own that boundary.

**`net/aaa/v1` — new package, core-ish, narrower than the atlas's full AAA
survey.** Model only what is near-universal and device-observable, not the
domain/scheme model (Comware/Huawei-only, per atlas) or per-command
authorization (ProCurve-only):
```
message AaaServer {
  oneof protocol {
    option (buf.validate.oneof).required = true;
    RadiusServer radius = 1;
    TacacsServer tacacs = 2;
  }
}
message RadiusServer {
  flowseer.net.addr.v1.IpAddress address = 1 [(buf.validate.field).required = true];
  uint32 auth_port = 2;   // UDP port; unset = vendor default (1812 per RFC 2865, not hardcoded here)
  uint32 acct_port = 3;
}
message TacacsServer {
  flowseer.net.addr.v1.IpAddress address = 1 [(buf.validate.field).required = true];
  uint32 port = 2;        // TCP port; unset = vendor default (49)
}
```
Do not model RADIUS/TACACS+ shared secrets here — that is `model/credential`
territory (a secret is identity-adjacent, not a ref-free value), and this
package must not import it (would break the primitive/entity line). Defer a
`AaaServerStats` (request/response/timeout counters from RFC 4668) until a
consumer needs server health, not just server identity.

**`net/dot1x/v1` — new package, the atlas's highest-value unmodelled
entity.** One row per authenticated session, keyed `(interface name, mac)`
with VLAN as a column — the shape the atlas says covers IEEE/ProCurve/Cisco
SMB/Aruba and merges D-Link's two-VLAN-one-MAC edge case:
```
message Dot1xSession {
  string interface_name = 1 [(buf.validate.field).required = true];
  flowseer.net.addr.v1.MacAddress mac = 2 [(buf.validate.field).required = true];
  uint32 vlan_id = 3 [(buf.validate.field).uint32.(vlan_id) = true]; // reuse net/switching's predefined rule
  string username = 4;               // unset = MAB/no identity (e.g. MAC-only auth)
  Dot1xMethod method = 5;            // normalized: DOT1X, MAB, WEB_AUTH
  bool authorized = 6;               // device's controlled-port-status answer
}
```
This is a **table** (device-scoped, per Facet-vs-table rule), not a facet —
sessions do not belong to one interface's identity the way an Ethernet
speed does; they are rows discovery/topology query device-wide, same
argument the direction record already made for FDB and the neighbor cache.

**`net/flowexport/v1` — new package, core for sFlow-capable devices, thin
for NetFlow/IPFIX (config-only, not a wire-format decoder).** Per §06-qos
"this is collector configuration, not device state":
```
message SflowSettings {
  flowseer.net.addr.v1.IpAddress collector = 1 [(buf.validate.field).required = true];
  uint32 collector_port = 2;
  uint32 sampling_rate = 3;   // 1-in-N; unset = sFlow not configured for this interface
  google.protobuf.Duration counter_poll_interval = 4;
}
```
Do **not** attempt a cross-vendor IPFIX/NetFlow template model in v1 — no
standard MIB exists (confirmed: IPFIX-MIB is upstream-only, absent from the
vendored corpus), and the atlas found every vendor doing something different
(Comware's field-offset templates vs Huawei's NetStream vs Cisco's Flexible
NetFlow monitors/records/exporters). A future `net/flowexport` addition for
IPFIX/NetFlow record decode is a distinct, larger effort (would need the
IANA IE registry as its own leaf enum, likely `net/packet`-adjacent) —
explicitly deferred, not modelled here.

**`net/syslog/v1` — new package, core, FlowSeer's Event source per atlas.**
This is a *primitive* (a log-line shape any producer emits), distinct from
the eventual `SyslogEvent` entity/event envelope another package would carry:
```
message SyslogFacility {  // registry pass-through, RFC 5424 §6.2.1
  // 0-23; kernel=0 ... local7=23. Zero is a real registry value (kernel).
}
enum SyslogSeverity {      // registry pass-through, RFC 5424 §6.2.1
  SYSLOG_SEVERITY_EMERGENCY = 0;
  SYSLOG_SEVERITY_ALERT = 1;
  SYSLOG_SEVERITY_CRITICAL = 2;
  SYSLOG_SEVERITY_ERROR = 3;
  SYSLOG_SEVERITY_WARNING = 4;
  SYSLOG_SEVERITY_NOTICE = 5;
  SYSLOG_SEVERITY_INFORMATIONAL = 6;
  SYSLOG_SEVERITY_DEBUG = 7;
}
```
`SyslogSeverity` is a genuine pass-through (RFC assigns 0=Emergency, a real
value, same shape as `IpDscp`). `SyslogFacility` as an enum would need 24
named values plus 8 "local" aliases repeated at 16-23 — consider a plain
`uint32` with a `[0,23]` protovalidate range instead of an enum, since the
facility names carry little semantic weight FlowSeer consumers act on
(unlike DSCP's AF/EF classes); flag as an open question rather than decide.
Do not model the message body, structured data, or RFC 5424 header fields
here (hostname, app-name, timestamp) — those belong wherever the Event
entity/envelope lands, this package supplies only the two registries.

**Explicitly not modelled as `net/` primitives now (defer to later or to an
entity layer), answering which deserve a package now and which later:**
- **ACL/firewall session tables, PKI/certificate values, port-security**:
  niche today per the provider matrix; certificates in particular need an
  identity-bearing home (a device's cert is arguably `model/inventory`-
  adjacent state, not primitive) — defer to the planner's entity design.
- **DSL/PON/DOCSIS**: atlas explicitly flags these mostly out of FlowSeer's
  device classes; if LANCOM DSL WAN interfaces are in scope, a minimal
  `net/dsl/v1` with just `Xdsl2LineStatus`-equivalent bitmask + attainable
  rate is cheap later, not needed now.
- **BRAS/PPPoE, Fibre Channel, wan-serial (TDM/ATM)**: out of scope, no
  primitive proposed.
- **IPsec/WireGuard tunnel status**: in scope only if routing does not cover it, and the routing/net-instance domain is reserved (`net/routing`, per direction
  record) and the atlas's routing-policy section (05-ip.md) may
  already claim tunnels as a routing-adjacent concept; flagged as an open
  question for the planner to resolve ownership, not decided here.

## 5. Entity candidates (`model/`)

- **AaaServerConfig entity?** — No. `AaaServer` above is a Primitive (no
  ref, named by address+port like `net/filter`'s rule sets are named by
  string). An entity would only be needed if AAA server *bindings* per
  device needed independent lifecycle tracking beyond "the device reports
  this list" — not evidenced in the corpus.
- **Dot1xSession** — stays a Primitive table (per §4), reported wholesale
  by State polls; no Config side (nobody configures individual sessions,
  only the port's dot1x admin mode, which is a Config concern on the
  Interface entity/facet, out of this domain's scope).
- **Certificate** — the one candidate that plausibly deserves entity
  status later: a device's own TLS/SSH server certificate has an identity
  (subject+serial+fingerprint) an operator might want alerted on expiry
  independent of any single poll. Not proposed now; flagged in §7.
- **SyslogEvent** — belongs to the `event/` root per the existing tree
  (`event/access/v1` is the precedent), not `model/`, and not this dossier's package to design: `ietf-alarms` belongs to `04-platform-system.md`.
- Nothing else in this domain crosses the primitive/entity line: QoS
  policy, filter rules, flow-export settings, and WAN/cellular signal
  quality are all device-reported values with no independent identity,
  tenancy, or lifecycle of their own — they hang off the Interface facet
  or a device-scoped table, same as everything else in `net/`.

## 6. Traps

- **DSCP vs 802.1p PCP vs IP Precedence**: three different code points, three
  different bit widths (6/3/3), living in different packet layers (IP header
  vs VLAN tag vs legacy IP header reuse). `net/packet` already owns DSCP;
  PCP is a VLAN-tag field (`net/switching`'s territory) — do not add a PCP
  enum to `net/packet` by analogy with DSCP.
- **IPFIX IE numbers ≠ NetFlow v9 field numbers**, despite numeric overlap
  for early fields (both use 1/2/4/7/8/10/11/12/14 for the same concepts) —
  they diverge further in (e.g. timestamp encoding, epoch, field width) and
  are governed by separate IANA registries. A primitive that hard-codes "IE
  8" must say which registry.
- **dBm vs dB**: RSRP/RSSI are absolute power (dBm); RSRQ/SINR/SNR are
  ratios (dB). Never let one `SignalQuality` field silently serve both —
  keep them as separate named fields (done above), not a generic
  `signal_strength` scalar with a units enum.
- **Loss percent range and sentinel**: Meraki's `lossPercent` is a plain
  float; under FlowSeer's explicit-presence rule, "0% loss" and "not
  measured" must be distinguishable via field presence, never via a `-1`
  sentinel — confirmed no vendor API needs a sentinel once presence exists.
- **dot1x port numbering**: `dot1xPaePortNumber` is *not* always `ifIndex`
  (atlas: "on some it is `dot1dBasePort`") — resolving this is the mapper's
  job per the direction record's "one interface identity" rule; the
  `Dot1xSession.interface_name` primitive must already be resolved to the
  normalized name before it reaches this schema, never carry a raw MIB
  index.
- **ACL exact-match vs match-expression conflation** (atlas, citing
  `hwAclBasicRuleTable` vs `hwAclAdvancedRuleTable`): an *observed* rule a
  device reports and a *match expression* a human authors are different
  types even when they reuse the same field names — worth remembering if
  `net/filter/v1` is ever split into Config/observed-State style shapes
  (it currently is not, being a pure Primitive).
- **TACACS+ session id "cryptographically strong random"** (RFC 8907 §4.1)
  — this is a wire-protocol requirement on the *device/server*, not
  something FlowSeer's schema enforces; do not add a validation rule
  implying FlowSeer generates or checks session-id randomness.
- **AAA "domain model"** (Comware/Huawei ISP-domain-keyed AAA): a
  normalized `AaaServer` list loses this distinction entirely — acceptable
  lossiness per the atlas's own conclusion ("nobody else does this"), but
  worth a one-line note in the package README so a future Comware/Huawei
  mapper author doesn't assume the schema round-trips their domain scheme.
- **RSRP/RSRQ range citation is secondary-sourced**, not fetched directly
  from the 3GPP PDF for this dossier (the FTP directory returned only a file
  listing) — treat the exact clause numbers (§9.1.4, §9.1.7) as attributed,
  not independently verified; a planner citing them in a schema comment
  should re-verify against the actual TS 36.133 PDF before treating the
  numbers as load-bearing for a validation rule (e.g. min/max range checks).

## 7. Open questions for the planner

1. **Where does `SignalQuality` live?** — `net/wlan/v1` (reserved, wireless-
   adjacent) vs a new `net/cellular/v1` vs a shared leaf under `net/phy`.
   Both cellular (LTE RSRP/RSRQ/SINR) and Wi-Fi (RSSI/SNR) need it; this
   dossier proposes the shape, not the package.
2. **Who owns tunnel/VPN status (IPsec/WireGuard)?** In scope here only if routing does not cover it. Decide it together with the atlas's `05-ip.md` routing-policy section and the reserved `net/routing` package.
3. **Is `SyslogFacility` an enum or a plain ranged `uint32`?** — Facility
   names carry less actionable semantics than DSCP's AF/EF classes; flagged
   rather than decided.
4. **Should `net/filter/v1.FilterMatch` be reshaped into a typed
   `acl-type`-style variant** (L2-only / L3v4 / L3v6 / mixed) to match RFC
   8519's own warning that exact-observation and match-expression are
   different types? This is a landed package outside this dossier's scope
   to redesign, but the gap (no L2 match, no time-range condition) is real
   and the pre-stability breaking-change license would allow it.
5. **Certificate/PKI as a future entity** — worth an accepted-direction
   note now (cert expiry is a real, silent operational fault per the
   atlas) even if no package lands yet.
6. **NetFlow v9 / IPFIX record decode** — explicitly deferred in §4; is a
   collector-config-only `net/flowexport/v1` (sFlow + generic collector
   target) sufficient for v1, with full IE-registry modelling pushed to a
   "flow ingestion source" milestone the atlas calls "a different plane
   entirely"?
