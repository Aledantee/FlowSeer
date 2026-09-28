# L3 routing and IP services — schema primitives dossier

## 1. Scope and sources

Domain: RIB/FIB, static routes, ECMP next-hop groups, route source taxonomy;
OSPFv2/v3; BGP; IS-IS; VRRP/HSRP; BFD; DHCP; DNS; NAT; multicast routing
(PIM); MPLS/SR at primitive level; tunnels (IPsec, WireGuard); IP SLA
(ping/traceroute); ICMP/TCP/UDP stack counters; and the IPv6 `AddressOrigin`
taxonomy bug in `net/ip/v1`.

**Repo sources fetched for this dossier:**
- `docs/conventions/protobuf.md` (triad, ref pair, primitives, enums, typed
  variants — lines cited inline below)
- `docs/code-style-proto.md` (edition 2024, presence, validation, evolution)
- `docs/architecture/2026-08-20-network-model-structure-direction.md`
  (net/ vs model/ split, `net/routing` reservation, protocol-package rule)
- `spec/proto/flowseer/net/ip/v1/*.proto` (all files), `net/addr/v1/*.proto`,
  `net/packet/v1/*.proto`, and their READMEs
- `spec/proto/flowseer/net/` directory tree (existing subpackages)
- `docs/research/network-domain-atlas/entities/04-ip.md`, `05-routing.md`,
  `10-wan-access.md`
- `spec/mib/ietf/`: `IP-FORWARD-MIB`, `IANA-RTPROTO-MIB`, `OSPF-MIB`,
  `OSPFV3-MIB`, `BGP4-MIB`, `BGP4V2-TC-MIB`, `VRRP-MIB`, `VRRPV3-MIB`,
  `BFD-STD-MIB`, `DISMAN-PING-MIB`, `DISMAN-TRACEROUTE-MIB`, `IP-MIB`,
  `TCP-MIB`, `UDP-MIB`, `IPMROUTE-STD-MIB`, `MPLS-VPN-MIB`,
  `MPLS-L3VPN-STD-MIB`
- `spec/mib/huawei/HUAWEI-NAT-MIB`, `spec/mib/hp/hh3c/HH3C-NAT-MIB`,
  `HH3C-BGP4V2-MIB` (IETF NAT-MIB and full BGP4V2-MIB peer table are **not**
  vendored — vendor equivalents only)
- `spec/yang/openconfig/`: `openconfig-network-instance.yang`,
  `openconfig-local-routing.yang`, `openconfig-aft-common.yang`,
  `openconfig-bgp-neighbor.yang`, `openconfig-ospfv2*.yang`,
  `openconfig-isis.yang`, `openconfig-bfd.yang` (mpls/pim/igmp/evpn/rib-bgp
  modules present but not deep-read)
- `spec/openapi/mikrotik/routeros-7.24-openapi.json` (route, wireguard,
  ipsec), `spec/openapi/ubiquiti/unifi-network-openapi-v10.4.57.json` (dhcp,
  nat, dns), `spec/openapi/ruckus/vsz/vsz-7.1.1-v13_1-openapi.json` (dhcp/nat
  profiles); `spec/openapi/meraki/` **does not exist in this repo**
- `spec/proto/ruckus/icx/switches.proto` (StaticRoute, BgpNeighborEntry,
  DHCPServer/DHCPOption, ospfArea, natIp — string/ad-hoc, pre-triad legacy
  shape)
- RFC 8349, RFC 4292, IANA-RTPROTO-MIB registry, RFC 4750, RFC 5643,
  RFC 2328 §10.1, OpenConfig `openconfig-isis-types.yang` (IS-IS states),
  RFC 4271, RFC 4273, IANA AFI/SAFI registries, RFC 6793, RFC 1997, RFC 8092,
  RFC 5798, RFC 6527, RFC 2281 (HSRP), RFC 5880, RFC 7331, RFC 2131,
  RFC 8415, IANA BOOTP/DHCP parameters registry, RFC 4787, RFC 4008,
  RFC 7761, RFC 7296, WireGuard quickstart + `wg(8)` man page, RFC 4293,
  RFC 8344, RFC 7217, RFC 8981, RFC 4560, RFC 4022, RFC 4113 — all fetched
  and cited below; fetch caveats noted where a tool truncated content.

**Unverified / not found:** IANA-BFD-TC-STD-MIB (BFD state/diag integers) is
referenced but not vendored — RFC 5880 §4.1 values used instead. IETF
NAT-MIB (RFC 4008) is not vendored — RFC 4008 web text used instead. Meraki
OpenAPI is absent from the repo despite being named in the dossier's provider
list.

## 2. Standards facts

**RIB (RFC 8349 ietf-routing):** route object carries `destination-prefix`
(mandatory), `route-preference` (admin distance — "a lower value indicates a
route that is more preferred"), `source-protocol` (`identityref`, base
`routing-protocol`), `active` (presence-typed `empty` leaf, not a bool — its
*presence* means "installed/preferred among routes to the same prefix"),
`last-updated`. No base-module `metric` leaf — metric is protocol-specific
(OSPF/IS-IS/BGP augment separately). `special-next-hop` enumerates
`blackhole` (silent discard — **not** "discard"), `unreachable` (discard +
ICMP unreachable), `prohibit` (discard + admin-prohibited notice), `receive`
(delivered locally) — four values, no plain "discard". `next-hop-list` for
ECMP is a `list next-hop { key index; leaf outgoing-interface; }`, index is
an opaque per-entry identifier, not a priority/weight. (Section numbering for
`route-metadata`/`special-next-hop` came back inconsistent across two
fetches, §5 vs §7 — content/names are solid, re-verify exact clause number
against the YANG tree before citing a specific section in a proto comment.)

**FIB (RFC 4292 IP-FORWARD-MIB, vendored `spec/mib/ietf/IP-FORWARD-MIB`):**
`inetCidrRouteTable` key is six columns: `inetCidrRouteDestType`,
`inetCidrRouteDest`, `inetCidrRoutePfxLen`, `inetCidrRoutePolicy`,
`inetCidrRouteNextHopType`, `inetCidrRouteNextHop` (lines 100-156). Also
carries `inetCidrRouteIfIndex`, `inetCidrRouteType` (`other(1)`,
`reject(2)`, `local(3)`, `remote(4)`, `blackhole(5)` — lines 266-275),
`inetCidrRouteProto IANAipRouteProtocol`, `inetCidrRouteAge`,
`inetCidrRouteNextHopAS`, `inetCidrRouteMetric1..5`.

**Route source registry (IANA-RTPROTO-MIB, vendored `spec/mib/ietf/
IANA-RTPROTO-MIB:48-79`):** `other(1)`, `local(2)`, `netmgmt(3)`, `icmp(4)`,
`egp(5)`, `ggp(6)`, `hello(7)`, `rip(8)`, `isIs(9)`, `esIs(10)`,
`ciscoIgrp(11)`, `bbnSpfIgp(12)`, `ospf(13)`, `bgp(14)`, `idpr(15)`,
`ciscoEigrp(16)`, `dvmrp(17)`, `rpl(18)`, `dhcp(19)`, `ttdp(20)` (Train
Topology Discovery Protocol. The registered name is `ttdp`, not "ttag"). Registry pass-through: keep the real integers.
Separate `IANAipMRouteProtocol` TC (same file, lines 81-100) has its own
values including `pimSparseMode(8)`, `pimDenseMode(9)`, `msdp(12)` — do not
reuse the unicast enum for multicast route source.

**OSPFv2 (RFC 4750, RFC 2328 §10.1; vendored `spec/mib/ietf/OSPF-MIB`):**
`ospfNbrState` 8-value FSM: `down(1)`, `attempt(2)`, `init(3)`, `twoWay(4)`,
`exchangeStart(5)`, `exchange(6)`, `loading(7)`, `full(8)` (OSPF-MIB:2370-
2388). RFC 2328 §10.1 confirms the same 8 states as a strict progression
(Down → Attempt[NBMA only] → Init → 2-Way → ExStart → Exchange → Loading →
Full); 2-Way is the DR/BDR-eligibility threshold, not all neighbors proceed
past it. `ospfIfType`: `broadcast(1)`, `nbma(2)`, `pointToPoint(3)`,
`pointToMultipoint(5)` — **value 4 is intentionally absent**, don't
"renumber to be sequential" when porting this enum. `AreaID` TC syntax is
`IpAddress`-shaped (dotted-quad) in OSPFv2.

**OSPFv3 (RFC 5643; vendored `OSPFV3-MIB`):** `ospfv3NbrState` mirrors
OSPFv2's 8 values exactly (`OSPFV3-MIB:2256-2274`). Key difference: Router
ID, Area ID, and Link State ID are `Unsigned32` in OSPFv3 (no IP-address
semantics), and the OSPFv3 interface identifier is an IPv6 interface index,
not an IPv4-address/ifIndex pair as in OSPFv2 — an OSPF area/router-id
primitive must not assume dotted-quad shape once v3 is included. Interface
instance ID (0-255) allows multiple OSPFv3 instances per link.

**IS-IS:** OpenConfig `openconfig-isis-types.yang` `isis-interface-adj-state`
is **4-valued**: `UP`, `DOWN`, `INIT`, `FAILED` (not a 3-valued
down/initializing/up set). `level-type`: `LEVEL_1`,
`LEVEL_2`, `LEVEL_1_2`. No RFC 1195/ISO 10589 text was fetchable for this dossier for the canonical adjacency-state clause — OpenConfig is the only
verified source for this fact.

**BGP (RFC 4271 §8.2.2, RFC 4273; vendored `BGP4-MIB`, `BGP4V2-TC-MIB`):**
FSM states in order: Idle, Connect, Active, OpenSent, OpenConfirm,
Established (RFC 4271 §8.2.2, confirmed via raw-text fetch after the
summarized WebFetch truncated this section twice — flag: long RFCs need
`.txt` fetch, not summarized WebFetch, for exact clause text).
`bgpPeerState` mirrors this 1:1 as integers 1-6 (`BGP4-MIB:195-210`,
`REFERENCE "RFC 4271, Section 8.2.2."`). `bgpPeerAdminStatus`: `stop(1)`,
`start(2)`. IANA AFI: 1=IPv4, 2=IPv6, 25=L2VPN. IANA SAFI: 1=unicast,
2=multicast, 4=MPLS-labeled, 70=EVPN, 128=MPLS VPN — vendored
`BGP4V2-TC-MIB` only defines a narrow subset (`unicast(1)`,
`multicast(2)`, `mpls(4)`) so AFI/SAFI as a schema primitive should be
IANA-registry pass-through, not the MIB's truncated enum. 4-octet ASN
(RFC 6793 §1/§3): AS_PATH/AGGREGATOR get 4-octet variants (AS4_PATH=17,
AS4_AGGREGATOR=18), negotiated via BGP Capability code 65 — model ASN as a
32-bit value, not 16-bit. Communities: RFC 1997 standard community = 32-bit
value (conventionally two 16-bit halves, AS:value); RFC 8092 large community
= three 4-octet fields (Global Administrator, Local Data Part 1, Local Data
Part 2) — these are two distinct, non-nesting shapes.

**VRRP (RFC 5798 v3; vendored `VRRP-MIB` — the file vendored under that
name is actually the RFC 6527-era version-independent revision, not the
v2-only original):** states Initialize/Backup/Master (RFC 5798 §6.4).
`VRID` range 1-255, no default. `Priority`: 255 reserved (IP-address owner),
0 reserved (Master releasing), 1-254 backups, default 100. Advertisement
interval unit is **centiseconds**, default 100 (=1s) — unit trap. Vendored
`vrrpOperationsState` (`VRRP-MIB:232-255`) = `initialize(1)`, `backup(2)`,
`master(3)`, matching RFC 5798 exactly; the older `vrrpOperState`
(`VRRP-MIB:951-979`) is deprecated, same 3 values. **`VRRPV3-MIB` (vendored)
defines no state object at all** — state tracking lives entirely in the
`VRRP-MIB` file; don't expect a `vrrpv3State` object in this repo's MIB set.

**HSRP (RFC 2281 §5.1/§5.3 — Cisco-proprietary, VRRP's predecessor,
"still dominant on Cisco networks" per atlas):** states Initial, Learn,
Listen, Speak, Standby, Active (6 states — more granular than VRRP's 3).
Group field: 0-2 for Token Ring, 0-255 other media. Priority: 1 octet,
higher wins; RFC itself sets no default (Cisco IOS defaults to 100 per
secondary vendor docs, not RFC text). Atlas 05-routing.md groups
VRRP/HSRP/GLBP/VSRP/XRRP/VGMP as "the same entity with different protocols
... one entity with a protocol discriminator beats five packages."

**BFD (RFC 5880 §4.1; vendored `BFD-STD-MIB`):** State: `AdminDown(0)`,
`Down(1)`, `Init(2)`, `Up(3)`. Diagnostic: 0=No Diagnostic, 1=Control
Detection Time Expired, 2=Echo Function Failed, 3=Neighbor Signaled Session
Down, 4=Forwarding Plane Reset, 5=Path Down, 6=Concatenated Path Down,
7=Administratively Down, 8=Reverse Concatenated Path Down, 9-31=Reserved.
Vendored `BFD-STD-MIB` has `bfdSessState`/`bfdSessDiag` object *positions*
(entries 11/13) but their concrete integer TCs live in
`IANA-BFD-TC-STD-MIB`, which is **not vendored** — use the RFC 5880 §4.1
integers directly as the source of truth. Session identification is a
triple: `bfdSessIndex` (opaque local index), `bfdSessDiscriminator` (local),
`bfdSessRemoteDiscr` (remote) — atlas calls this "a good pattern for any
entity whose internal id is opaque." OpenConfig's `bfd-session-state`
typedef differs in spelling/order (`UP`, `DOWN`, `ADMIN_DOWN`, `INIT`) —
don't silently merge the RFC and OpenConfig enums without normalizing.

**DHCP (RFC 2131 v4, RFC 8415 v6):** v4 message types DISCOVER, OFFER,
REQUEST, DECLINE, ACK, NAK, RELEASE, INFORM (§3.1 Table 2). Client states
(§4.4 Fig. 5): INIT, SELECTING, REQUESTING, BOUND, RENEWING, REBINDING, plus
INIT-REBOOT/REBOOTING for the reboot path. Lease fields: `yiaddr`, lease
time, T1 (renew) / T2 (rebind) timers. v6 message types are numbered 1-13
(SOLICIT..RELAY-REPL); v6 introduces IA_NA (non-temporary address
association, §21.4) and IA_PD (prefix delegation association, §21.21),
each with its own T1/T2, preferred-lifetime, valid-lifetime. IANA DHCP
option registry: 1=Subnet Mask, 3=Router, 6=Domain Server, 15=Domain Name,
51=Lease Time, 53=Message Type, 54=Server Identifier, 58=Renewal(T1),
59=Rebinding(T2).

**NAT (RFC 4787 terms, RFC 4008 NAT-MIB — not vendored as IETF module, only
Huawei/HH3C vendor MIBs vendored):** Session = 4-tuple (+ protocol);
Mapping = internal↔external endpoint translation; three mapping-behavior
classes — Endpoint-Independent, Address-Dependent, Address-and-Port-
Dependent (RFC 4787 §4.1). RFC 4008 `natAddrMapTable` key fields:
entry type (static/dynamic), local/global address+port ranges, protocol
bitmap. `natSessionTable`: private/public src+dst address+port, protocol,
bind-id linking session to its mapping row. Huawei's vendored
`HUAWEI-NAT-MIB` has its own OID tree (`hwNatAddressGroupInfoTable`,
`hwNatInternalServerTable`, `hwNatTimeoutTable`, `hwNatAlgEnableTable`) —
distinct shape from IETF NAT-MIB, useful only for Huawei-specific mapping,
not as the primary source.

**Multicast (RFC 7761 PIM-SM only — PIM-DM is a separate, unfetched RFC):**
(S,G) = source-specific tree state (§4.1.3); (*,G) = shared/RP-tree state
(§4.1.2); RP = "root of the non-source-specific distribution tree for a
multicast group" (§2.1). Vendored `IANAipMRouteProtocol` TC has
`pimSparseMode(8)`/`pimDenseMode(9)` as distinct registry values — model
PIM mode as this registry enum, not a boolean.

**Tunnels — IPsec (RFC 7296 IKEv2):** IKE SA negotiates crypto/auth to
protect further exchanges; Child SA is what actually carries ESP/AH traffic
— one IKE SA can have many Child SAs (§1.2). Negotiated parameters:
encryption alg, integrity/PRF alg, DH group (§3.3.3). **RFC 7296 explicitly
does not negotiate an SA lifetime** (§2.4: "there is no reason to negotiate
and agree upon an SA lifetime") — lifetime is local policy, not a wire
attribute; a "negotiated lifetime" field on an IKE proposal message would
misrepresent the protocol.

**Tunnels — WireGuard (quickstart + `wg(8)`, protocol whitepaper does NOT
cover config vocabulary):** peer config fields: `PublicKey`, `AllowedIPs`,
`Endpoint`, `PersistentKeepalive`, `PresharedKey` (optional). Interface:
`PrivateKey`, `ListenPort`. Runtime/state (`wg show`): `latest-handshake`
(timestamp), `transfer-rx`/`transfer-tx` (byte counters).

**IP SLA / diagnostics (RFC 4560 DISMAN-PING-MIB + DISMAN-TRACEROUTE-MIB;
vendored):** `pingResultsTable`: min/max/average RTT, sent probes, probe
responses (packet loss = derived, not a dedicated counter).
`pingResultsOperStatus` (vendored, `DISMAN-PING-MIB:734-748`):
`enabled(1)`, `disabled(2)`, `completed(3)`. `pingProbeHistoryTable`:
per-probe elapsed time, status, last reply code, timestamp.
`traceRouteHopsTable` keyed by `traceRouteHopsHopIndex` — a shape agreeing
with the atlas's recommendation to adopt "owner+name key, definition table,
results table, history table" as FlowSeer's general long-running-operation
pattern (atlas `04-ip.md:456-459`).

**Stack counters:** ICMP counters live in RFC 4293 IP-MIB §3.2.10 (not a
separate document) — `icmpStatsInMsgs`/`InErrors` (per-AFI, vendored
`IP-MIB:3371-3387`) and `icmpMsgStatsInPkts`/`OutPkts` (per AFI+ICMP-type,
`IP-MIB:3477-3491`). TCP (RFC 4022, vendored `TCP-MIB`): `tcpConnectionState`
12-value enum `closed(1)`..`deleteTCB(12)`; key = 6-tuple (local/remote
address-type+address+port). UDP (RFC 4113, vendored `UDP-MIB`): scalar
counters `udpInDatagrams`(1)/`udpNoPorts`(2)/`udpInErrors`(3)/
`udpOutDatagrams`(4); `udpEndpointTable` keyed by local+remote
address-type+address+port+instance, plus non-key `udpEndpointProcess`.

**Address-origin — the three-axis disambiguation (load-bearing for the
current bug):**
- **Axis A — assignment provenance**, RFC 4293 `IpAddressOriginTC`
  (§5, vendored `IP-MIB:60-84`): `other(1)`, `manual(2)`, `dhcp(4)`,
  `linklayer(5)`, `random(6)` — **value 3 does not exist**, this is not a
  fetch gap, the enum genuinely skips 3. RFC 8344 `ip-address-origin`
  (§4, ietf-ip YANG) is a *functionally aligned but not identical* 5-value
  set: `other`, `static` (renamed from `manual`), `dhcp`, `link-layer`,
  `random` — RFC 8344 §3 maps to IP-MIB's `ipAddressOrigin` object but does
  not copy the TC verbatim.
- **Axis B — IID generation algorithm**, RFC 7217: defines only an
  algorithm (`RID = F(Prefix, Net_Iface, Network_ID, DAD_Counter,
  secret_key)`, §5) for a stable, opaque-per-subnet IPv6 interface
  identifier. **It mints no enum value at all** — it is cited only as an
  example inside RFC 8344's `random` value description.
- **Axis C — temporary/privacy addressing**, RFC 8981 (obsoletes RFC 4941):
  defines temporary addresses with randomized IIDs, each with **their own,
  shorter lifetime pair** (`TEMP_VALID_LIFETIME`=2 days,
  `TEMP_PREFERRED_LIFETIME`=1 day minus a desync factor, §3.4/§3.8) —
  orthogonal to origin, expressed as a flag + lifetime pair, not a sibling
  origin value. RFC 8344's `random` value name-drops RFC 4941/8981 as an
  *example*, it does not create a first-class `temporary` origin case.

  **Conclusion:** a single enum trying to express
  manual/dhcp/slaac-eui64/slaac-opaque(7217)/temporary(8981) as sibling
  values is conflating three schema dimensions (provenance mechanism, IID
  algorithm, temporariness) into one. FlowSeer's current
  `net/ip/v1/address_origin.proto` enum (see §4.1 in existing schema) is
  exactly this conflation, and the atlas independently flagged it
  (`04-ip.md:74-77`): "`linklayer(4)` and `random(5)` are both really
  'SLAAC, with different IID generation.'"

## 3. Provider data matrix

| Concept | MikroTik RouterOS (vendored) | UniFi Network (vendored) | Ruckus ICX (vendored proto) | OpenConfig | Core or niche |
|---|---|---|---|---|---|
| Static route | `/ip/route`: `dst-address, gateway, distance, pref-src, routing-table, vrf-interface, scope, target-scope, active, dynamic, blackhole, ecmp` (protocol flags `bgp/ospf/rip/is-is/static/connect/dhcp` as booleans on the same row) | not exposed as a schema/endpoint (grep empty) | `StaticRoute{id, groupId, familyId, destinationIp, nextHop, adminDistance, switchId}` (legacy string/ad-hoc proto, no triad) | `openconfig-local-routing`: `static{key prefix}`, `next-hop{key index}` | **Core** (every router-class device) |
| Route protocol source | boolean-per-protocol columns on the route row (not a discriminant enum) | absent | absent (route model not exposed) | `identifier` leafref to `IDENTITY` type, e.g. `STATIC` | Core, but shape varies: enum (IANA/OpenConfig) vs per-protocol booleans (MikroTik) — do not assume vendors expose a clean enum |
| ECMP | `ecmp` boolean flag, no next-hop-group substructure exposed in the fetched endpoint | absent | absent | `next-hop-groups{list next-hop-group{key id}}` with full next-hop list | Niche in vendor APIs surveyed, core in the standards model — expect to synthesize ECMP from repeated route rows unless a provider gives groups |
| BGP peer | absent from fetched MikroTik route endpoint (separate `/routing/bgp` likely exists, not fetched) | absent | `BgpNeighborEntry{switchId, neighborIp, remoteAs, connectionState (string!), uptime, imetRoutes, macRoutes}` + `BgpNeighborMonitor{localAs, totalNeighbors, totalEstablishedNeighbors}` | `session-state` enum `IDLE/CONNECT/ACTIVE/OPENSENT/OPENCONFIRM/ESTABLISHED`, `neighbor{key neighbor-address}`, `afi-safi{key afi-safi-name}` | Core for router/switch-with-EVPN class; Ruckus models FSM state as a bare **string**, not an enum — a trap for provider-to-normalized mapping |
| OSPF | `ospf` boolean flag only in the fetched route endpoint | absent | `string ospfArea` / `string ipv6OspfArea` (string-typed area id, no structured neighbor/interface state) | `areas{list area{key identifier}}`, full neighbor/interface/LSA structure | Core concept, shallow vendor exposure — Ruckus doesn't expose neighbor state at all, just area membership as a string |
| VRRP/HSRP | not found in fetched endpoints | not found | not found | none of the fetched OC modules cover VRRP/HSRP | Confirmed niche in this vendored corpus; standards (VRRP-MIB, RFC 5798, RFC 2281) are the primary source, not vendor OpenAPI |
| BFD | not found | not found | not found | `openconfig-bfd`: `session-state` enum `UP/DOWN/ADMIN_DOWN/INIT`, `local-address/remote-address`, echo/async timers | Niche in vendored OpenAPI corpus, present only via MIB/OpenConfig |
| DHCP server | not found in fetched MikroTik route/wireguard/ipsec endpoints (separate MikroTik DHCP endpoint likely exists, not fetched) | `dhcpConfiguration` oneof `{RELAY, SERVER}`; server: `ipAddressRange{start,stop}`, `leaseTimeSeconds` (0-31536000), `dnsServerIpAddressesOverride[]`, `gatewayIpAddressOverride`, `domainName`, `ntpServerIpAddresses[]`, `option43Value`, `dhcpGuarding.trustedDhcpServerIpAddresses[]` | `DHCPServer{defaultRouterIp, dhcpOptions[]}`, `DHCPOption{type enum incl. STATICROUTE=8}` | none of the fetched OC modules include DHCP | **Core** — every access/gateway class exposes DHCP server/relay config, shapes vary widely (range vs pool vs static-route-as-option) |
| NAT | `natOutboundConfigurations` oneof `{AUTO, STATIC}` (UniFi actually, not MikroTik — see below) | see left | `string natIp` (single scalar, "NAT IP of switch" — not a mapping table) | none of the fetched OC modules cover NAT | Core for router/gateway class, absent/minimal on switch class (Ruckus's single scalar confirms NAT is out of scope for switches per atlas) |
| IPsec | `/ip/ipsec/peer`: `address, local-address, exchange-mode, profile, responder, passive, ppk-secret`; `/ip/ipsec/identity`, `/ip/ipsec/policy` also present | not fetched | not present | not covered by fetched OC modules | Niche across the surveyed corpus outside MikroTik; RFC 7296 is the primary normalized source |
| WireGuard | `/interface/wireguard`: `listen-port, mtu, private-key, public-key, running, vrf`; `/interface/wireguard/peers` sub-resource present but not deep-read | not fetched | not present | not applicable (not an IETF/OpenConfig protocol) | Niche outside MikroTik; `wg(8)`/quickstart are the primary normalized source |
| DNS | not fetched for MikroTik | `/v1/sites/{siteId}/dns/policies` (separate Site-Manager-style endpoint, not a Network schema field) | not present | not covered | Niche/thin per atlas (`04-ip.md`/`dns` section) — low schema value beyond forwarder address list |

Note on Meraki: Cisco Meraki is a provider this dossier set out to survey, but
`spec/openapi/meraki/` does not exist in this repo (Glob returned no
files) — Meraki appliance L3/DHCP/NAT/VPN facts are **not verified from a
vendored source** for this dossier; would need a live web fetch of Meraki
Dashboard API docs if required before finalizing.

## 4. Proposed primitives

Package layout follows the architecture doc's explicit reservation
(`docs/architecture/2026-08-20-network-model-structure-direction.md:204-
205`, `:730-731`): `net/routing` is reserved for RIB/FIB/route
tables/network-instance, kept separate from `net/ip`. Its "protocols own
their packages" rule (`:239-259`) means BGP/OSPF/IS-IS/VRRP/HSRP/BFD each
get `net/protocol/<x>/v1/`, mirroring the existing `net/protocol/{lacp,
lldp,stp}/v1` pattern. DHCP/DNS/NAT/tunnels have no reservation yet — they
are new IP-*service* packages, distinct from both `net/ip` (address/neighbor
facts) and `net/routing` (path-selection facts).

Proposed packages (all ref-free primitives per `docs/conventions/
protobuf.md:167-181`; typed-variant oneofs per `:238-283`; registry
pass-through enums keep MIB/IANA integers, normalized enums get
`_UNSPECIFIED = 0`):

- **`net/routing/v1`** — network-instance-scoped RIB/FIB facts, no protocol
  internals.
  - `NetworkInstance{name}` — key used by every routing/route/FDB message
    per atlas `05-routing.md` and the network-instance research
    (`04-ip.md`-adjacent atlas file, `236-279`). Keep name-keyed, not an
    opaque id, matching MikroTik's `routing-table` string and OpenConfig's
    `list protocol{key "identifier name"}` shape.
  - `RouteSourceProtocol` enum — **registry pass-through** of
    `IANAipRouteProtocol` (`other=1..ttdp=20`), reusing the exact integers
    from `IANA-RTPROTO-MIB:48-79`. Do not renumber to `_UNSPECIFIED=0`; the
    registry's own `other(1)` plays that role, per the conventions doc's
    registry-pass-through class.
  - `RouteAction` enum — normalized, `_UNSPECIFIED=0`, then `FORWARD`
    (implicit unless special), `BLACKHOLE`, `UNREACHABLE`, `PROHIBIT`,
    `RECEIVE` — mirrors RFC 8349 `special-next-hop` (4 real values, no
    "discard").
  - `NextHop` typed variant: oneof `{ interface_next_hop (interface name +
    optional gateway IpAddress), special (RouteAction restricted to the 4
    non-forward values) }`. `NextHopGroup{repeated NextHop members}` for
    ECMP, index implicit in list position (RFC 8349's `index` leaf is
    purely an opaque per-entry id, not a priority — do not add a weight
    field unless a provider proves one exists; none surveyed did).
  - `RouteRow` (Facet-style primitive, table = repeated rows not a map per
    convention): `network_instance` (name), `prefix (IpPrefix from
    net/addr/v1)`, `source_protocol (RouteSourceProtocol)`, `preference`
    (admin distance, uint32), `metric` (uint32, protocol-defined — comment
    that RFC 8349 base module leaves this to protocol augments),
    `next_hop_group (NextHopGroup)`, `active` (bool — RFC 8349 uses
    presence-of-empty-leaf, FlowSeer's explicit-presence convention makes
    an unset-vs-false bool already carry that meaning), `rib_or_fib`
    (new normalized enum `RIB`/`FIB` per atlas's explicit call for a
    RIB-vs-FIB discriminator, `04-ip.md:196-201` region).
  - Reuse: `IpPrefix`, `IpAddress` from `net/addr/v1`; `IpProtocol` already
    in `net/packet/v1` for anything needing L4 protocol, not route
    source protocol (different registries — do not conflate `IpProtocol`
    with `RouteSourceProtocol`).

- **`net/protocol/bgp/v1`** — global config/local-system block, per-peer
  config, peer state table (per architecture doc's protocol-package rule).
  - `BgpSessionState` enum — registry pass-through of `bgpPeerState`
    integers 1-6 (`IDLE=1..ESTABLISHED=6`), matching RFC 4271 §8.2.2 /
    BGP4-MIB exactly.
  - `Asn` — plain `uint32` field (RFC 6793: 4-octet ASN is now the norm),
    do not model as a 16-bit type or split high/low words.
  - `AddressFamily{ afi (IANA AFI pass-through enum, reuse `net/addr/v1`'s
    existing `IpVersion` IANA-AFI enum if it already covers 1/2, else add
    one), safi (IANA SAFI pass-through enum, new) }` — model as a
    (afi, safi) pair message, not a combined single enum, since IANA
    maintains them as two independent registries.
  - `BgpCommunity` typed variant: oneof `{ standard (fixed32, two 16-bit
    halves documented, not split into sub-fields per RFC 1997's "32 bit
    value" framing), large (three fixed32 fields per RFC 8092 §3:
    global_administrator, local_data_part_1, local_data_part_2) }` —
    these are non-nesting shapes, must be separate variants.
  - `BgpPeer` Facet: `local_asn`, `remote_asn (Asn)`, `local_address`,
    `remote_address (IpAddress)`, `state (BgpSessionState)`,
    `admin_status` (bool; RFC 4273 `stop(1)/start(2)` collapses cleanly to
    a bool since it's binary), `address_families (repeated AddressFamily)`.
    Note the Ruckus trap: their `connectionState` is a bare string — any
    ingestion mapping must normalize vendor strings into
    `BgpSessionState`, not assume vendors emit the MIB integers.

- **`net/protocol/ospf/v1`** — separate v2/v3 neighbor-state shapes because
  their key/area-id types differ.
  - `OspfNeighborState` enum — registry pass-through, 8 values `DOWN=1
    .. FULL=8`, shared by v2 and v3 (both MIBs use identical integers).
  - `OspfInterfaceType` enum — registry pass-through, **do not renumber**:
    `BROADCAST=1, NBMA=2, POINT_TO_POINT=3, POINT_TO_MULTIPOINT=5` (value 4
    is genuinely absent in the source MIB).
  - `OspfAreaId` typed variant: oneof `{ dotted_quad (fixed32, OSPFv2
    shape) , numeric (uint32, OSPFv3 shape) }` — do not use a single
    `bytes`/`uint32` field for both versions; RFC 5643 explicitly changed
    the underlying type from `IpAddress`-shaped to `Unsigned32`.
  - `OspfNeighbor` Facet: `neighbor_router_id`, `area (OspfAreaId)`,
    `interface_type (OspfInterfaceType, v2 only — flag as
    version-conditional in the comment)`, `state (OspfNeighborState)`.

- **`net/protocol/isis/v1`**:
  - `IsisAdjacencyState` enum — registry pass-through of OpenConfig's
    4-value set `UP/DOWN/INIT/FAILED`, not a 3-value down/initializing/up set.
  - `IsisLevel` enum — `LEVEL_1`, `LEVEL_2`, `LEVEL_1_AND_2`.

- **`net/protocol/vrrp/v1`** (also covers HSRP/GLBP/etc. per atlas's "one
  entity, protocol discriminator" recommendation, `05-routing.md:317-319`):
  - `FhrpProtocol` enum (new, normalized): `VRRP`, `HSRP`, `GLBP`, `OTHER`
    — the discriminator atlas recommends instead of five packages.
  - `FhrpState` enum — normalized (states don't align 1:1 across
    protocols: VRRP has 3, HSRP has 6): `UNSPECIFIED=0, INITIALIZE, LEARN,
    LISTEN, SPEAK, BACKUP, STANDBY, MASTER/ACTIVE` — comment which native
    states map to which normalized value per protocol (see §6 traps).
  - `FhrpGroup` Facet: `protocol (FhrpProtocol)`, `group_id` (uint32; VRRP
    1-255, HSRP 0-255 per media — validate range per protocol via CEL, not
    a shared bound), `priority` (uint32 0-255; VRRP has reserved 0/255
    semantics — document in comment, don't drop the reserved-value
    meaning), `virtual_address (IpAddress)`, `state (FhrpState)`,
    `advertisement_interval_ms` (VRRP is centiseconds-native — convert to
    ms in the primitive per FlowSeer's "units in field names" convention,
    document the source unit).

- **`net/protocol/bfd/v1`**:
  - `BfdSessionState` enum — registry pass-through of RFC 5880 §4.1
    integers: `ADMIN_DOWN=0, DOWN=1, INIT=2, UP=3` (note: 0 is a real
    value here, not "unspecified" — BFD's own registry already reserves 0
    for AdminDown, so this enum cannot get a synthetic `_UNSPECIFIED=0`
    without shifting away from the wire values; treat this as a
    registry-pass-through case that starts at a meaningful 0, matching the
    existing `net/packet/v1` pattern for `IP_PROTOCOL_HOPOPT=0`).
  - `BfdDiagnosticCode` enum — registry pass-through, integers 0-8 named
    per RFC 5880 §4.1 (0=No Diagnostic to 8=Reverse Concatenated Path
    Down), 9-31 reserved (leave as open enum, no explicit reserved-range
    values needed since protobuf enums are already open).
  - `BfdSession` Facet: `local_discriminator`, `remote_discriminator`
    (uint32 each — RFC 5880's opaque local/remote discriminator pair),
    `state (BfdSessionState)`, `diagnostic (BfdDiagnosticCode)`,
    `local_address`, `remote_address (IpAddress)`.

- **`net/service/dhcp/v1`** (new — no existing reservation; naming as a
  "service" package since DHCP/DNS/NAT are IP *services* layered above
  addressing, distinct from `net/ip` facts and `net/routing` path
  selection):
  - `DhcpRole` enum (new, normalized): `CLIENT`, `SERVER`, `RELAY`,
    `SNOOPING` — atlas identifies these four roles explicitly
    (`04-ip.md`-adjacent atlas section, `373-412`).
  - `DhcpMessageType` enum — registry pass-through would need two separate
    enums for v4 (DISCOVER/OFFER/REQUEST/DECLINE/ACK/NAK/RELEASE/INFORM,
    RFC 2131 §3.1) vs v6 (1-13, RFC 8415 §7.3) since the numbering spaces
    don't align — do not merge into one enum across IP versions.
  - `DhcpLeaseState` enum (new, normalized from RFC 2131 §4.4 client
    states): `INIT`, `SELECTING`, `REQUESTING`, `INIT_REBOOT`,
    `REBOOTING`, `BOUND`, `RENEWING`, `REBINDING`.
  - `DhcpLease` Facet: `client_identifier` (bytes or MAC via
    `net/addr/v1` EuiAddress), `assigned_address (IpAddress)`,
    `lease_time_seconds` (uint32), `t1_renew_seconds`, `t2_rebind_seconds`,
    `state (DhcpLeaseState)`. For v6: separate `Ia{ type (NA|PD), iaid,
    preferred_lifetime, valid_lifetime, addresses/prefixes }` since IA_NA
    and IA_PD are structurally different (address vs prefix).
  - `DhcpSnoopingBinding` Facet (atlas calls this out as more reliable
    than FDB+ARP join for endpoint location, `04-ip.md`-adjacent,
    `385-388`): `mac (EuiAddress)`, `ip (IpAddress)`, `vlan_id`,
    `interface_name`, `lease_expiry`.

- **`net/service/nat/v1`** (new):
  - `NatMappingBehavior` enum — normalized from RFC 4787 §4.1:
    `ENDPOINT_INDEPENDENT`, `ADDRESS_DEPENDENT`, `ADDRESS_AND_PORT_
    DEPENDENT`.
  - `NatSession` Facet: `protocol (IpProtocol from net/packet/v1 — reuse,
    this IS the L4 protocol, correctly distinct from RouteSourceProtocol)`,
    `private_address, private_port, public_address, public_port` (typed via
    `IpAddress` + a port scalar), `mapping_behavior`. Keep separate from
    `NatMapping` (the static/dynamic pool-level rule) vs `NatSession` (the
    live 4-tuple translation instance) — RFC 4008 models these as two
    tables for a reason.

- **`net/service/tunnel/v1`** or split further — recommend splitting by
  protocol since IPsec and WireGuard share almost no fields:
  - **IPsec**: `IkeSaState` enum (new, normalized — RFC 7296 doesn't
    enumerate states explicitly, derive from §1.2-1.4: `ESTABLISHING`,
    `ESTABLISHED`, `REKEYING`, `DELETING`), `IkeProposal{ encryption_
    algorithm, integrity_algorithm, dh_group }` (registry pass-through
    enums per IANA IKEv2 transform-type registries — not fetched for this dossier, flag as open question), `IkeSa{ local_address, remote_
    address, state, proposal }`, `ChildSa{ ike_sa ref-free back-pointer by
    key, spi, protocol (ESP|AH) }`. **Do not add a "negotiated lifetime"
    field on `IkeProposal`** — RFC 7296 §2.4 explicitly excludes it from
    the wire negotiation; if FlowSeer wants to surface a lifetime it must
    be a local-policy field on the FlowSeer-side config Facet, commented
    as such.
  - **WireGuard**: `WireGuardPeer` Facet: `public_key` (bytes, 32),
    `preshared_key_configured` (bool — never surface the key itself),
    `allowed_ips (repeated IpPrefix)`, `endpoint (IpAddress + port)`,
    `persistent_keepalive_seconds`, `latest_handshake` (timestamp),
    `transfer_rx_bytes`, `transfer_tx_bytes` (uint64 counters).

- **`net/protocol/pim/v1`** (multicast, primitive-level only):
  - `PimMode` enum — registry pass-through reusing `IANAipMRouteProtocol`
    values `pimSparseMode(8)`/`pimDenseMode(9)` rather than inventing a
    new 2-value enum, since the vendored MIB already assigns integers.
  - `MulticastRouteRow` Facet: `source (IpAddress, optional — absent means
    (*,G))`, `group (IpAddress)`, `rendezvous_point (IpAddress, optional)`.

- **`net/ip/v1` fix — `AddressOrigin` replacement.** Split the single
  conflated enum into two orthogonal fields on `InterfaceAddress`:
  - `AddressAssignment` enum (replaces `AddressOrigin`, normalized,
    `_UNSPECIFIED=0`): `OTHER`, `STATIC`, `DHCP`, `SLAAC` — collapses RFC
    4293/8344's `linklayer`+`random` into one `SLAAC` value, since both
    are SLAAC with different IID generation (axis A, correctly scoped to
    assignment mechanism only).
  - `IidGenerationMethod` enum (new, only meaningful when `assignment =
    SLAAC`): `UNSPECIFIED`, `EUI64` (link-layer derived), `OPAQUE_STABLE`
    (RFC 7217), `TEMPORARY` (RFC 8981) — axis B+C folded together since
    both only apply to SLAAC addresses and are mutually exclusive per
    address in practice.
  - Keep `IpLifetime{preferred, valid}` (already exists in `net/addr/v1`)
    as the carrier for RFC 8981's shorter temporary-address lifetimes —
    no new lifetime type needed, just populate the existing field with the
    temporary-address values when `IidGenerationMethod = TEMPORARY`.
  - This is a breaking change to `net/ip/v1/address_origin.proto` and
    `interface_address.proto` — per `AGENTS.md`, break it directly, no
    compatibility shim; state the removal of `AddressOrigin` as a fact in
    the plan.

## 5. Entity candidates (model/)

None of the above are entities by the conventions doc's own test (`docs/
conventions/protobuf.md:167-170`: "the moment a `net/` message grows a ref
it has become an Entity"). The primitives above are deliberately kept
ref-free. Entity candidates only emerge where FlowSeer needs to *track* a
specific instance across time with a device/tenant ref:

- **`BgpPeerSession` entity** (`model/routing/bgp/v1` or similar) — if
  FlowSeer needs Config/State/Event for "this device's peering with that
  remote AS," it would carry `DeviceGlobalRef` (or a network-instance-
  scoped parent), `local_asn`, `remote_asn`, `remote_address` as its
  identity key (LocalRef), embedding `BgpPeer` primitive fields in its
  State. Owning parent: the device (or network-instance entity, once one
  exists).
- **`DhcpLeaseRecord` entity** — if FlowSeer tracks lease history over
  time (not just current snapshot), it needs Config (none — pure
  observation) / State (current lease) / Event (lease granted/renewed/
  released) triad, owning parent likely the DHCP server entity or the
  device. The atlas explicitly favors the snooping-binding-table shape for
  this because it is more reliable than FDB+ARP join.
- **`RouteEntity`** — likely NOT needed as an entity; routes are typically
  too high-churn/high-cardinality for entity tracking and better modeled as
  a `RouteRow` primitive table refreshed wholesale per poll, per the
  atlas's own recommendation to treat static-route intent (Config-only,
  no State/Event) separately from observed RIB/FIB snapshots.
- **`StaticRouteIntent`** entity candidate — pure-intent (Config-only, no
  State/Event per the "no State, no Config suffix" — actually here it's
  the opposite: pure intent means present but no State/Event) shape for
  operator-declared static routes, keyed by (network_instance, prefix,
  next_hop) — Huawei's two-VPN-name key (per atlas) is a useful precedent
  for a VRF-aware identity key.
- **`IpsecTunnel` / `WireGuardPeer` entities** — if FlowSeer needs to track
  tunnel identity/config over time (not just current state), these need
  LocalRef/GlobalRef with device as parent; the primitives in §4 would
  populate their State.

None of these are committed shapes — flagged as candidates only, since this dossier is primitives-first with entities noted for the
planner to decide.

## 6. Traps

- **"Discard" is not an RFC 8349 value.** The base YANG model's
  `special-next-hop` has `blackhole` (silent), `unreachable`, `prohibit`,
  `receive` — four values, no plain "discard." Naming a `RouteAction`
  value `DISCARD` would misrepresent the spec; use `BLACKHOLE`.
- **OSPF interface-type value 4 is genuinely missing** (`broadcast(1),
  nbma(2), pointToPoint(3), pointToMultipoint(5)`), both in `OSPF-MIB` and
  confirmed via two independent fetches — don't "fix" this by renumbering
  when porting to a pass-through enum.
- **IANA route-protocol value 20 is `ttdp`, not `ttag`** (value 20 is easy to
  misremember as `ttag`), verified against both the IANA registry and the
  vendored `IANA-RTPROTO-MIB:48-79`.
- **IS-IS adjacency state is 4-valued (`UP/DOWN/INIT/FAILED`), not
  3-valued**, verified against OpenConfig
  `openconfig-isis-types.yang`, no contradicting source found.
- **BFD state 0 is a real, meaningful value** (`AdminDown`), not a
  synthetic zero — a naive `_UNSPECIFIED=0` normalization would collide
  with a genuine wire value; treat as registry pass-through matching
  `net/packet/v1`'s existing `IP_PROTOCOL_HOPOPT=0` precedent.
- **VRRP advertisement interval is in centiseconds on the wire** (RFC
  5798), a unit that's easy to silently drop a factor of 10 on when
  converting to the repo's millisecond-in-field-name convention.
- **VRRP priority 0 and 255 are reserved sentinels**, not just range
  bounds (0 = Master releasing, 255 = IP-address-owner) — don't validate
  the range as if all 256 values are equally ordinary priorities; document
  the sentinels in the field comment.
- **BGP FSM states from vendors are not guaranteed to be the MIB
  integers** — Ruckus's `BgpNeighborEntry.connectionState` is a bare
  `string`, confirmed from the vendored proto. Any provider-to-primitive
  mapping layer must normalize strings, not assume integer alignment.
- **RFC 7296 IKEv2 has no negotiated SA lifetime field** — modeling one on
  an `IkeProposal` message as if it were wire data is a spec error; it's
  local policy only (§2.4).
- **The `AddressOrigin` fix must not create a fourth new conflation.**
  RFC 8344's `random` value bundles RFC 7217 (algorithm) and RFC 8981
  (temporariness+lifetime) together as *examples* — the fix proposed in
  §4 separates them into `IidGenerationMethod` values `OPAQUE_STABLE` vs
  `TEMPORARY`, which are not mutually implied by the standards (an address
  can in principle be RFC-7217-stable *and* not temporary, or temporary
  without being RFC-7217-generated) — comment this ambiguity rather than
  asserting they're always paired.
- **OSPFv2 vs OSPFv3 area/router IDs have different underlying types**
  (dotted-quad `IpAddress`-shaped vs bare `Unsigned32`) — a single
  `AreaId` scalar type across both versions will misrepresent OSPFv3.
- **AFI/SAFI are two independent IANA registries**, not a combined
  enumeration — model as a pair, not a merged AFI_SAFI enum, so future
  registry growth in either dimension doesn't require touching the other.
- **`RouteSourceProtocol` (unicast route source) and
  `IANAipMRouteProtocol` (multicast route source) are separate registries**
  with overlapping-looking but distinct value spaces (e.g. multicast has
  `pimSparseMode(8)` where unicast has `ospf(13)` at a different integer)
  — do not reuse one enum for both route tables.
- **NAT session vs NAT mapping are two different RFC 4008 tables** — a
  session is a live 4-tuple translation instance; a mapping/address-map is
  the configured pool/rule that produces sessions. Collapsing them into one
  message loses the static-vs-dynamic distinction the MIB models
  explicitly.
- **Meraki was named in the dossier's provider list but has no vendored
  OpenAPI in this repo** — any Meraki-specific fact in this dossier would
  be unverified; none is included in the provider matrix for that reason.

## 7. Open questions for the planner

1. Does the DHCP/DNS/NAT/tunnel package family get a new top-level
   `net/service/` tree (as proposed here), or should it live under
   `net/ip/` despite the README's "deliberately absent" list, or under its
   own `net/<x>/v1` siblings matching `net/routing`'s reservation style?
   The architecture doc reserves `net/routing` explicitly but says nothing
   about DHCP/DNS/NAT/tunnels — this is a genuine gap, not something this dossier's sources resolve.
2. Should FHRP (VRRP/HSRP/GLBP) really share one package with a protocol
   discriminator (as the atlas recommends and this dossier proposes), or
   does the architecture doc's "protocols own their packages" rule
   (`:239-259`, written with BGP/OSPF/VRRP/CDP each getting their own
   package) argue for keeping them separate despite the semantic overlap?
   These two guiding sources point in different directions and the planner
   needs to pick one.
3. IKEv2 transform-type registries (encryption/integrity/DH-group IANA
   numbers) were not fetched for this dossier — needed before finalizing
   `IkeProposal`'s enum shapes.
4. Should `RouteRow` be a single message covering both RIB and FIB via a
   discriminator field, or two separate messages in `net/routing/v1`? The
   atlas calls for "an explicit RIB-vs-FIB discriminator" but doesn't say
   which shape; this dossier proposed a discriminator field for
   simplicity, but a planner might prefer separate types given the
   FIB-specific fields (ifIndex, hardware-offload flags MikroTik exposes
   as `hw-offloaded`).
5. Live Meraki Dashboard API research is needed (not available in this
   repo's vendored corpus) if Meraki-specific NAT/DHCP/VPN shapes are
   required before the planner finalizes the provider matrix.
6. Confirm exact RFC 8349 section numbers for `route-metadata` and
   `special-next-hop` groupings against the actual YANG module tree before
   citing a specific clause in a proto file comment — this dossier's two
   fetches disagreed (§5 vs §7) though the content itself was consistent.
