---
title: A golang.org/x/net/bpf RawInstruction Compiles Against bpf.NewVM but Always Fails to Build
date: 2026-09-09
last_verified: 2026-09-09
category: conventions
module: src/modules/capture/rawsocket
problem_type: bug
component: packet_capture
severity: critical
symptoms:
  - "bpf.NewVM(prog) returns \"BPF program must end with RetA or RetConstant\" for a program that plainly ends in a return instruction"
  - "a filter that compiles, assembles, and attaches to a real AF_PACKET socket with SO_ATTACH_FILTER cannot be run through the pure-Go bpf.VM interpreter at all"
root_cause: "bpf.RawInstruction implements the bpf.Instruction interface syntactically (it has an Assemble method), so a []bpf.RawInstruction type-checks everywhere a []bpf.Instruction is expected, including as bpf.NewVM's parameter. But bpf.NewVM's own terminal-instruction check and its Run dispatch both type-switch on each instruction's concrete Go type, and neither has a case for bpf.RawInstruction — only for the high-level types (RetA, RetConstant, and so on) that bpf.Assemble consumed to produce the raw form in the first place."
resolution_type: "call bpf.Disassemble first to recover the high-level []bpf.Instruction form, then pass that to bpf.NewVM"
applies_when:
  - "passing a compiled BPF program to golang.org/x/net/bpf.NewVM anywhere it was produced by bpf.Assemble, filter.Assemble, or otherwise already exists as a []bpf.RawInstruction"
  - "a kernel-attached filter (SO_ATTACH_FILTER) and a userspace-run filter must both execute the same compiled program"
  - "debugging a BPF-related \"must end with RetA or RetConstant\" error against a program that visibly does"
related_components: [rawsocket, engine]
tags: [bpf, golang.org/x/net/bpf, classic-bpf, packet-capture, gotcha]
---

# A golang.org/x/net/bpf RawInstruction Compiles Against bpf.NewVM but Always Fails to Build

## The situation

`src/modules/capture` compiles one `CaptureFilter` to one classic BPF program
and runs it two ways: attached to the kernel via `SO_ATTACH_FILTER` for the
AF_PACKET local-interface source, and through `golang.org/x/net/bpf`'s own
`VM` for the mirror receiver, since a mirror-receiver filter reads fields at
offsets that assume the frame is already decapsulated — envelope-dependent,
so no single kernel-attachable program covers every encapsulation. Both paths
start from the same `filter.Assemble` output, a `[]bpf.RawInstruction`
(`golang.org/x/net/bpf@v0.58.0`'s assembled/raw form, what `SO_ATTACH_FILTER`
needs). Passing that slice straight to `bpf.NewVM` type-checks — the build
compiles cleanly — and then fails at runtime on every real filter, with an
error claiming the program does not end in a return instruction even when it
plainly does.

## What is true, and why

`bpf.RawInstruction` implements the `bpf.Instruction` interface (it has an
`Assemble() (RawInstruction, error)` method), so `[]bpf.RawInstruction`
satisfies `[]bpf.Instruction` as a parameter type. But `bpf.NewVM` works
entirely by type-switching on each instruction's *concrete* Go type, not by
calling interface methods:

```go
// golang.org/x/net/bpf@v0.58.0, vm.go:65-69
switch filter[len(filter)-1].(type) {
case RetA, RetConstant:
default:
    return nil, errors.New("BPF program must end with RetA or RetConstant")
}
```

A `bpf.RawInstruction` value is never a `RetA` or a `RetConstant`, no matter
what opcode it encodes, so this check rejects every program built from
`bpf.Assemble`'s output. `VM.Run`'s own instruction dispatch (`vm_instructions.go`)
has the same shape: a type switch over the high-level instruction types,
with no `RawInstruction` case at all. There is no partial failure mode here —
a raw-instruction program cannot run in the VM at all, only fail to
construct one.

`src/modules/capture/rawsocket/mirror_linux.go:170-192`'s `openMirrorReceiver`
hits this directly: it receives `prog []bpf.RawInstruction` (the same value
`local_linux.go` passes to `SetsockoptSockFprog`) and must build a `*bpf.VM`
from it.

## How to apply

Convert back to the high-level form with `bpf.Disassemble` before calling
`bpf.NewVM`:

```go
// src/modules/capture/rawsocket/mirror_linux.go:180-186
insts, allDecoded := bpf.Disassemble(prog)
if !allDecoded {
    return nil, errs.New().Code(ErrCodeSourceOpen).
        Msg("disassemble the mirror receiver's filter program")
}
v, err := bpf.NewVM(insts)
```

`bpf.Disassemble` reports `allDecoded == false` for any raw instruction it
cannot map back to a high-level one (rare for a program this package itself
assembled); check it rather than assume a lossless round trip.

## Evidence

- The check that rejects every raw-instruction program:
  `golang.org/x/net/bpf@v0.58.0`'s `vm.go:65-69` (see above).
- The fix: `src/modules/capture/rawsocket/mirror_linux.go:170-192`.
- `src/modules/capture/rawsocket/mirror_linux_test.go`'s
  `TestOpenMirrorReceiver_BuildsVMFromRealFilterProgram` builds a real
  program through `filter.Compile` + `filter.Assemble` (not a hand-built
  `[]bpf.Instruction`) and confirms `openMirrorReceiver` gets past VM
  construction with it — the one path in this package that previously could
  not, since every other test constructs its `bpf.VM` directly from a
  `[]bpf.Instruction` literal and never exercises the raw-to-VM conversion.

## What this does not cover

`src/edge/netpen/link/bpf.go` (the codebase's other BPF compiler,
kept as a separate implementation rather than imported into this package
because it pulls in `gopacket` transitively) has not been checked against
the same failure mode; if it or any future code ever needs to run an
assembled program back through `bpf.NewVM`, the same conversion applies.
