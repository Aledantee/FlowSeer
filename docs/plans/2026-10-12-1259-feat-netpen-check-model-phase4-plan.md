---
title: netpen Check Model Phase 4, Defensive-Control Checks - Plan
type: feat
date: 2026-10-12
artifact_contract: flowseer-plan/v2
execution: code
---

# netpen Check Model Phase 4, Defensive-Control Checks - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

An operator runs `netpen full --max-impact none` and learns which of the
controls a switch is expected to run are active on the access segment:
DHCP snooping, dynamic ARP inspection, IPv6 RA-Guard and ND inspection,
port security, storm control, BPDU guard and root guard, and 802.1X. The
means: one check per control, each with a probe whose impact the plan
classifies from evidence, an observable, and a response fixture from a
second source.

**Stop condition:** a control has no probe whose impact is `none` or
`temporary` and whose effect a raw leg can observe. That control is then
recorded as out of reach for netpen in the matrix rather than probed.

## Decisions

The parent plan's Decisions apply. Local to this phase:

- Each check's impact is decided per probe from the control's documented
  reaction, not from the check's name. Why: a BPDU sent at a port under
  BPDU guard err-disables that port until recovery or an operator's
  intervention, which is `temporary` or `permanent` depending on the
  recovery setting, never `none`. The same holds for port security's
  violation modes. (The vendor documents stating the reaction are fetched
  and cited at re-plan.)
- Passive detection comes first. Why: DHCP snooping can be inferred from
  Option 82 insertion on relayed requests and from the absence of offers
  from untrusted ports, dynamic ARP inspection from dropped or rate-limited
  ARP, and 802.1X from EAPOL identity requests on link-up. These probes
  change nothing and carry `Impact: none`.
- Active probes send one frame that a conforming control must drop and
  observe on the watch leg whether it was forwarded. Why: this is the
  `ghost` and `raguard` shape that already exists and is the only way to
  distinguish an absent control from a silent one.

## Requirements

Parent requirement 17 is claimed here. The re-plan writes one requirement
per control with the probe, the observable, the impact, and the fixture.
Candidates and their likely class, each unverified until the vendor or
standard document is cited:

| Control | Passive observable | Active probe | Likely impact |
| --- | --- | --- | --- |
| DHCP snooping | Option 82 on forwarded requests | one offer from the attack leg, watched on the watch leg | none |
| Dynamic ARP inspection | ARP replies rate-limited or absent for bogus bindings | one reply with a binding that contradicts recon, watched | none |
| RA-Guard | no RA from host ports | `raguard` as it exists | none |
| ND inspection | NA with bogus binding dropped | one NA, watched | none |
| Port security | sticky MAC count from CAM behavior | a second source MAC, watched | temporary or permanent, depends on violation mode |
| Storm control | broadcast suppression threshold | bounded broadcast burst, watched | temporary |
| BPDU guard | no BPDU reaches the watch leg | not probed by default | temporary or permanent |
| Root guard | superior BPDU ignored | `stproot` as it exists | temporary |
| 802.1X | EAPOL request-identity after link-up | EAPOL start | none |

## Out of scope

- Authenticating through 802.1X. The check observes the challenge only.
- Any wireless control.

## Units

Written at re-plan time, one per control that passes the stop condition.

## Verification

```bash
cd src/edge/netpen && go vet ./... && CGO_ENABLED=0 go test -race -count=1 ./...
```

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] Each new check has a matrix row with its impact and observable.
- [ ] `uv run tools/scripts/run.py plan record implemented <phase> --units <n> --from <t> --to <t>`.

## Open questions

- Which controls the T1 FRR segment can exercise at all. FRR is a router
  and runs none of the switch controls above, so most fixtures need a
  vendor capture or the T2 lab.
- Whether the checks live in a fifth attack package (`attacks/guard`) or
  inside the family packages by protocol. The re-plan decides from the
  observables' shared code.
