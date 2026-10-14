---
title: FreeBSD Packet I/O, Phase 1, BPF Device, Agent Build, Gate, and VM - Plan
type: feat
date: 2026-10-12
artifact_contract: flowseer-plan/v2
execution: mixed
amends: docs/architecture/2026-10-03-edge-high-availability-direction.md
---

# FreeBSD Packet I/O, Phase 1, BPF Device, Agent Build, Gate, and VM - Plan

## Goal

`src/common/net/bpfdev` opens, reads, writes, and counts a FreeBSD BPF
device, and a FreeBSD VM proves it against a real kernel. The agent builds
for FreeBSD, and a conformance gate keeps every FreeBSD file compiling. The
means are a pure-Go buffer parser with a fixture captured on FreeBSD, a
FreeBSD-only device file over `golang.org/x/sys/unix` ioctls, a Vagrantfile
with a `uv run` command that cross-compiles and runs tagged tests in the
guest, and a FreeBSD build of the flock store lock.

Stop condition: the plan is wrong if the guest's `bpf_hdr` layout differs
from `unix.BpfHdr` in `ztypes_freebsd_arm64.go`, since the parser and the
compile-time assertion both rest on it.

## Decisions

The parent's decisions apply
([parent plan](2026-10-12-2029-feat-freebsd-packet-io-plan.md)) and the
[FreeBSD packet I/O record](../architecture/2026-10-12-freebsd-packet-io-direction.md).
This phase adds:

- The store lock builds for FreeBSD by adding `freebsd` to the constraint of
  `storelock_posix.go`. Why: it uses `unix.Flock` with `LOCK_EX|LOCK_NB`,
  which x/sys defines for FreeBSD, and with that one change
  `GOOS=freebsd go build ./src/edge/agent/cmd/agent` succeeds for amd64 and
  arm64 (checked with `go build -overlay`).
- bpfdev's API is `Open(iface string, cfg Config)`, and a `*Device` with
  `ReadBatch`, `Write`, `Stats`, `SetReadFilter`, `LinkType`, and `Close`.
  `ReadBatch` returns `[]Record` that alias the device's buffer until the
  next call. Why: phase 2's local source copies each frame anyway, and
  phase 3's netpen leg copies in its `ReadPacketData` adapter, so an owned
  copy here would copy twice.
- `Config` fields, all optional: `BufferLen` (requested, default 524288,
  the `net.bpf.maxbufsize` default per bpf(4)), `ReadTimeout` (default
  100ms, the `pollTimeout` of `local_linux.go`, negative refused),
  `Immediate`, `Promiscuous`, `HeaderComplete`, `Direction`, and
  `ReadFilter` (`[]bpf.RawInstruction`, nil accepts all). A
  `ReadTimeout` under one kernel clock tick is raised to one tick (read
  from `kern.hz`), since the kernel truncates the timeout to whole ticks
  and a result of zero means no timeout, and `Close` would then wait on a
  read that never returns. `Close` waits up to one `ReadTimeout`.
- `Open` runs the ioctls in this order: `BIOCSBLEN`, `BIOCSETIF`,
  `BIOCGDLT`, `BIOCIMMEDIATE`, `BIOCSRTIMEOUT`, `BIOCSHDRCMPLT`,
  `BIOCSDIRECTION`, `BIOCPROMISC`, `BIOCSETF`. Why: bpf(4) requires
  `BIOCSBLEN` before `BIOCSETIF` and `BIOCGDLT` after it.
  `BIOCPROMISC` needs an attached interface too, the order gopacket uses
  (`bsd_bpf_sniffer.go` lines 119 and 153). `BIOCIMMEDIATE` comes before
  `BIOCSRTIMEOUT`, as that file's comment at line 132 requires.
- `BIOCSBLEN` goes through the raw ioctl path and reads the length back
  from its argument. Why: it is `_IOWR` (`0xc0044266`), the kernel writes
  the clamped size back, and bpf(4) says a read with any other size fails
  with `EINVAL`. `IoctlSetPointerInt` (`ioctl_unsigned.go:24`) discards
  that write-back.
- `Direction` is bpfdev's own typed constant set, `DirectionIn` 0,
  `DirectionInOut` 1, `DirectionOut` 2. Why: x/sys v0.48.0 defines no
  `BPF_D_*` for FreeBSD, and `enum bpf_direction` in `sys/net/bpf.h` lists
  `BPF_D_IN`, `BPF_D_INOUT`, `BPF_D_OUT` in that order without explicit
  values.
- The deprecated standard-library helpers in `syscall/bpf_bsd.go`
  (`SetBpfInterface` and the rest) are not used. Why: staticcheck reports
  them as SA1019, and they cover neither `BIOCSDIRECTION` nor the
  `BIOCSBLEN` write-back.
- `Open` opens `/dev/bpf`, the device bpf(4) names, and makes no
  `/dev/bpfN` scan. Why: FreeBSD's `/dev/bpf` clones a fresh descriptor on
  each open (bpf(4) names only `/dev/bpf`), and the scan is what makes
  gopacket's `pickBpfDevice` panic.
- `Record` carries `Data`, `CapturedLength`, `WireLength` (`bh_datalen`),
  and `Timestamp` (`bh_tstamp`, microsecond). A read of 0 bytes returns an
  empty batch and `nil`, the timeout case `bpfread` returns.
- The parser takes the header layout as a value (`hdrLayout64`: seconds at
  0, microseconds at 8, caplen at 16, datalen at 20, hdrlen at 24, word
  size 8) and has no build constraint, so its tests run on every host. A
  FreeBSD-only file asserts at compile time that `hdrLayout64` matches
  `unsafe.Offsetof` on `unix.BpfHdr` and that `unsafe.Sizeof(uintptr(0))`
  is 8. Why: the gate then fails on a layout mismatch without a VM.
- The parser rejects a record whose `bh_hdrlen` is under 26, whose caplen
  runs past the read length, or whose aligned length is zero, with a coded
  error and no panic. The buffer is the kernel's, so the check is against
  a parser bug or a layout mismatch rather than a hostile author.
- Ioctls that pass a struct or need a write-back (`BIOCSBLEN`,
  `BIOCSETIF`, `BIOCSETF`, `BIOCSRTIMEOUT`, `BIOCGSTATS`) go through `unix.Syscall(unix.SYS_IOCTL, ...)` with
  `unsafe.Pointer`. Why: x/sys v0.48.0 exports `IoctlSetPointerInt` and
  `IoctlGetInt` for the integer ioctls but no typed setter for these
  structs, and its `ioctlPtr` is unexported. `BIOCSETIF` takes a 32-byte
  `ifreq` (size field `0x20` of `BIOCSETIF = 0x8020426c`) with the name in
  the first 16 bytes.
- The gate is a Go test, `test/conformance/crossbuild`, that runs
  `go vet` with `GOOS=freebsd` for `amd64` and `arm64`, with the default
  tags and each opt-in VM tag, over the FreeBSD-relevant packages of the
  root module and of `src/edge/netpen`. Why: `go vet` type-checks test
  files too, and the Stop hooks run every conformance gate. All opt-in tags
  go in one tag set, so a run is two tag sets times two architectures times
  two modules, eight cached `go vet` runs.
- The VM is `test/freebsd/Vagrantfile` on `bento/freebsd-14` pinned to
  version `202510.26.0`, with arm64 builds for `virtualbox`, `utm`,
  `parallels`, and `vmware_desktop` in the Vagrant catalogue. The command
  `uv run tools/scripts/run.py freebsd vmtest` reads the guest's
  architecture with `uname -p`, cross-compiles each suite's test binary
  with `go test -c -tags <tag>`, uploads it with `vagrant upload`, and runs
  it as root with `vagrant ssh`. Why: the guest needs no Go toolchain, and
  the binary is exactly what the host built.
- Each suite owns a shell setup script under `test/freebsd/` that creates
  its `epair(4)` or jail and tears it down on exit. Phase 1 registers the
  `bpfdev` and `service` suites, and later phases add theirs to the same
  table in `vmtest.py`.
- The box gets a statement under `docs/dependencies/statements/vagrant/`.
  Why: the admission record pins and records "other inputs", and an image
  record "says who publishes it and stops there". Chef's Bento project
  publishes this box.

- Ruled: setup scripts under `test/freebsd/` stay POSIX `sh` and the
  scripting gate exempts the directory. Why: they run inside the guest,
  which holds no uv, and the user approved the exemption as a policy
  change. Cost if wrong: moving each suite's setup into `vmtest.py`'s table.
- Ruled: a zero `Config` applies `DirectionIn`, the plan's value 0, rather
  than leaving the kernel's in-out default. Why: the plan fixes the
  constants to `enum bpf_direction` and every field to optional with a zero
  default. Cost if wrong: a later phase that wants both directions sets
  `DirectionInOut`, or `Direction` gains an unset value.

## Requirements

1. The agent builds for FreeBSD. Example: `CGO_ENABLED=0 GOOS=freebsd
   GOARCH=amd64 go build ./src/edge/agent/cmd/agent` exits 0.
2. The parser splits a read buffer into records at word-aligned offsets.
   Example: a 178-byte buffer holding a 60-byte and a 64-byte frame, each
   with `bh_hdrlen` 26, yields two records. The second header starts at 88
   (`26 + 60` aligned up to 8) and its `Data` at 114. `bpf_hdrlen` in
   `sys/net/bpf.c` gives 26 for Ethernet on a 64-bit kernel:
   `BPF_WORDALIGN(26 + 14) - 14`, which aligns the network header rather
   than the frame. The fixture in requirement 5 checks this against a real
   kernel.
3. The parser reports the wire length separately. Example: a header with
   caplen 64 and datalen 1514 yields `CapturedLength` 64 and `WireLength`
   1514.
4. The parser refuses a truncated record. Example: caplen 2000 in a
   100-byte read returns an error with code `bpfdev/buffer` and no record.
5. The parser agrees with tcpdump on a FreeBSD capture. Example:
   `testdata/freebsd14-arm64-read.bin` parses to three frames equal, in
   order and byte for byte, to the three frames
   `testdata/freebsd14-arm64.pcap` holds.
6. A frame written to one `epair(4)` end is read from the other. Example:
   in the VM, `Write` on `epair0a` with `HeaderComplete` and source
   `02:00:00:00:00:99`, then `ReadBatch` on `epair0b`, returns a record whose
   bytes 6 to 11 are that source.
7. `Stats` counts what the kernel saw. Example: after three frames reach
   `epair0b` with no read, `Stats` reports `Recv` 3 and `Drop` 0.
8. The store lock is exclusive on FreeBSD. Example:
   `TestStartLocalBusIsListenerFreeDurableAndExclusive` passes in the VM.
9. The gate catches a broken FreeBSD file on a non-FreeBSD host. Example:
   deleting a used import from `device_freebsd.go` fails
   `go test ./test/conformance/crossbuild` on macOS.

## Out of scope

- Any capture, netpen, or simload code. Phases 2 and 3 own it.
- The bpf write filter (`BIOCSETWF`), zero-copy buffers
  (`BIOCSETBUFMODE`), and timestamp formats other than the default
  `BPF_T_MICROTIME`.
- The parser reads only kernel-written buffers. Its checks are against a
  layout mismatch, not a hostile author.

## Units

### U1. Store lock builds for FreeBSD

Files: src/common/service/storelock_posix.go,
docs/architecture/2026-10-03-edge-high-availability-direction.md,
docs/plans/2026-10-03-1533-feat-edge-high-availability-plan.md
After: none
Change: `storelock_posix.go` builds for `linux || darwin || freebsd`. The
edge high availability record's "The agent runs on Linux and FreeBSD"
section no longer says the agent fails to build, and keeps the capture
sentence until phase 2. That plan's requirement 9 drops "It fails today".
Tests: `TestStartLocalBusIsListenerFreeDurableAndExclusive` runs unchanged
on the host. The VM run is U5's `service` suite.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/service/storelock_posix.go docs/architecture/2026-10-03-edge-high-availability-direction.md docs/plans/2026-10-03-1533-feat-edge-high-availability-plan.md`

### U2. bpfdev buffer parser

Files: src/common/net/bpfdev/doc.go, src/common/net/bpfdev/buffer.go,
src/common/net/bpfdev/buffer_test.go, src/common/README.md
After: none
Change: `parseBuffer(buf []byte, layout hdrLayout) ([]Record, error)` walks
the buffer with `BPF_WORDALIGN` on `layout.wordSize`, and errors carry
`ErrCodeBuffer` (`bpfdev/buffer`). `Record` and `hdrLayout64` live here.
`src/common/README.md` gets a `net/bpfdev` row.
Tests: `buffer_test.go`: `TestParseBufferTwoRecordsAligned` (requirement
2), `TestParseBufferWireLength` (3), `TestParseBufferTruncatedCaplen` (4),
`TestParseBufferEmpty` (zero bytes gives no records and no error),
`TestParseBufferShortHdrlen` (hdrlen 10 is refused).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/net/bpfdev src/common/README.md`

### U3. bpfdev device on FreeBSD

Files: src/common/net/bpfdev/device_freebsd.go,
src/common/net/bpfdev/layout_freebsd.go,
src/common/net/bpfdev/device_vm_test.go
After: U2
Change: `Open(iface string, cfg Config) (*Device, error)` applies the
options in the order Decisions gives and returns coded errors
(`bpfdev/open`) carrying the interface name. `ReadBatch` reads exactly the returned buffer length.
`Write` is one `write(2)`. `Stats` returns `unix.BpfStat`. `SetReadFilter`
calls `BIOCSETF`, which flushes and zeroes the counters per bpf(4).
`LinkType` calls `BIOCGDLT`. Closing twice returns nil. A mutex serialises
`Close` against an in-flight read, the pattern
`src/modules/capture/rawsocket/local_linux.go` uses. `layout_freebsd.go`
holds the compile-time assertions.
Tests: `device_vm_test.go`, `//go:build freebsd && bpfdev_vm`, reading
`FLOWSEER_BPFDEV_A` and `FLOWSEER_BPFDEV_B`:
`TestDeviceWriteReadAcrossEpair` (requirement 6),
`TestDeviceStatsCountUnread` (7), `TestDeviceReadTimeoutReturnsEmpty` (a
quiet interface returns an empty batch within twice `ReadTimeout`),
`TestDeviceLinkTypeEthernet` (`epair` is `DLT_EN10MB`),
`TestDeviceOpenUnknownInterface` (a coded `bpfdev/open` error),
`TestDeviceCloseDuringReadDefaultTimeout` (with a zero `Config`, `Close`
during a blocked read returns within 200ms),
`TestDeviceBufferLenIsKernelValue` (a request of twice the 524288-byte
`net.bpf.maxbufsize`, which the kernel clamps and writes back, reads
without `EINVAL`). The kernel does not round a request: FreeBSD 14.3's
`bpf_buffer_ioctl_sblen` (`sys/net/bpf_buffer.c` lines 167 to 170) only
clamps it to `BPF_MINBUFSIZE` and `bpf_maxbufsize`, so a 3000-byte request
came back unchanged and the test passed with the write-back ignored.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/net/bpfdev`

### U4. FreeBSD cross-build gate

Files: test/conformance/crossbuild/crossbuild_test.go,
test/conformance/crossbuild/doc.go
After: U1, U3
Change: the test runs `go vet` for each `(GOARCH, tags)` pair over
`./src/common/net/bpfdev/... ./src/common/service/... ./src/edge/agent/...
./src/modules/capture/... ./src/edge/simload/...`, then from
`src/edge/netpen` over `./...`, and fails with the vet output. The tag list
starts as `bpfdev_vm`, and phases 2 and 3 append theirs.
Tests: the gate itself. Requirement 9 is checked once by hand and recorded
in the unit's commit message.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- test/conformance/crossbuild`

### U5. Vagrant VM and vmtest command

Files: test/freebsd/Vagrantfile, test/freebsd/README.md,
test/freebsd/bpfdev-setup.sh, tools/scripts/freebsd/__init__.py,
tools/scripts/freebsd/vmtest.py, tools/scripts/tests/freebsd/__init__.py,
tools/scripts/tests/freebsd/test_vmtest.py,
docs/dependencies/statements/vagrant/bento-freebsd-14.md,
docs/dependencies/README.md
After: U3
Change: `bpfdev-setup.sh` creates `epair0` and brings both ends up with
`inet6 ifdisabled`, so no MLD or neighbour solicitation adds to the
counters the VM tests assert on. `vmtest --suite {bpfdev,service,all}`
runs `vagrant up` if the machine is not running, maps `uname -p` (`aarch64` to `arm64`, `amd64`)
to `GOARCH`, builds and uploads each suite's binary, runs its setup
script, runs the binary as root with the suite's environment, tears the
setup down, and exits with the first failing status. The `service` suite
runs `TestStartLocalBusIsListenerFreeDurableAndExclusive` and has no setup
script. The README states the providers, the first `vagrant up`, and the
suites. `docs/dependencies/README.md` names `statements/vagrant/` and says
the statement gate does not read it, as it says for `statements/pypi/`.
Tests: `test_vmtest.py` with `subprocess.run` replaced by a recorder:
`uname -p` `aarch64` builds with `GOARCH=arm64`, a suite's environment
reaches the remote command, a failing suite's status is the exit status,
and teardown runs after a failure.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- test/freebsd tools/scripts/freebsd tools/scripts/tests/freebsd docs/dependencies/statements/vagrant`

### U6. FreeBSD read-buffer fixture

Files: src/common/net/bpfdev/device_vm_test.go,
src/common/net/bpfdev/buffer_test.go,
src/common/net/bpfdev/testdata/freebsd14-arm64-read.bin,
src/common/net/bpfdev/testdata/freebsd14-arm64.pcap,
test/freebsd/bpfdev-setup.sh
After: U2, U3, U5
Change: with `FLOWSEER_BPFDEV_FIXTURE_DIR` set, the setup script starts
`tcpdump -U -w <file> ether proto 0x88b5` on `epair0b` first. The VM test
then opens its device on `epair0b` with a read filter for the same
EtherType, sends three frames of 60, 98, and 1514 bytes with EtherType
`0x88b5` (IEEE 802 local experimental) on `epair0a`, and writes the one
read buffer that holds them. Both captures hold only those three frames,
so no stray frame either side of the test's attach gets in.
Both files are copied back with `vagrant ssh -c cat` and committed.
Tests: `TestParseBufferMatchesTcpdump` in `buffer_test.go` reads the pcap
with a test-local reader of the classic pcap format and compares its three
frames to the parsed records (requirement 5). Tcpdump reads the kernel
through libpcap, a decoder this repository did not write.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/net/bpfdev test/freebsd`

Waves: U1 U2 | U3 | U4 U5 | U6

## Verification

- `CGO_ENABLED=0 GOOS=freebsd GOARCH=amd64 go build ./src/edge/agent/cmd/agent`
  and the same for `arm64`.
- `go test ./test/conformance/crossbuild`.
- `uv run tools/scripts/run.py freebsd vmtest --suite all` on a fresh
  `vagrant up`.
- The verifier on every changed path.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `src/common/README.md`, `test/freebsd/README.md`, and the edge high
      availability record updated in the same change.
- [ ] The VM suites pass, and their output is quoted in the phase's last
      commit message.
- [ ] Outcome recorded with `uv run tools/scripts/run.py plan record
      implemented docs/plans/2026-10-12-2029-feat-freebsd-packet-io-phase1-plan.md
      --units 6 --from <t> --to <t>`.
- [ ] No plan labels in code.

## Open questions

None. The three this plan opened with are answered in
`test/freebsd/README.md`: the provider is `utm` through the `vagrant_utm`
plugin on the host that ran it, the box's guest is 14.3-RELEASE, and
`/usr/sbin/tcpdump` is installed.

## Review gaps

Follow-ups, which do not hold the verdict:

- test/conformance/crossbuild/crossbuild_test.go:81: vet type-checks and never links; fails: a body-less func in a FreeBSD file passes the gate while `GOOS=freebsd go build ./src/edge/agent/cmd/agent` fails; class: gap
- test/conformance/crossbuild/doc.go:6: "across supported architectures and build tags" overstates two architectures and the VM tags, and "do not compile" reads as broken rather than excluded; fails: a reader trusts coverage the gate lacks; class: convention
- src/common/net/bpfdev/device_freebsd.go:171: `Write` taking `mu.Lock` instead of `mu.RLock`; fails: a Write waits up to one ReadTimeout behind a blocked ReadBatch, and no test notices; class: gap
- src/common/net/bpfdev/device_freebsd.go:115: zero-timeval guard removed; fails: `ReadTimeout: time.Nanosecond` passes a zero timeval and reads never time out; class: gap
- src/common/net/bpfdev/device_freebsd.go:154: closed check in `ReadBatch` removed; fails: a read after Close returns a raw `EBADF` instead of `bpfdev/io`; class: gap
- src/common/net/bpfdev/device_freebsd.go:175: closed check in `Write` removed; fails: a write after Close returns a raw `EBADF` instead of `bpfdev/io`; class: gap
- src/common/net/bpfdev/buffer.go:71: short-remainder check replaced by `false`; fails: `parseBuffer(make([]byte, 10), hdrLayout64)` panics on a slice bound instead of returning `bpfdev/buffer`; class: gap
- src/common/net/bpfdev/device_vm_test.go:149: the read-timeout test has no lower bound; fails: a ReadBatch that returns at once on a quiet interface passes; class: gap
- tools/scripts/freebsd/vmtest.py:72: `-tags` dropped from the build; fails: the bpfdev suite builds without `bpfdev_vm` and runs only the parser tests, and no recorder test notices; class: gap
- tools/scripts/freebsd/vmtest.py:117: the `vagrant up` failure return removed; fails: a failed boot goes on to arch discovery; class: gap
- tools/scripts/freebsd/vmtest.py:63: `uname -p` stderr captured and dropped; fails: an SSH failure reports only "cannot map the guest's uname -p"; class: convention
