---
title: Entity records — QoS and ACLs
date: 2026-08-30
part: 6 of 10
scope: classification and policy, queues and schedulers, access control lists
---

# QoS and ACLs

Three entities that share one substrate: a **match expression** plus an
**action**, attached to an interface in a direction. FlowSeer's `net/packet/v1`
was built to be that substrate, and its research doc deferred a monolithic
matcher "until its first real consumer". These three entities, plus
[routing-policy](04-ip.md#routing-policy), are that consumer.

Index: [qos-policy](#qos-policy) · [qos-queue](#qos-queue) · [acl](#acl)

---

## qos-policy

**What it is.** Classify traffic, then mark, police, or steer it.

### Canonical models

**`DIFFSERV-MIB` (RFC 3289)** is the standard, and it is unusually well
designed — worth studying even though almost nobody implements it fully:

| Table | Key | Role |
|---|---|---|
| `diffServDataPathTable` | `ifIndex, direction` | the entry point: which functional-block chain applies to this interface+direction |
| `diffServClfrTable` | `diffServClfrId` | a classifier |
| `diffServClfrElementTable` | `clfrId, elementId` | one branch of it, pointing at a filter and a next block |
| `diffServMultiFieldClfrTable` | `id` | the actual 6-tuple filter (src/dst address+mask, DSCP, protocol, port ranges) |
| `diffServMeterTable`, `diffServTBParamTable` | | policing |
| `diffServActionTable`, `diffServDscpMarkActTable`, `diffServCountActTable` | | actions |
| `diffServAlgDropTable`, `diffServRandomDropTable` | | drop behaviour |
| `diffServQTable`, `diffServSchedulerTable`, `diffServMinRateTable`, `diffServMaxRateTable` | | queuing |

The design point: **functional blocks chained by pointer**, so an arbitrary
classify→meter→mark→queue pipeline is expressible. Vendors almost universally
replaced it with a flatter class-map/policy-map model because the pointer chain
is miserable to walk over SNMP.

`openconfig-qos` (with `-elements`, `-interfaces`, `-mem-mgmt`, `-types`) is the
modern equivalent: `qos/classifiers/classifier[name]/terms/term[id]/
{conditions, actions}`, `qos/forwarding-groups`, `qos/queues`,
`qos/scheduler-policies`, `qos/interfaces/interface[id]/{input, output}`.

### Vendor mapping

Two families of shape:

**Class-map / policy-map** (the Cisco MQC pattern, adopted widely):

| Family | Surface |
|---|---|
| D-Link | `DLINKSW-QOS-MIB::dQosClassMapTable[name]`, `dQosPolicyMapTable[name]`, `dQosPolicyMapCfgTable[policyMapName, classMapName]` — the cleanest expression of the pattern in the corpus |
| Comware | `HH3C-CBQOS2-MIB`: `hh3cCBQoSClassifierCfgInfoTable[classifierIndex]`, `hh3cCBQoSMatchRuleCfgInfoTable[classifierIndex, matchRuleIndex]`, plus per-match-type tables (`hh3cCBQoSMatchCpProtoCfgTable`, `hh3cCBQoSMatchCpGroupCfgTable`) and behaviour tables; `HH3C-IFQOS2-MIB` for the interface binding |
| Huawei | `HUAWEI-CBQOS-MIB` (same lineage), `HUAWEI-IF-QOS-MIB`, `HUAWEI-HQOS-MIB` (hierarchical), `HUAWEI-XQoS-MIB`, `HUAWEI-BRAS-QOS-MIB` (profile-based, for subscriber QoS) |

**Flat per-port maps** (the access-switch pattern):

| Family | Surface |
|---|---|
| FASTPATH family | `FASTPATH-QOS-COS-MIB`: `agentCosMapIpDscpTable[ifIndex, dscpValue]`, `agentCosMapIpPrecTable[ifIndex, precValue]`, `agentCosMapIntfTrustTable[ifIndex]` (trust boundary), `agentCosQueueControlTable[ifIndex]`; `FASTPATH-QOS-DIFFSERV-PRIVATE-MIB` for the MQC-ish part |
| LANCOM SX GS2310 | `gs2310QosPortDSCPTable[Port]`, `gs2310QosDSCPTable[List]`, `gs2310QosDSCPTranslationTable[List]`, `gs2310QosDSCPClassificationTable[QoSClass, DPL]` |
| HP ProCurve | `CONFIG-MIB::hpSwitchCosDSCPPolicyConfigTable[index]` |
| Foundry lineage | `snQosProfileTable[index]` |
| Cisco SMB | `CISCOSB-POLICY-MIB` — a *classifier/rule* framework with keys like `[classifierType, listIndex, subListIndex, index]`; `CISCOSB-QOS-APPS-MIB`, `CISCOSB-QOS-CLI-MIB` |
| HP BladeSystem | `qosCurCfgPortPriorityTable` / `qosNewCfgPortPriorityTable`, `qosCurCfgPriorityCoSTable` — the current/new pattern again |
| MikroTik | `mtxrQueueSimpleTable[index]`, `mtxrQueueTreeTable[index]` — RouterOS's own model, closer to Linux tc than to anything else here |

### The trust boundary

`agentCosMapIntfTrustTable` names the most operationally important QoS fact on
an access switch: **does this port trust the incoming CoS/DSCP marking, or
remark it**. It is one leaf, it is per-port, and it determines whether the
entire QoS design works. Every vendor has it somewhere and almost no
cross-vendor model surfaces it. If FlowSeer models one QoS thing, model this.

---

## qos-queue

**What it is.** Egress queues, the scheduler that drains them, shapers, and
congestion avoidance.

### Canonical models

`DIFFSERV-MIB::diffServQTable`, `diffServSchedulerTable`,
`diffServMinRateTable`, `diffServMaxRateTable`, `diffServAlgDropTable`,
`diffServRandomDropTable` (which is WRED).

`IEEE8021-FQTSS-MIB` for 802.1Qav credit-based shaping:
`ieee8021FqtssTxSelectionAlgorithmTable[componentId, port, trafficClass]`,
`ieee8021FqtssBapTable`, `ieee8021FqtssSRClassToPriorityTable` — the
component-and-port key again.

`IEEE8021-PFC-MIB` for 802.1Qbb priority flow control.

`openconfig-qos` `queues`, `scheduler-policies/scheduler-policy[name]/
schedulers/scheduler[sequence]/{inputs, one-rate-two-color, two-rate-three-color}`.

### Vendor mapping

| Family | Surface |
|---|---|
| D-Link | `dQosCosToQueueMapTable[cos]`, `dQosQueueBandwidthCtrlTable[dot1dBasePort, queueId]`, `DLINKSW-WRED-MIB::dWredStateTable[ifIndex, queueId]` + `dWredProfileTable[profileId, type]` |
| Comware | `HH3C-CBQOS2-MIB::hh3cCBQoSQueueCfgInfoTable[behaviorIndex]`, `hh3cCBQoSWredCfgInfoTable`, `hh3cCBQoSWredClassCfgInfoTable[behaviorIndex, wredClassValue]`, `hh3cCBQoSIfQueueRunInfoTable[ifIndex, direction, classIndex]` — **run-time queue stats**, which is the part operators want |
| Huawei | `HUAWEI-HQOS-MIB` and the BRAS profile set (`hwBRASQoSSchedulerProfileTable`, `hwBRASQoSQueueProfileTable`, `hwBRASQoSDropProfileTable`, `hwBRASQoSQueueClassTable`) |
| Cisco SMB | `CISCOSB-QUEUE-STATISTICS-MIB`, `CISCOSB-WeightedRandomTailDrop-MIB` (WRTD, not WRED), `rlQosCosQueueTable[cosIndex]`, `rlQosDscpQueueTable[dscpIndex]`, `rlQosTcpPortQueueTable[tcpPort]` |
| LANCOM SX | `lcsQosWREDTable[queue]`, `lcsQosPortShaperTable[ifIndex]`, GS2310's `gs2310QosPortSchedulerTable[Port, Queue]` and `gs2310QosPortSchedulerModeTable[Port]` |
| HP ProCurve | `HP-ICF-RATE-LIMIT-MIB::hpEgressRateLimitPortQueueConfigTable[portIndex, queueIndex]` |
| LANCOM LCOS | `lcsSetupWanQosQueuesTable[name]`, `lcsSetupWanQosQueueListTable[name]` |
| Cisco IOS-XE | `Cisco-IOS-XE-diffserv-target-oper` `queuing-statistics`, `wred-stats` |

### Note on TSN

`IEEE8021-FQTSS-MIB`, `IEEE8021-PSFP-MIB`, `IEEE8021-ST-MIB` (scheduled
traffic / 802.1Qbv), `IEEE8021-Preemption-MIB` (802.1Qbu),
`IEEE8021-CB-FRER-MIB`, `IEEE8021-STREAM-IDENTIFICATION-MIB` are all present in
the corpus in current revisions. That is the full 802.1 TSN suite. Nothing in
FlowSeer's device classes implements it today, but if industrial switches enter
scope this is where the models are, and they are all keyed
`[componentId, port, ...]` in the modern IEEE style.

---

## acl

**What it is.** An ordered list of match-and-action rules bound to an interface
and a direction.

### Canonical models

**`openconfig-acl`** is the model to follow:

```
acl/acl-sets/acl-set[name, type]        type ∈ ACL_IPV4 | ACL_IPV6 | ACL_L2 | ACL_MIXED
  acl-entries/acl-entry[sequence-id]
    l2/config/{source-mac, source-mac-mask, destination-mac, ethertype}
    ipv4/config/{source-address, destination-address, dscp, protocol, hop-limit}
    transport/config/{source-port, destination-port, tcp-flags}
    input-interface/interface-ref
    actions/config/{forwarding-action, log-action}
acl/interfaces/interface[id]/{ingress-acl-sets, egress-acl-sets}
```

Its match vocabulary comes from `openconfig-packet-match` and
`openconfig-packet-match-types` — which is precisely the role
`flowseer.net.packet.v1` plays in FlowSeer, and the two vocabularies line up
closely (EtherType, IP protocol, DSCP, port ranges, TCP flags, ICMP type/code).

There is **no IETF ACL MIB**. `ietf-access-control-list` (RFC 8519) exists in
YANG and is not in this corpus.

### Vendor mapping — four distinct shapes

**1. Numbered ACL, number encodes type** (the Cisco IOS classic, cloned by
Chinese vendors):

| Family | Surface |
|---|---|
| Huawei | `HUAWEI-ACL-MIB`: `hwAclNumGroupTable[aclNum]`, `hwAclBasicRuleTable[aclNum, subitem]` (source only), `hwAclAdvancedRuleTable[aclNum, subitem]` (full tuple), `hwAclEthernetFrameRuleTable[aclNum, subitem]` (L2), `hwAclIfRuleTable[aclNum, subitem]` — **rule type determined by which table, ACL type by the number range** |
| Comware | `HH3C-ACL-MIB`: `hh3cAclNumGroupTable[aclNum]`, `hh3cAclMACTable[groupType, groupIndex, ruleIndex]`, `hh3cAclNamedMACTable[groupType, groupName, ruleIndex]` — named and numbered in parallel; plus `HH3C-ACFP-MIB` (an ACL *framework* letting multiple clients install rules: `hh3cAcfpClientInfoTable[clientID]`, `hh3cAcfpPolicyTable[clientID, policyIndex]`, `hh3cAcfpRuleTable[clientID, policyIndex, ruleIndex]`) |

**2. Named ACL with a rule sub-table** (the FASTPATH/OpenConfig-ish shape):

| Family | Surface |
|---|---|
| FASTPATH family | `EdgeSwitch-QOS-ACL-MIB` / `FASTPATH-QOS-ACL-MIB`: `aclTable[aclIndex]`, `aclRuleTable[aclIndex, aclRuleIndex]`, `aclMacTable[aclMacIndex]`, `aclVlanTable[vlanIndex, direction, sequence, aclType, aclId]`, `aclIfTable[ifIndex, direction, sequence, aclType, aclId]` — note the binding tables carry **sequence**, so multiple ACLs stack on one interface in order |
| Cisco SMB | `CISCOSB-IPSTDACL-MIB::rlIpStdAclTable[aclName, aceIndex]` + `rlIpStdAclNameToIndexTable[name]` |
| D-Link (enterprise) | `DLINKSW-ACL-MIB`; `DLINKSW-CPU-ACL-MIB` for control-plane policing |

**3. Profile + rule** (D-Link's consumer line): `aclProfileTable[profileNo]`
defines *which fields are matched* (a TCAM mask template) and
`aclL2RuleTable[profileID, accessID]` / `aclL3v4RuleTable[...]` /
`aclL3v4ExtRuleTable[...]` carry the values. This exposes the hardware's
mask-then-match structure directly, and it is the shape that most resists
normalisation.

**4. Flat indexed ACL** (Foundry lineage): `snAgAclTable[aclIndex]`,
`snAgAclBindToPortTable[portNum, direction]`, `snAgAclIfBindTable[ifBindIndex,
direction]`, `agAclAccntTable[kind, ifIndex, direction, aclNumber, filterId]`
(per-rule hit counters — the useful part).

### Cross-cutting: time ranges

ACLs and QoS both take a **time range** as a condition, and it is its own small
entity: `FASTPATH-TIMERANGE-MIB` / `EdgeSwitch-TIMERANGE-MIB`
(`timeRangeTable[index]`, `timeRangeAbsoluteEntryTable[index, entryIndex]`,
`timeRangePeriodicEntryTable[index, entryIndex]`),
`CISCOSB-TBI-MIB::rlTBITimeRangeTable[name]`, `DLINKSW-TIME-RANGE-MIB`,
D-Link `TIMERANGE-MIB`, plus
`CISCOSB-TIMEBASED-PORT-SHUTDOWN-MIB` (time-based port shutdown, the same
primitive applied to link state). A shared absolute/periodic schedule object,
referenced by name from several policy types. Worth modelling once if policy is
ever modelled at all.

### Recommendation for FlowSeer

Do not model "an ACL". Model:

1. A **match expression** (already begun in `net/packet/v1`) — and take the
   corpus's warning seriously that exact observations and match expressions are
   different types. `hwAclBasicRuleTable` vs `hwAclAdvancedRuleTable` is exactly
   that distinction expressed as two tables.
2. A **rule** = sequence + match + action.
3. A **rule set** = name + type + ordered rules.
4. A **binding** = (interface | VLAN | control-plane) + direction + sequence.

That decomposition covers all four vendor shapes and both
[routing-policy](04-ip.md#routing-policy) and [qos-policy](#qos-policy) reuse
levels 1 and 2.
