# netpen

netpen runs L2 and L3 security checks against a lab segment from a Linux
host and reports each check's verdict as JSONL or in a terminal display.
It is its own Go module under `src/edge/netpen` so its packet and
terminal dependencies stay out of the main module.

## The check model

A check is a behavior the runner drives under a contract the `check`
package defines (`check/check.go`):

- A behavior is a `Run` function that reads its target and the attack
  leg's own addresses from `Deps`, sends what it sends, and returns a
  `Verdict`: `confirmed` when its observable showed the attack taking
  effect, `resisted` when the observable stayed quiet for the whole
  observation window, `pending` when it has no observable yet, and
  `skipped` when it did not run. The runner emits the verdict as the
  attack's last record, so a behavior cannot forget to decide.
- The catalog entry of each (behavior, mode) pair carries three cost
  fields: `Impact` (`none`, `temporary`, `permanent`), `Restore`
  (`none`, `decay` with the bound the probe announces, or `active`), and
  `Affects` (`none`, `device`, `clients`, `fabric`). The shipped set is
  `attacks.Default()`; `catalog/zz_generated_catalog.go` is its committed
  data view and `go generate ./catalog/...` regenerates it.
- The runner gates each attack in order: the impact ceiling, the per-run
  acknowledgment of a permanent mode, then the legs. A ceiling refusal
  records a refusal and a skipped verdict and the run continues; a
  permanent mode without its acknowledgment stops the run. The runner
  arms a teardown handle only for an entry that restores actively,
  spaces every send to `--rate`, and hands each behavior the
  observation window.
- A behavior names the target fields it needs through `Target.Need`.
  When one is missing or of the wrong form the runner records a skipped
  verdict naming every flag to pass, with the form a wrong value wants,
  and a single command exits 2. Under `full` the run continues with the
  next attack.
- Each observable is one predicate over the frames the attack leg (or the
  watch leg) carries within the window, read through `check.First`,
  `check.Scan`, or `check.Sustain` (`check/observe.go`). A predicate
  attributes a frame to its sender: an Ethernet source other than the
  attack leg's MAC, and the probe's group, VRID, autonomous system,
  transaction id, or query id where the protocol scopes a conversation.
  Where the protocol speaks without a probe (a BPDU, an HSRP or GLBP
  hello, a VRRP advertisement, a VTP summary, a DTP frame, an LLDP-MED
  advertisement), the check first reads the leg for what the segment
  already says and confirms only on a change from that. A pre-read that
  carries nothing of the protocol ends in `pending`.
- A probe that needs sustaining alternates one send with one bounded read
  of the protocol's interval through `check.Sustain` until the window is
  spent or the predicate matches: the HSRP and GLBP hellos every 3 s, the
  VRRP advertisement every second, the STP BPDU every hello time, the
  OSPF hello every 10 s. The run's `--observe` window is the only window,
  and a check whose protocol timer is longer than the window returns
  `pending` with a detail naming the window to pass (`--observe 15s` for
  HSRP and GLBP, `--observe 6s` for VRRP, `--observe 20s` for OSPF).
  The matrix (`test/integration/VALIDATION_MATRIX.md`) quotes each
  predicate and its details.

Why a verdict and three cost fields instead of one durability class: one
enum answered consent and mechanism at once, which forced no-op teardown
steps and left the decay bound as prose, and a run-time "no verdict
emitted" error would only report what the compiler now enforces.

## Running a check

Every command takes the global flags `-i`, `-w`, `--json`, `--rate`,
`--timeout`, `--td-budget`, `--max-impact` (default `temporary`), and
`--observe` (default `3s`), plus the target flags the check reads. Each
flag is typed by family: `--victim` and `--gateway` take an IPv4 host as
`<ip>` or `<ip>,<mac>`, `--victim6` and `--server6` an IPv6 host in the
same form, `--vip`, `--router-id`, and `--offer-ip` an IPv4 address,
`--route4` and `--sweep-net` an IPv4 network and `--prefix6` an IPv6
network in CIDR form, the VLAN flags an id from 1 to 4094, `--vrid` 1 to
255, and `--group` 0 and up. A value of the other family is refused at
the flag with the form it wants. A check that frames to a host, such as
`arpspoof`, needs the `<ip>,<mac>` form of its host flags.

```sh
sudo netpen arpspoof -i eth0 --json=true \
  --victim 172.16.0.10,00:aa:bb:cc:dd:01 \
  --gateway 172.16.0.1,00:aa:bb:cc:dd:fe
```

The run prints one JSONL record per line: the meta header, the finding,
the verdict, and the summary. The same command without the two target
flags exits 2 after a skipped verdict whose detail reads
`the check needs: --victim <ip>,<mac>, --gateway <ip>,<mac>`: the four
fields the check reads, the two addresses and the two MACs, rendered as
the two flags that supply them. The command with the addresses but
without the MACs reports the same two flags, since each flag carries both
parts.

A permanent mode runs only with both the ceiling raised and the pair
acknowledged:

```sh
sudo netpen vtp --wipe --vtp-domain LAB --max-impact permanent \
  --i-accept-permanent vtp=wipe
```

`netpen full` runs recon, a bounded burst, recon-gated follow-ups, and a
report; it passes the same target flags and ceiling to every phase.

## Testing

```sh
go -C src/edge/netpen test -race ./...
```

Behavior tests run each check through the runner against an in-memory leg
(`attacks/testtest`) whose addresses and target are the fixture's values,
so the frames under `attacks/testdata` are compared byte for byte. The
Linux leg's tests build only on Linux. The validation matrix under
`test/integration/VALIDATION_MATRIX.md` carries each pair's cost fields and
observable; `TestValidationMatrixCompleteness` compares it with the
committed catalog.
