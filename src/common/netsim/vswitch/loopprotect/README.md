# loopprotect

Package `loopprotect` implements netsim's own loop-protection mechanism for
the virtual switch: a probe frame that floods a foreign switch but returns to
its own sender through an unmanaged loop, and a per-port action and recovery
timer that reacts to it. It is drawn from vendor loop-detection features
(H3C loop detection, HPE Aruba loop-protect, Huawei loopback-detect) but is
not an emulation of any of them — each vendor's probe format is proprietary,
so this package parses none of them, and defines its own instead.

The layer runs deterministically in memory without background goroutines or
wall clocks. Time advances through explicit, time-stamped calls to `Receive`,
`Wake`, and `LinkChange`. It is independent of spanning tree: both may be
configured on one switch, and a `Layer` here neither knows about STP nor
consults its own gate before deciding whether to keep sending probes — see
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
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/loopprotect"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
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

	layer, err := loopprotect.New(loopprotect.Config{
		Interval: 5 * time.Second,
		Ports: map[string]loopprotect.Port{
			"1/1/1": {Action: loopprotect.Block},
			"1/1/2": {Action: loopprotect.Block},
		},
	}, ports, mac)
	if err != nil {
		panic(err)
	}

	t0 := time.Unix(1700000000, 0)
	layer.LinkChange(t0, "1/1/1", true)
	layer.LinkChange(t0, "1/1/2", true)

	// The first probes go out one interval after the links came up.
	fx := layer.Wake(t0.Add(5 * time.Second))

	// The unmanaged hub loops 1/1/1's probe back onto the switch; the
	// switch decodes it and finds it names this switch as sender.
	probe, err := loopprotect.Decode(loopprotect.Encode(fx.Emissions[0].Probe, mac))
	if err != nil {
		panic(err)
	}
	layer.Receive(t0, probe.VID, probe)

	fmt.Printf("1/1/1: %s\n", layer.PortInfo("1/1/1").Action) // Block
	fmt.Printf("1/1/2: %s\n", layer.PortInfo("1/1/2").Action) // (none)
}
```

## Detection

A probe whose payload names this switch's own MAC as the originator is a
loop on the port the payload names as the sender — `p.Port`, not the port
`Receive`'s ingress argument names. The switch that decodes the probe and
recognizes itself is responsible for calling `Receive` only in that case;
this layer trusts the caller and never re-derives ownership from the wire.

The `vid` `Receive` takes is the VLAN the frame was classified into on
arrival, which can differ from `Probe.VID`, the VLAN the probe carried when
it was sent: a probe that returns under a different VLAN tag has crossed an
inter-VLAN loop, and `PortInfo.InterVLAN` records it. The comparison is
against the classified VLAN rather than a tag the returning frame may not
carry at all — an access-port loop returns the probe untagged.

## Actions and the gate

| Action | Learns | Forwards | Keeps probing |
| --- | --- | --- | --- |
| `Block` | denied | denied | yes |
| `NoLearn` | denied | allowed | yes |
| `Disable` | denied | denied | no |

`NoLearn` contains the MAC flapping a loop causes; it does not break the
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
or `Disable`) — a repeat probe for a port already carrying one flushes
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
while the action is still applied changes nothing under `Timer` — the timer
does not restart, matching the "whether or not the loop is gone" rule.
`LoopCleared` is the opposite: every returned probe restarts the wait,
whether the action was already applied or is being applied for the first
time, so a persistent loop holds the port down indefinitely.

`Manual`'s link-cycle recovery follows spanning tree's BPDU-guard precedent:
clearing happens on the down transition, not the up one, so an up report
with no preceding down leaves the action applied. `LinkChange`'s first call
to the layer, on any port, also arms the probe-emission cadence — this
mirrors how `stp.Layer` arms its hello timer on the first `LinkChange` or
`Receive` rather than at construction, since `New` is never given the
fabric's current time.

`Config.Validate` refuses `LoopCleared` paired with `Disable`: a disabled
port sends no probes, so `LoopCleared` recovery could never observe the loop
clearing and would hold the port down forever.

## Emission ignores the gate

`Wake` emits one probe per protected port per VLAN in `Port.VLANs` — or one
probe for VID 0, which the switch sends on the port's PVID, when `VLANs` is
empty — for every port that is not currently applying `Disable`, walking
ports in sorted name order so a wake's emissions are deterministic. A
`Disable`-configured port keeps probing until a returned probe applies the
action; only an *applied* `Disable` stops emission, because only a returned
probe can apply it in the first place — gating on the configured action
alone would make `Disable` unreachable. Sequence numbers increase per port.

The layer meters its own probes: a `Wake` before the interval is due lifts
whatever recovery windows have elapsed and emits nothing. A switch wakes its
layers together, so `Wake` runs at every spanning tree hello and at every
recovery expiry as well, and a probe on each of those would run far ahead of
the configured interval. `NextWake` reports the earlier of the next probe and
the next recovery.

Crucially, emission does not consult `Learns`/`Forwards` on its own port: a
`Block`ed port keeps sending probes. If it stopped, a `Block`ed port's probes
would stop returning and `LoopCleared` would decay into a plain timer that
lifts while the cable is still looped, defeating the point of the mode.
Whether a port's operational state and any other gate (spanning tree
included) actually let a probe onto the wire is the switch's decision, not
this layer's; `Disable` is the one action defined to stop probing, which is
also why pairing it with `LoopCleared` recovery is refused at construction.

## Not modeled

- Vendor probe formats, SNMP traps, and log actions.
- The physical consequences of a `Disable`d port: this layer holds the port
  down internally and never touches `port.Table`, so its peer does not see
  the link go down, unlike a real errdisabled port.
- Per-VLAN recovery: an action is per port, matching every fetched vendor's
  default target.
- Operational state and spanning tree, for emission. This layer knows about
  neither; the switch drops a probe for a port that is not forwarding or that
  a spanning tree holds discarding before it reaches the wire. See "Emission
  ignores the gate" above.
