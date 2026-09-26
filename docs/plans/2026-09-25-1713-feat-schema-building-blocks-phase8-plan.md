---
title: Schema Building Blocks Phase 8, QoS, AAA, Flow Export, and Cellular - Plan
type: feat
date: 2026-09-25
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: code
parent: docs/plans/2026-09-25-1713-feat-schema-building-blocks-plan.md
---

# Schema Building Blocks Phase 8, QoS, AAA, Flow Export, and Cellular - Plan

> Implemented. 5 units, 2026-09-26T12:03Z to 2026-09-26T12:11Z. Each unit
> ran its targeted `Verify:` command, and the phase closed with one
> targeted run over the union of the five units' paths against `main`; no
> `--full` run, which builds and race-tests `generated/go/yang` and
> exhausts host memory. The verification-dirty marker still holds the
> Bash-mutation line `buf generate` leaves, which only `--full` clears;
> every targeted run regenerated `generated/go/proto` and diffed it clean.

## Goal

The edge services a switch or gateway runs have a place in the schema:
`net/qos/v1` holds the per-interface trust mode, classifiers with their
terms and policers, and per-interface queues; `net/aaa/v1` holds RADIUS and
TACACS+ server identity; `net/flow/v1` holds sFlow receivers, samplers,
and pollers and NetFlow/IPFIX exporter targets; `net/cellular/v1` holds a
cellular interface's radio technology, band, identifiers, serving cell,
and signal quality; and `net/filter` gains L2 match terms (MAC with mask,
EtherType, PCP) plus a DSCP term, so QoS classifiers reuse `FilterMatch`
instead of a second match type. The means is five packages' worth of rows
and enums, each pinned by conformance validation tests, plus the README,
allowlist, and conventions lines the gates hold them to, and one guard in
the simulator's filter translation.

Stop condition: a targeted source whose QoS trust, queue, or cellular
facts cannot be resolved to an interface name, or a cellular source that
reports a conformant RSRP, RSRQ, or SINR outside the bounds below. Either
means the row keys or the 3GPP bounds chosen here are wrong, and the phase
stops for a re-cut before it lands.

## Decisions

The record's rules 1 (units), 3 (keys and string bounds), 4 (network
instance), 5 (facets and rows), and 6 (no union messages) govern
([record](../architecture/2026-09-25-schema-building-blocks-direction.md)).
The re-plan starts from a tree holding phases 1 to 6: phase 8's only
`After:` is phase 1, whose last commit `ed9f129e` is an ancestor of
`HEAD`, and the parent's U8 `Landed:` is empty.

- **L2 match terms are fields of `FilterMatch`, with a 48-bit MAC and an
  optional mask.** `FilterMatch` gains `ether_type = 8`, `src_mac = 9`,
  `dst_mac = 10`, `pcps = 11`, and `dscps = 12`. Why: RFC 8519 §4.2
  (`ietf-packet-fields`) defines the Ethernet match as
  `destination-mac-address`, `destination-mac-address-mask`,
  `source-mac-address`, `source-mac-address-mask`, and `ethertype`, and
  its IP header fields include `dscp` (`inet:dscp`,
  https://www.rfc-editor.org/rfc/rfc8519.html, fetched for this plan);
  OpenConfig's `ethernet-header-config` carries the same five leaves
  (`spec/yang/openconfig/openconfig-packet-match.yang:113-146`) and its IP
  fields carry `dscp` and `dscp-set` (`:257-268`). The OpenConfig ACL and
  the OpenConfig QoS classifier both reuse that one grouping
  (`openconfig-acl.yang:453`,
  `spec/yang/cisco/iosxe/2611/openconfig-qos-elements.yang:229-232`), which
  is the record's reason for one match type. The MAC is `Eui48Address`,
  not `MacAddress`: `yang:mac-address` is six octets
  (`spec/yang/ietf/ietf-yang-types.yang:409-411`), and an EUI-64 mask on an
  Ethernet header has no meaning. `MacMatch` holds `address` (required)
  and `mask` (absent means every bit is compared); bits of the address
  outside the mask are ignored and not rejected, because neither source
  requires them clear and a device reporting them would otherwise fail
  the whole rule set (record rule 3).
- **PCP and DSCP are match terms because QoS classifies on them.**
  EdgeSwitch's DiffServ class rules match CoS, destination MAC with mask,
  DSCP, and EtherType as separate objects
  (`spec/mib/ubiquiti/edgemax/EdgeSwitch-QOS-DIFFSERV-PRIVATE-MIB:624`,
  `:674`, `:683`, `:704`, `:923`). `pcps` is a repeated `uint32` with the
  landed `vlan_pcp` rule, and `dscps` a repeated `IpDscp` with the
  `ip_dscp` rule, both `unique`; a non-empty list matches a packet whose
  value is any member, the semantics of the existing prefix and port
  lists and of OpenConfig's `dscp-set`. The `vlan_pcp` rule lives in
  `net/switching`, so the `net/filter` allowlist row gains `net/switching`
  (no cycle: `net/switching` imports `addr`, `packet`, `key`).
- **The simulator refuses a rule set it cannot evaluate rather than
  widening it.** `src/common/netsim/vswitch/netmodel/netmodel.go:1906-2047`
  translates `FilterMatch` field by field into the IP-layer
  `filter.Match` (`src/common/netsim/vswitch/filter/config.go:84-92`),
  which has no L2, PCP, or DSCP term. Translating a rule without them
  would widen it, and dropping the rule would let a later rule decide
  instead; either way the simulated verdict is wrong. A set any of whose
  rules carries one of the new terms is left out of the filter
  configuration with an `IssueSkippedUnsupportedFacet` issue, and a
  binding to it reports the same code instead of `IssueMissingFilterSet`.
  It is the only non-test Go consumer of `net/filter`
  (`grep -rln filterv1\. src`).
- **QoS trust mode is a FlowSeer-normalized enum with five values.**
  `TrustMode`: `UNSPECIFIED = 0`, `UNTRUSTED = 1`, `COS = 2`, `DSCP = 3`,
  `IP_PRECEDENCE = 4`, `COS_DSCP = 5`. Why: no registry owns it, and the
  sources report `untrusted/trustDot1p/trustIpPrecedence/trustIpDscp`
  (`spec/mib/ubiquiti/edgemax/EdgeSwitch-QOS-COS-MIB:286-293`),
  `cos/dscp` (`spec/mib/dlink/DLINKSW-QOS-MIB:907-911`), and
  `cos/dscp/cos-dscp` (`spec/mib/cisco/smb/CISCOSBqosclimib.mib:379-399`,
  cos-dscp meaning PCP for L2 traffic and DSCP for L3). Untrusted, CoS,
  and DSCP are the three every source has; IP precedence and cos-dscp are
  kept because mapping them to one of the three would report a trust the
  port does not apply. OpenConfig QoS has no trust leaf at all. The enum
  names the vendor mode (CoS); the fields that carry the value name the
  header field (`pcp`), as the landed `vlan_pcp` rule does.
- **Per-interface QoS is a row keyed by interface name, not a facet on
  `Interface`.** `QosInterface{interface_name, trust_mode,
  input_classifier, output_classifier, queues}`. Why: OpenConfig models
  QoS interface bindings as a device-level list keyed by `interface-id`,
  outside `/interfaces`
  (`spec/yang/cisco/iosxe/2611/openconfig-qos-interfaces.yang:984-985`),
  as the landed STP `PortState` is a per-interface row in its own package;
  and phase 1 wrote the `net/qos` and `net/cellular` allowlist rows with
  `net/key` and gave `net/interface` no row to import either package
  (`test/conformance/proto/layering_test.go:51`, `:54`, `:63`), so a facet
  would edit another package's row, which the parent reserves to that
  package. The row omits `network_instance`: its key is scoped by an
  interface, as `portaccess.v1.Session`'s is (conventions doc, Units and
  keys). Cost if wrong: see Open questions.
- **Classifiers are device-scoped named rows like `FilterRuleSet`.**
  `Classifier{name, terms}` and `ClassifierTerm{id, match, forwarding_group,
  set_dscp, set_pcp, policer}` follow OpenConfig's
  `classifiers/classifier/terms/term[id]` with `conditions` and
  `actions/{target-group, remark}` (`openconfig-qos-elements.yang:179-269`,
  remark `set-dscp`/`set-dot1p` at `:1370-1404`). `set_dscp` takes the
  `ip_dscp` rule, so a term setting DSCP 64 fails. `QosInterface` names
  its classifiers by `name`, as `FilterFacet` names rule sets. The
  classifier `type` enum (IPV4/IPV6/MPLS/ETHERNET, `:281-307`) is left
  out: the `FilterMatch` fields present already say which headers a term
  reads, and OpenConfig's two copies of that enum disagree
  (`openconfig-qos-interfaces.yang:276-303`).
- **A policer is committed and peak rate and burst, in canonical units.**
  `Policer{committed_rate_bps (required), committed_burst_bytes,
  peak_rate_bps, peak_burst_bytes}`, from OpenConfig's one-rate-two-color
  `cir`/`bc` and two-rate-three-color `cir`/`pir`/`bc`/`be`, which are
  already bits per second and bytes
  (`openconfig-qos-elements.yang:869-943`, `:1023-1101`). CEL rules: a
  present peak rate is at least the committed rate, and a peak burst needs
  a peak rate. Conform, exceed, and violate actions and the percentage
  variants (`cir-pct`) stay out; a mapper reporting a percentage-only
  policer leaves `policer` absent.
- **A queue is identified by name, number, or both.** `Queue{name,
  queue_id, discipline, min_rate_bps, max_rate_bps,
  min_bandwidth_basis_points, max_bandwidth_basis_points}`. OpenConfig
  keys queues by string `name` with an optional `uint8 queue-id`
  (`openconfig-qos-elements.yang:609-624`); EdgeSwitch numbers them
  `0..n-1` (`EdgeSwitch-QOS-COS-MIB:573-585`) and states bandwidth as a
  whole `Percent` (`:65-69`, `:605`, `:635`), hence the basis-point pair
  beside the bit-rate pair. A CEL rule requires `name` or `queue_id`;
  `queue_id` takes `lte 255`. `QueueDiscipline`: `UNSPECIFIED`,
  `DROP_TAIL = 1`, `RED = 2`, `WRED = 3` (`QOS_QUEUE_TYPE`,
  `openconfig-qos-types.yang:56-92`). RED and WRED thresholds stay out
  (OpenConfig's WRED body is empty, `openconfig-qos-elements.yang:527-551`).
- **`net/aaa` is server identity only; no secret field exists.** RADIUS
  and TACACS+ shared secrets are credential material: they live in the
  secret store behind a credential ref
  (`docs/architecture/2026-08-20-device-service-and-inventory-direction.md:153`)
  and are carried only as `model/credential` material
  (`docs/architecture/2026-08-20-network-model-structure-direction.md:60`).
  OpenConfig's `secret-key` and `secret-key-hashed`
  (`spec/yang/openconfig/openconfig-aaa-radius.yang:106-118`,
  `openconfig-aaa-tacacs.yang:99-111`) have no counterpart, and a
  conformance test fails if any `net/aaa` field name contains `secret`,
  `key`, `password`, or `passphrase`. The package README and the
  conventions doc say where the secret lives instead.
- **One `AaaServer` row with a required protocol oneof.** Common fields
  are `address` (required `IpAddress`), `name`, `group_name`, `priority`,
  `timeout`, `source_address`, `status`; the arms are `RadiusServer
  radius = 10` (`auth_port`, `acct_port`, `retransmit_attempts`) and
  `TacacsServer tacacs = 11` (`port`), numbered from 10 because the oneof
  sits beside other fields (conventions doc, Field numbering). Why:
  OpenConfig lists servers by `address` under a typed server group with
  the common `name`, `address`, `timeout` and per-protocol containers
  (`spec/yang/openconfig/openconfig-aaa.yang:172-195`, `:254-301`;
  `openconfig-aaa-radius.yang:88-132`; `openconfig-aaa-tacacs.yang:88-118`);
  IOS-XE keys server statistics by group, address, and ports
  (`spec/yang/cisco/iosxe/2611/Cisco-IOS-XE-aaa-oper.yang:1786-1796`) and
  reports `alive`/`dead` (`:537-551`); D-Link reports a numeric priority,
  lower first (`spec/mib/dlink/DLINKSW-AAA-SERVER-MIB:156-167`, `:185`).
  The address is an IP address because no source names a server by
  hostname (OpenConfig `oc-inet:ip-address`; `RADIUS-AUTH-CLIENT-MIB`
  `IpAddress`, `spec/mib/ietf/RADIUS-AUTH-CLIENT-MIB:102-103`). Ports take
  `lte 65535` and nothing more, the MIBs' `0..65535`
  (`RADIUS-AUTH-CLIENT-MIB:112-113`); `retransmit_attempts` takes
  `lte 255` (OpenConfig `uint8`). `AaaServerStatus`: `UNSPECIFIED`,
  `ALIVE = 1`, `DEAD = 2`. Both arms set is unrepresentable, so the tests
  check the empty case
  (`docs/solutions/conventions/a-oneof-both-arms-set-is-unrepresentable-so-validate-the-empty-case.md`).
- **`net/flow` is collector configuration; decoding is out.** sFlow is
  three rows mirroring `SFLOW-MIB` (`spec/mib/ietf/SFLOW-MIB`):
  `SflowReceiver{index, address, port, max_datagram_size_bytes,
  datagram_version}` (`sFlowRcvrTable`, `:241-379`; index `1..65535`,
  port default 6343 at `:358-365`); `SflowSampler{interface_name,
  instance, receiver_index, sampling_rate, max_header_size_bytes}`
  (`sFlowFsTable`, `:385-480`); `SflowPoller{interface_name, instance,
  receiver_index, interval}` (`sFlowCpTable`, `:487-564`). The MIB's
  disabled encodings (receiver address `0.0.0.0`, sampling rate 0, poll
  interval 0, receiver 0) mean "not configured", so a mapper omits the
  row, and the schema requires `sampling_rate >= 1`, `interval >= 1s`, and
  `receiver_index >= 1`. Only interface data sources are kept; a VLAN or
  entity data source has no interface name and is out. NetFlow and IPFIX
  are one `FlowExporter{name, destination, port, protocol,
  source_interface_name, dscp, template_refresh}` row from IOS-XE's
  exporter (`spec/yang/cisco/iosxe/2611/Cisco-IOS-XE-flow.yang:4400-4470`,
  `:4709-4745`; name `1..60` at `:5071-5080`): `FlowExportProtocol` is
  `UNSPECIFIED`, `NETFLOW_V5 = 1`, `NETFLOW_V9 = 2`, `IPFIX = 3` (`:4462-4470`),
  and `template_refresh` takes `1s..86400s` (`:4714-4728`). Huawei
  NetStream exposes no collector objects
  (`spec/mib/huawei/HUAWEI-NETSTREAM-MIB:72-166`), so IOS-XE is the one
  typed source. Flow monitors, records, and NetFlow samplers stay out:
  they describe the record format, the half of the domain this phase does
  not decode. The `net/flow` allowlist row gains `net/key` for
  `interface_name`.
- **Configuration rows carry no network instance.** `Classifier`,
  `AaaServer`, `SflowReceiver`, and `FlowExporter` are keyed by name,
  address, or index, not by interface, and carry no `network_instance`.
  Why: rule 4's reason is that a forwarding table's key is ambiguous
  without its instance, which a classifier name or a collector address is
  not; the landed `FilterRuleSet` sets the precedent. The VRF a device
  reaches a collector through (IOS-XE's exporter `vrf`,
  `Cisco-IOS-XE-flow.yang:4412-4418`) is left out, because OpenConfig AAA
  and `SFLOW-MIB` have none and a required field would force a mapper to
  claim `default` it cannot see. Cost if wrong: one required field per
  row; see Open questions.
- **Cellular facts are one row per cellular interface.**
  `CellularInterface{interface_name, technology, band, imei, imsi, iccid,
  mcc, mnc, cell_id, tracking_area_code, physical_cell_id, signal}`. Why:
  IOS-XE keys every cellular table by `cellular-interface`
  (`spec/yang/cisco/iosxe/2611/Cisco-IOS-XE-cellwan-oper.yang:1804-1841`),
  Meraki reports cellular as an uplink `interface` value (dossier 07,
  `docs/research/schema-building-blocks/07-qos-security-ops-wan.md:149`),
  and HH3C indexes by wireless card
  (`spec/mib/hp/hh3c/HH3C-3GMODEM-MIB:125`), which a mapper resolves to
  its interface. Unlike an 802.11 radio, which the structure record keeps
  off interfaces, every cellular source presents the modem as an
  interface. The row is keyed by an interface and omits
  `network_instance`, like `QosInterface`.
- **`RadioAccessTechnology` is generation-level.** `UNSPECIFIED`,
  `GSM = 1`, `UMTS = 2`, `CDMA2000 = 3`, `LTE = 4`, `NR = 5`. IOS-XE's
  `rat-technology` has 24 values that fold into these (GPRS and EDGE into
  GSM; HSPA variants into UMTS; LTE FDD and TDD into LTE; `nr5g-sa` into
  NR; `Cisco-IOS-XE-cellwan-oper.yang:387-527`), and HH3C's current
  connection (`HH3C-3GMODEM-MIB:251-274`) folds the same way, TD-SCDMA
  into UMTS. No targeted source distinguishes NR non-standalone, so NR has
  one value.
- **Identifiers are digit strings; MCC and MNC keep leading zeros.**
  `imei` matches `^[0-9]{15}$` (TS 23.003 §6.2.1: TAC 8, SNR 6, one spare
  or check digit; IMEISV is out); `imsi` matches `^[0-9]{6,15}$` (TS 23.003
  §2.2: at most 15 digits, MCC 3 plus MNC 2 or 3 plus MSIN); `mcc`
  matches `^[0-9]{3}$` and `mnc` `^[0-9]{2,3}$` (§2.2). These clauses were
  read through search results quoting the specification
  (https://www.arib.or.jp/english/html/overview/doc/STD-T63V12_00/5_Appendix/Rel13/23/23003-d50.pdf
  is the primary; secondary source, verify against TS 23.003). The MNC is
  a string because `01` and `1` are different networks and IOS-XE's
  `uint16` (`Cisco-IOS-XE-cellwan-oper.yang:1358-1367`) loses the
  difference. `iccid` matches `^[0-9]{1,22}$`: ITU-T E.118 is not fetched
  here, and 22 digits is a deliberately loose ceiling (not verified
  against ITU-T E.118) so that no real card fails; a mapper strips BCD
  `F` padding. `cell_id` is `uint64` (IOS-XE `:1403`),
  `tracking_area_code` `uint32` (`:1398`, `:1413`), `physical_cell_id`
  `uint32` (`:1246-1250`), `band` `uint32` (`:1204-1208`, `:1261-1265`),
  each unbounded, because no 3GPP range for them was fetched and a device
  value must not fail the row. MSISDN, APN, and GPS stay out.
- **Cellular signal quality is its own message with 3GPP report-mapping
  bounds.** `CellularSignal{rsrp_millidbm, rsrq_millidb, rssi_millidbm,
  sinr_millidb}`. It shares the record's rule-1 units with `net/wlan`'s
  `rssi_millidbm` and `noise_floor_millidbm`, not a message: the two
  field sets overlap in RSSI alone, and `net/measure` holds no dBm or dB
  type to import (its files are `basis_points.proto`, `sensor.proto`,
  `path_quality.proto`). The bounds are the union of the LTE and NR
  measurement report mapping ranges, in milli-units:

  | Field | Bound | Source |
  | --- | --- | --- |
  | `rsrp_millidbm` | `-156000..-30000` | TS 36.133 §9.1.4 (LTE, −156 to −44 dBm extended) and TS 38.133 Table 10.1.6.1-1 (NR SS-RSRP, −156 to −30 dBm), 1 dB steps |
  | `rsrq_millidb` | `-43000..20000` | TS 36.133 §9.1.7 (LTE, −34 to +2.5 dB, 0.5 dB steps) and TS 38.133 (NR SS-RSRQ, −43 to +20 dB, 0.5 dB steps) |
  | `sinr_millidb` | `-23000..40000` | TS 38.133 §10.1.16 (NR SS-SINR, −23 to +40 dB, 0.5 dB steps); the LTE RS-SINR range was not found and is assumed no wider |
  | `rssi_millidbm` | none | no report mapping for RSSI was found in either clause; unbounded, as `net/wlan`'s `rssi_millidbm` is |

  Every bound above is secondary source, verify against TS 36.133 / TS
  38.133: the 3GPP archive serves no extractable table (dossier 07,
  `07-qos-security-ops-wan.md:27-32`), and this plan read
  https://hicelltek.com/en/3gpp-rsrp-threshold-mapping/ (RSRP, both
  clauses), https://www.techplayon.com/5g-nr-sinr-measurement-and-its-mapping/
  (SS-SINR), dossier 07 (LTE RSRQ), and a search result quoting
  techplayon's SS-RSRQ page (NR RSRQ). Secondary sources disagree on the
  NR RSRP clause number (10.1.6.1 versus 10.1.2); the table cites the one
  that names a table. The report mapping saturates (reported value 0 means
  "below −156 dBm"), so a mapper writes the endpoint for a saturated
  value. Requirement 2's RSRQ of −19.5 dB is the classic LTE floor and
  passes.
- **WAN path quality stays `measure.v1.PathQuality`.** No message in this
  phase redefines latency, jitter, or loss, and none carries them: a WAN
  uplink row is not in the record's tree, and path quality is a property
  of the uplink, not of the cellular radio.
- Ruled: no predefined rule is added. Every bound here is used by one
  field, so it is a plain field rule; the extension-number table in
  `docs/code-style-proto.md` is unchanged (next free `UInt32Rules` number
  stays 50007). Cost if wrong: a later second user promotes it.
- Ruled: string bounds follow record rule 3. Names with no standard size
  (`Classifier.name`, term `id`, `forwarding_group`, queue `name`,
  `AaaServer.name`, `group_name`, classifier references on
  `QosInterface`) take `min_len 1`, `max_len 1024`; `FlowExporter.name`
  takes `max_len 60`, IOS-XE's `flow-name`, its only typed source.
- Ruled: the rule tests use `fieldCase` and `runFieldCases` from
  `test/conformance/proto/stp_rules_test.go`, asserting the violated field
  path or the CEL rule id, as phase 6 did. Why: a message failing another
  rule would otherwise pass for the rule under test.
- Ruled: `TestAaaCarriesNoSecret` walks every file the registry holds for
  the `flowseer.net.aaa.v1` package rather than a list of the generated
  `File_flowseer_net_aaa_v1_*` variables, and fails when it finds none.
  Why: a file added to the package later is walked without editing the
  test. Cost if wrong: one test body.
- Ruled: the rule tests share one `testNet1(host)` helper for
  `192.0.2.host` (RFC 5737) instead of a four-octet `ipv4`. Why: `unparam`
  rejects a parameter every caller passes as 192. Cost if wrong: one
  helper.

## Requirements

1. A RADIUS server with no address fails. Example: `AaaServer{radius:
   {auth_port: 1812}}` fails with `address: value is required`;
   `AaaServer{address: 192.0.2.10, radius: {auth_port: 1812, acct_port:
   1813}}` passes; `AaaServer{address: 192.0.2.10}` fails on the
   `protocol` oneof (`exactly one field is required`); `tacacs: {port:
   65536}` fails; `radius: {retransmit_attempts: 256}` fails; `timeout:
   -1s` fails.
2. A cellular signal with `rsrq_millidb = -19500` passes (RSRQ −19.5 dB).
   `rsrq_millidb -43500`, `rsrp_millidbm -29000`, `rsrp_millidbm -156001`,
   and `sinr_millidb 40500` each fail on their field; `rssi_millidbm
   -120000` passes.
3. A QoS classifier term that sets a DSCP of 64 fails. Example:
   `ClassifierTerm{id: "voice", set_dscp: 64}` fails with rule
   `enum.ip_dscp`; `set_dscp: IP_DSCP_EF` passes; `set_pcp: 8` fails;
   a `Classifier` with two terms of id `voice` fails its uniqueness rule;
   a `Policer{committed_rate_bps: 2000000, peak_rate_bps: 1000000}` fails;
   `Policer{peak_burst_bytes: 1500, committed_rate_bps: 1000000}` without
   `peak_rate_bps` fails; a policer without `committed_rate_bps` fails.
4. A filter rule matching destination MAC and EtherType passes validation.
   Example: `FilterRule{action: ACCEPT, match: {dst_mac: {address:
   01:80:c2:00:00:00, mask: ff:ff:ff:ff:ff:f0}, ether_type:
   ETHER_TYPE_LLDP}}` passes; `ether_type: 1500` fails (a length, not an
   EtherType); a `MacMatch` without `address` fails; a 5-octet address
   fails; `pcps: [8]` fails; `pcps: [5, 5]` fails; `dscps: [64]` fails.
5. `QosInterface` without `interface_name` fails. A queue with neither
   `name` nor `queue_id` fails its rule; `queue_id: 256` fails;
   `min_rate_bps: 2000, max_rate_bps: 1000` fails;
   `max_bandwidth_basis_points: 10001` fails; two queues with the same
   `queue_id` fail; `{interface_name: "1/0/1", trust_mode:
   TRUST_MODE_DSCP, queues: [{queue_id: 0, discipline:
   QUEUE_DISCIPLINE_WRED, min_bandwidth_basis_points: 1000}]}` passes.
6. sFlow and exporter rows reject the disabled encodings and a missing
   target. `SflowSampler{interface_name: "ge-0/0/1", receiver_index: 1,
   sampling_rate: 0}` fails; `sampling_rate: 4096` passes; an
   `SflowPoller` with `interval: 0s` fails; `SflowReceiver{index: 1}`
   without `address` fails; `index: 0` fails; `FlowExporter{name: "ex1",
   protocol: FLOW_EXPORT_PROTOCOL_IPFIX}` without `destination` fails;
   `dscp: 64` fails; `template_refresh: 86401s` fails; a 61-character
   name fails.
7. `CellularInterface` without `interface_name` fails; `imei:
   "35209900176148"` (14 digits) fails and a 15-digit IMEI passes; `mnc:
   "1"` fails and `mnc: "01"` passes; `mcc: "26"` fails; `imsi:
   "262011234567890"` passes.
8. No field in `net/aaa` carries a secret: a walk over the package's
   descriptors finds no field name containing `secret`, `key`, `password`,
   or `passphrase`, and the same walk over a synthetic message with a
   `secret_key` field reports it.
9. The simulator skips a filter rule set it cannot evaluate. A
   `FilterRuleSet` whose rule matches `dst_mac`, bound ingress on an
   interface with an IP facet, yields no binding for that set in the
   loaded filter configuration and an `IssueSkippedUnsupportedFacet`
   issue, and no `IssueMissingFilterSet` issue.
10. The conformance gates hold the new shapes: `TestImportOrder`,
    `TestProtoReadmeImports`, `TestProtoReadmeCoverage`,
    `TestKeyFieldsUseKeyRules`, `TestCanonicalUnitSuffixes`,
    `TestNoFloatingPointFields`, and
    `TestEveryDeclaredProtoPackageIsLinked` pass with `net/qos/v1`,
    `net/aaa/v1`, `net/flow/v1`, and `net/cellular/v1` present.

## Out of scope

- Mappers that fill any of these rows (parent, Out of scope). The only
  Go change is the simulator guard.
- QoS: DiffServ functional-block chains, TSN, WRED curves and RED
  thresholds, policer conform/exceed/violate actions, percentage-relative
  policers, scheduler policies, forwarding-group definitions, CoS-to-queue
  and DSCP-to-queue maps (`EdgeSwitch-QOS-COS-MIB:89-194`), and QoS
  counters.
- AAA: server statistics (RFC 4668 counters, round-trip time), the
  Comware/Huawei ISP-domain scheme, per-command authorization, method
  lists, and any secret, key, or hashed secret.
- Flow: NetFlow/IPFIX record decoding and the IANA IPFIX Information
  Element registry, flow monitors and records, NetFlow samplers, sFlow
  receiver ownership leases (`sFlowRcvrOwner`, `sFlowRcvrTimeout`), VLAN
  and entity sFlow data sources, and the exporter's VRF.
- Cellular: MSISDN, APN and bearer settings, GPS, SMS, NR non-standalone
  as a distinct technology, ARFCN, and the modem as a `Component` kind.
- A WAN uplink row and the Meraki uplink status enum.
- ACL type discriminators (RFC 8519 `acl-type`) and time-range conditions
  for filters.

## Units

### U1. L2, PCP, and DSCP match terms in `net/filter`, and the simulator guard

Files: `spec/proto/flowseer/net/filter/v1/{filter.proto,mac_match.proto,README.md}`,
`generated/go/proto/flowseer/net/filter/v1/`,
`spec/proto/flowseer/net/README.md`,
`spec/proto/flowseer/net/switching/v1/README.md`,
`test/conformance/proto/layering_test.go`,
`test/conformance/proto/filter_rules_test.go` (new),
`src/common/netsim/vswitch/netmodel/{netmodel.go,netmodel_test.go}`
After: none

Change:

- `mac_match.proto` declares `MacMatch`: `address = 1`
  (`flowseer.net.addr.v1.Eui48Address`, required) and `mask = 2`
  (`Eui48Address`; absent compares every bit; address bits outside the
  mask are ignored). The comment cites RFC 8519 §4.2 and says the width is
  `yang:mac-address`'s six octets.
- `FilterMatch` gains `ether_type = 8` (`EtherType`, `ether_type` rule),
  `src_mac = 9` and `dst_mac = 10` (`MacMatch`), `pcps = 11` (repeated
  `uint32`, `unique`, items `vlan_pcp`), `dscps = 12` (repeated `IpDscp`,
  `unique`, items `ip_dscp`). Each list's comment says a non-empty list
  matches any member, as the prefix lists do. The message comment keeps
  "Omitted or empty fields match any packet".
- `layering_test.go`: the `net/filter` row becomes `{"net/addr",
  "net/packet", "net/key", "net/switching"}`.
- The filter README: Imports becomes `net/addr, net/packet,
  net/switching`; the body names the L2 terms and their RFC 8519 §4.2 and
  OpenConfig `ethernet-header-config` sources, the EUI-48 choice, and
  that the simulator skips sets using them; Sources gains the OpenConfig
  packet-match module. `net/switching/v1/README.md` `Imported by:` gains
  `net/filter`. `net/README.md`'s `filter/v1/` line becomes "Packet
  filter rule sets, rules, L2 to L4 match terms, and the interface filter
  facet."
- `netmodel.go`: while building `setByName`, a set any of whose rules has
  `HasEtherType()`, `HasSrcMac()`, `HasDstMac()`, a non-empty `GetPcps()`,
  or a non-empty `GetDscps()` is recorded in an `unevaluable` set of names,
  not added to `setByName`, and reported once with
  `addSkippedAt(rootScope, "", "filter", fmt.Sprintf("rule set %q carries
  L2, PCP, or DSCP match terms the simulator does not evaluate", name),
  analysis.Incomplete, IssueSkippedUnsupportedFacet)`. In the binding
  loop, an `inSet` or `outSet` in `unevaluable` reports
  `IssueSkippedUnsupportedFacet` at the interface scope and is not bound,
  ahead of the missing-set check.

Tests: `filter_rules_test.go`, `TestFilterMatchL2Rules`, holds
Requirement 4. `netmodel_test.go`, `TestLoad_FilterSetWithL2MatchIsSkipped`,
holds Requirement 9 beside `TestLoad_FilterFacetMissingSet`
(`netmodel_test.go:1416`), and the existing filter tests pass unchanged.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/filter/v1 generated/go/proto/flowseer/net/filter/v1 spec/proto/flowseer/net/README.md spec/proto/flowseer/net/switching/v1/README.md test/conformance/proto/layering_test.go test/conformance/proto/filter_rules_test.go src/common/netsim/vswitch/netmodel`

### U2. `net/qos`: trust mode, classifiers, policers, and queues

Files: `spec/proto/flowseer/net/qos/v1/{trust_mode.proto,classifier.proto,policer.proto,queue.proto,queue_discipline.proto,qos_interface.proto,README.md}`,
`generated/go/proto/flowseer/net/qos/v1/`,
`spec/proto/flowseer/net/README.md`,
`spec/proto/flowseer/net/{filter,packet,key,measure,switching}/v1/README.md`,
`test/conformance/proto/layering_test.go`,
`test/conformance/proto/qos_rules_test.go` (new),
`docs/conventions/protobuf.md`,
`CONCEPTS.md`
After: U1 (the term tests use `FilterMatch.dscps`, and both units edit
`net/README.md`, the switching README's `Imported by:` line, and
`layering_test.go`)

Change:

- `trust_mode.proto` declares `TrustMode` with the five values of the
  Decisions, each value citing its vendor source object.
- `policer.proto` declares `Policer`: `committed_rate_bps = 1` (uint64,
  required), `committed_burst_bytes = 2` (uint64), `peak_rate_bps = 3`,
  `peak_burst_bytes = 4`, with CEL rules `policer.peak_rate_ordered`
  (`!has(this.peak_rate_bps) || !has(this.committed_rate_bps) ||
  this.peak_rate_bps >= this.committed_rate_bps`) and
  `policer.peak_burst_needs_peak_rate` (`!has(this.peak_burst_bytes) ||
  has(this.peak_rate_bps)`).
- `classifier.proto` declares `ClassifierTerm`: `id = 1` (required,
  1..1024), `match = 2` (`flowseer.net.filter.v1.FilterMatch`; absent
  matches every packet), `forwarding_group = 3` (1..1024; OpenConfig
  `target-group`), `set_dscp = 4` (`IpDscp`, `ip_dscp` rule), `set_pcp = 5`
  (`vlan_pcp` rule), `policer = 6`; and `Classifier`: `name = 1`
  (required, 1..1024), `terms = 2` (repeated, in the source's order),
  with CEL rule `classifier.term_ids_unique`
  (`this.terms.map(t, t.id).unique()`).
- `queue_discipline.proto` declares `QueueDiscipline`; `queue.proto`
  declares `Queue`: `name = 1` (1..1024), `queue_id = 2` (`lte 255`),
  `discipline = 3`, `min_rate_bps = 4`, `max_rate_bps = 5`,
  `min_bandwidth_basis_points = 6`, `max_bandwidth_basis_points = 7`
  (both `basis_points`), with CEL rules `queue.identified`
  (`has(this.name) || has(this.queue_id)`), `queue.rate_ordered`, and
  `queue.bandwidth_ordered` (min at most max when both present).
- `qos_interface.proto` declares `QosInterface`: `interface_name = 1`
  (required, `interface_name` rule), `trust_mode = 2`,
  `input_classifier = 3` and `output_classifier = 4` (1..1024, names of
  `Classifier` rows on the same device; absent means none bound),
  `queues = 5` (repeated `Queue`, the egress queues), with CEL rules
  `qos_interface.queue_ids_unique` (`this.queues.filter(q,
  has(q.queue_id)).map(q, q.queue_id).unique()`) and
  `qos_interface.queue_names_unique` (same over `name`).
- `layering_test.go`: the `net/qos` row gains `net/switching`.
- The package README: Boundaries (`Imports: net/filter, net/key,
  net/measure, net/packet, net/switching`; `Imported by: nothing`), why
  trust has five values, why the interface binding is a row, that terms
  reuse `FilterMatch`, the OpenConfig and vendor sources, and what stays
  out. `Imported by:` of the filter, packet, key, measure, and switching
  READMEs gain `net/qos`. `net/README.md` drops the `qos/v1/` planned
  marker.
- `docs/conventions/protobuf.md`, Units and keys, "Facets, settings, and
  rows": one sentence naming `QosInterface` as a per-interface row keyed
  by `interface_name` that omits `network_instance`, as `Session` does.
- `CONCEPTS.md`, Network model, gains "Trust mode": the header field a
  port believes when it classifies an incoming frame (PCP, DSCP, IP
  precedence, or PCP for L2 and DSCP for L3), or none.

Tests: `qos_rules_test.go`, `TestClassifierRules`, `TestPolicerRules`,
`TestQosInterfaceRules`, hold Requirements 3 and 5.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/qos/v1 generated/go/proto/flowseer/net/qos/v1 spec/proto/flowseer/net/README.md spec/proto/flowseer/net/filter/v1/README.md spec/proto/flowseer/net/packet/v1/README.md spec/proto/flowseer/net/key/v1/README.md spec/proto/flowseer/net/measure/v1/README.md spec/proto/flowseer/net/switching/v1/README.md test/conformance/proto/layering_test.go test/conformance/proto/qos_rules_test.go docs/conventions/protobuf.md CONCEPTS.md`

### U3. `net/aaa`: RADIUS and TACACS+ server identity

Files: `spec/proto/flowseer/net/aaa/v1/{aaa_server.proto,radius_server.proto,tacacs_server.proto,aaa_server_status.proto,README.md}`,
`generated/go/proto/flowseer/net/aaa/v1/`,
`spec/proto/flowseer/net/README.md`,
`spec/proto/flowseer/net/addr/v1/README.md`,
`test/conformance/proto/aaa_rules_test.go` (new),
`docs/conventions/protobuf.md`,
`CONCEPTS.md`
After: U2 (both edit `net/README.md`, `docs/conventions/protobuf.md`, and
`CONCEPTS.md`)

Change:

- `radius_server.proto` declares `RadiusServer`: `auth_port = 1`,
  `acct_port = 2` (each `lte 65535`), `retransmit_attempts = 3`
  (`lte 255`). `tacacs_server.proto` declares `TacacsServer`: `port = 1`
  (`lte 65535`). Each port comment says absent means unreported, not the
  RFC default.
- `aaa_server_status.proto` declares `AaaServerStatus` (IOS-XE
  `aaa-server-states`).
- `aaa_server.proto` declares `AaaServer`: `address = 1` (required
  `IpAddress`), `name = 2`, `group_name = 3` (each 1..1024), `priority = 4`
  (uint32; lower is tried first; absent means the source reports no
  order), `timeout = 5` (Duration, `gte {}`), `source_address = 6`
  (`IpAddress`), `status = 7`, and the required oneof `protocol` with
  `radius = 10` and `tacacs = 11`, commented with the both-arms sentence
  of the solution doc. The file-level comment says server secrets are
  credential material held outside this package.
- The package README: Boundaries (`Imports: net/addr`; `Imported by:
  nothing`), identity only and where secrets live, the Comware/Huawei
  domain scheme a normalized list does not round-trip (dossier 07,
  `07-qos-security-ops-wan.md:425-429`), and Sources (OpenConfig AAA,
  RADIUS MIBs, IOS-XE aaa-oper, D-Link). `net/addr/v1/README.md`
  `Imported by:` gains `net/aaa`. `net/README.md` drops the `aaa/v1/`
  planned marker.
- `docs/conventions/protobuf.md`, "Primitives refer to peers by key",
  gains one paragraph: a primitive never carries credential material; a
  shared secret, key, or password a device uses is held in the secret
  store behind a credential ref and carried only as `model/credential`
  material, so a `net/` message names the peer and never the secret.
- `CONCEPTS.md`, Network model, gains "AAA server": a RADIUS or TACACS+
  server a device is configured to use, identified by address and
  protocol; its shared secret is a credential, never part of the row.

Tests: `aaa_rules_test.go`, `TestAaaServerRules` (Requirement 1, the
empty-oneof case asserted at the `protocol` path) and
`TestAaaCarriesNoSecret` (Requirement 8: walk
`aaav1.File_flowseer_net_aaa_v1_*` descriptors' fields and fail on the
four substrings; its synthetic case uses `buildSyntheticFile` from
`schema_language_test.go` with a `secret_key` field).

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/aaa/v1 generated/go/proto/flowseer/net/aaa/v1 spec/proto/flowseer/net/README.md spec/proto/flowseer/net/addr/v1/README.md test/conformance/proto/aaa_rules_test.go docs/conventions/protobuf.md CONCEPTS.md`

### U4. `net/flow`: sFlow and NetFlow/IPFIX collector configuration

Files: `spec/proto/flowseer/net/flow/v1/{sflow_receiver.proto,sflow_sampler.proto,sflow_poller.proto,flow_exporter.proto,flow_export_protocol.proto,README.md}`,
`generated/go/proto/flowseer/net/flow/v1/`,
`spec/proto/flowseer/net/README.md`,
`spec/proto/flowseer/net/{addr,key,packet}/v1/README.md`,
`test/conformance/proto/layering_test.go`,
`test/conformance/proto/flow_rules_test.go` (new)
After: U3 (both edit `net/README.md` and the addr README's `Imported by:`
line; U2, before U3, edits the key and packet lines and
`layering_test.go`)

Change:

- `sflow_receiver.proto` declares `SflowReceiver`: `index = 1` (required,
  `gte 1`, `lte 65535`), `address = 2` (required `IpAddress`), `port = 3`
  (`lte 65535`), `max_datagram_size_bytes = 4` (uint64),
  `datagram_version = 5` (uint32). The comment says a receiver the MIB
  reports with address `0.0.0.0` is unclaimed and has no row.
- `sflow_sampler.proto` declares `SflowSampler`: `interface_name = 1`
  (required, rule), `instance = 2` (uint32, `sFlowFsInstance`),
  `receiver_index = 3` (required, `gte 1`, `lte 65535`),
  `sampling_rate = 4` (required, `gte 1`; one packet in N),
  `max_header_size_bytes = 5` (uint64).
- `sflow_poller.proto` declares `SflowPoller`: `interface_name = 1`
  (required, rule), `instance = 2`, `receiver_index = 3` (required,
  `gte 1`, `lte 65535`), `interval = 4` (Duration, required,
  `gte {seconds: 1}`).
- `flow_export_protocol.proto` declares `FlowExportProtocol`;
  `flow_exporter.proto` declares `FlowExporter`: `name = 1` (required,
  `min_len 1`, `max_len 60`), `destination = 2` (required `IpAddress`),
  `port = 3` (`lte 65535`), `protocol = 4`, `source_interface_name = 5`
  (`interface_name` rule), `dscp = 6` (`IpDscp`, `ip_dscp` rule),
  `template_refresh = 7` (Duration, `gte 1s`, `lte 86400s`).
- `layering_test.go`: the `net/flow` row becomes `{"net/addr",
  "net/packet", "net/key"}`.
- The package README: Boundaries (`Imports: net/addr, net/key,
  net/packet`; `Imported by: nothing`), configuration only and why
  decoding is a different plane, the MIB's disabled encodings and the
  omitted rows, interface data sources only, and Sources (`SFLOW-MIB`,
  IOS-XE flow, the sFlow v5 spec as dossier 07 cites it). `Imported by:`
  of the addr, key, and packet READMEs gain `net/flow`. `net/README.md`
  drops the `flow/v1/` planned marker.

Tests: `flow_rules_test.go`, `TestSflowRules` and `TestFlowExporterRules`,
hold Requirement 6.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/flow/v1 generated/go/proto/flowseer/net/flow/v1 spec/proto/flowseer/net/README.md spec/proto/flowseer/net/addr/v1/README.md spec/proto/flowseer/net/key/v1/README.md spec/proto/flowseer/net/packet/v1/README.md test/conformance/proto/layering_test.go test/conformance/proto/flow_rules_test.go`

### U5. `net/cellular`: technology, identifiers, serving cell, and signal quality

Files: `spec/proto/flowseer/net/cellular/v1/{radio_access_technology.proto,cellular_signal.proto,cellular_interface.proto,README.md}`,
`generated/go/proto/flowseer/net/cellular/v1/`,
`spec/proto/flowseer/net/README.md`,
`spec/proto/flowseer/net/key/v1/README.md`,
`test/conformance/proto/cellular_rules_test.go` (new),
`docs/conventions/protobuf.md`,
`CONCEPTS.md`
After: U4 (both edit `net/README.md` and the key README's `Imported by:`
line; U3, before U4, edits the conventions doc and `CONCEPTS.md`)

Change:

- `radio_access_technology.proto` declares `RadioAccessTechnology` with
  the six values of the Decisions and the fold of each source's values in
  the enum comment.
- `cellular_signal.proto` declares `CellularSignal`: `rsrp_millidbm = 1`
  (`gte -156000`, `lte -30000`), `rsrq_millidb = 2` (`gte -43000`,
  `lte 20000`), `rssi_millidbm = 3` (no bound), `sinr_millidb = 4`
  (`gte -23000`, `lte 40000`), all `sint32`. Each comment names the
  quantity (absolute power in dBm or ratio in dB), its bound as the union
  of the LTE and NR report mapping ranges with the TS 36.133 and TS 38.133
  clauses, that a saturated report is written as the endpoint, and that
  absent means unreported.
- `cellular_interface.proto` declares `CellularInterface`:
  `interface_name = 1` (required, rule), `technology = 2`, `band = 3`
  (uint32; the E-UTRA or NR operating band number, read with
  `technology`), `imei = 4`, `imsi = 5`, `iccid = 6`, `mcc = 7`,
  `mnc = 8` (patterns of the Decisions), `cell_id = 9` (uint64),
  `tracking_area_code = 10` (uint32), `physical_cell_id = 11` (uint32),
  `signal = 12` (`CellularSignal`). The row carries no `network_instance`.
- The package README: Boundaries (`Imports: net/key`; `Imported by:
  nothing`), why the row is per interface and not a component, the unit
  sharing with `net/wlan` and why there is no shared message, the bound
  table with the "secondary source, verify against TS 36.133 / TS 38.133"
  mark, the identifier formats and that IMSI and ICCID identify a
  subscription (the observability conventions' privacy rules apply when
  they are logged), and Sources (IOS-XE cellwan-oper, HH3C 3G modem MIB,
  Meraki uplink status, TS 23.003, TS 36.133, TS 38.133).
  `net/key/v1/README.md` `Imported by:` gains `net/cellular`.
  `net/README.md` drops the `cellular/v1/` planned marker.
- `docs/conventions/protobuf.md`: the U2 sentence on per-interface rows
  names `CellularInterface` beside `QosInterface`.
- `CONCEPTS.md`, Network model, gains "Cellular interface": the cellular
  side of a WAN interface (modem, SIM, serving cell, and signal), keyed by
  the interface name, as opposed to an 802.11 radio, which is a
  component.

Tests: `cellular_rules_test.go`, `TestCellularSignalRules` (Requirement 2,
including each bound's first failing value on both sides) and
`TestCellularInterfaceRules` (Requirement 7). The tests pin the bounds
this plan chose; they cannot show the bounds are 3GPP's, which the
README's verification mark covers.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/cellular/v1 generated/go/proto/flowseer/net/cellular/v1 spec/proto/flowseer/net/README.md spec/proto/flowseer/net/key/v1/README.md test/conformance/proto/cellular_rules_test.go docs/conventions/protobuf.md CONCEPTS.md`

Waves: U1 | U2 | U3 | U4 | U5

The chain is forced by shared files, not imports: every unit removes a
line's planned marker in `net/README.md`, the `Imported by:` lines of the
addr, key, packet, measure, and switching READMEs each take two or three
units, and U2, U3, and U5 edit the conventions doc and `CONCEPTS.md`.
Only U2 imports U1's code. Phase 7 of the parent may land beside this
phase and edits some of the same `Imported by:` lines and
`layering_test.go` rows; resolve those merges by taking the union of both
sides.

## Verification

```bash
buf lint
buf generate && git status --porcelain generated/   # empty after each unit's commit
go build ./... && go vet ./...
go test -race ./test/conformance/proto/... ./src/common/netsim/vswitch/netmodel/...
.claude/skills/verify-change/scripts/verify-change.sh -- <union of the five units' Verify paths>
```

Run the verifier over the union of changed paths, not with `--full`,
which builds and race-tests `generated/go/yang` and exhausts host memory.
No lab check: no mapper fills these rows in this phase, and no lab device
has a cellular interface.

## Definition of done

- [ ] Verifier green for every changed path of every unit.
- [ ] The filter, qos, aaa, flow, and cellular READMEs, `net/README.md`,
      the touched layer READMEs, `docs/conventions/protobuf.md`, and
      `CONCEPTS.md` match the tree in the unit that changed them.
- [ ] Every requirement above has its test case, and the cellular README
      carries the TS 36.133 / TS 38.133 verification mark.
- [ ] No `net/aaa` field carries a secret, and the test that says so
      passes.
- [ ] No plan label appears in schema, code, comments, or commit messages.
- [ ] This plan's `status` is `implemented` with an outcome note under the
      title, and the parent's U8 `Landed:` line carries the commit range.

## Open questions

- Whether per-interface QoS and cellular facts should be facets on
  `Interface` (`qos = 22`, `cellular = 23`) rather than rows keyed by
  interface name. The plan chooses rows: OpenConfig keys QoS interface
  bindings outside `/interfaces`, IOS-XE keys cellular tables by
  interface, phase 1's allowlist gives `net/interface` neither import, and
  the STP `PortState` row is precedent. For facets: record rule 5 calls a
  per-interface bundle a facet, and `FilterFacet` is the direct sibling of
  a QoS binding. Switching is additive: two facet messages in place of the
  two rows' non-key fields, two `Interface` fields, and `net/qos` and
  `net/cellular` in the `net/interface` allowlist row. Recommendation:
  keep rows; revisit if a consumer reads QoS or cellular state through
  `Interface`.
- Whether record rule 4 requires `network_instance` on configuration
  rows (`Classifier`, `AaaServer`, `SflowReceiver`, `FlowExporter`). The
  plan reads rule 4 as covering forwarding tables, the reason the record
  gives, and follows `FilterRuleSet`. The literal sentence ("every table
  whose key is not already scoped by an interface") would require it on
  all four, forcing mappers to claim `default` for sources that report no
  VRF. Recommendation: keep them without it and state the reading in the
  record at its next amendment.
- The 3GPP bounds and the TS 23.003 identifier formats rest on secondary
  sources. Before the first cellular mapper lands, fetch TS 36.133
  §9.1.4, §9.1.7, the LTE RS-SINR clause, TS 38.133 §10.1 (SS-RSRP,
  SS-RSRQ, SS-SINR tables), TS 23.003 §2.2 and §6.2.1, and ITU-T E.118
  for the ICCID length, and tighten or widen the rules to match.
