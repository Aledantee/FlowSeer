---
title: A family check on a rendered address accepts a v4-mapped IPv6 network
date: 2026-10-15
last_verified: 2026-10-15
category: conventions
module: src/edge/netpen/cmd/netpen
problem_type: bug
component: netpen
severity: medium
symptoms:
  - "an IPv4-only flag or field accepts `::ffff:10.0.0.0/104` and stores a 16-byte address and mask"
  - "a refusal the flag should have given comes later from the code that consumes the value, as a skipped verdict or an error record"
root_cause: >
  net.ParseCIDR keeps the parsed address's width, so a v4-mapped network holds
  a 16-byte address and mask while IPNet.String renders it as an IPv4 network.
  A check that re-parses the rendered string, or asks To4() != nil alone, sees
  the IPv4 form.
resolution_type: design_fix
applies_when:
  - "Deciding whether a `net.IP` or `*net.IPNet` is IPv4 or IPv6 in a flag parser, a validator, or a form rule"
  - "Writing a family check that calls `String()` on a parsed address and parses the result again, or that tests `To4() != nil` alone"
  - "Adding refusal rows for a typed address flag and choosing which mixed-family inputs to pin"
related_components: [protocol_client, conformance-gates]
tags: [net, ipv6, v4-mapped, cidr, validation, netpen, review-finding]
---

# A family check on a rendered address accepts a v4-mapped IPv6 network

## The situation

`cidrFlag.Set` in `src/edge/netpen/cmd/netpen/target.go` judged the family of
a network by rendering it and parsing the string again
(`parseIP(network.IP.String(), f.v6)`, the form commit `e295c485a` replaced).
`--route4 ::ffff:10.0.0.0/104` passed, and the runner, not the flag, refused
the 16-byte network later as a skipped verdict with a record. Requirement 8 of
the phase plan puts that refusal at the flag.

## What is true and why

`net.ParseCIDR` parses the address with `netip`, masks with the address's own
bit length, and stores the address as 16 bytes
(`$GOROOT/src/net/ip.go:560-571`, go1.27.2). `IP.Mask` trims a v4-mapped
address against a 4-byte mask, so an IPv4 network ends up 4 bytes wide while a
v4-mapped one keeps 16 bytes of address and mask. Rendering erases that:
`networkNumberAndMask` converts a `To4`-able address and cuts a 16-byte mask
to its last four bytes (`ip.go:460-481`), and `To4` is non-nil for the mapped
form (`ip.go:217-228`).

| Input                 | `String()`   | `len(IP)` | `len(Mask)` | `To4() != nil` |
| --------------------- | ------------ | --------- | ----------- | -------------- |
| `10.0.0.0/8`          | `10.0.0.0/8` | 4         | 4           | true           |
| `::ffff:10.0.0.0/104` | `10.0.0.0/8` | 16        | 16          | true           |
| `fd00::/64`           | `fd00::/64`  | 16        | 16          | false          |

(go1.27.2, 2026-10-15.) The two IPv4-looking rows are the same string and the
same `To4` answer, so neither test tells them apart. The width of the parsed
value does.

## How to apply it

Judge the family on the parsed value, address width and mask width together:

```go
// src/edge/netpen/cmd/netpen/target.go:150-155
func networkOfFamily(n *net.IPNet, v6 bool) bool {
	if v6 {
		return n.IP.To4() == nil && len(n.Mask) == net.IPv6len
	}
	return n.IP.To4() != nil && len(n.Mask) == net.IPv4len
}
```

The `check` package's form rules do the same for hosts
(`src/edge/netpen/check/target.go:289-293`) and networks (`:339`). Pin the
mapped form in the refusal rows of every typed flag, as
`src/edge/netpen/cmd/netpen/main_test.go:340-342` does, since an all-IPv4
table passes either implementation.

A `To4() != nil` test by itself answers "renders as IPv4", which is the
question only where the mapped form is welcome.

## Evidence

- `src/edge/netpen/cmd/netpen/target.go:145-155`: the rule and its comment.
- `src/edge/netpen/cmd/netpen/main_test.go:340-342`: the mapped-network rows
  for `--route4`, `--sweep-net`, and `--prefix6`.
- `src/edge/netpen/attacks/ip6/ip6_test.go:237`: the mapped host refused by
  the `check` form rule.

## What it does not cover

`netip.Addr`, which keeps the mapped form distinct through `Is4In6`. The
`check` package's own refusal of a mapped host, which this phase pinned
separately. Whether a given caller should accept the mapped form: this
solution is about telling the forms apart, not about which one a flag wants.
