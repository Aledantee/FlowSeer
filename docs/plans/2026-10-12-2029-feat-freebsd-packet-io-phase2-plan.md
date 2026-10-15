---
title: FreeBSD Packet I/O, Phase 2, Capture Module - Plan
type: feat
date: 2026-10-12
artifact_contract: flowseer-plan/v2
execution: code
amends: docs/architecture/2026-10-12-freebsd-packet-io-direction.md
---

# FreeBSD Packet I/O, Phase 2, Capture Module - Plan

## Goal

`rawsocket.OpenLocalInterface` and `rawsocket.OpenMirrorReceiver` work on
FreeBSD. The means are a local source over `bpfdev` and a mirror receiver
over raw and UDP sockets with FreeBSD's control messages, both behind the
receive loops the Linux sources already have.

Stop condition: the plan is wrong if the VM shows `bs_recv` excluding
frames a userspace filter later rejects, since the counter contract rests
on it.

## Decisions

The parent's decisions apply
([parent plan](2026-10-12-2029-feat-freebsd-packet-io-plan.md)) and the
[FreeBSD packet I/O record](../architecture/2026-10-12-freebsd-packet-io-direction.md),
"Per-path choices". This phase adds:

- The local source's receive loop moves out of `local_linux.go` into a file
  with no build constraint, over a reader seam that yields one frame with
  its wire length and capture time, or a retryable or terminal error. The
  Linux reader wraps `recvmsg` and `restoreVLAN`. The FreeBSD reader drains
  one `ReadBatch` at a time and copies each record. Why: the loop's
  cancellation, close, and panic paths are the subtle part
  (`receiveOnce`'s comment), and one copy tested on every host beats two.
- The mirror loop, `runMirrorLoop`, moves to a `linux || freebsd` file,
  since it uses `unix.Sockaddr`. Socket opening and control-message parsing
  stay per platform.
- FreeBSD control-message parsers have no build constraint and take raw
  bytes: `IP_RECVDSTADDR` (a 4-byte `in_addr`), `IP_RECVIF` (a
  `sockaddr_dl` with `sdl_index` at offset 2, native order), and
  `IPV6_PKTINFO` (address at 0, `ipi6_ifindex` at 16).
- The `*_other.go` stubs become `!linux && !(freebsd && (amd64 || arm64))`,
  matching the FreeBSD files' constraint, so FreeBSD on another
  architecture keeps building through the stub.
  `ErrUnsupportedPlatform`'s hint names Linux and FreeBSD. Each stub's
  constraint changes in the unit that adds the FreeBSD file it yields to,
  since the crossbuild gate vets this package for FreeBSD on every run.
- The mirror loop takes, per socket, a function that reads the destination
  address and receive interface index from the ancillary data, and the
  interface index `bind_interface` resolves to (0 for none). A datagram from
  another interface is dropped before `received` counts it. Why: FreeBSD has
  no `SO_BINDTODEVICE` (x/sys v0.48.0 `zerrors_freebsd_*.go` defines none),
  and requirement 3 needs the drop before counting. On Linux the wanted
  index stays 0, since `SO_BINDTODEVICE` already filters in the kernel.
  A parser that finds no index while the wanted index is nonzero means a
  drop. Each socket also carries its ancillary buffer size, and the loop
  treats `MSG_CTRUNC` as terminal, as the local loop does. Why: the raw
  IPv4 socket on FreeBSD gets two messages, `IP_RECVDSTADDR` and
  `IP_RECVIF`, which need `CmsgSpace(4) + CmsgSpace(SizeofSockaddrDatalink)`
  (x/sys v0.48.0 `ztypes_freebsd_arm64.go`: `SizeofSockaddrDatalink = 0x36`,
  `SizeofCmsghdr = 0xc`), more than today's `CmsgSpace(inet6PktinfoLen)`.
  A truncated buffer would read as no index and drop every datagram.
- The FreeBSD local source opens its `bpfdev` device with
  `Direction: DirectionInOut`, `Immediate: true`, and `ReadTimeout` at
  `pollTimeout`. Why: an `AF_PACKET` socket bound with `ETH_P_ALL` sees
  outgoing frames too (packet(7), `PACKET_OUTGOING`), and a zero `Config`
  would give `BPF_D_IN` (the FreeBSD packet I/O record, "One BPF device
  package serves every FreeBSD packet path"). `Immediate` delivers each
  frame without waiting for the buffer to fill, as `recvmsg` does.
- The FreeBSD reader drops the unread records of a batch when the source
  closes. Why: a closed Linux socket's queue is lost the same way, and the
  capture engine stops reading at `Close`.
- The VM suite runs inside a VNET jail created with
  `jail -c name=<jail> vnet persist path=/ allow.raw_sockets`, its `lo0`
  given `127.0.0.1/8` and brought up. The host side creates `vxlan(4)` and
  `gre(4)` interfaces whose remote end is the jail's `epair0b` address,
  and keeps them sending with a root `ping -i 0.5` for the suite's
  duration. Why, from probes in the bento 14.3 guest that U6 records with
  their commands in `test/freebsd/README.md`: `ifconfig vxlan create`
  fails with `EINVAL` inside a VNET jail and works on the host; a receiver
  in the jail sees the encapsulated traffic because no tunnel interface
  there claims it; `path=/` keeps the host's `/dev/bpf` visible in the
  jail; `allow.raw_sockets` lets the jail open raw sockets; and the host's
  VXLAN traffic reached the jail in each of 40 consecutive seconds, so
  ARP's hold-down does not starve the test.
- GRE is checked two ways. `gre(4)` sends protocol 0x0800 (an IPv4 payload,
  the probe above), which `mirror.Decode` counts and returns without an
  inner frame (`src/modules/capture/mirror/decode.go`, the `default` arm's
  `protocolType != ethPTypeTEB`). The test also sends one transparent
  Ethernet bridging GRE packet (protocol 0x6558) from a second jail address,
  `10.99.0.3` on `epair0b`, to `10.99.0.2`, so the source and destination
  differ and the destination assertion cannot pass through the loop's
  fallback to the source address.

## Requirements

1. Parent requirement 2, in the VM.
2. Parent requirement 3, in the VM. Example: VXLAN from the host's
   `vxlan(4)` arrives in the jail as a `Frame` whose `Envelope` is the VXLAN
   arm and whose inner frame is the ARP request the host's ping triggers.
   GRE from the host's `gre(4)` raises `Stats` received and yields no
   `Frame`. The VXLAN `Frame`'s destination is `10.99.0.2`, not the sender
   `10.99.0.1`. A transparent Ethernet bridging GRE packet sent from
   `10.99.0.3` to `10.99.0.2` arrives as a `Frame` whose `Envelope` is the
   GRE arm, with destination `10.99.0.2`.
3. `bind_interface` drops traffic from another interface before counting
   it, and keeps traffic from the bound one. Example: with
   `bind_interface` `epair1b`, VXLAN arriving on `epair0b` yields no frame
   and leaves `Stats` received at 0. A GRE-only receiver with
   `bind_interface` `epair0b` reports `Stats` received above 0 from the
   host's `gre0`, which proves `IP_RECVIF` is read.
4. `Stats` reports deltas and counts frames the userspace filter rejects.
   Example: with a filter that accepts only VLAN 1000, three frames sent
   between two calls, one tagged 1000 and two untagged, report received 3
   and then 0, while one `Frame` is delivered.
5. A non-Ethernet interface is refused. Example: `OpenLocalInterface("lo0",
   false, nil)` returns an error with code `capture/source-open`.
6. The Linux behaviour is unchanged. Example: every test in
   `local_linux_test.go`, `mirror_linux_test.go`, and `mirror_udp_test.go`
   passes, the moved ones included.

## Out of scope

- Any change to the capture engine, the filter compiler, the pcapng writer,
  or `mirror.Decode`.
- Input trust: the mirror receiver reads datagrams from any network sender,
  which is untrusted, and `mirror.Decode` already handles them. The new
  control-message parsers read kernel-written ancillary data only, which is
  trusted. Their length checks guard against a layout mismatch, not a
  hostile author.

## Units

### U1. Local receive loop behind a frame reader

Files: src/modules/capture/rawsocket/local.go,
src/modules/capture/rawsocket/local_test.go,
src/modules/capture/rawsocket/local_linux.go,
src/modules/capture/rawsocket/retry_unix.go
After: none
Change: `local.go`, which has no build constraint, holds a `localSource`
with the receive loop, `Stats` delegation, idempotent `Close`, and the
filter VM, over a `frameReader` interface: `readFrame(buf []byte)` returns
the frame bytes, the wire length, the capture time, whether a failure is
retryable, and the error, plus `stats()` and `close()`. The reader
classifies retryability, so `local.go` imports nothing from
`golang.org/x/sys/unix`, which does not build on Windows
(`src/common/service/storelock_windows.go` shows the repository builds
there). The loop keeps
`recvLoop`'s cancellation, close, and panic paths and their comments.
`local_linux.go` keeps `openLocalInterface` and a Linux reader that wraps
`recvmsg`, the `MSG_CTRUNC` check, and `restoreVLAN`, and stamps
`time.Now()`. `pollTimeout`, `maxFrameLen`, and `sendTerminal` move to
`local.go`. `isRetryable` moves to `retry_unix.go` (`linux || freebsd`), since the
Linux reader and the mirror loop both use it.
Tests: `local_test.go` (no build constraint), with a fake reader:
`TestLocalSource_DeliversFrame`, `TestLocalSource_RetryableErrorIsRetried`,
`TestLocalSource_ReadErrorIsTerminal`,
`TestLocalSource_CancellationUnblocksWithinOnePoll`,
`TestLocalSource_FilterRejects`, `TestLocalSource_CloseIsIdempotent`.
`local_linux_test.go` passes unchanged (requirement 6), so U1 keeps
`packetSocket`, `newLinuxLocalSource(sock)`, a `vm` field the tests set,
and `Stats` passing the socket's `(packets, drops)` straight through.
Verify: `uv run tools/scripts/run.py verify change -- src/modules/capture/rawsocket`

### U2. Mirror loop shared by Linux and FreeBSD

Files: src/modules/capture/rawsocket/mirror_unix.go,
src/modules/capture/rawsocket/mirror_unix_test.go,
src/modules/capture/rawsocket/mirror_linux.go,
src/modules/capture/rawsocket/mirror_linux_test.go,
src/modules/capture/rawsocket/mirror_udp_test.go
After: U1
Change: `mirror_unix.go` (`//go:build linux || freebsd`) holds
`mirrorSocket` (its `recvmsg` now also returns the flags),
`fdMirrorSocket`, `setRecvTimeout`, `splitEncapsulations`, the mirror
source type, `Receive`, `Stats`, `Close`, `receiveOnce`, `runMirrorLoop`,
`decodeV4`, and `sockaddrIP`. Each socket is paired with a
`controlParser` (destination address and receive interface index from
`oob`, with an ok flag) and its ancillary buffer size. The loop treats
`MSG_CTRUNC` as terminal, and drops before counting a datagram whose
index differs from a nonzero wanted index or whose parser found no index.
`mirror_linux.go` keeps `openMirrorReceiver`, the Linux socket openers,
and the Linux parser built on `pktinfoDestination`, with buffer size
`CmsgSpace(inet6PktinfoLen)` and a wanted index of 0.
Tests: `mirror_unix_test.go` (`linux || freebsd`) takes
`TestSplitEncapsulations` from `mirror_linux_test.go` and adds
`TestMirrorSource_DropsOtherInterfaceBeforeCounting` (a fake parser
reports index 7 with wanted index 3: no frame, `Stats` received 0),
`TestMirrorSource_DeliversBoundInterface` (index 3, wanted 3: one frame),
`TestMirrorSource_MissingIndexIsDroppedWhenBound`, and
`TestMirrorSource_ControlTruncationIsTerminal`. `mirror_linux_test.go`
and `mirror_udp_test.go` move to the new seam where their fakes and
`runMirrorLoop` calls change (requirement 6).
Verify: `uv run tools/scripts/run.py verify change -- src/modules/capture/rawsocket`

### U3. FreeBSD control-message parsers

Files: src/modules/capture/rawsocket/cmsg_freebsd_parse.go,
src/modules/capture/rawsocket/cmsg_freebsd_parse_test.go,
src/modules/capture/rawsocket/cmsg_layout_freebsd.go
After: none
Change: `cmsg_freebsd_parse.go` has no build constraint and parses raw
bytes: `parseRecvDstAddr` (a 4-byte `in_addr`), `parseRecvIf` (a
`sockaddr_dl`, `sdl_index` a native-order `uint16` at offset 2), and
`parseIn6Pktinfo` (address at 0, `ipi6_ifindex` a native-order `uint32`
at 16). Each refuses a short buffer. `cmsg_layout_freebsd.go`
(`//go:build freebsd && (amd64 || arm64)`) asserts at compile time that
offset 2 and 16 and the sizes match `unix.RawSockaddrDatalink.Index` and
`unix.Inet6Pktinfo.Ifindex` (x/sys v0.48.0 `ztypes_freebsd_arm64.go`
lines 174 to 183 and 248 to 251), the same idiom as
`src/common/net/bpfdev/layout_freebsd.go`.
Tests: `cmsg_freebsd_parse_test.go`, table tests over byte slices built
little-endian (both supported architectures are, per
`src/common/net/bpfdev/layout_freebsd.go`) and over short buffers. The
compile-time check is the second source for the offsets, and U5's VM test
reads real kernel ancillary data.
Verify: `uv run tools/scripts/run.py verify change -- src/modules/capture/rawsocket`

### U4. FreeBSD local source over bpfdev

Files: src/modules/capture/rawsocket/local_freebsd.go,
src/modules/capture/rawsocket/local_other.go,
src/modules/capture/rawsocket/rawsocket.go,
src/modules/capture/rawsocket/local_vm_test.go,
src/modules/capture/rawsocket/stats_delta.go,
src/modules/capture/rawsocket/stats_delta_test.go,
src/modules/capture/rawsocket/doc.go,
docs/architecture/2026-10-12-freebsd-packet-io-direction.md
After: U1, U6
Replaces: `run on Linux, where the capture engine uses AF_PACKET and raw IP sockets`
Change: `local_freebsd.go` (`//go:build freebsd && (amd64 || arm64)`)
opens `bpfdev.Open` with the Decisions' `Config`, refuses a link type other
than `DLT_EN10MB` with `capture/source-open`, and implements the reader:
drain one `ReadBatch`, copy each record, report `WireLength` and the
record's `Timestamp`, drop the batch on close. An empty `ReadBatch` result
(`nil, nil`, the read timeout per `src/common/net/bpfdev/device_freebsd.go`)
is retryable and yields no frame. `stats_delta.go` (no build
constraint) turns two cumulative `unix.BpfStat`-shaped counter pairs into
`received = Δrecv - Δdrop` and `dropped = Δdrop` with modular `uint32`
arithmetic. `local_other.go` takes the stub constraint the Decisions give.
`ErrUnsupportedPlatform`'s hint and `Msg`, `OpenLocalInterface`'s doc, and
the package comment in `doc.go` ("Linux-only byte layer") name FreeBSD. The FreeBSD packet I/O record's local-capture bullets gain the
`BPF_D_INOUT` and `Immediate` choice.
Tests: `stats_delta_test.go`: three frames between calls report 3 then 0
(requirement 4), and a wrap past `2^32` reports the true delta.
`local_vm_test.go` (`//go:build freebsd && rawsocket_vm`), on the jail's
`epair1a`/`epair1b`: `TestLocalSourceDeliversTaggedFrame` (requirement 1,
parent requirement 2: `81 00 03 e8` at offset 12 and a VLAN 1000 filter
accepts it), `TestLocalSourceRefusesNonEthernet` (requirement 5, `lo0`),
`TestLocalSourceStatsDeltas` (requirement 4, opened with the VLAN 1000
filter so received counts the frames it rejects, which tests the Stop
condition).
Verify: `uv run tools/scripts/run.py verify change -- src/modules/capture/rawsocket docs/architecture/2026-10-12-freebsd-packet-io-direction.md`

### U5. FreeBSD mirror receiver

Files: src/modules/capture/rawsocket/mirror_freebsd.go,
src/modules/capture/rawsocket/mirror_other.go,
src/modules/capture/rawsocket/mirror_vm_test.go,
src/modules/capture/rawsocket/rawsocket.go
After: U2, U3, U4, U6
Change: `mirror_freebsd.go` (`//go:build freebsd && (amd64 || arm64)`)
opens the raw IPv4 GRE socket with `IP_RECVDSTADDR` and `IP_RECVIF`, and
the raw IPv6 GRE socket and the dual-stack UDP socket (`IPV6_V6ONLY` 0)
with `IPV6_RECVPKTINFO`, each with `SO_RCVTIMEO` at `pollTimeout`. Its
control parsers use U3's functions at level `IPPROTO_IP` or
`IPPROTO_IPV6`. The raw IPv4 socket's ancillary buffer is
`CmsgSpace(4) + CmsgSpace(SizeofSockaddrDatalink)`, the others
`CmsgSpace(SizeofInet6Pktinfo)`. `bind_interface` resolves through
`net.InterfaceByName` to the wanted index, and an unknown name fails with
`capture/source-open`. `mirror_other.go` takes the stub constraint the
Decisions give, and `OpenMirrorReceiver`'s doc in `rawsocket.go` names
FreeBSD and says how it honours `bindInterface` there.
Tests: `mirror_vm_test.go` (`//go:build freebsd && rawsocket_vm`), in the
jail: `TestMirrorReceiverDecodesHostVXLAN` (destination `10.99.0.2`),
`TestMirrorReceiverCountsHostGRE`, `TestMirrorReceiverDecodesTEBGRE` (sent
from `10.99.0.3`), all requirement 2, and
`TestMirrorReceiverBindInterfaceDropsOtherInterface` and
`TestMirrorReceiverBindInterfaceKeepsBoundInterface` (requirement 3).
Verify: `uv run tools/scripts/run.py verify change -- src/modules/capture/rawsocket`

### U6. rawsocket VM suite

Files: test/freebsd/rawsocket-setup.sh, test/freebsd/README.md,
tools/scripts/freebsd/vmtest.py,
tools/scripts/tests/freebsd/test_vmtest.py,
tools/scripts/tests/freebsd/test_rawsocket_setup.py,
test/conformance/crossbuild/crossbuild_test.go
After: none
Change: `rawsocket-setup.sh up` first runs `down`'s cleanup for any
leftovers, then loads `if_vxlan` and `if_gre`, creates the jail with the
Decisions' parameters, moves `epair0b`, `epair1a`, and `epair1b` into it
and brings each up with `inet6 ifdisabled`, gives `epair0a` `10.99.0.1/24`
and `epair0b` `10.99.0.2/24` and `10.99.0.3/32`, creates the host's
`vxlan0` (VNI 42) and `gre0` toward `10.99.0.2`, and starts a root
`ping -i 0.5` through each that runs until `down`, keeping its PID file
as `bpfdev-setup.sh` keeps tcpdump's. `down` stops the pings, removes the
jail (which returns its interfaces to the host), then destroys `vxlan0`,
`gre0`, and both epairs on the host, and fails if a step fails, after
attempting all. The README records the probe commands and results the
Decisions cite.
`vmtest.py`'s `Suite` gains an optional jail name, and a suite with one runs
its binary through `jexec <jail>`. The `rawsocket` suite registers the
package, tag `rawsocket_vm`, the script, the jail, and the interface
names. `vmTags` gains `rawsocket_vm`.
Tests: `test_vmtest.py`: a jailed suite's remote command starts with
`sudo jexec <jail> env`, and
`test_first_failure_wins_and_later_suites_still_run` covers the third
suite. `test_rawsocket_setup.py` uses the shim approach
of `test_bpfdev_setup.py`: `down` after a failed step still removes the
jail and interfaces and exits non-zero. The live run is U4 and U5's VM
tests.
Verify: `uv run tools/scripts/run.py verify change -- test/freebsd tools/scripts/freebsd tools/scripts/tests/freebsd test/conformance/crossbuild`

Waves: U1 U3 U6 | U2 U4 | U5

## Verification

- `go test -race ./src/modules/capture/...` on macOS and Linux.
- `go test ./test/conformance/crossbuild`.
- `uv run tools/scripts/run.py freebsd vmtest --suite all` with UTM.app
  open.
- The verifier on every changed path.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `test/freebsd/README.md` and the FreeBSD packet I/O record updated
      in the same change.
- [ ] The VM suites pass, and their output is quoted in the phase's last
      commit message.
- [ ] Outcome recorded with `uv run tools/scripts/run.py plan record
      implemented docs/plans/2026-10-12-2029-feat-freebsd-packet-io-phase2-plan.md
      --units 6 --from <t> --to <t>`.
- [ ] No plan labels in code.

## Open questions

- Whether a raw IPv4 socket on FreeBSD 14.3 delivers `IP_RECVIF` for a
  datagram that arrives through `lo0` (the self-sent TEB GRE case).
  Unverified. U5's test reads the index it gets and asserts only the
  destination for that case.
