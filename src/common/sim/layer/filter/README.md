# Packet filtering capability

`filter` evaluates interface-bound access-control rules and stateful reverse
matches for routed traffic in the virtual switch. It does not route or schedule
frames. It models firewall rule sets bound to interfaces in ingress and egress
directions.

## Architecture and evaluation order

A filter configuration attaches rule sets to interfaces through bindings. Each
binding specifies an interface name, a direction (`in` or `out`), and the target
rule set.

Ingress evaluation occurs before routing:

1. Look up the rule set bound to the ingress interface in the `in` direction.
2. Evaluate rules in order (first-match semantics). A matching `Accept` allows
   the packet to proceed to layer 3 routing. A matching `Drop` or `Reject`
   terminates immediately without routing.
3. If no rule matches and the set is stateless, apply the set's default action.
4. If no rule matches and the set is stateful, defer the decision until routing
   resolves the egress interface. When the egress interface is known, reverse
   the 5-tuple (swap source and destination addresses and ports) and evaluate
   the set bound `in` on that egress interface. If the counterpart set is
   stateful and has a forward rule that accepts the reversed tuple, accept the
   reply under rule `filter.state` and name the counterpart rule in the trace
   inputs. Otherwise, apply the ingress set's own default action.

Egress evaluation occurs after routing resolves the egress interface:

1. Look up the rule set bound to the egress interface in the `out` direction.
2. Evaluate rules in order. If a rule matches, apply its action.
3. If no rule matches and the set is stateful, evaluate the reversed 5-tuple
   against the rule set bound `out` on the frame's ingress interface.

## Rule matching

Each rule specifies a packet match criteria:

- `Protocol`: match on IP protocol number (e.g. 6 for TCP, 17 for UDP, 1 for ICMPv4).
- `Src` and `Dst`: match on list of IP CIDR prefixes.
- `SrcPorts` and `DstPorts`: match inclusive transport port ranges.
- `ICMP`: match ICMP type and optional code.
- `TCPFlags`: match TCP control bits against a bitwise mask and expected value.

Omitted match fields match any packet.

## State retention and diffs

`Diff` computes differences between two configurations. It reports additions,
removals, and modifications of rules, sets, and bindings as typed `trace.Change`
records.

## Boundaries

- Imports: `src/common/errs`, `src/common/net/*`, `src/common/netsim/analysis`,
  `src/common/netsim/trace`, `src/common/netsim/vswitch/port`.
- Imported by: `src/common/netsim/vswitch`, `src/common/netsim/vswitch/netmodel`.
