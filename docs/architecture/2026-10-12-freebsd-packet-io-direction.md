---
title: FreeBSD Packet I/O - Direction
type: direction
date: 2026-10-12
topic: freebsd-packet-io
status: accepted-direction
amends: docs/architecture/2026-10-03-edge-high-availability-direction.md
---

# FreeBSD Packet I/O - Direction

How FlowSeer captures, sends, and receives raw packets on FreeBSD, so that
an edge on FreeBSD has the packet paths a Linux edge has: local interface
capture and the mirror receiver in `src/modules/capture/rawsocket`, the
netpen leg in `src/edge/netpen/link`, and the simload sender in
`src/edge/simload/packetio`.

## Context

The [edge high availability record](2026-10-03-edge-high-availability-direction.md)
makes FreeBSD a deployment target for the agent and leaves "What capture
does on FreeBSD" open. Every packet path in the tree is Linux-only today.
Each has a `*_other.go` stub under `//go:build !linux` that returns
`ErrUnsupportedPlatform`.

The Linux paths use `AF_PACKET` (`local_linux.go`, `link_linux.go`,
`sender_linux.go`) and, for the mirror receiver, raw IP and UDP sockets with
`IP_PKTINFO` and `SO_BINDTODEVICE` (`mirror_linux.go`). None of those exist
on FreeBSD under that name. `golang.org/x/sys` v0.48.0 defines the FreeBSD
`BIOC*` ioctls, `IP_RECVDSTADDR`, `IP_RECVIF`, and `IPV6_RECVPKTINFO`, and
defines neither `IP_PKTINFO` nor `SO_BINDTODEVICE` for FreeBSD
(`unix/zerrors_freebsd_amd64.go`).

## Decision

### One BPF device package serves every FreeBSD packet path

A new package, `src/common/net/bpfdev`, opens `/dev/bpf`, attaches it to an
interface, sets its options, reads and splits its buffer, writes frames, and
reads its counters. The local capture source, the netpen leg, and the
simload sender all use it. It has no domain types and three unrelated
importers, which is the test `src/common/README.md` sets for a common
package.

The buffer parser has no build constraint, so its tests run on every host.
The device builds for FreeBSD on amd64 and arm64 only: the parser reads the
64-bit `bpf_hdr` little-endian, a compile-time check pins its offsets to
`unix.BpfHdr`, and powerpc64 is big-endian. A zero `Config` applies
`BPF_D_IN`, the value 0 of `enum bpf_direction`, not the kernel's
`BPF_D_INOUT` default, so a path that needs both directions sets
`DirectionInOut`.

### What the kernel guarantees, and what each path relies on

| Fact | Source | Used by |
| --- | --- | --- |
| `BIOCSBLEN` must come before `BIOCSETIF`, and a read buffer must be exactly the returned size | bpf(4), FreeBSD 15.1 | bpfdev |
| `BIOCSBLEN` clamps the request to `BPF_MINBUFSIZE` and `net.bpf.maxbufsize` and writes the result back, without rounding | `sys/net/bpf_buffer.c` `bpf_buffer_ioctl_sblen`, `sys/net/bpf.c` `bpfread` | bpfdev |
| `BIOCSRTIMEOUT` stores `floor(t * hz)` ticks, and 0 means no timeout, so bpfdev raises a read timeout under one tick to one tick, rounded up | `sys/net/bpf.c` `BIOCSRTIMEOUT`, `sys/kern/kern_clock.c` `tvtohz` | bpfdev, every reader's poll loop |
| A read timeout with nothing buffered returns 0 bytes, no error | `sys/net/bpf.c`, `bpfread` | every reader's poll loop |
| `bs_recv` counts every packet that reached the filter. Both counters are cumulative since open, `BIOCFLUSH`, `BIOCSETIF`, or `BIOCSETF`, and are `u_int` | bpf(4), `sys/net/bpf.c` `BIOCGSTATS` | local source `Stats` |
| A hardware-stripped VLAN tag is put back before BPF sees the frame | `sys/net/if_ethersubr.c` `ether_bpf_mtap_if` calls `ether_vlan_mtap` when `M_VLANTAG` is set | local source, netpen filters |
| `BPF_MAXINSNS` is 512, against 4096 on Linux | `sys/net/bpf.h` | netpen `SetFilter` |
| `BIOCSHDRCMPLT` 1 writes the source MAC as given | bpf(4) | netpen, simload |
| `BIOCSDIRECTION` accepts `BPF_D_IN`, `BPF_D_INOUT`, `BPF_D_OUT`, default `BPF_D_INOUT` | bpf(4), `sys/net/bpf.c` | local source, netpen |
| A raw IPv4 socket gets the IPv4 header, a raw IPv6 socket does not | `sys/netinet/raw_ip.c` `rip_append`, `sys/netinet6/raw_ip6.c` `m_adj(n, *offp)` | mirror receiver |
| GRE that no `gre(4)` tunnel claims falls through to raw sockets | `sys/netinet/ip_encap.c` `encap4_input`, `encap6_input` | mirror receiver |
| A raw IPv4 socket reports the destination with `IP_RECVDSTADDR` and the interface with `IP_RECVIF`. FreeBSD has no `IP_PKTINFO` | `sys/netinet/ip_input.c` `ip_savecontrol` | mirror receiver |
| A dual-stack UDP socket gets `IPV6_PKTINFO` for IPv4 datagrams too, with a v4-mapped destination and the interface index | `sys/netinet/udp_usrreq.c` `udp_append`, `sys/netinet6/ip6_input.c` `ip6_savecontrol_v4` | mirror receiver |

The source files are read from `freebsd-src` `main` and the man page from
FreeBSD 15.1, and the two `BIOCSBLEN` and `BIOCSRTIMEOUT` rows from
`releng/14.3`. The minimum release FlowSeer supports is the one the test VM
runs, FreeBSD 14 (see below). The bpfdev VM suite passes on 14.3, which
covers the `BIOCSBLEN`, read-timeout, `BIOCGSTATS`, `BIOCSHDRCMPLT`, and
`BIOCSDIRECTION` rows. The rows the later paths use stay unverified on 14
until their suites pass there.

### Per-path choices

- **Local capture** runs the capture filter in userspace through
  `golang.org/x/net/bpf`'s VM, as the Linux source does, and installs no
  kernel read filter. One filter path keeps the counters' meaning the same
  on both platforms. It also leaves programs between 513 and 4096
  instructions working, which `filter.Compile` allows (`maxInstructions` in
  `src/modules/capture/filter/compile.go`). The cost is that every frame is
  copied out of the kernel.
- **Local capture** opens its device with `BPF_D_INOUT` and `Immediate`.
  A zero `Config` would apply `BPF_D_IN`, which would hide the frames the
  host itself sends from a capture of a local interface. `Immediate` makes a
  read return as soon as a frame arrives, instead of when the buffer fills
  or the read timeout expires, so capture latency does not depend on the
  traffic rate.
- **Local capture** computes each counter's delta with modular `uint32`
  arithmetic. It reports at most the receive delta as dropped and reports
  the remainder as received. Excess drops carry into the next sample by
  advancing the drop baseline only by the reported drops
  (`src/modules/capture/rawsocket/stats_delta.go`). FreeBSD 14.3 reads
  `bs_recv` before `bs_drop` without a lock, so a concurrent packet can
  appear in the drop count before the receive count
  ([`sys/net/bpf.c`, lines 1709–1710](https://github.com/freebsd/freebsd-src/blob/releng/14.3/sys/net/bpf.c#L1709-L1710)).
- **Local capture** takes `CapturedAt` from the BPF header's timestamp,
  which is the kernel's receive time.
- **Local capture** refuses an interface whose `BIOCGDLT` is not
  `DLT_EN10MB`, because the engine writes `LINK_TYPE_ETHERNET` for every
  frame (`src/modules/capture/engine.go`). `lo0` is `DLT_NULL` and is
  refused, although Linux captures `lo`.
- **The mirror receiver** uses `IP_RECVDSTADDR` and `IP_RECVIF` on the raw
  IPv4 socket and `IPV6_RECVPKTINFO` on the raw IPv6 and dual-stack UDP
  sockets. It honours `bind_interface` by dropping, before counting, every
  datagram whose receive interface index differs, since FreeBSD has no
  `SO_BINDTODEVICE`.
- **The netpen leg** opens its device with `BPF_D_IN`. A Linux `AF_PACKET`
  socket never receives the frames it sent itself. bpf(4) does not say
  whether the writing descriptor sees its own frames under `BPF_D_INOUT`, and
  `sys/net/bpf.c` has no check that skips the writer. `BPF_D_IN` avoids the
  question. The leg then also misses frames other host processes send,
  which netpen's attacks do not read.
- **The netpen leg** installs its filter in the kernel with `BIOCSETF` and
  refuses a program over 512 instructions. On FreeBSD that filter sees
  the VLAN tag that Linux's kernel filter misses.
- **The simload sender** opens its device with a read filter that rejects
  every packet, so the kernel buffers nothing for a descriptor that never
  reads.

### Tests run on a FreeBSD VM through Vagrant

Real-kernel tests are opt-in, build-tagged, and run on a FreeBSD VM that
Vagrant boots from the `bento/freebsd-14` box, which publishes arm64 builds
for VirtualBox, UTM, Parallels, and VMware. Test binaries are cross-compiled
on the host and uploaded, so the guest needs no Go toolchain. `epair(4)`
supplies the interface pairs that `veth` supplies on Linux. A conformance
gate cross-compiles the FreeBSD packages and their tests for amd64 and
arm64 on every run.

A path's VM suite registers in the `SUITES` table of
`tools/scripts/freebsd/vmtest.py`, with a build tag ending in `_vm` and a
setup script under `test/freebsd/`. The tag also goes into the gate's
`vmTags` (`test/conformance/crossbuild`), which fails when a `_vm` tag in a
vetted directory is missing from it. Setup scripts stay POSIX `sh`, since
the guest holds no uv, and the repository scripting record exempts
`test/freebsd/`.

## Alternatives

- **`github.com/gopacket/gopacket/bsdbpf` (v1.7.3).** Already in netpen's
  module graph. Rejected: `pickBpfDevice` panics when no device opens, a
  nil `Options` makes `NewBPFSniffer` write into the shared
  `defaultOptions`, `CaptureInfo.Length` reports the captured length rather
  than the wire length, and it has no write, filter, or counter calls.
- **A kernel read filter for local capture when the program fits.** Cheaper
  per packet. Rejected for now: two filter paths to test, and counters that
  mean different things depending on program length.
- **Two UDP sockets on FreeBSD, one per family.** Rejected:
  `ip6_savecontrol_v4` already gives a dual-stack socket the destination
  and interface of an IPv4 datagram, so the Linux shape carries over.
- **Cross-compile only, no VM.** Rejected: nothing would check the buffer
  layout or the write path against a real kernel.

## Consequences

- `rawsocket`, `link`, and `packetio` keep their `*_other.go` stubs for
  platforms other than Linux and FreeBSD. Their hints name both platforms.
- On FreeBSD these paths need root, since there is no `CAP_NET_RAW`.
- On a FreeBSD host, a `gre(4)` tunnel whose endpoints match a mirror
  sender takes that sender's GRE before the mirror receiver sees it.
- FreeBSD 32-bit architectures are out of scope. The buffer parser knows
  the 64-bit `bpf_hdr` layout only.
- Running the VM suite needs Vagrant and one of the box's providers on the
  host.

## Sources

- bpf(4), FreeBSD 15.1: https://man.freebsd.org/cgi/man.cgi?query=bpf&sektion=4
- `sys/net/bpf.c`, `sys/net/bpf.h`, `sys/net/ethernet.h`,
  `sys/net/if_ethersubr.c`, `sys/netinet/ip_encap.c`,
  `sys/netinet/raw_ip.c`, `sys/netinet/ip_input.c`,
  `sys/netinet/udp_usrreq.c`, `sys/netinet6/raw_ip6.c`,
  `sys/netinet6/ip6_input.c`: https://github.com/freebsd/freebsd-src
- `~/go/pkg/mod/golang.org/x/sys@v0.48.0/unix/zerrors_freebsd_amd64.go` and
  `ztypes_freebsd_amd64.go`
- `~/go/pkg/mod/github.com/gopacket/gopacket@v1.7.3/bsdbpf/bsd_bpf_sniffer.go`
- Vagrant box catalogue: https://app.vagrantup.com/api/v2/search?q=freebsd-14
