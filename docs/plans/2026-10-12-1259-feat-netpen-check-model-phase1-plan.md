---
title: netpen Check Model Phase 1, Contracts - Plan
type: feat
date: 2026-10-12
artifact_contract: flowseer-plan/v2
execution: code
amends: docs/architecture/2026-09-23-netpen-lab-vendor-validation-direction.md, docs/plans/2026-10-12-1259-feat-netpen-check-model-plan.md
---

# netpen Check Model Phase 1, Contracts - Plan

## Goal

Every package under `src/edge/netpen` compiles against the check contract:
a behavior returns a verdict and reads a typed target, the catalog declares
impact, restore, and blast radius, the runner enforces an impact ceiling,
a rate limit, and an observation window, and the command exposes them.
Every existing behavior runs under the new contract, returning `pending`
where it has no observable yet. The means: three additive units that keep
the module building, then one cutover unit that changes the signature in
the runner, the four attack packages, `full`, and `cmd` at once, since a
signature change across all of them cannot land in parts with the build
green between them.

**Stop condition:** the fixture pins cannot be held by passing the fixture
addresses as the target, because a behavior derives a frame field from
something other than the target or the leg address. The migration unit then
needs a per-behavior fixture seam the parent did not decide.

**Re-plan of 2026-10-13.** The first run landed the contract, the schema,
the leg addresses, and the cutover (`0ff28cc7..09b173f9`), and the review
sent it back as `rework` on three mechanisms: a target value that is
present but of the wrong family or out of range passes `Need` and sends a
wrong frame or stops the process, every observing check stops on a frame
count or a fixed timer instead of the window, and the `raguard` probe
costs more than its catalog row says. The Units below hold only that open
work. Phase 1 is done when every check validates the form of its target
before any frame, every observer returns at the window or on its match,
and the `raguard` probe changes no host state.

## Decisions

The parent plan's Decisions apply. Local to this phase:

- A new leaf package `src/edge/netpen/check` owns the contract types and
  imports `findings`, `link`, and `errs` and nothing under `netpen` beyond
  the first two: `Entry` (name, mode, protocols, legs,
  impact, restore, affects, help, orchestration flag), `Impact`,
  `Restore`, `Affects`, `Target`, `Verdict`, `Deps`, an `Emitter`
  interface (`Emit`, `Finding`, `Progress`), a `Teardown` interface
  (`Arm`), and the `Run` function type. `catalog` imports `check` and adds
  `Behavior` (an `Entry` plus `Run` and `Modes`) and the `Catalog` value
  built by `catalog.New(...)`. `runner` imports both and implements the two
  interfaces. The shipped set is `attacks.Default()` in a new package
  `src/edge/netpen/attacks`, since `catalog` cannot import the family
  packages that import it. Why: today `Deps` names `catalog.Entry`,
  `*runner.Emitter`, and `*runner.Teardown`
  (`src/edge/netpen/runner/options.go:110-148`), so a `check.Deps` naming
  those concrete types while `catalog.Behavior.Run` took `check.Deps`
  would be an import cycle. `runner.go:294` fails at run time when
  `Options.Behaviors` lacks a name the catalog has, and `catalog.go:143`
  latches a global registry so an embedder cannot compose.
- `Impact` is one of `none`, `temporary`, `permanent`. `Restore` is a
  struct with a `Kind` (`none`, `decay`, `active`) and a `Bound` duration
  that is zero when no source states one. `Affects` is one of `none`,
  `device`, `clients`, `fabric`. Why: most teardown text today names no
  timer ("CAM table ages out", "NUD / real master resumes",
  `src/edge/netpen/catalog/registrations.go:23-48`), and inventing one is
  a claim about a device the plan cannot cite. The runner announces
  `decay bound unknown` for a zero bound, as `decayAnnouncement` does for
  empty text today (`src/edge/netpen/runner/durability.go:129-131`).
- The bounds with a source are the lifetimes the probe itself announces,
  read from the frame the behavior sends: `roguera` and `roguedhcp` 1800 s,
  `roguedhcp6` 600 s (the valid lifetime its advertise carries, corrected
  by the re-plan from the 300 s preferred lifetime), `stproot` 6 s (the
  max age its BPDU carries), `wpad`
  and `llmnr` 30 s (the TTL in the answer). Every other `decay` entry
  carries a zero bound.
- `Verdict` is a struct with a `Kind`, a `Detail` string, and an `Observed`
  duration, with kinds `confirmed`, `resisted`, `pending`, `skipped`.
  Why: three of the four exist as rollup kinds
  (`src/edge/netpen/findings/findings.go:47-56`), and `confirmed` is what
  `full/full.go:312` records as a `KindFinding` verdict today. They fold
  into one record kind `verdict`.
- `Target` has one field per value that names a host, address, group, or
  identity on the network: `Victim`, `Gateway`, `Victim6`, `Server6` (each
  an IP and a MAC), `VirtualIP`, `Group`, `VRID`, `OuterVLAN`,
  `InnerVLAN`, `VoiceVLAN`, `VTPDomain`, `RouterID`, `Prefix6`, `Route4`,
  `OfferIP`, and `SweepNet`. The attacker's own addresses come from
  `Leg.Addr()`. A frame field that only has to be well-formed (a DNS
  transaction ID, a DHCP XID, the MAC stamp of a CAM-flood frame) is probe
  content and stays a constant in the behavior. The cutover classifies
  each of today's constants by that rule, per behavior, in its doc
  comment. Why: the constants under `attacks/*/helpers.go` and the
  per-file literals (`attacks/ip6/helpers.go:32-40`,
  `attacks/ip6/roguedhcp.go:43`, `attacks/l2/portsteal.go:38`,
  `attacks/routing/eigrp.go:257`) hold values one `Victim.IP` cannot, so a
  rule, not a list, decides the edge cases.
- Each behavior test builds its own target from the constants it pins
  today, through a `fixtureTarget()` in its package's test file. Why:
  `arpspoof` pins victim `172.16.0.10` (`attacks/fh/helpers.go:41-46`),
  `llmnr` pins `10.0.0.2`, and the IPv6 suite pins `fd00::10`, so one
  shared target cannot hold them.
- `Target.Need(fields ...Field) error` returns one coded error naming every
  missing field, with `ErrCodeTargetMissing` and exit code 2. The runner
  maps it to a `skipped` verdict and, when `Options.Orchestrated` is
  false, returns it from `Run` so the command exits 2. Any other error
  from a behavior becomes a `pending` verdict carrying the error text, and
  the run continues. Why: parent requirement 7 wants exit 2 on a single
  command, and under `full` a behavior whose target recon could not fill
  is a skipped check, not a run failure.
- A ceiling refusal emits the `refusal` record and continues to the next
  attack. A missing acknowledgment keeps today's behavior and stops the
  run with the coded error (`src/edge/netpen/runner/runner.go:277-292`).
  Why: a ceiling states what the operator asked for, so refusing an entry
  above it is the run working as asked. A missing acknowledgment on an
  entry the operator named is a mistake to stop on.
- `link.Leg` gains `Addr() Addr` carrying the MAC and the IPv4 and IPv6
  addresses, read once at open from `net.InterfaceByName`. Why:
  every attack package has a `srcMAC()` returning the fixture value because
  the leg exposes nothing (`attacks/l2/dtp.go:106`).
- The linux leg keeps one lifecycle mutex for `closed` and the fd and adds
  a write mutex. `readOnce` takes the lifecycle mutex only for the closed
  check and a wait-group increment, releases it, calls `ReadPacketData`,
  and decrements. `Close` sets `closed` under the lifecycle mutex, waits
  for the wait group, then takes the write mutex and calls `tp.Close()`.
  `Send` takes the write mutex and checks `closed` under it. Why:
  `src/edge/netpen/link/link_linux.go:201-210` holds the lock across the
  100 ms poll and `Send` at line 68 waits on it, and afpacket's `Close`
  writes the fd with no lock
  (`~/go/pkg/mod/github.com/gopacket/gopacket@v1.7.3/afpacket/afpacket.go:243-254`)
  while `WritePacketData` reads it (`:567-569`), so a write path with no
  ordering against `Close` races on the fd.
- The rate limit is a `link.Leg` wrapper in `runner` that spaces sends by
  `time.Second / rate` on a clock the test replaces, handed to the behavior
  as `Deps.AttackLeg`. No module is added, and `stproot` drops its own
  pacing (`src/edge/netpen/attacks/l2/stproot.go:61-66`) so nothing paces
  twice. Why: one enforcement point for every behavior, and a token bucket
  is more than a packet pacer needs.
- The runner emits the verdict record itself after `Run` returns, stamped
  with the attack and mode. Why: a behavior cannot forget to emit.
- `SignalHandler` moves to `cmd/netpen`. The runner exports
  `AbandonedSteps` (today `abandonedSteps`,
  `src/edge/netpen/runner/runner.go:549`) and `StopContext` (today the
  unexported `stopCtx` that `signals.go:72` and `:120` read). Why:
  `src/edge/netpen/runner/signals.go:140` calls `os.Exit` from a library
  package.
- `Options.SuppressOutput` is deleted. Why: `runAttack`
  (`cmd/netpen/main.go:382`) and `full.runPhase` (`full/full.go:245`) set
  it and nothing reads it.
- Phase 1 removes teardown arming on the permanent modes (`dtp
  --keep-trunk`, `vlanhop --persist`, `voicevlan --persist`) and leaves the
  no-op steps of the active-restore entries (`arpspoof`, `etherchannel`,
  `portsteal --relay`, `vlanhop`, `voicevlan`) in place with the verdict
  detail naming them. Parent requirement 8 is phase 2's. Why: a teardown
  that does host work needs the sysctl and netlink seams phase 2 decides,
  and a permanent mode with a nil `Deps.Teardown` would otherwise panic on
  `Arm` (`attacks/l2/vlanhop.go:44`, `attacks/l2/voicevlan.go:43`).
- The lab vendor validation record is amended where it says "fixed
  fixture", and the T1 and T2 harness commands pass the flags the
  behaviors now need. Why: `test/integration/lab/lab.go:113-116` and
  `test/integration/t2_lab_ospf_test.go:70` run the command with no
  target, which after the cutover exits 2, and the build tags hide that
  from `go test ./...`.
- The `-race` runs drop `CGO_ENABLED=0`. Why: `go test -race` needs cgo on
  Linux, and the Taskfile sets it for the static release build only.
- `ackList.Set` rejects an entry without `name=mode`. Why: `main.go:97`
  accepts a bare mode that can never match a gate key.
- The WPAD listener comes from `Deps.Listen`, a `func(ctx, network,
  address) (net.Listener, error)` defaulting to `net.Listen`, and the test
  passes an in-memory listener. Why: the suite fails wherever port binding
  is denied.

Decisions of the re-plan:

- `Target.Need` checks the form of each named field, not only its
  presence: `Victim`, `Gateway`, `VirtualIP`, `RouterID`, `OfferIP`,
  `Route4`, and `SweepNet` are IPv4 (`To4() != nil`, a 4-byte mask for the
  networks, at least one host address in `SweepNet`), `Victim6`,
  `Server6`, and `Prefix6` are IPv6 and not v4-mapped (`To4() == nil`), a
  MAC has six octets, a VLAN is 1 to 4094
  (`spec/yang/cisco/iosxe/2611/Cisco-IOS-XE-vlan.yang:951-957`), `VRID`
  keeps zero as absent and its `uint8` type holds the upper bound
  (`spec/proto/flowseer/net/protocol/vrrp/v1/vrrp_group.proto:61-64`
  gives 1 to 255), and `VTPDomain` is at most 32 bytes (`src/edge/netpen/layers/vtp.go:214-216`
  cuts it there). A field that fails returns the same coded error family
  as a missing one, with a second code `ErrCodeTargetInvalid`
  (`netpen/target-invalid`, exit 2) and a message naming the flag and the
  form it wants. The runner maps both codes to a `skipped` verdict through
  one `check.IsTargetError`. Why: the review found `ospf.go:61` and
  `eigrp.go:303` stop the process on an IPv6 value, `portsteal.go:93`
  sends a zero-length ARP, and four IPv6 behaviors frame a v4-mapped
  address, all on an honest operator mistake, and one rule table in
  `check` covers an embedder that builds `Target` directly as well as the
  flags.
- `Target.Group` is a `*uint16`, and the rule "a zero number is absent"
  in the `Target` doc comment (`src/edge/netpen/check/target.go:25-26`)
  no longer covers it. The error text of `Need` and `Within`, the one the
  runner copies into the verdict detail, reads the flag renderings, so
  every detail assertion in the family tests changes with it. Why: HSRP group 0 is valid
  (`spec/yang/cisco/iosxe/2611/Cisco-IOS-XE-hsrp.yang:227-231` gives
  `0..4095`, `spec/yang/cisco/iosxe/2611/MIBS/CISCO-HSRP-MIB.yang:213-215`
  gives `0..255`), so presence needs its own marker. `VRID` and the VLAN
  ids keep zero as absent, since zero is outside their ranges.
- A behavior whose frame field is narrower than the target field bounds
  it itself with `Target.Within(field, max)`, which returns the invalid
  code: `hsrp` bounds `Group` at 255, since the HSRPv1 group field is one
  octet (`src/edge/netpen/layers/hsrp.go:55`, RFC 2281 section 5.1 gives
  0 to 255), and `voicevlan` bounds `VoiceVLAN` at 127, since its network
  policy field packs the id into bits the fixture layout gives seven of
  (`attacks/l2/voicevlan.go:95`). Why: a value the frame cannot carry is
  the operator's mistake to hear about, not a different group to attack.
  Correcting the policy field's layout changes pinned bytes and is phase
  2's.
- A `Field` renders as the flag that supplies it: `--victim <ip>,<mac>`
  for `victim` and `victim-mac` alike, `--group <n>` for `group`. The
  `Need` message lists those renderings and the hint names them. Why:
  the review found the error names `victim-mac`, which is no flag.
- The command's flag values are typed by family: an IPv4 host, an IPv6
  host, an IPv4 address, an IPv4 network, an IPv6 network. The value
  stored is the 4-byte form for IPv4. `--group` accepts 0. Why: the flag
  is where the operator reads the error, and the same rule in `check`
  then only has to catch an embedder.
- `check` gains one observation helper over a `link.Leg` and the window:
  `check.First(ctx, leg, window, match)` returns the first frame `match`
  accepts, or no frame when the window ends, and `check.Collect(ctx, leg,
  window, match)` returns every accepted frame at the window's end. Both
  count the frames they skipped and follow four rules: a frame carrying
  `Err` returns that error at once, cancellation wins over a closed
  channel and returns `ctx.Err()`, a closed channel otherwise ends the
  window early, and a window of zero or less is an error, since the
  runner always supplies one. Each helper runs `Receive` under a child
  context it cancels and drains before returning, as the `Leg` contract
  asks (`src/edge/netpen/link/link.go:127-128`). Every observer uses
  one of them: `ghost` and `raguard` take `First` with an attribution
  predicate, `vlanenum` takes `Collect` over tagged frames, `ospf` takes
  `First` for the hello and `eigrp` for the goodbye, `daddos` for the
  defending advertisement, and `roguedhcp6` for a Solicit from the
  victim's MAC. Why: seven hand-written loops each stopped on a frame
  count or a fixed timer, and parent requirement 10 names the window as
  the bound.
- `raguard` sends its own probe advertisement, not `roguera`'s: Router
  Lifetime 0, Cur Hop Limit 0, Reachable Time 0, Retrans Timer 0, no
  Source Link-Layer Address option, no MTU option, and one Prefix
  Information option carrying the target prefix with the L and A flags
  clear and both lifetimes 0. Why: RFC 4861 section 6.3.4 has a host
  ignore each of those fields when zero or absent ("the parameter should
  be ignored and the host should continue using whatever value it is
  already using"), time out a default-router entry on a zero lifetime
  rather than create one, and take "no information concerning on-link
  determination" from an option with L clear, and RFC 4862 section 5.5.3
  rule (a) has it "silently ignore the Prefix Information option" when A
  is clear. RFC 6105 section 3 has stateless RA-Guard decide on the
  sender's link-layer address, the ingress port, the IP source, and the
  prefix list, so the probe still exercises the guard. One effect
  remains: section 6.3.4 also says that when no Source Link-Layer Address
  option is present "but a corresponding Neighbor Cache entry exists",
  its IsRouter flag "MUST be set to TRUE", so a host that already holds a
  neighbor entry for the probe's link-local source marks it a router,
  which changes no default route, prefix, address, or link parameter
  while no default-router entry exists. `Impact: none` stays with that
  effect named in the matrix row. The fixture bytes under
  `attacks/testdata/ip6/raguard.pcap` change, which amends the parent's
  byte-pin Decision for this one fixture, and the harvest header lists
  `raguard` as spec-authored. (decided by the user, 2026-10-13)
- `raguard` attributes a crossing only to an ICMPv6 Router Advertisement
  (type 134) whose Ethernet source is the attack leg's MAC and whose
  prefix option carries `Target.Prefix6`. Why: the review found a source
  MAC match alone, which any broadcast from the attack interface
  satisfies.
- `Interrupt` sends a `pending` verdict with detail `interrupted` for the
  attack in flight itself, before it cancels the stop context, the way it
  already sends the partial-failure record
  (`src/edge/netpen/runner/runner.go:575`), and `Run` then returns
  `context.Canceled` on every such run, decided by a flag the interrupt
  path sets rather than by reading the run context after the fact. A
  caller-context cancellation emits no verdict, since the pump is
  canceled with the context (`runner.go:197`). Why: parent requirement 4
  asks for one verdict per pair, and the review found the return value
  depends on goroutine scheduling and a send after the stop context
  cancels is dropped by the pump.
- `roguedhcp6`'s decay bound is 600 s, the valid lifetime its advertise
  carries (`attacks/ip6/roguedhcp6.go:146`). Why: the bound is the
  lifetime the probe announces, and 300 s is the preferred lifetime.
- The Linux leg opens with `afpacket.OptAddVLANHeader(true)`. Why:
  `~/go/pkg/mod/github.com/gopacket/gopacket@v1.7.3/afpacket/options.go:102-109`
  says AF_PACKET strips the 802.1Q header by default, so `vlanenum` can
  see no tag without it. The option re-inserts the header only for a
  non-zero TCI (`afpacket/header.go:147-149`, the v3 path passing the
  whole TCI at `:244-245`), so a VID-0 frame with PCP and DEI 0 stays
  untagged while a priority-tagged frame comes back tagged with VID 0,
  which the matrix row says.

## Requirements

Parent requirements 1 to 7, 9 to 11, 13, 14, and 15 are claimed here.
Local examples that pin the shape:

1. `attacks.Default().Entries()` is byte-stable and the generator commits
   it. Example: `go generate ./catalog/...` produces no diff against
   `zz_generated_catalog.go` on a clean tree, and the oracle test in
   `attacks/attacks_test.go` names all 40 (behavior, mode) pairs (34
   behaviors and 6 modes) with their three cost fields.
2. The JSONL schema version is 2 and the `verdict` kind exists. Example:
   `findings_test.go` marshals a verdict record and asserts after
   unmarshal, field by field, `schema_version` 2, `kind` `verdict`,
   `attack` `ghost`, and a `verdict` object with `kind` `resisted`,
   `detail` `0 frames in 3s`, and `observed_ms` 3000.
3. The gate order is ceiling, then acknowledgment, then legs. Example: a
   permanent entry under `MaxImpact: temporary` with a valid ack is refused
   with reason `impact permanent exceeds ceiling temporary`, the test leg
   records zero sends for it, and the next attack in the list runs.
4. The runner arms a teardown handle only when `Restore.Kind == active`.
   Example: a behavior registered with `Restore: decay 6s` receives a nil
   `Deps.Teardown`, and a behavior with `Restore: active` that arms no step
   still fails with `ErrCodeTeardownPartial`.
5. The migrated behaviors keep their fixture pins. Example: every existing
   `*_FixturePins` test passes with `Deps.Target` built by its package's
   `fixtureTarget()` and the test leg's `Addr()` returning
   `00:11:22:33:44:55` and the attacker IPv4 the family pins today.
7. A missing target is a skipped verdict and exit 2. Example: `netpen
   arpspoof` with no flags emits a `verdict` record with kind `skipped`
   and detail naming `victim` and `gateway`, exits 2, and the leg records
   zero sends. The same behavior under `full` with an empty target emits
   the same record and the run continues.
6. A behavior without an observable returns `pending` with a reason.
   Example: `RunHSRP` returns `Verdict{Kind: pending, Detail: "no
   observable: coup acceptance needs a hello listener"}`, and
   `VALIDATION_MATRIX.md` carries that text in a new `Observable` column.

Requirements 1 to 7 landed in the first run. The re-plan adds:

8. A target value of the wrong family or out of range never reaches a
   frame. Example: `netpen ospf --router-id 2001:db8::1`, `netpen eigrp
   --route4 2001:db8::/64`, and `netpen portsteal --victim 2001:db8::1`
   exit 2 at flag parsing with the flag's own error (`invalid value
   "2001:db8::1" for flag -router-id: want an IPv4 address`) and emit no
   record. A `Target` built by an embedder with those values, and the two
   values the flags cannot judge, `netpen arpsweep --sweep-net
   10.0.0.5/32` and `netpen hsrp --group 300 --vip 10.0.0.1`, reach the
   runner and produce a `skipped` verdict whose detail names the flag and
   the form (`--group at most 255`), zero sends, and exit 2. `netpen hsrp
   --group 0 --vip 10.0.0.1` runs and frames group 0.
9. The usage error names flags. Example: `netpen arpspoof --victim
   172.16.0.10 --gateway 172.16.0.1` prints `the check needs: --victim
   <ip>,<mac>, --gateway <ip>,<mac>` and exits 2.
10. An observer returns at its match or at the window, never on a frame
    count or an idle timer. Examples, each inside a `testing/synctest`
    bubble so the window is the fake clock's: `ghost` with three
    unrelated frames pushed before the two ghost frames returns
    `confirmed` within the window. `raguard` with a 2 s window and a
    watch leg carrying only an advertisement from another MAC returns
    `resisted` at 2 s. `vlanenum` with a 1 s window on a leg that
    delivers a tagged frame every 100 ms for 5 s returns `confirmed` at
    1 s with the ids seen. `roguedhcp6` on a quiet leg returns `pending`
    with detail `no solicit in 3s` at the window. `ospf` with an ARP
    frame before the hello still reads the hello.
11. The `raguard` probe installs no default router, prefix, address, or
    link parameter on a host, and a crossing is attributed by content.
    Example: the regenerated fixture decodes as an RA whose router
    lifetime, hop limit, reachable time, retrans timer, and flags byte
    are all 0, whose options are exactly one Prefix Information option
    with flags 0x00, both lifetimes 0, and the target prefix, and which
    carries no Source Link-Layer Address and no MTU option. On the watch
    leg a broadcast ARP from the attack leg's MAC, an RA from the leg's
    MAC with another prefix, and a truncated RA each leave the verdict
    `resisted`, and the probe RA itself makes it `confirmed`.
12. An interrupted attack gets a verdict and a deterministic return.
    Example: `Interrupt` during a stub behavior yields a `verdict` record
    with kind `pending` and detail `interrupted` on the stream, and
    twenty runs of that case all return `context.Canceled`.
13. The catalog and the matrix agree with the frames. Example: the oracle
    row for `roguedhcp6` reads `decay` with a 600 s bound, the matrix row
    reads `decay 10m0s`, the decay note in the finding reads `600s`, and
    `go generate ./catalog/...` leaves no diff.

## Out of scope

- Real observables for the existing behaviors (phase 2) and `full`'s use of
  recon to fill the target (phase 3). `full` passes the flag target in
  this phase, and a burst behavior whose fields are missing is a `skipped`
  verdict the summary counts.
- Changing any frame byte the fixtures pin, except the `raguard` probe the
  Decisions name. The VRRP checksum, the sweep request's source, and the
  voice VLAN policy field's layout are phase 2 and phase 3.
- Frames buffered in the capture ring before `Receive` is called. A watch
  leg shared across `full`'s phases may read earlier attacks' frames first.
  Phase 3 owns the shared leg and decides a drain or a timestamp cut-off.
  The kernel's ring-fill behavior while nothing polls is unverified.
- The no-op restore steps of `vlanhop`, `voicevlan`, and `etherchannel`
  and their cost rows, which phase 2 makes real.
- The command's second signal subscription (`cmd/netpen/main.go:203`
  beside `signals.go`), `full` exiting 0 after a teardown failure, and the
  single-attack summary's record count, all of which predate the first run
  and are recorded under Review gaps for a plan of their own.
- Input and trust: the flags and the catalog are written by the lab
  operator or a contributor and are trusted, so the form checks catch
  honest mistakes, not crafted values. Frames read from a leg come from a
  network peer and are untrusted. The observation helper decodes nothing
  itself and hands each frame to the behavior's predicate, which returns
  false on a frame it cannot decode.

## Units

### U5. Target form rules and the observation helper in check

Files: src/edge/netpen/check/target.go, src/edge/netpen/check/check.go, src/edge/netpen/check/observe.go, src/edge/netpen/check/check_test.go, src/edge/netpen/check/observe_test.go, src/edge/netpen/attacks/fh/hsrp.go, src/edge/netpen/attacks/fh/glbp.go, src/edge/netpen/attacks/fh/fh_test.go, src/edge/netpen/cmd/netpen/target.go
After: none
Change: `Target.Group` is a `*uint16`, and the four sites that read or
set it as a number (`hsrp.go:44`, `glbp.go:45`, `cmd/netpen/target.go:30`,
`fh_test.go:40`) dereference or take the address so the module keeps
building, and U7 and U8 then change what those sites do. The `Target` doc
comment names `Group` as the one number whose zero is a value. `Need`
applies the form rules the Decisions list and returns
`ErrCodeTargetMissing` for an absent field and `ErrCodeTargetInvalid` for
a present one of the wrong form, with one text for `Error()` and the user
message that lists the flags through `Field.Flag()` and, for an invalid
field, the form it wants. `Target.Within(field, max)` returns the invalid
code when a numeric field exceeds `max`. `IsTargetError(err)` reports
either code. `observe.go` holds `First` and `Collect` over a `link.Leg`
and a window under the four rules of the Decision, each running `Receive`
under a child context it cancels and drains before returning, and
returning an `Observation` with the count of skipped frames and the
elapsed time. The package doc no longer claims to import only `findings`
and `link`, and the `[Skipped]` link in `target.go` names
`findings.VerdictSkipped`.
Tests: `check_test.go` gains a table with one case per field: the full
target with that one field cleared names exactly that field, and the full
target with that one field set to a wrong-form value (an IPv6 victim, a
v4-mapped `Victim6`, a 20-octet MAC, VLAN 4095, a 33-byte domain, a `/32`
sweep net, a `/64` `Route4`) returns the invalid code naming the flag and
the form. A case pins `Group` 0 as present and `Within(FieldGroup, 255)`
refusing 256. A case pins the rendered text for a missing `victim` plus
`victim-mac` as `--victim <ip>,<mac>`. `observe_test.go` runs inside a
`testing/synctest` bubble and drives `First` and `Collect` on the test
leg: three skipped frames then a match returns the match before the
window ends, a window with only skipped frames returns no frame at the
window, a frame carrying `Err` returns that error at once, a closed
channel ends early with no error, a context canceled while the channel
is closed returns `ctx.Err()`, a zero window returns an error, and the
leg's receiver has exited when the helper returns.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/netpen/check src/edge/netpen/attacks/fh src/edge/netpen/cmd`, then `go build ./... && go vet ./...` from `src/edge/netpen`.

### U6. The runner's target mapping and interrupt verdict, and the leg's VLAN header

Files: src/edge/netpen/runner/runner.go, src/edge/netpen/runner/runner_test.go, src/edge/netpen/runner/durability_test.go, src/edge/netpen/link/link_linux.go, src/edge/netpen/link/link.go, src/edge/netpen/link/link_linux_test.go
After: U5
Change: the runner's skipped mapping reads `check.IsTargetError`.
`Interrupt` sends the `pending` verdict with detail `interrupted` for the
attack in flight before it cancels the stop context, the dispatch loop
skips its own verdict send for that attack, and `Run` returns
`context.Canceled` when the interrupt path engaged, read from a flag that
path sets. The Linux leg's `open` takes its options from a function
`socketOptions(iface)` that includes `afpacket.OptAddVLANHeader(true)`,
and the `Receive` doc says frames in the ring before the call are
delivered first. `runner_test.go:467` reads the flag rendering in the
detail.
Tests: `runner_test.go` gains a case where a stub returns the invalid code
and the verdict is `skipped` with exit 2, and a case that calls
`Interrupt` during a stub twenty times in a loop, asserting the
`interrupted` verdict on the stream and `context.Canceled` on every
iteration. `link_linux_test.go` asserts `socketOptions` contains the VLAN
header option.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/netpen/runner src/edge/netpen/link`, then `GOOS=linux go vet ./link/...` from `src/edge/netpen`.

### U7. Typed target flags, the usage text, and the README

Files: src/edge/netpen/cmd/netpen/target.go, src/edge/netpen/cmd/netpen/main.go, src/edge/netpen/cmd/netpen/main_test.go, src/edge/netpen/README.md
After: U5
Change: `target.go` has one flag value per family, each storing the
4-byte form for IPv4, refusing the other family, capping VLAN ids at 4094,
requiring six-octet MACs, and accepting `--group 0` into the pointer. The
usage block in `main.go` says which flags take IPv4 and which IPv6. The
README's arpspoof example lists the records the command emits (meta,
finding, verdict, summary), names the four fields a missing target
reports as flags, and says a host flag needs the MAC form for the checks
that frame to a host.
Tests: `main_test.go` extends `TestTargetFlagsParse` with `--router-id
2001:db8::1`, `--victim6 10.0.0.5`, `--prefix6 10.0.0.0/8`, `--route4
2001:db8::/64`, `--outer-vlan 4095`, and `--victim 10.0.0.1,0102.0304.0506.0708`
exiting 2, and `--group 0` reaching the runner as a set group through a
stub catalog. `TestMissingTargetExitsTwo` asserts the flag renderings in
stderr. A case runs `arpspoof` with `--observe 750ms` through a stub that
returns the window as its detail.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/netpen/cmd src/edge/netpen/README.md`

### U8. First-hop family: bounds, the ghost observer, and the empty-target table

Files: src/edge/netpen/attacks/fh/hsrp.go, src/edge/netpen/attacks/fh/glbp.go, src/edge/netpen/attacks/fh/ghost.go, src/edge/netpen/attacks/fh/arpsweep.go, src/edge/netpen/attacks/fh/vrrp.go, src/edge/netpen/attacks/fh/gratarp.go, src/edge/netpen/attacks/fh/icmpredirect.go, src/edge/netpen/attacks/fh/lldpspoof.go, src/edge/netpen/attacks/fh/llmnr.go, src/edge/netpen/attacks/fh/arpspoof.go, src/edge/netpen/attacks/fh/helpers.go, src/edge/netpen/attacks/fh/fh_test.go, src/edge/netpen/attacks/fh/ghost_test.go
After: U5
Change: `hsrp` calls `Within(FieldGroup, 255)` after `Need` and reads the
pointer. `glbp` reads the pointer. `arpsweep` returns the invalid code
when the sweep network yields no host. `ghost` observes through
`check.First` with the ghost source address as its predicate and returns
`confirmed` on the match or `resisted` at the window. The file headers
describe each check in `Impact`, `Restore`, and `Affects` terms and carry
no deleted class name, no roadmap word, and no em dash. The `vrrp`
comment says the fixture compares bytes.
Tests: `fh_test.go` gains a table `TestNeedFieldsPerBehavior` with one
row per behavior that reads a target, asserting the exact missing list
for an empty target, and a row for `hsrp` with group 300 asserting the
invalid code and zero sends. `ghost_test.go` gains the ambient-only case
(the `ghost_ambient.pcap` frames alone, `resisted`) and the ambient-then-
ghost case (`confirmed` within the window). `TestARPSpoof_UnreachableTargetAfterPoison`
asserts the partial record names both repair steps.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/netpen/attacks/fh`

### U9. DHCP and IPv6 family: the raguard probe, the observers, and the cost row

Files: src/edge/netpen/attacks/ip6/raguard.go, src/edge/netpen/attacks/ip6/roguera.go, src/edge/netpen/attacks/ip6/roguedhcp6.go, src/edge/netpen/attacks/ip6/daddos.go, src/edge/netpen/attacks/ip6/ndpspoof.go, src/edge/netpen/attacks/ip6/roguedhcp.go, src/edge/netpen/attacks/ip6/dhcpstarve.go, src/edge/netpen/attacks/ip6/mld.go, src/edge/netpen/attacks/ip6/raflood.go, src/edge/netpen/attacks/ip6/register.go, src/edge/netpen/attacks/ip6/helpers.go, src/edge/netpen/attacks/ip6/harvest_ip6.go, src/edge/netpen/attacks/ip6/ip6_test.go, src/edge/netpen/attacks/ip6/receive_test.go, src/edge/netpen/attacks/testdata/ip6/raguard.pcap, src/edge/netpen/attacks/attacks_test.go, src/edge/netpen/catalog/zz_generated_catalog.go, src/edge/netpen/test/integration/VALIDATION_MATRIX.md, docs/plans/2026-10-12-1259-feat-netpen-check-model-plan.md
After: U5
Change: `raguard` crafts the probe advertisement the Decisions describe
in its own `craftProbeRA`, observes through `check.First` with a
predicate that decodes an ICMPv6 Router Advertisement from the leg's MAC
carrying the target prefix, and records `traversal: unobserved` with no
watch leg. `harvest_ip6.go` writes the new fixture. `roguedhcp6` waits
through `First` for a Solicit whose Ethernet source is `Victim6.MAC`,
replies to that frame's source address, and returns `pending` with
`no solicit in <window>` at the window. `daddos` waits through `First`
for the defending advertisement. `register.go` carries the 600 s bound,
the finding's decay note (`roguedhcp6.go:106`) reads `600s`, and the
oracle, the generated file, and the matrix rows for `raguard` (the probe
it sends, the neighbor-cache effect, and the vendor behavior marked
unverified), `roguedhcp6` (`decay 10m0s`), `ghost` (resisted at the
window), and `vlanenum` (`pending` on no tags, and a VID-0 frame's
visibility) follow. The parent plan's bound Decision reads 600 s, and
`harvest_ip6.go`'s header lists `raguard` as spec-authored. The file
headers state the cost fields and carry no deleted class name, no
history wording, no false claim about duration or randomness, and no em
dash.
Tests: `ip6_test.go` pins the regenerated fixture byte for byte and
decodes it to assert the fields and the option list of Requirement 11.
`TestRAGuard_AmbientOnlyIsResisted` feeds a broadcast ARP from the leg
MAC, an RA from the leg MAC with another prefix, an advertisement from
another MAC, and a truncated RA, and wants `resisted` at the window.
`TestRAGuard_TraversalAttribution` asserts `confirmed` on the probe
itself. `TestRogueDHCPv6_QuietLegIsPending`,
`TestRogueDHCPv6_IgnoresOtherClients`, and `TestDADDOS_LateDefenseIsRead`
cover the observers, inside `synctest` bubbles. `receive_test.go:51`
keeps a terminal `Frame.Err` as the behavior's error, and its malformed
Solicit cases (`:99-163`) now expect `pending` with the window detail and
zero sends, since the predicate skips what it cannot decode.
`TestCatalogCostFields` (`ip6_test.go:570-582`) and the decay note
assertion (`:251`) read 600 s. `TestNeedFieldsPerBehavior` covers every
behavior that reads a target, including a v4-mapped `Victim6` and an
IPv6 `OfferIP` as invalid. `attacks_test.go` reads the 600 s bound for
`roguedhcp6`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/netpen/attacks/ip6 src/edge/netpen/attacks/attacks_test.go src/edge/netpen/catalog src/edge/netpen/test/integration`, then `go generate ./catalog/... && git diff --exit-code catalog/zz_generated_catalog.go` from `src/edge/netpen`.

### U10. L2 and routing families: the observers and the bounds

Files: src/edge/netpen/attacks/l2/vlanenum.go, src/edge/netpen/attacks/l2/voicevlan.go, src/edge/netpen/attacks/l2/vtp.go, src/edge/netpen/attacks/l2/portsteal.go, src/edge/netpen/attacks/l2/doubletag.go, src/edge/netpen/attacks/l2/vlanhop.go, src/edge/netpen/attacks/l2/camflood.go, src/edge/netpen/attacks/l2/stproot.go, src/edge/netpen/attacks/l2/mvrp.go, src/edge/netpen/attacks/l2/etherchannel.go, src/edge/netpen/attacks/l2/dtp.go, src/edge/netpen/attacks/l2/craft.go, src/edge/netpen/attacks/l2/helpers.go, src/edge/netpen/attacks/l2/l2_test.go, src/edge/netpen/attacks/l2/vlanenum_test.go, src/edge/netpen/attacks/routing/ospf.go, src/edge/netpen/attacks/routing/eigrp.go, src/edge/netpen/attacks/routing/wpad.go, src/edge/netpen/attacks/routing/helpers.go, src/edge/netpen/attacks/routing/routing_test.go, src/edge/netpen/attacks/routing/receive_test.go
After: U5
Change: `vlanenum` collects tagged frames through `check.Collect` for the
window and returns `confirmed` with the ids seen, or `pending` with
`no tagged frames in <window>`. `voicevlan` calls `Within(FieldVoiceVLAN,
127)`. `ospf` reads the hello and `eigrp` the goodbye through `check.First`
within the window, skipping frames their decoders reject, and proceed as
today when the window ends without one. `vtp` reports the mode from
`deps.Entry.Mode`. The `wpad` doc comment says the listener binds loopback
and closes before return. The file headers state the cost fields and carry
no deleted class name, no roadmap word, no stale fixture text, and no em
dash.
Tests: `l2_test.go` and `routing_test.go` gain `TestNeedFieldsPerBehavior`
rows for every behavior that reads a target, including an IPv6 victim
for `portsteal`, VLAN 4095 for `doubletag`, voice VLAN 200, and a 33-byte
domain, each asserting the invalid code and zero sends.
`vlanenum_test.go` sets a window on every case (today `:25`, `:45`, and
`:65` pass zero) and gains the busy-leg case of Requirement 10 and the
no-tags case, inside `synctest` bubbles. `routing_test.go` gains an ARP
frame before the OSPF hello (the hello is still read), a non-matching
first frame before the EIGRP goodbye (`resisted`), an IPv6 router ID and
route as invalid with zero sends, a teardown step name assertion for
`ospf-lsa-flush`, `ospf-goodbye`, and `eigrp-goodbye`, and
`TestWPAD_ListenFailureIsCoded` asserting `errs.CodeOf` on the returned
error. `receive_test.go:43-44` keeps a terminal `Frame.Err` as the
behavior's error, and its closed-receiver case (`:86-91`) now expects the
sequence to proceed at the window's end with the full send count, since
a closed channel ends the window.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/netpen/attacks/l2 src/edge/netpen/attacks/routing`

Waves: U5 | U6 U7 U8 U9 U10

## Verification

```bash
cd src/edge/netpen && go vet ./... && GOOS=linux go vet ./... && go test -race -count=1 ./... && go generate ./catalog/... && git diff --exit-code catalog/zz_generated_catalog.go
.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/netpen test/conformance
```

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `src/edge/netpen/README.md` written and the matrix regenerated in
      the same change.
- [ ] `uv run tools/scripts/run.py plan record implemented <phase> --units 6 --from <t> --to <t>`.
- [ ] The matrix row for `raguard` and `vlanenum` says what the probe
      sends and what the verdict rests on, with the vendor behavior marked
      unverified until the lab tier runs it.
- [ ] No plan labels in code.

## Open questions

- The parent's open questions on the `--max-impact` and `--observe`
  defaults. The first run took `temporary` and `3s`.
- The GLBP group range has no vendored source (`spec/yang/cisco/iosxe/2611`
  carries no GLBP module), so `glbp` takes the full `uint16` its frame
  field holds and the plan states no bound. Unverified.
- Whether a given vendor's RA-Guard or host stack treats the zero-lifetime
  probe as RFC 4861, 4862, and 6105 describe is unverified until the lab
  tier runs it. The `raguard` matrix row carries the same note.
- The kernel's filling of the PACKET_RX_RING while nothing polls is
  unverified. The Out of scope entry on buffered frames rests on the
  afpacket source alone (`afpacket.go:316-352`), which shows only that
  the reader takes the next ring block in order.

## Review gaps

Follow-ups from the review of 2026-10-12 (`rework`, round 0). None holds
the verdict.

- src/edge/netpen/runner/durability.go:55: the acknowledgment check moved ahead of the ceiling; fails: a permanent mode without its acknowledgment under a temporary ceiling stops the run instead of recording a ceiling refusal and continuing; class: gap
- src/edge/netpen/runner/runner.go:222: the paced leg wrapping removed; fails: a run with `Options.Rate` set sends unpaced; class: gap
- src/edge/netpen/runner/emitter.go:31: `Progress` emitted as `KindFinding` or without the attack stamp; fails: no test reads the emitter's kind or stamp; class: gap
- src/edge/netpen/runner/runner_test.go:538: `listen` made to fail always; fails: the test skips instead of failing; class: false test
- src/edge/netpen/runner/durability_test.go:148: `wantRun` derived from `Impact.Level()`, the code under test; fails: two levels swapped; class: convention
- src/edge/netpen/full/full.go:219: `Target` or `Observe` dropped from the phase runner's options; fails: no test records what a phase's behavior received; class: gap
- src/edge/netpen/cmd/netpen/main.go:373: `Observe` or `Acknowledged` dropped from the runner options; fails: no command test runs with a non-default window or an acknowledged permanent mode; class: gap
- src/edge/netpen/cmd/netpen/main.go:412: `MaxImpact` dropped from `full`'s config; fails: no test runs the `full` command through `run`; class: gap
- src/edge/netpen/attacks/fh/ghost.go:129: confirmed on `observed > 0`; fails: ambient-only input must be resisted, which TestGhost_TraversalAttribution's comment states and no test feeds; class: false test
- src/edge/netpen/attacks/ip6/raguard.go:122: confirmed on `observed > 0`; fails: the same ambient-only case; class: false test
- src/edge/netpen/attacks/*/: a `Need` field dropped from hsrp, glbp, vrrp, gratarp, icmpredirect, llmnr, arpsweep, daddos, roguera, raguard, ndpspoof, roguedhcp6, doubletag, voicevlan, vtp, portsteal, eigrp; fails: only arpspoof, roguedhcp, vlanhop, and ospf have an empty-target test, and arpspoof's checks `Contains("victim")`, which `victim-mac` satisfies; class: gap
- src/edge/netpen/attacks/fh/arpspoof.go:43: `Arm` moved after the poison sends; fails: no test reads the partial record's step names; class: gap
- src/edge/netpen/attacks/routing/routing_test.go:578: the code compared with its own string; fails: `ErrCodeWPADPortCollision` removed from the bind error; class: false test
- src/edge/netpen/attacks/routing/routing_test.go:626: step counts from TX frames only; fails: a renamed or extra no-op teardown step; class: gap
- src/edge/netpen/check/target.go:118: one `has` case swapped for another field; fails: only victim versus victim-mac is pinned; class: gap
- src/edge/netpen/catalog/catalog.go:150: the missing-help, mode-without-flag, unknown-restore-kind, and mode-bound checks removed; fails: no rejection case; class: gap
- src/edge/netpen/catalog/catalog.go:143: the clones in `Lookup`, `New`, and `rows` removed; fails: the detachment the deleted snapshots test pinned; class: gap
- src/edge/netpen/link/link_linux_test.go:773: the poll wait removed from `Close`; fails: only when the read goroutine loses the race, so the test is racy against its own mutation; class: false test
- src/edge/netpen/cmd/netpen/signals_test.go:98: the planted step never runs; fails: the test asserts exit 1 only, which a canceled run also gives; class: false test
- src/edge/netpen/findings/findings_test.go:21: `omitempty` added to `observed_ms`; fails: no zero-observed case; class: gap
- test/conformance/netpen/process_policy_test.go:236: `NotifyContext` removed from the table, a dot import, a nested directory, `log.Fatal`, or `syscall.Exit`; fails: the fixture proves `Notify` and `os.Exit` only; class: hardening
- src/edge/netpen/README.md:57: "the decay announcement" and "names `victim` and `gateway`"; fails: arpspoof restores actively and needs four fields including the MACs; class: convention
- src/edge/netpen/check/target.go:63: "The names match the command's flags" and the hint "name the target through the command's target flags"; fails: `victim-mac` and the three other MAC fields are not flags; class: convention
- src/edge/netpen/check/check.go:5: "imports only findings and link"; fails: target.go imports errs; class: convention
- src/edge/netpen/check/target.go:12: `[Skipped]` doc link resolves to nothing; class: convention
- src/edge/netpen/findings/findings.go:22: "Version 2 replaced the … rollup kinds"; fails: history wording; class: convention
- src/edge/netpen/attacks/*/*.go: "Durability (from the catalog): …" and "Classified transient-decay" headers in 24 non-test files name the deleted classes; roadmap "yet" in etherchannel.go:31, portsteal.go:33, vlanhop.go:27, voicevlan.go:31; stale fixture text in eigrp.go:258, ospf.go:1, l2/helpers.go:3; em dashes in 15 non-test files; class: convention
- src/edge/netpen/attacks/fh/vrrp.go:10: "verify the field set rather than the byte-for-byte checksum"; fails: TestVRRP_FixturePins compares bytes; class: convention
- src/edge/netpen/full/full.go:25: "the command's signal handler engages the runner's teardown"; fails: `runFull` installs none; class: convention
- src/edge/netpen/catalog/codes.go:27: `ErrCodeCeilingRefused` has no user; class: convention
- src/edge/netpen/cmd/netpen/main.go:415: `SweepNet` hard-coded to `172.16.0.0/24` while `--sweep-net` is accepted and ignored, and `--ra-prefix` and `--stp-tag` are read by nothing; class: convention
- src/edge/netpen/test/integration/VALIDATION_MATRIX.md:70: ghost and raguard "resisted on an empty window" and vlanenum confirmed-only; fails: the code resists after a frame count and vlanenum ends pending on no tags; class: convention
- src/edge/netpen/test/integration/VALIDATION_MATRIX.md:119: the T1 target flags and the injector's IPv4 prerequisite are not listed; class: convention
- src/edge/netpen/attacks/ip6/register.go:19: roguedhcp6 bound 300 s while the advertise's valid lifetime is 600 s; class: convention
- src/edge/netpen/attacks/l2/register.go:25: vlanhop rated temporary/active/device for the frame doubletag sends as none, voicevlan and etherchannel active with no-op restore steps; class: convention
- src/edge/netpen/catalog/catalog.go:188: `New` accepts permanent with an active restore, temporary with none, none with a restore or affects, and an orchestration entry with modes or a run; class: hardening
- src/edge/netpen/catalog/gen_catalog.go:132: a sub-second bound is emitted as whole seconds and `time` is imported when no row has a bound; class: hardening
- src/edge/netpen/runner/runner.go:213: an unknown `MaxImpact` orders above permanent and admits everything; class: hardening
- src/edge/netpen/runner/runner.go:421: a behavior-returned skipped verdict with a nil error ends the run with `fail(nil)`, and a teardown-partial `runErr` is replaced by a later target error; class: hardening
- src/edge/netpen/runner/runner.go:418: an empty verdict kind passes through uncounted; class: hardening
- src/edge/netpen/runner/runner.go:392: an interrupted or canceled attack gets no verdict record, and Run returns nil or `context.Canceled` by scheduling (the race predates the change); class: hardening
- src/edge/netpen/cmd/netpen/signals.go:71: `stop` closes the channel before `signal.Stop`, so a late signal can still force-exit; class: hardening
- src/edge/netpen/cmd/netpen/stream.go:53: the single-attack summary's `Attacks` counts records (predates the change); class: hardening
- src/edge/netpen/attacks/ip6/helpers.go:75: a MAC-less attack interface panics in `linkLocalFromMAC`, and a leg without IPv4 or link-local IPv6 fails in craft with an uncoded error; class: hardening
- src/edge/netpen/attacks/ip6/raguard.go:98: `traversal: not-forwarded` recorded with no watch leg, and daddos.go:64 reads one frame in 50 ms for its `resisted` field; class: hardening
- src/edge/netpen/attacks/routing/wpad.go:109: the PAC listener binds loopback and closes before return while the answers point clients at the leg IPv4, and the doc comment no longer says so; class: convention
- src/edge/netpen/link/link_linux.go:55: `OptAddVLANHeader` not set, so an offloading NIC strips the 802.1Q tag before vlanenum sees it (afpacket/options.go:102-109); class: hardening
- src/edge/netpen/link/link.go:122: `Receive` delivers frames buffered in the ring since open, undocumented, so a shared watch leg under `full` reads earlier attacks' frames first (kernel fill unverified); class: hardening
