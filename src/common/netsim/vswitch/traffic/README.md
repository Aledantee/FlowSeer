# Switch traffic configuration

`traffic` holds the virtual switch's mirrors, ingress policers, and per-PCP
queue limits. The package does not forward or schedule frames. It exposes the
value-level decisions that the switch and fabric apply.

## Mirrors

A mirror selects a frame when `SelectAll` is true, its ingress port is in
`SelectSrcPorts`, or at least one successful relay egress is in
`SelectDstPorts`. A non-empty `SelectVLANs` narrows that result to the frame
classified VLAN. Dropped relay egresses do not count. Port selectors name
logical ports: a LAG name is valid, while one of its physical members is not.

`OutputPort` produces one copy of the received frame, including every tag.
`OutputVLAN` produces one copy for each switchport that carries that VLAN,
apart from the ingress port. A tagged switchport receives a C-tag whose VID is
the output VLAN. It keeps PCP and DEI from an outer received C-tag. An untagged
switchport or a tunnel for that VID receives the remaining tag stack without
the outer tag. `OutputVLAN` and each `SelectVLANs` entry require a VLAN-aware
bridge and must name an entry in its VLAN table. Reserved bridge-group
destinations are never copied to a VLAN.

`Copies` produces candidate copies from these selection and tag rules. Each
VLAN-output copy keeps that configured logical VLAN separately from the emitted
tag stack. The switch uses the logical VLAN for LAG member selection even when
an untagged or tunnel output removed its outer tag. The switch then checks each
candidate's output state before exposing it. A physical output must be known up.
A logical LAG output also needs an enabled, known-up member. Unknown state
suppresses the copy and marks the forwarding result incomplete; known-down
state is a definite copy drop with complete readiness.

For example, suppose VLAN 99 is tagged on `1/1/24`, untagged on `1/1/4`, and
the frame's ingress is `1/1/1`. A received C-tag with VID 10 and PCP 5 becomes a
VID 99, PCP 5 C-tag on the `1/1/24` copy. The `1/1/4` copy has no outer tag, and
there is no copy on `1/1/1`.

`SnapLen` limits each copy after its output tag form is built. Zero leaves the
copy untruncated; other values must be at least 18 octets, the size of a tagged
Ethernet header. Truncation cuts payload only. When stacked tags make the
header longer than `SnapLen`, the copy keeps the complete header and has an
empty payload, so its encoded length exceeds `SnapLen`.

## Policing and queue rates

`Bucket` starts with `BurstOctets` tokens. Each call to `Admit` lazily adds
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
that raises the physical endpoint's queue above one port-MTU-sized encoded
frame. `QueueThresholdFact` records the depth before that enqueue, the frame's
encoded octets, and the threshold. A LAG supplies the logical queue and MTU;
the selected member owns the physical queue and its issue. Later crossings on
another PCP of the same member create no second event.

## State retention

`RetentionKey(cfg Config) string` encodes the normalized traffic configuration
as `Diff` sees it, each queue's rate and stated buffer included.
`vswitch.Derive` retains active token buckets per matching policer when the
layer's retention key is unchanged.

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
