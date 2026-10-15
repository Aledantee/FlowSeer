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
  `ReadTimeout` over one hour is refused. Why: `Close` waits up to one
  `ReadTimeout`, and a value near `math.MaxInt64` overflows the `+999`
  rounding of x/sys `NsecToTimeval` (`unix/timestruct.go:48`) into a
  negative timeval that `itimerfix` refuses with `EINVAL`.
  A `ReadTimeout` under one kernel clock tick is raised to one tick, with
  the tick computed as `time.Second/hz` rounded up to the nanosecond from
  `kern.hz`. Why: `BIOCSRTIMEOUT` stores `tvtohz(tv) - 1` as `bd_rtout`
  (`sys/net/bpf.c` line 1665), `tvtohz` is the truncated tick count plus
  one (`sys/kern/kern_clock.c` lines 564 to 565 and 626 to 629), and
  `bpfread` passes `bd_rtout` to `msleep` (`sys/net/bpf.c` line 1070),
  which hands `_sleep` a timeout of `tick_sbt * timo` (`sys/sys/systm.h`
  lines 457 to 459), and `_sleep` arms a timeout only when that is nonzero
  (`sys/kern/kern_synch.c` lines 208 to 222), so 0 sleeps until a packet
  or a signal. So `bd_rtout` is 0 unless
  `usec * hz >= 1000000`. A floor of `time.Second/hz` rounded down misses
  that for an hz that does not divide 1e6: at `kern.hz=1001` it is 999000ns,
  999µs, and `999 * 1001 < 1000000`. `kern.hz` is a boot tunable clamped
  to `[HZ_MINIMUM, HZ_MAXIMUM]`, 8 to `INT_MAX/15625`
  (`sys/kern/subr_param.c` line 187, `sys/sys/time.h` lines 591 to 592).
  The floor is a pure function so a host test checks every hz from 8 to
  137438.
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
  which holds no uv, the same reason the container scripts in the gate's
  exempt list stay shell (`docs/code-style-python.md`, the scripting gate
  paragraph). Cost if wrong: moving each suite's setup into `vmtest.py`'s table.
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
10. A sub-tick `ReadTimeout` still times out for every hz the kernel
    allows. Example: with `kern.hz=1001`, the raised timeout converts with
    `NsecToTimeval` to at least 1000µs, so `usec * hz >= 1000000` and
    `bd_rtout` is at least 1. The same holds for hz 8, 100, 1000, 1024,
    11111, and 137438.
11. `Open` refuses a `ReadTimeout` over one hour. Example:
    `Config{ReadTimeout: 2 * time.Hour}` returns a `bpfdev/open` error from
    `Config.resolve` on any host, and `Open` in the guest returns it before
    opening `/dev/bpf`.
12. `vmtest` names why it could not read the guest's architecture.
    Example: when `vagrant ssh -c 'uname -p'` exits 255 with stderr
    `ssh: connect to host 127.0.0.1 port 2222: Connection refused`,
    `vmtest` prints that `vagrant ssh -c 'uname -p'` exited 255 and the
    stderr line, prints no "cannot map" message, and exits 1. When
    `uname -p` prints `riscv64`, the "cannot map" message names `riscv64`.

## Out of scope

- Any capture, netpen, or simload code. Phases 2 and 3 own it.
- The bpf write filter (`BIOCSETWF`), zero-copy buffers
  (`BIOCSETBUFMODE`), and timestamp formats other than the default
  `BPF_T_MICROTIME`.
- The parser reads only kernel-written buffers. Its checks are against a
  layout mismatch, not a hostile author.

## Units

U1 to U6 are built on this branch. U7 makes the read-timeout floor hold for
every hz and caps `ReadTimeout`. U8 makes `vmtest` name an ssh failure.

### U7. One-tick read timeout floor for every hz

Files: src/common/net/bpfdev/config.go,
src/common/net/bpfdev/config_test.go,
src/common/net/bpfdev/device_freebsd.go,
src/common/net/bpfdev/device_vm_test.go,
docs/plans/2026-10-12-2029-feat-freebsd-packet-io-phase1-plan.md
After: none
Change: `config.go`, which has no build constraint, holds
`Config.resolve` (moved from `device_freebsd.go`), which refuses a
negative `ReadTimeout` and one over one hour with an error coded
`bpfdev/open`, and a pure
`minReadTimeout(hz uint32) time.Duration` that returns `time.Second/hz`
rounded up to the nanosecond. `configure` in `device_freebsd.go` reads
`kern.hz` as now and raises the timeout to `minReadTimeout(hz)`, and its
comment cites `sys/net/bpf.c` and `sys/kern/kern_clock.c`. The
`ReadTimeout` doc names the one-hour cap. The plan's `Config` Decisions
bullet states this behavior, and the unit keeps it matching the code.
Tests: `config_test.go`: `TestMinReadTimeoutReachesOneTick` loops over
every hz from 8 to 137438, converts `minReadTimeout(hz)` with
`unix.NsecToTimeval`, and asserts
`(int64(tv.Sec)*1_000_000 + int64(tv.Usec)) * int64(hz) >= 1_000_000`, the
kernel's condition for a nonzero `bd_rtout` (requirement 10). The
conversions are needed because `Timeval.Usec` is `int32` on darwin
(x/sys `ztypes_darwin_arm64.go` line 28) and `int64` on FreeBSD
(`ztypes_freebsd_arm64.go` line 28), and the file builds for both, since
the crossbuild gate vets it with `GOOS=freebsd`. Named subtests for hz 1001
and 11111 must fail with the floor rounded down. `TestResolveRefusesReadTimeoutOverOneHour`
(requirement 11, asserting the `bpfdev/open` code) and
`TestResolveRefusesNegativeReadTimeout`, both on the host. In
`device_vm_test.go`, `TestDeviceOpenRefusesReadTimeoutOverOneHour` sits
beside `TestDeviceOpenRefusesNegativeReadTimeout`. The VM suite still
passes, including
`TestDeviceReadTimeoutBelowOneTickReturns`.
Verify: `uv run tools/scripts/run.py verify change -- src/common/net/bpfdev docs/plans/2026-10-12-2029-feat-freebsd-packet-io-phase1-plan.md`

### U8. vmtest names an ssh failure

Files: tools/scripts/freebsd/vmtest.py,
tools/scripts/tests/freebsd/test_vmtest.py
After: none
Change: when `vagrant ssh -c 'uname -p'` exits non-zero, `guest_arch`
prints `vagrant ssh -c 'uname -p' exited <code>` and the captured stderr
to standard error, and `main` returns 1 without the "cannot map" message.
When `uname -p` succeeds with a value `GOARCH` does not map, the "cannot
map" message names that value. `docs/code-style-python.md` asks a script to
name a failure on standard error, and the old message pointed at the
architecture when ssh had failed.
Tests: `test_vmtest.py`. `Recorder` gains a `stderr` callable beside
`fail`, returning the stderr text for a call, and `run_main` returns
`(status, stderr_text)` (every existing caller is updated to take the
status). `test_failed_uname_names_the_ssh_failure` (requirement 12): the
recorder answers `uname -p` with exit 255 and the ssh stderr line, and the
test asserts status 1, that the captured stderr holds `exited 255` and the
ssh line, and that it holds no "cannot map" message.
`test_unknown_architecture_fails_before_building` also asserts the message
names `riscv64`.
Verify: `uv run tools/scripts/run.py verify change -- tools/scripts/freebsd tools/scripts/tests/freebsd`

Waves: U7 U8

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
      --units 2 --from <t> --to <t>`.
- [ ] No plan labels in code.

## Open questions

None. The three this plan opened with are answered in
`test/freebsd/README.md`: the provider is `utm` through the `vagrant_utm`
plugin on the host that ran it, the box's guest is 14.3-RELEASE, and
`/usr/sbin/tcpdump` is installed.

## Review gaps

Blocking, from the review after the re-plan (behavior):

- tools/scripts/freebsd/vmtest.py:119: the `finally` runs the setup script's `down` and discards its status; fails: the test binary passes, `down` exits non-zero (a dropped ssh session, a failed `ifconfig ... destroy`), and `vmtest` exits 0 with the epair left behind; smallest fix: keep the run's status, check teardown's, and return teardown's status when nothing failed earlier, with a stderr line either way; class: behavior
- test/freebsd/bpfdev-setup.sh:74: under `set -e`, `down` exits when `stop_capture` fails, before `destroy` runs, and `stop_capture` removed the PID file first; fails: with `FLOWSEER_BPFDEV_FIXTURE_DIR` set and tcpdump still alive after its wait, the epair stays and a second `down` cannot stop the capture; smallest fix: attempt `destroy` whatever `stop_capture` returns, return failure after both, and keep the PID file until tcpdump has exited; class: behavior
- test/conformance/crossbuild/crossbuild_test.go:147: `watchInputs` reads only the package directories `go list` reports, never their ancestors; fails: after a cached pass, a new package `src/edge/agent/zzprobe` holding a broken `//go:build freebsd` file prints `ok (cached)` (reproduced), since `src/edge/agent` holds no Go file and is never read; smallest fix: walk each target tree with `filepath.WalkDir`, which reads every directory, as the cached-gate solution's last section says; `doc.go:11` ("any source change reruns the gate") is false until then; class: behavior

Follow-ups, which do not hold the verdict:

- src/common/net/bpfdev/buffer.go:72: the three error returns changed to `return recs, ...`; fails: a valid record followed by a truncated one returns the valid prefix with the error, and no test has a valid first record; class: false test, other
- tools/scripts/tests/freebsd/test_vmtest.py:95: the cleanup guard changed to `if suite.setup and status == 0`; fails: a setup that fails partway skips `down`; class: false test, other
- tools/scripts/freebsd/vmtest.py:97: the return after a failed build, or after a failed setup, removed; fails: a failed `go test -c` still uploads, and a failed setup still runs the binary; class: gap
- tools/scripts/freebsd/vmtest.py:86: the env merge reversed to `{"GOOS": ..., **os.environ}`; fails: a host with GOOS or GOARCH set builds for the wrong target; class: gap
- test/conformance/crossbuild/crossbuild_test.go:14: `vmTags` is a hand-kept list; fails: a suite tag added to `vmtest.py` without this list leaves its files unvetted; class: convention
- tools/scripts/freebsd/vmtest.py:28: "A later phase adds its suite here" narrates planned work; fails: the comment rule in docs/code-style-python.md; class: convention
- tools/scripts/tests/freebsd/test_vmtest.py: `run_main(...)[0]` kept as bare statements; fails: reads as an assertion that is not there; class: convention
