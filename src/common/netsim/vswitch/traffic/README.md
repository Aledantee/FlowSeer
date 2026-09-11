# Switch traffic configuration

`traffic` holds the virtual switch's mirrors, ingress policers, and per-PCP
queue limits. The package does not forward or schedule frames. It exposes the
value-level decisions that the switch and fabric apply.

## Mirrors

A mirror selects a frame when `SelectAll` is true, its ingress port is in
`SelectSrcPorts`, or at least one successful relay egress is in
`SelectDstPorts`. A non-empty `SelectVLANs` narrows that result to the frame
classified VLAN. Dropped relay egresses do not count.

`OutputPort` produces one copy of the received frame, including every tag.
`OutputVLAN` produces one copy for each switchport that carries that VLAN,
apart from the ingress port. A tagged switchport receives a C-tag whose VID is
the output VLAN. It keeps PCP and DEI from an outer received C-tag. An untagged
switchport or a tunnel for that VID receives the remaining tag stack without
the outer tag. VLAN-unaware bridges cannot resolve an output VLAN, and reserved
bridge-group destinations are never copied to a VLAN.

For example, suppose VLAN 99 is tagged on `1/1/24`, untagged on `1/1/4`, and
the frame's ingress is `1/1/1`. A received C-tag with VID 10 and PCP 5 becomes a
VID 99, PCP 5 C-tag on the `1/1/24` copy. The `1/1/4` copy has no outer tag, and
there is no copy on `1/1/1`.

`SnapLen` limits each copy after its output tag form is built. Truncation only
cuts payload. A value shorter than the Ethernet header leaves an empty
payload because the header itself cannot be shortened.

## Policing and queue rates

`Bucket` starts with `BurstOctets` tokens. Each call to `Admit` lazily adds
`RateBPS * elapsed / 8` octets, capped at the burst, then spends the frame size
when enough tokens exist. A refused frame spends nothing. Rate zero disables
policing.

`Config.MaxRate` looks up a port and PCP in the queue table. A missing entry
means that priority has no configured maximum.

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
