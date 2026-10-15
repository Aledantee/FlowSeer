---
title: A FreeBSD Kernel Timeout Truncates to Whole Ticks, So a Duration Must Be Raised to One Tick, Rounded Up
date: 2026-10-15
category: conventions
module: src/common/net/bpfdev
problem_type: bug
component: freebsd-packet-io
severity: high
applies_when:
  - "Passing a Go time.Duration to a FreeBSD ioctl or sysctl that takes a struct timeval and stores it as clock ticks, such as BIOCSRTIMEOUT."
  - "A FreeBSD read that should time out blocks forever, or Close hangs on a read that never returns."
  - "Guarding a kernel timeout against zero by checking the timeval, or computing one tick as time.Second/hz."
related_components: [vmtest]
tags: [freebsd, bpf, timeout, kern-hz, tvtohz, timeval]
symptoms:
  - "ReadBatch on a quiet interface never returns when ReadTimeout is under one clock tick, for example 1µs at kern.hz=100."
  - "The same read still hangs with the tick floor computed as time.Second/hz on a host whose kern.hz does not divide 1e6, such as 1001."
root_cause: "The kernel stores tvtohz(tv) - 1 ticks, which is floor(t*hz), and treats 0 as no timeout. A zero-timeval guard misses every positive sub-tick value, and a floor rounded down in nanoseconds can still land under one tick."
resolution_type: code_fix
---

## Situation

`bpfdev` sets the BPF read timeout with `BIOCSRTIMEOUT`, which takes a
`struct timeval`. The obvious guard, raising an exactly zero timeval to 1µs,
looks right and does nothing. The next fix, raising the timeout to
`time.Second/hz`, is right for common `kern.hz` values and wrong for others.
Both hung `ReadBatch` on a quiet interface in the FreeBSD 14.3 guest.

## What is true

FreeBSD 14.3 converts the timeval to ticks and subtracts one:

- `sys/net/bpf.c` line 1665: `d->bd_rtout = tvtohz(tv) - 1;`
- `sys/kern/kern_clock.c` lines 564 to 565 and 626 to 629: `tvtohz` is
  `TIME_TO_TICKS(sec, usec, hz) + 1`, and `TIME_TO_TICKS` truncates.
- `sys/net/bpf.c` line 1070 passes `bd_rtout` to `msleep`. `sys/sys/systm.h`
  lines 457 to 459 turn that into `tick_sbt * timo`, and
  `sys/kern/kern_synch.c` lines 208 to 222 arm a timeout only when it is
  nonzero.

So the stored value is `floor(t * hz)` ticks, and 0 means sleep until a packet
or a signal. A timeout is kept only when `usec * hz >= 1000000`.

Two easy guards fail that condition:

- Checking for a zero timeval. x/sys `NsecToTimeval` rounds up to a
  microsecond (`unix/timestruct.go` line 48 in v0.48.0), so a positive
  `Duration` never yields a zero timeval, and 1µs is still under one tick.
- A floor of `time.Second/hz` rounded down. At `kern.hz=1001` it is 999000ns,
  which converts to 999µs, and `999 * 1001 = 999999`. `kern.hz` is a boot
  tunable clamped to 8 to 137438 (`sys/sys/time.h` lines 591 to 592,
  `sys/kern/subr_param.c` line 187), so such values are allowed.

Sources: FreeBSD `releng/14.3`,
<https://github.com/freebsd/freebsd-src/tree/releng/14.3/sys>.

## How to apply it

Read `kern.hz`, compute one tick rounded up to the nanosecond, and raise the
duration to it before converting:

```go
func minReadTimeout(hz uint32) time.Duration {
	return (time.Second + time.Duration(hz) - 1) / time.Duration(hz)
}

hz, err := unix.SysctlUint32("kern.hz")
// ...
timeout = max(timeout, minReadTimeout(hz))
tv := unix.NsecToTimeval(timeout.Nanoseconds())
```

Keep the arithmetic in a file with no build constraint, so a host test can
check every allowed hz against the kernel's condition. The guest runs one hz
(100), so a VM test alone cannot catch the hz=1001 case.

## Evidence

- `src/common/net/bpfdev/config.go:101-103`: `minReadTimeout`.
- `src/common/net/bpfdev/device_freebsd.go:91-103`: the comment citing the
  kernel, the `kern.hz` read, and the floor.
- `src/common/net/bpfdev/config_test.go:16` and `:24`: `kernelTicks` models
  the kernel condition, and `TestMinReadTimeoutReachesOneTick` checks every
  hz from 8 to 137438, with named subtests for 1001 and 11111 that fail when
  the floor rounds down.
- `src/common/net/bpfdev/device_vm_test.go:162`:
  `TestDeviceReadTimeoutBelowOneTickReturns` failed in the guest against the
  zero-timeval guard with "ReadBatch did not return within 1s with a 1µs
  ReadTimeout".

## The same kernel clamps BIOCSBLEN, it does not round it

The buffer-length ioctl in the same package fails the same way when the
kernel's behavior is guessed. `bpf_buffer_ioctl_sblen`
(`sys/net/bpf_buffer.c` lines 167 to 170) only clamps a request to
`BPF_MINBUFSIZE` and `net.bpf.maxbufsize` and writes the result back, and
`bpfread` refuses any other read size with `EINVAL` (`sys/net/bpf.c` line
1010). A test that asks for a value inside that range never sees the
write-back. `TestDeviceBufferLenIsKernelValue`
(`src/common/net/bpfdev/device_vm_test.go:326`) asks for twice the default
maximum, which the kernel changes.

## What it does not cover

Other FreeBSD timeouts that do not go through `tvtohz` (for example a socket
option the kernel stores as an `sbintime_t`) may round differently. Read the
handler before applying this rule to them.
