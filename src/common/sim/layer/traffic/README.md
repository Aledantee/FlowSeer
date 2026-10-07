# Switch traffic configuration

`traffic` holds the virtual switch's mirrors, ingress policers, and per-PCP
queue limits. The package exposes `Layer` with `New`, `Admit`, and `Retain`,
alongside the value-level decisions that the switch and fabric apply.

## Mirrors

A mirror selects a frame when `SelectAll` is true, its ingress port is in
`SelectSrcPorts`, or at least one successful relay egress is in
`SelectDstPorts`. A non-empty `SelectVLANs` narrows that result to the frame
classified VLAN. Dropped relay egresses do not count. Port selectors name
logical ports: a LAG name is valid, while one of its physical members is not.

`OutputPort` produces one copy of the received frame, including every tag. A
frame that arrived on the output port is not copied back to it.
`OutputVLAN` produces one copy for each switchport that carries that VLAN,
apart from the ingress port. A tagged switchport receives a C-tag whose VID is
the output VLAN above the received tags. An untagged switchport or a tunnel for
that VID receives the received tags alone. `OutputVLAN` and each `SelectVLANs`
entry require a VLAN-aware bridge and must name an entry in its VLAN table.
Reserved bridge-group destinations are never copied to a VLAN.

The tag rule reads the ingress switchport and the output switchport apart. The
output's tunnel decides the output form alone. The ingress port decides which
received tags a copy keeps. The outer received tag is replaced by the output
C-tag, or dropped from an untagged copy, only when its TPID is `0x8100` or
unset and the ingress port is no tunnel. An outer S-tag stays, and a tunnel
port's outer C-tag is the customer's and stays. The output C-tag takes its PCP
and DEI from an outer received C-tag on either kind of port, and has PCP 0
otherwise.

`Layer.Copies` produces candidate copies from these selection and tag rules. Each
VLAN-output copy keeps that configured logical VLAN separately from the emitted
tag stack. The switch uses the logical VLAN for LAG member selection even when
an untagged or tunnel output removed its outer tag. The switch then checks each
candidate's output state before exposing it. A physical output must be known up.
A logical LAG output also needs an enabled, known-up member. Unknown state
suppresses the copy and marks the forwarding result incomplete; known-down
state is a definite copy drop with complete readiness.

For example, suppose VLAN 99 is tagged on `1/1/24`, untagged on `1/1/4`, and
the frame's ingress is `1/1/1`, which is no tunnel. A received C-tag with VID 10
and PCP 5 becomes a VID 99, PCP 5 C-tag on the `1/1/24` copy. The `1/1/4` copy
has no tag, and there is no copy on `1/1/1`. A frame with an S-tag of VID 100
over a C-tag for VID 20 reaches `1/1/24` as a VID 99, PCP 0 C-tag over both
received tags, and reaches `1/1/4` with both.

`SnapLen` limits each copy after its output tag form is built. Zero leaves the
copy untruncated; other values must be at least 18 octets, the size of a tagged
Ethernet header. Truncation cuts payload only. When stacked tags make the
header longer than `SnapLen`, the copy keeps the complete header and has an
empty payload, so its encoded length exceeds `SnapLen`.

## Policing and queue rates

Both tables follow `OVS`. Its `ingress_policing_rate` and
`ingress_policing_burst` columns belong to an Interface, and its Queue table
hangs off the `qos` column of a Port. A policer therefore names a port that is
no LAG, and a LAG member is a valid name. A queue names a LAG or a port that is
no member, because the egress queue of a member answers to its LAG's
statement. `Validate` refuses a policer on a LAG at `policers.<name>` and a
queue on a member at `queues.<name>`. `Layer.MaxRate` and `Layer.QueueBuffer`
answer a member's name with its LAG's statement.

Each policer bucket starts with `BurstOctets` tokens. Each call to `Admit` lazily adds
`RateBPS * elapsed / 8` octets, capped at the burst, then spends the frame size
when enough tokens exist. A refused frame spends nothing. Rate zero disables
policing.

`Config.MaxRate` looks up a port and PCP in the queue table. A missing entry
means that priority has no configured maximum. `PortQueues.BufferOctets` states
a queue buffer in encoded frame octets, the length `ethernet.Frame.Encode`
returns, so it excludes the wire's preamble, start delimiter, and interpacket
gap. `Config.QueueBuffer` looks it up the same way; a missing entry means that
priority's buffer is unbounded and its queue never tail-drops.

On an unstated queue, `traffic.queue.buffer-unstated` names the first enqueue
that raises one of the physical endpoint's per-PCP queues above one
port-MTU-sized encoded frame. `QueueThresholdFact` records the depth before that
enqueue, the frame's encoded octets, and the threshold. A LAG supplies the
logical queue and MTU; the selected member owns the physical queue and its
issue. Later crossings on another PCP of the same member create no second event.

## Limits

- A VLAN-output copy never leaves the port the frame arrived on. `OVS` sends
  it there, and its note on unmanaged switches describes the reflected copy.
- Two mirrors with one destination each produce a copy. `OVS` mirrors a
  packet "to a particular destination only once". A copy names the mirror that
  produced it, and a merged copy has no single name.

## State retention

`RetentionKey(cfg Config, env layer.Env) string` encodes the normalized traffic configuration
as `Diff` sees it, each queue's rate and stated buffer included.
`Layer.Retain` carries over active token buckets from a previous layer for policers
whose configuration has not changed.

## Sources

The shapes follow the Open vSwitch database schema on
[`branch-3.3`](https://raw.githubusercontent.com/openvswitch/ovs/branch-3.3/vswitchd/vswitch.xml).
The relevant Mirror column text is:

- `select_all`: "every packet arriving or departing on any port"
- `select_src_port`: "Ports on which arriving packets are selected"
- `select_dst_port`: "Ports on which departing packets are selected"
- `select_vlan`: "An empty set selects packets on all VLANs"
- `snaplen`: "A mirrored packet with size larger than snaplen will be truncated"

The destination columns are `output_port` and `output_vlan`. The Interface
columns are `ingress_policing_rate`, described as "Data received faster than
this rate is dropped", and `ingress_policing_burst`. Queue `max-rate` says
"the queue's rate will not be allowed to exceed the specified value, even if
excess bandwidth is available".
