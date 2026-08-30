---
title: Entity records — security and AAA
date: 2026-08-30
part: 7 of 10
scope: 802.1X and port access, AAA, port security, firewall, PKI and crypto
---

# Security and AAA

Five entities. One of them — [dot1x](#dot1x) — is quietly the most valuable
unmodelled entity in the whole atlas for a network-management product, because
its session table is an *authenticated* endpoint inventory: MAC, port, VLAN,
username, and authorisation result in one row.

Index: [dot1x](#dot1x) · [aaa](#aaa) · [port-security](#port-security) ·
[firewall](#firewall) · [pki-crypto](#pki-crypto)

Also relevant: [l2-security-guard](04-ip.md#l2-security-guard) (first-hop
security) and [macsec](03-switching.md#macsec).

---

## dot1x

**What it is.** Port-based network access control (802.1X), plus the two
fallbacks that always ship with it — MAC authentication bypass (MAB) and web
authentication (captive portal) — and the *result* of authentication: a VLAN, a
role, an ACL, pushed from the RADIUS server.

### Canonical models

`IEEE8021X-PAE-MIB` (802.1X-2010; the corpus has 4 revisions plus the older
`IEEE8021-PAE-MIB`):

| Table | Key | Carries |
|---|---|---|
| `dot1xPaePortTable` | `dot1xPaePortNumber` | port capabilities, initialize, reauthenticate |
| `dot1xAuthConfigTable` | `dot1xPaePortNumber` | `dot1xAuthPaeState`, `dot1xAuthBackendAuthState`, `dot1xAuthAdminControlledDirections`, `dot1xAuthAuthControlledPortStatus` (**authorized / unauthorized** — the answer), `dot1xAuthAuthControlledPortControl` (forceAuthorized / auto / forceUnauthorized), reauth period, quiet period, tx period, max req |
| `dot1xAuthStatsTable` | port | EAPOL frame counters |
| `dot1xAuthDiagTable` | port | state-machine transition counters — the diagnosis |
| `dot1xAuthSessionStatsTable` | port | **`dot1xAuthSessionUserName`**, octets, duration, terminate cause |

**`dot1xPaePortNumber` is yet another port namespace.** On most implementations
it equals `ifIndex`; on some it is the `dot1dBasePort`.

The 802.1X-2010 MIB is **single-supplicant-per-port** by design. Real networks
run multi-auth (a phone and a PC behind it, or a hub), and *every vendor added a
private multi-session table* — that is the single most consistent divergence in
this record.

`openconfig` has no 802.1X model of its own; Ruckus added
`icx-openconfig-aaa-aug` `system/aaa/authentication/dot1x`.

### Vendor mapping

| Family | Base | Multi-session extension |
|---|---|---|
| IEEE | `IEEE8021X-PAE-MIB` | — |
| HP ProCurve | `HP-DOT1X-EXTENSIONS-MIB::hpicfDot1xPaePortTable [AUG dot1xPaePortEntry]`, `hpicfDot1xAuthConfigTable [AUG]` | `hpicfDot1xSMAuthConfigTable[paePort, macAddr]` |
| Cisco SMB | `CISCOSB-DOT1X-MIB::rldot1xExtAuthSessionStatsTable [AUG]`, `rldot1xAuthenticationPortTable[dot1xPaePortNumber]`, `rldot1xUnAuthenticatedVlanTable[dot1qFdbId]` (**guest VLAN keyed on FID**) | `rldot1xAuthMultiStatsTable[portNumber, sourceMac]` |
| D-Link | `DLINKSW-DOT1X-EXT-MIB::dDot1xExtPaePortTable[portNumber]`; DGS-1210 line `swAuthPortAccessControlTable[portNumber]` | `dDot1xExtAuthStatsTable[portNumber, macAddr, vlanId]` — **MAC and VLAN in the key** |
| Aruba CX | — | `ARUBAWIRED-PORT-ACCESS-MIB::arubaWiredPortAccessClientTable[portName, mac]` + `arubaWiredPortAccessRoleTable[roleName]` — **the client table with an assigned role**, the cleanest expression of the modern model |
| Comware | `HH3C-8021PAE-MIB::hh3cdot1xAuthConfigExtTable[dot1xPaePortNumber]`, `HH3C-8021X-EXT2-MIB` | |
| Huawei | `HUAWEI-AAA-MIB::hwDot1xPortConfigTable[portIndex]`, `hwDot1xSystemConfigTable[templateIndex]`, `hwDot1xAccessProfileTable[profileName]`, `hwDot1xSessionDisplayByMacTable[userMac]` | that last one is the multi-session view |
| FASTPATH family | `agentDot1xPortConfigTable[ieee8021XPaePortNumber]`, `agentDot1xAuthenticatorPortConfigTable [AUG]`, `FASTPATH-DOT1X-ADVANCED-FEATURES-MIB`, `FASTPATH-DOT1X-AUTHENTICATION-SERVER-MIB` (the switch as a *local* auth server), `FASTPATH-MAB-MIB`, `FASTPATH-AUTHENTICATION-MANAGER-MIB` (method ordering) | |
| LANCOM SX | `lcsDot1xSupplicantTable[index]`, GS2310's `gs2310Dot1xSupplicantTable[index]` — the switch as a *supplicant*, authenticating itself upstream | |
| Ruckus ICX | `FOUNDRY-SN-MAC-AUTHENTICATION-MIB::snMacAuthTable[ifIndex, vlanId, mac]`, `snMacAuthClearIfCmdTable[ifIndex]` | MAB only |
| HP BladeSystem | `dot1xCurCfgPortTable` / `dot1xNewCfgPortTable` / `dot1xInfoPortTable` | |

### The other two access methods

**MAC authentication (MAB)**: `FASTPATH-MAB-MIB`, `DLINKSW-MAC-AUTH-MIB`,
`HUAWEI-MAC-AUTHEN-MIB` (`hwMACAuthenAccessProfileTable[profileName]`),
`FOUNDRY-SN-MAC-AUTHENTICATION-MIB`.

**Web auth / captive portal**: `EdgeSwitch-CAPTIVE-PORTAL-MIB`
(`cpCaptivePortalTable[instanceId]`, `cpLocalUserTable[userIndex]`,
`cpLocalUserGroupTable`, `cpLocalUserGroupAssociationTable[userIndex,
groupIndex]` — a complete local user database), `DLINKSW-WEB-AUTH-MIB`,
`DLINKSW-JWAC-MIB` (Japanese Web Auth), `HH3C-WEB-AUTHENTICATION-MIB`,
`HH3C-PORTAL-MIB` (`hh3cPortalServerTable[serverName]`,
`hh3cPortalIfInfoTable[ifIndex]`), `HUAWEI-PORTAL-MIB`,
`CISCOSB-WBA-MIB`, `CPPM-MIB::webAuthProtoTable[protocolIdx]` (Aruba
ClearPass), `DLINKSW-NETWORK-ACCESS-MIB` (the unified method manager).

### Why this matters most

Join `arubaWiredPortAccessClientTable[portName, mac]` (or its equivalent) with
[fdb](03-switching.md#fdb) and [arp-nd](04-ip.md#arp-nd) and you get an
endpoint record with an *identity* attached, not just an address. That is the
difference between "a MAC is on port 12" and "alice's laptop is on port 12, in
VLAN 30, with the contractor role". Netdisco, LibreNMS, and SuzieQ all stop at
the MAC. Modelling the session is a genuine differentiator, and every vendor in
the corpus exposes it in some form.

The obstacle is that the *shape* differs: per-port single session (IEEE), per
port+MAC (ProCurve, Cisco SMB, Aruba), per port+MAC+VLAN (D-Link), per MAC
(Huawei). A model keyed on `(interface, mac)` with VLAN as a column covers all
five, at the cost of merging D-Link's two-VLAN-one-MAC edge case.

---

## aaa

**What it is.** Who may manage the device and who may attach to it: RADIUS and
TACACS+ server lists, method ordering, local users, roles and privilege levels,
and accounting.

### Canonical models

- `RADIUS-AUTH-CLIENT-MIB` (RFC 4668) `radiusAuthServerTable[index]` and
  `RADIUS-ACC-CLIENT-MIB` (RFC 4670) `radiusAccServerTable[index]` — server
  identity plus request/response/timeout/retransmit counters. Widely
  implemented, genuinely useful for diagnosing "authentication is slow".
- `TACACS-CLIENT-MIB` — much less common.
- `openconfig-aaa` + `-radius` + `-tacacs`:
  `system/aaa/{authentication, authorization, accounting}`,
  `server-groups/server-group[name]/servers/server[address]` with
  `radius/config` or `tacacs/config`, and `authentication/users/user[username]`.
  Ruckus ICX augments this (`icx-openconfig-aaa-aug`).

### Vendor mapping

| Family | Surface |
|---|---|
| Aruba CX | `ARUBAWIRED-AAA-MIB::arubaWiredRadiusServerTable[vrfName, address, port, portType]` and `arubaWiredTacacsServerTable[vrfName, address, port]` — **VRF in the key**, the only vendor that gets management-plane VRF right in the MIB |
| Cisco SMB | `CISCOSB-AAA`: `rlAAAMethodListTable[listName]`, `rlAAALineTable[linePortType, ifIndex, serviceType]`, `rlAAALocalUserTable[userName]`, `rlAAAUserTable[index]`; `CISCOSB-RADIUSSRV` (the switch as a RADIUS *server*) |
| Comware | `HH3C-AAA-MIB` (`hh3cAAASlotStatTable[chassisId, slotId]`), `HH3C-DOMAIN-MIB` (`hh3cDomainInfoTable[domainName]`, `hh3cDomainSchemeTable[domainName, schemeIndex]` — **the ISP-domain model**, where AAA method is selected by the domain part of the username), `HH3C-RADIUS-MIB`, `HH3C-LOCAL-AAA-SERVER-MIB`, `HH3C-AAA-NASID-MIB`, `HH3C-RBAC-MIB` |
| Huawei | `HUAWEI-AAA-MIB`: `hwAuthenSchemeTable[schemeName]`, `hwAcctSchemeTable[schemeName]`, `hwDomainTable[domainName]` — the same domain/scheme model as Comware; plus `HUAWEI-HWTACACS-MIB`, `HUAWEI-BRAS-RADIUS-MIB` |
| HP ProCurve | `HP-USER-AUTH`, `HP-AUTZ-MIB`: `hpSwitchAutzUserRoleTable[roleName]`, `hpSwitchLocalMgmtPrivGroupsTable[groupIndex]`, `hpSwitchLocalMgmtPrivCommandsTable[groupIndex, cmdSequenceIndex]` — **per-command authorisation expressed in SNMP**, unusually granular |
| D-Link | four MIBs — `DLINKSW-AAA-COMMON-MIB`, `-AAA-AUTH-MIB`, `-AAA-ACCOUNTING-MIB` (`dAaaAcctGeneicAcctMethodTable[type, name, priority]` (sic), `dAaaAcctCommandsAcctMethodTable[privLevel, listName, priority]`), `-AAA-SERVER-MIB` |
| FASTPATH family | `FASTPATH-RADIUS-AUTH-CLIENT-MIB`, `FASTPATH-LDAP-CLIENT-MIB` (**LDAP as an auth backend**, rare), `agentUserAuthenticationConfigTable [AUG agentUserConfigEntry]`, `agentRadiusServerConfigTable[index]`, `agentRadiusAccountingConfigTable[index]` |
| LANCOM SX GS2310 | `gs2310RADIUSAuthenticationServerTable[index]`, `gs2310RADIUSAccountingServerTable[index]`, `gs2310TACACSPlusAuthenticationServerTable[index]`, `gs2310RADIUSStatisticsTable[serverIndex]` |
| LANCOM LCOS | `lcsStatusTcpIpRadiussAccessClientsTable[ipAddress]` etc. — LCOS devices are RADIUS *servers* as well as clients; plus `lcsStatusWlanRadiusCacheTable[macAddress]` |
| LANCOM LX | `lcosLXSetupRADIUSRADIUSServer[name]`, `lcosLXSetupRADIUSLANSupplicant[interfaceName]`, `lcosLXSetupRADIUSWLANSupplicant[profileName]` |
| Ruckus ICX | `FOUNDRY-SN-SWITCH-GROUP-MIB::snRadiusServerTable[serverIp]`, `snTacacsServerTable[serverIp]` |
| Ruckus wireless | `RUCKUS-ZD-AAA-MIB::ruckusZDAAAConfigTable[configID]`, `ruckusZDAAASvrTable[configID]` |
| Aruba wireless | `CPPM-MIB` (ClearPass: `radiusServerTable[hostname]`, `radiusServerAuthTable[sourceIdx]`, `policyServerAutzTable[sourceIdx]`, `tacacsAuthTable[hostname]`), `WLSX-USER-MIB` / `WLSX-USER6-MIB` |
| Cisco IOS-XE | `Cisco-IOS-XE-aaa-oper::aaa-users/aaa-sessions[aaa-uid]`, `aaa-user-info[username]` — **live session list**, which the MIBs lack |

### The domain model

Comware and Huawei both key AAA on an **ISP domain** parsed out of the username
(`user@domain`), with a scheme per domain. Nobody else does. It is a genuine
structural difference: on those platforms "which RADIUS server authenticated
this user" is a function of the username, not of the port. Any normalised AAA
model must either carry the domain or accept lossiness there.

---

## port-security

**What it is.** Limiting which MAC addresses may appear on a port (a count, a
static list, or sticky learning), plus the broader family of control-plane and
denial-of-service protections that vendors bundle with it.

### Two distinct things under one name

**1. MAC-limiting port security** — a per-port max count, a learned/sticky MAC
list, and a violation action (protect / restrict / shutdown).

| Family | Surface |
|---|---|
| Aruba CX | `ARUBAWIRED-PORTSECURITY-MIB::arubaWiredPortSecurityPortTable[ifIndex]`, `arubaWiredPortSecurityClientTable[portName, mac]`, `arubaWiredPortSecurityMacCfgTable[ifIndex, staticMacType, staticClientMac]` |
| FASTPATH family | `agentPortSecurityTable[ifIndex]`, `agentPortSecurityDynamicTable[ifIndex, vlanId, macAddress]` |
| Comware | `HH3C-PORT-SECURITY-MIB::hh3cSecurePortTable[ifIndex]`, `hh3cSecureAddressTable[ifIndex, mac, vlanID]` |
| Huawei | `HUAWEI-L2MAM-MIB::hwPortSecurityTable[port]`, `HUAWEI-MACBIND-MIB` |
| D-Link | `DLINKSW-PORT-SECURITY-MIB` |
| Cisco IOS-XE | `Cisco-IOS-XE-port-security-rpc` — RPCs only, no oper model |

Note that `agentPortSecurityDynamicTable` and `hh3cSecureAddressTable` are, in
effect, another copy of the FDB with policy attached. Three entities
([fdb](03-switching.md#fdb), [dot1x](#dot1x), port-security) all observe the
same underlying fact: *this MAC is on this port in this VLAN*. A model that
keeps them separate must at least make the relationship explicit.

**2. Control-plane and DoS protection** — a different feature that happens to
share the security label:

| Family | Surface |
|---|---|
| D-Link | `DLINKSW-SAFEGUARD-ENGINE-MIB`, `DLINKSW-DOS-PREVENT-MIB` (`dDosPrevCtrlTable[attackType]`), `DLINKSW-CPU-PROTECT-MIB` (`dCpuProtectProtoRateLimitTable[protoType]`, `dCpuProtectSubIntfRLTable`, plus per-unit counters), `DLINKSW-NETWORK-PROTOCOL-PORT-PROTECT-MIB`, `DLINKSW-CPU-ACL-MIB` |
| FASTPATH family | `FASTPATH-DENIALOFSERVICE-PRIVATE-MIB`, `EdgeSwitch-LLPF-PRIVATE-MIB` (`agentSwitchLlpfPortConfigTable[ifIndex, protocolType]` — link-local protocol filtering) |
| Huawei | `HUAWEI-ATK-MIB` / `-ATK-EUDM-MIB`: `hwAtkSynFloodIPTable[vrfName, ip]`, `hwAtkUdpFloodIPTable`, `hwAtkIcmpFloodIPTable` — **detected attack sources, per VRF**; plus `HUAWEI-SECSTAT-MIB`, `-SECSTAT-IP-MONITOR-MIB`, `-SECSTAT-EUDM-MIB` |
| Comware | `HH3C-SECHIGH-MIB` |
| Cisco SMB | `CISCOSB-SECURITY-SUITE` |

Huawei's attack tables are the interesting outlier: they are *observations of
attacks*, i.e. events, not configuration. That belongs with
[syslog-events](09-ops.md#syslog-events), not here.

---

## firewall

**What it is.** Stateful policy between zones, plus the session table.

Almost absent from the switch vendors and rich on the router/gateway vendors —
which is exactly what you would expect, and confirms that FlowSeer's device
classes mostly do not need it.

| Family | Surface |
|---|---|
| Comware | `HH3C-FIREWALL-MIB`, `HH3C-IDS-MIB`, `HH3C-SESSION-MIB`, `HH3C-IPSEC-MONITOR-MIB` / `-V2-MIB`, `HH3C-IKE-MONITOR-MIB`, `HH3C-SSLVPN-MIB`, `HH3C-LI-MIB` (lawful intercept), `hh3cCBQoSFirewallCfgInfoTable[behaviorIndex]` (firewall as a QoS behaviour!) |
| Huawei | `HUAWEI-SZONE-MIB` (security zones), `HUAWEI-ASPF-EUDM-MIB` (`hwAspfEudmAppEnableTable[vrfName, zoneID1, zoneID2]` — **zone-pair keyed**), `HUAWEI-IPSESSION-MIB` (`hwIpSessIfCfgTable[ifIndex]`), `HUAWEI-NAT-MIB`, `HUAWEI-VGMP-MIB`, `HUAWEI-LI-MIB` |
| LANCOM LCOS | `lcsStatusIpv6FirewallForwardingFilterTable[idx]`, `lcsStatusIpv6FirewallInboundFilterTable[idx]`, `lcsStatusIpv6FirewallLogTableTable[idx]`, and **`lcsStatusIpv6FirewallForwardingSessionsTable[srcAddress, dstAddress, prot, srcPort, dstPort, srcInterface]`** — a live 6-tuple session table over SNMP, which is unusual and genuinely useful |
| D-Link | `ZONE-DEFENSE-MGMT-MIB::swZoneDefenseTable[address]`, `swZoneDefenseMacTable[mac]` — the switch acting on quarantine instructions from a D-Link firewall |
| Cisco IOS-XE | `Cisco-IOS-XE-crypto-oper`, `-crypto-events` for IPsec |
| MikroTik | RouterOS `/ip/firewall/{filter,nat,mangle,connection}` over REST — the real surface for RouterOS |

**The zone-pair key** (`[zoneID1, zoneID2]` on Huawei) is the canonical
firewall-policy shape and does not resemble any of the interface+direction
bindings elsewhere in this atlas. If firewalls ever enter scope, that is the
model to start from — not an extension of [acl](06-qos.md#acl).

---

## pki-crypto

**What it is.** Device certificates, SSH host keys, key chains for routing
protocol authentication, and the TLS/SSH server configuration.

| Family | Surface |
|---|---|
| Cisco SMB | `CISCOSB-SSH-MIB`: `rlSshServerHostPublicKeyTable[algorithm, fragmentId]` (**keys returned in fragments**, because SNMP octet strings are size-limited), `rlSshServerHostPublicKeyFingerprintTable[algorithm, digestFormat]`, `rlSshServerAuthorizedUsersPublicKeyTable[userName, fragmentId]`; `CISCOSB-SSL`, `CISCOSB-DIGITALKEYMANAGE-MIB` |
| Comware | `HH3C-SSH-MIB::hh3cSSHUserConfigTable[userName]`, `HH3C-RSA-MIB::hh3cRSALocalKeyPairTable[keyIndex]` + `hh3cRSAPeerPublicKeyTable[keyName]`, `HH3C-TRNG-MIB` / `-TRNG2-MIB` |
| Huawei | `HUAWEI-SECURITY-PKI-MIB` (`rootCertificateDescriptionTable[index]`, `clientCertificateDescriptionTable[index]`), `HUAWEI-KEYCHAIN-MIB` (`hwKeychainTable[keychainId]`, `hwKeyIdTable[keychainId, keyId]`), `HUAWEI-MASTERKEY-MIB`, `HUAWEI-SSH-MIB`, `HUAWEI-SSL-MIB` |
| D-Link | `DLINKSW-SSH-MIB` (`dSshCryptoKeyPairTable[index]`, `dSshConnectionTable[sessionId]`), `DLINKSW-SSL-MIB`, `DLINKSW-SSH-CLIENT-MIB`, `dRipKeyChainTable[name]` + `dRipKeyChainConfTable[name, keyID]` |
| LANCOM LCOS | `lcsStatusCertsDeviceCertificatesTable[filename]`, `lcsSetupVpnCertificatesAndKeysIkeKeysTable[name]`, `lcsSetupRoutProtBfdKeyChainsTable[name, number]` |
| FASTPATH family | `FASTPATH-KEYING-PRIVATE-MIB::agentFeatureKeyingTable[index]` — **feature licence keys**, not crypto keys. Name collision; see [license](01-platform.md#license) |
| OpenConfig | `openconfig-keychain`: `keychains/keychain[name]/keys/key[key-id]` with `send-lifetime` / `receive-lifetime` — used by IS-IS, BGP, OSPF authentication |
| Cisco IOS-XE | `Cisco-IOS-XE-crypto*` (5 modules), `Cisco-IOS-XE-boot-integrity-oper` `sudi-certificate` |

Two things worth carrying forward:

- **Certificate expiry is a real operational fault** and only LANCOM, Huawei,
  and Cisco expose it. A device whose web certificate expired is unmanageable
  through its UI while looking perfectly healthy over SNMP.
- **Key chains** (`openconfig-keychain`, `HUAWEI-KEYCHAIN-MIB`,
  `dRipKeyChainTable`) are a shared object referenced by several routing
  protocols, with per-key send/receive lifetimes. If routing protocol
  authentication is ever modelled, model the keychain once.
