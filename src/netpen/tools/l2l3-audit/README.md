# l2l3-audit — dev-only fixture factory

This is the original Python L2/L3 security audit tool, retired as the
production audit engine and quarantined here as a **dev-only fixture
factory** per KTD14/U13.

## Why it's here

netpen's Go behaviors are pinned against characterization fixtures
harvested from this tool (KTD14: characterization-first port). The
Python tool stays as the fixture factory during and after the port:
per-attack reference frames are captured to pcap, and each Go behavior
is pinned against them — byte-for-byte where the baseline is
deterministic, field-set where it randomizes.

**KTD14 pins remain the behavioral authority.** This tool generates the
fixtures; it is not shipped, not installed, and not a runtime dependency
of the netpen binary.

## Usage (dev only)

```sh
cd src/netpen/tools/l2l3-audit
sudo ./l2l3-audit <subcommand>   # needs root for raw sockets
```

Requires Python 3.10+ and `uv` (the shebootstraps scapy via `uv run`).
Lab use only — never run against a production network.
