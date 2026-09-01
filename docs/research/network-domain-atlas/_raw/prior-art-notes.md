# Raw notes: prior art (working file)

## SNMP::Info (Perl, powers Netdisco) — the closest prior art to FlowSeer's collection layer

Normalisation model: a base class defines *normalized method names*, each backed by a
MIB object; per-vendor subclasses override the mapping or the post-processing. This is
exactly the "decoder declines / coerces" problem FlowSeer's `CONCEPTS.md` describes.

Globals: `uptime` (sysUpTime), `contact`, `name` (sysName), `location`, `layers`
(sysServices — the L1..L7 bitmask), `ports` (ifNumber), `ipforwarding`.

Interface table (all keyed by ifIndex):
`i_index` ifIndex · `i_description` ifDescr · `i_name` ifName · `i_alias` ifAlias ·
`i_type` ifType · `i_mtu` · `i_speed` (ifSpeed, falls back to ifHighSpeed) ·
`i_speed_raw` · `i_speed_high` (ifHighSpeed) · `i_mac` ifPhysAddress ·
`i_up` ifOperStatus · `i_up_admin` ifAdminStatus · `i_lastchange` ifLastChange ·
`if_ignore` (vendor-specific ignore list).

Counters, with the 32/64-bit split made explicit in the method name:
`i_octet_in`/`i_octet_out` vs `i_octet_in64`/`i_octet_out64` (ifHCInOctets),
`i_pkts_ucast_in(64)`, `i_pkts_multi_in(64)`, `i_pkts_bcast_in(64)`,
`i_pkts_nucast_*` (deprecated), `i_errors_in/out`, `i_discards_in/out`,
`i_bad_proto_in` (ifInUnknownProtos), `i_qlen_out`, `i_specific`.

IPv4 addressing: `ip_index`, `ip_table`, `ip_netmask`, `ip_broadcast` — and note the
method deliberately falls back from the deprecated `ipAddrTable` (ipAdEntAddr) to the
modern `ipAddressTable` (ipAddressIfIndex/ipAddressPrefix). That dual-source fallback
is a pattern FlowSeer needs.

Routing: `ipr_route`, `ipr_if`, `ipr_1`..`ipr_5` (metrics), `ipr_dest` (next hop),
`ipr_type`, `ipr_proto`, `ipr_age`, `ipr_mask`, `ipr_info` — all off the *deprecated*
`ipRouteTable`, which is why FlowSeer's own research doc is right to defer routing.

Topology: `has_topo()` returns which of `lldp cdp sonmp fdp edp amap` a device speaks,
then one unified neighbour view: `c_ip`, `c_if`, `c_port`, `c_id`, `c_platform`,
`c_cap`. **This is the key prior-art idea: a protocol-agnostic neighbour row with a
per-protocol source, not one table per discovery protocol.**

Device family coverage (partial, the ones that intersect FlowSeer's vendors):
Cisco IOS/CatOS/NX-OS/CiscoSB/C9800, HP Procurve + HP Virtual Connect, H3C/HP A-series,
Aruba, Huawei, Foundry/Brocade, DLink, EdgeSwitch, Netgear, Mikrotik RouterOS,
Ubiquiti APs, Extreme, Juniper, Arista, Cumulus, Dell PowerConnect, Fortinet, Palo Alto.

MIB-support mixin classes (the reusable decoder units): AdslLine, Aggregate,
Airespace, AMAP, Bridge, CDP, CiscoAgg/BGP/Config/PortSecurity/Power/QOS/RTT/Stack/
Stats/StpExtensions/VTP, DocsisCM/HE, EDP, Entity, EtherLike, IEEE802_Bridge,
IEEE802dot11, IEEE802dot3ad, IPv6, LLDP, MAU, NortelStack, PortAccessEntity,
PowerEthernet, RapidCity, SONMP.

Takeaway for FlowSeer: the mixin decomposition (one class per *MIB*, composed per
device family) is an alternative to FlowSeer's per-entity decoder decomposition. The
two differ on where "this device does it differently" is expressed.

## NAPALM getters (config-plane prior art, CLI/API driven)

`get_facts`, `get_interfaces`, `get_interfaces_counters`, `get_interfaces_ip`,
`get_arp_table`, `get_ipv6_neighbors_table`, `get_mac_address_table`, `get_vlans`,
`get_lldp_neighbors`, `get_lldp_neighbors_detail`, `get_route_to`,
`get_network_instances`, `get_bgp_config`, `get_bgp_neighbors`,
`get_bgp_neighbors_detail`, `get_optics`, `get_environment`, `get_users`,
`get_snmp_information`, `get_ntp_peers`/`get_ntp_servers`/`get_ntp_stats`,
`get_firewall_policies`, `get_probes_config`/`get_probes_results`, `get_config`,
`is_alive`, `cli`.

Schemas worth copying/avoiding:
- `get_facts` → uptime, vendor, model, hostname, fqdn, os_version, serial_number,
  interface_list. Note it flattens *one* serial and *one* model — breaks on stacks.
- `get_interfaces` → is_up, is_enabled, description, last_flapped, speed (float!),
  mtu, mac_address. `speed` as float in Mbps is a known NAPALM wart; FlowSeer's
  `uint64 speed_bps` is the better call.
- `get_vlans` → {vid: {name, interfaces}} — membership inverted onto the VLAN, which
  loses tagged-vs-untagged. FlowSeer's switchport facet keeps it on the port instead.
- `get_arp_table` → interface, mac, ip, age. No VRF key → same VRF-blindness FlowSeer's
  own research flagged for routing.

Not all drivers implement all getters; the driver declares unsupported by raising
`NotImplementedError`. That is NAPALM's version of "declining".

## LibreNMS discovery modules (the de-facto list of "things worth discovering")

os · ports · ports-stack · xdsl · entity-physical · processors · mempools ·
cisco-vrf-lite · ipv4-addresses · ipv6-addresses · route · sensors · storage ·
hr-device · discovery-protocols (xDP/OSPF/OSPFv3/BGP) · arp-table · fdb-table ·
discovery-arp · junose-atm-vp · bgp-peers · vlans · mac-accounting · cisco-pw · vrf ·
cisco-cef · slas · vminfo · printer-supplies · ucd-diskio · services · stp · ntp ·
loadbalancers · mef · wireless · applications.

Sensor discovery is filesystem-dispatched: `includes/discovery/sensors/$class/$os.inc.php`
— i.e. the per-OS override lives in a *file path*, not in code branches. ~350 vendor
OS definitions.

## Netdisco (PostgreSQL schema — the storage-shape prior art)

`device` (ip, mac, serial, vendor, model, snmp settings) · `device_ip` ·
`device_port` (status, speed, duplex, vlan, remote connectivity) ·
`device_port_vlan` (per-port VLAN membership rows) · `device_port_log` ·
`device_power` + `device_port_power` (PoE) · `node` (MAC ↔ port ↔ vlan ↔ first/last
seen) · `node_ip` (IP ↔ MAC over time) · `node_monitor` · `admin` (job queue).

The `node`/`node_ip` split with `time_first`/`time_last` is the canonical answer to
"where did this MAC/IP live over time" — FlowSeer's FDB and neighbour tables currently
have no temporal axis at all.

## SuzieQ (normalized multi-vendor state tables)

Arpnd · BGP · Device · EvpnVni · Filesystem · Interfaces · Inventory · LLDP · CDP ·
Macs · MLAG · Ospf · Routes · sqPoller · VLAN.

Note it keeps LLDP and CDP as *separate* tables (opposite choice from SNMP::Info's
unified `c_*`), and it has `sqPoller` — a first-class table recording *the collection
attempt itself*, which is precisely FlowSeer's Provenance concept.

## NetBox / Nautobot (intent + documentation model, not collected state)

Device → Interface (typed, with `mgmt_only`, LAG parent, MTU, MAC), Cable, Prefix,
IPAddress, VLAN (VID unique within a *scope*/group — not globally), VLANGroup, VRF,
Site/Region/Location, Rack, DeviceType/Module, Tag, CustomField.

Two ideas FlowSeer already mirrors: tags as a cross-cutting label tree, and custom
fields ≈ Attribute Definition/Assignment. One it does not: VLAN uniqueness scoped to a
VLANGroup rather than to a device — the same "bridge domain identity" gap FlowSeer's
net-core research called its largest unresolved dependency.

## OpenConfig model areas (the modern decomposition)

interfaces · if-ethernet/-aggregate/-ip/-poe/-types/-ethernet-ext/-ip-ext ·
vlan · lacp · lldp · spanning-tree · macsec · acl · packet-match · qos · aft ·
network-instance (+ -l2/-l3/-policy) · local-routing · bgp (+rib) · isis · ospfv2 ·
pim · igmp · mpls (+ldp/rsvp/sr/te) · segment-routing · srte-policy · bfd · evpn ·
ethernet-segments · nat · relay-agent · routing-policy · defined-sets · platform
(+cpu/fan/linecard/port/psu/transceiver) · system (+logging/terminal/grpc/management) ·
aaa (+radius/tacacs) · keychain · alarms · messages · procmon · probes · sampling ·
telemetry · openflow · p4rt · gribi · hashing · multicast · oam · ptp · pcep ·
access-points · ap-manager · wifi-mac · wifi-phy · wifi-types · optical transport set
(terminal-device, wavelength-router, optical-amplifier, channel-monitor, …).

## IETF ietf-interfaces (RFC 8343) — the naming/index caveats that matter

- Key is `name` (a string), **not** an index. `if-index` is a *state* leaf, present
  only under the `if-mib` feature.
- RFC 8343 deleted RFC 7223's `/interfaces-state` subtree (NMDA): operational state now
  lives beside config under `/interfaces/interface` as `config false`.
- Explicitly acknowledges IF-MIB permits **duplicate ifName values**, so the
  YANG-name ↔ ifIndex mapping is not reliably 1:1. Any model keyed on interface *name*
  (which FlowSeer's tables are — "rows reference interfaces by name") inherits this.
- system-controlled vs user-controlled interfaces: whether a name may be invented is a
  server capability (`arbitrary-names` feature).
- Media-specific augments hang off `when "type = 'ethernetCsmacd'"` etc. — the YANG
  equivalent of FlowSeer's facet-presence discriminator.
