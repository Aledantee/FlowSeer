# loopprotect

Package `loopprotect` implements sim's own loop-protection mechanism for
the virtual switch: a probe frame that floods a foreign switch but returns to
its own sender through an unmanaged loop, and a per-port action and recovery
timer that reacts to it. It is drawn from vendor loop-detection features
(H3C loop detection, HPE Aruba loop-protect, Huawei loopback-detect) but is
not an emulation of any of them. Each vendor's probe format is proprietary,
so this package parses none of them, and defines its own instead.

The layer runs deterministically in memory without background goroutines or
wall clocks. Time advances through explicit, time-stamped calls to `Receive`,
`Advance`, and `LinkChange`. It is independent of spanning tree: both may be
configured on one switch, and a `Layer` here neither knows about STP nor
consults its own gate before deciding whether to keep sending probes. See
"Emission ignores the gate" below.

## Example

A switch with two ports joined into a loop by an unmanaged hub sends a probe
out each port and hears one of them return, blocking the sender.

```go
package main

import (
	"fmt"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/loopprotect"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func main() {
	ports, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}).
		Build()
	if err != nil {
		panic(err)
	}

	mac, err := netaddr.Parse("00:11:22:33:44:01")
	if err != nil {
		panic(err)
	}

	lyr, err := loopprotect.New(loopprotect.Config{
		Interval: 5 * time.Second,
		Ports: map[string]loopprotect.Port{
			"1/1/1": {Action: loopprotect.Block},
			"1/1/2": {Action: loopprotect.Block},
		},
	}, layer.Env{Ports: ports, MAC: mac})
	if err != nil {
		panic(err)
	}

	t0 := time.Unix(1700000000, 0)
	lyr.LinkChange(t0, "1/1/1", true)
	lyr.LinkChange(t0, "1/1/2", true)

	// The first probes go out one interval after the links came up.
	fx := lyr.Advance(t0.Add(5 * time.Second))

	// The unmanaged hub loops 1/1/1's probe back onto the switch; the
	// switch decodes it and finds it names this switch as sender.
	probe, err := loopprotect.Decode(fx.Emissions[0].Frame)
	if err != nil {
		panic(err)
	}
	lyr.Receive(t0, loopprotect.Return{VID: probe.VID}, probe)

	fmt.Printf("1/1/1: %s\n", lyr.PortInfo("1/1/1").Action) // Block
	fmt.Printf("1/1/2: %s\n", lyr.PortInfo("1/1/2").Action) // (none)
}
```

## Probe frame

The format is this simulator's own, and this section is its specification.
No vendor document defines it.

A probe is an Ethernet II frame, 14 octets of header and a payload of 14
octets plus the port name, unpadded. The destination is `03:46:53:4c:50:00`,
a locally administered group address returned by `GroupAddress`. The
EtherType is `0x88b5`, which the IANA IEEE 802 Numbers registry lists as
"IEEE Std 802 - Local Experimental Ethertype"
(https://www.iana.org/assignments/ieee-802-numbers/ieee-802-numbers.txt).
IEEE Std 802, which defines that range, was not read, so what it requires of
a frame under the EtherType is unverified.

| Payload offset | Octets | Field |
| --- | --- | --- |
| 0 | 1 | Version, always 1 |
| 1 | 6 | Originating switch MAC |
| 7 | 2 | VLAN ID the probe was sent on, big endian |
| 9 | 4 | Sequence number of the sending port, big endian |
| 13 | 1 | Port name length, 1 to 255 |
| 14 | length | Port name, the sending port |

The fixture is the frame below, 36 octets. It reads as destination
`03:46:53:4c:50:00`, source `aa:bb:cc:dd:ee:ff`, EtherType `0x88b5`, version 1,
origin `02:11:22:33:44:55`, VLAN `0x0141`, sequence `0xa1b2c3d4`, name length
8, and the name `et-0/0/7`. `TestEncodeMatchesFixture` and `TestDecodeFixture`
hold it as literal octets.

```text
03 46 53 4c 50 00 aa bb cc dd ee ff 88 b5 01 02
11 22 33 44 55 01 41 a1 b2 c3 d4 08 65 74 2d 30
2f 30 2f 37
```

`Decode` refuses, before it reads the payload, a frame whose destination is
not the probe group or whose EtherType is not `0x88b5`. It then refuses a
payload shorter than 14 octets, a version other than 1, a name length of
zero, a name running past the payload, and octets after the name. `Encode`
refuses a port name that is empty or longer than 255 octets, the two names
`Decode` cannot return. `Config.Validate` bounds the names a layer probes, so
the layer's own emission cannot fail to encode.

## Detection

A probe whose payload names this switch's own MAC as the originator is a
loop on the port the payload names as the sender (`p.Port`), rather than the port
`Receive`'s ingress argument names. The switch that decodes the probe and
recognizes itself is responsible for calling `Receive` only in that case.
This layer trusts the caller and never re-derives ownership from the wire.

The `Return.VID` `Receive` takes is the VLAN the frame was classified into
on arrival, which can differ from `Probe.VID`, the VLAN the probe carried
when it was sent: a probe that returns under a different VLAN tag has
crossed an inter-VLAN loop, and `PortInfo.InterVLAN` records it. The
comparison is against the classified VLAN rather than a tag the returning
frame may not carry at all. An access-port loop returns the probe untagged.

A VID difference is not evidence of an inter-VLAN loop when the sending
port is itself an untagged member of both VLANs (`Return.SameUntaggedDomain`).
IEEE 802.1Q makes untagged egress a per-VLAN port set while ingress
classification is a per-port PVID scalar, so a port can legitimately untag
several VLANs while classifying ingress into only one of them. Vendors
ship this as "asymmetric VLAN" for a shared uplink to many tenant VLANs. An
untagged frame carries no tag at all, so both VLAN numbers on such a port
are local bookkeeping on either end of the wire, not something the wire
joined. The switch, which owns the VLAN configuration this layer does not,
computes `SameUntaggedDomain` and `Receive` only sets `InterVLAN` when it is
false.

## Actions and the gate

| Action | Learns | Forwards | Keeps probing |
| --- | --- | --- | --- |
| `Block` | denied | denied | yes |
| `NoLearn` | denied | allowed | yes |
| `Disable` | denied | denied | no |

`NoLearn` contains the MAC flapping a loop causes. It does not break the
loop, because the port keeps forwarding. An unprotected port, or a protected
port with no action currently applied, allows both, following the house
convention (see `stp`) that an untracked port never blocks.

## Flushing stale entries

`Receive` returns `Effects.Flush`, one `FlushTarget{Port, FIDs}` naming the
port whose action it just applied, mirroring `stp`'s topology-change flush:
the entries a port learned while the loop flooded through it are stale the
moment the port stops forwarding, exactly as spanning tree's own flush
covers a topology change. `FIDs` is always empty, meaning every FID on the
port.

This fires only on the transition into a forwarding-denying action (`Block`
or `Disable`). A repeat probe for a port already carrying one flushes
nothing again, and `NoLearn` never flushes, because it keeps forwarding and
denies only new learning.

## Recovery

| Mode | Lifts | Clears |
| --- | --- | --- |
| `Manual` | never on its own | `Clear`, or a link reported down and then up |
| `Timer` | `Recovery.Duration` after the action was applied, whether or not the loop is gone | (lifts on schedule) |
| `LoopCleared` | `Recovery.Duration` after the last returned probe, with none returned since | (lifts on schedule) |

`Timer` adds no backoff: a returned probe after it lifts reapplies the
action immediately and increments `PortInfo.Recurrences`. A returned probe
while the action is still applied changes nothing under `Timer`. The timer
does not restart, matching the "whether or not the loop is gone" rule.
`LoopCleared` is the opposite: every returned probe restarts the wait,
whether the action was already applied or is being applied for the first
time, so a persistent loop holds the port down indefinitely.

`Manual`'s link-cycle recovery follows spanning tree's BPDU-guard precedent:
clearing happens on the down transition, not the up one, so an up report
with no preceding down leaves the action applied. `LinkChange`'s first call
to the layer, on any port, also arms the probe-emission cadence. This
mirrors how `stp.Layer` arms its hello timer on the first `LinkChange` or
`Receive` rather than at construction, since `New` is never given the
fabric's current time.

`Clear` also resets `PortInfo.InterVLAN`, as it resets the recurrence
tracking. A lift by a `Timer` or `LoopCleared` window or by a link cycle keeps
both.

When `Advance`, `Clear`, or a link-down `LinkChange` lifts the action that had
stopped the last emitting port, the next probe is one interval after the lift,
not at a deadline set before every port was stopped.

`Config.Validate` refuses `LoopCleared` paired with `Disable`: a disabled
port sends no probes, so `LoopCleared` recovery could never observe the loop
clearing and would hold the port down forever.

## Emission ignores the gate

`Advance` emits one probe per protected port per VLAN in `Port.VLANs` (or one
probe for VID 0, which the switch sends on the port's PVID, when `VLANs` is
empty) for every port that is not currently applying `Disable`, walking
ports in sorted name order so a wake's emissions are deterministic. A
`Disable`-configured port keeps probing until a returned probe applies the
action. Only an *applied* `Disable` stops emission, because only a returned
probe can apply it in the first place. Gating on the configured action
alone would make `Disable` unreachable. Sequence numbers increase per port.

The layer meters its own probes: an `Advance` before the interval is due lifts
whatever recovery windows have elapsed and emits nothing. A switch wakes its
layers together, so `Advance` runs at every spanning tree hello and at every
recovery expiry as well, and a probe on each of those would run far ahead of
the configured interval. `NextWake` reports the earlier of the next probe and
the next recovery. `Receive`, `Advance`, and `LinkChange` each arm the cadence
on their first call, so `NextWake` reports a recovery deadline `Receive` set.
The probe time counts only while some port would emit, that is, while some
port is not under an applied `Disable`. With every port stopped, only recovery
deadlines remain, and a layer without ports reports none.

Crucially, emission does not consult `Learns`/`Forwards` on its own port: a
`Block`ed port keeps sending probes. If it stopped, a `Block`ed port's probes
would stop returning and `LoopCleared` would decay into a plain timer that
lifts while the cable is still looped, defeating the point of the mode.
Whether a port's operational state and any other gate (spanning tree
included) actually let a probe onto the wire is the switch's decision, not
this layer's. `Disable` is the one action defined to stop probing, which is
also why pairing it with `LoopCleared` recovery is refused at construction.

## State retention

`RetentionKey(cfg Config, env layer.Env) string` encodes
every normalized input the loop-protection runtime state depends on: its own
configuration as `Diff` sees it, port administrative and operational states, and
the switch's base MAC. `vswitch.Derive` retains the runtime layer only when both
keys match and rebuilds it otherwise.

## Not modeled

- Vendor probe formats, SNMP traps, and log actions.
- The physical consequences of a `Disable`d port: this layer holds the port
  down internally and never touches `port.Table`, so its peer does not see
  the link go down, unlike a real errdisabled port.
- Per-VLAN recovery: an action is per port, matching every fetched vendor's
  default target.
- Operational state and spanning tree, for emission. This layer knows about
  neither. The switch drops a probe for a port that is not forwarding or that
  a spanning tree holds discarding before it reaches the wire. See "Emission
  ignores the gate" above.

## Switch integration

The switch [intercepts](../../device/vswitch/forward.go) a decoded probe
naming itself after bridge classification. A foreign probe follows the
ordinary multicast relay path. A returning probe meets the ingress gate
before the layer acts on the sender's logical port.

[Probe emission](../../device/vswitch/timers.go) requires an operationally
forwarding port and spanning tree forwarding on the VLAN. It uses ordinary
egress VLAN tagging and ignores this layer's own bridge gate so a blocked
port can keep testing recovery. An applied `Disable` stops probes in this
layer. A forwarding-denying action flushes learned entries for that port.
`ClearLoopProtect` resolves a member to its logical port and returns an
empty output ([loop protection tests](../../device/vswitch/loopprotect_test.go)).

## Configuration diffs

`Diff` compares normalized interval and port configurations
([diff.go](diff.go)). VLAN members use quoted decimal coordinates and
`loopprotect.vlan_id` facts. Replacing VLAN 10 with 20 emits
`vlans["10"]: 10 -> nil` followed by `vlans["20"]: nil -> 20`.
Reordering and duplicate-only edits produce no records
([diff_coverage_test.go](diff_coverage_test.go)).

A zero submitted recovery duration inherits three probe intervals. Changing
interval from 1s to 2s therefore emits `interval: 1s -> 2s` followed by
`recovery.duration: 3s -> 6s`. An explicit recovery duration stays unchanged
([config_test.go](config_test.go), `TestDiffRecoveryDurationDependency`).
