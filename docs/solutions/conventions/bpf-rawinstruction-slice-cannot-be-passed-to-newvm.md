---
title: A bpf.RawInstruction Slice Cannot Be Passed to bpf.NewVM
date: 2026-09-09
last_verified: 2026-09-30
category: conventions
module: src/modules/capture/rawsocket
problem_type: bug
component: packet_capture
severity: critical
symptoms:
  - "cannot use []bpf.RawInstruction as []bpf.Instruction when passing an assembled program to bpf.NewVM"
  - "bpf.NewVM returns BPF program must end with RetA or RetConstant for a []bpf.Instruction containing RawInstruction values"
root_cause: "Go slices are not covariant, so []bpf.RawInstruction cannot become bpf.NewVM's []bpf.Instruction parameter. A []bpf.Instruction can contain bpf.RawInstruction values one at a time, but the pinned NewVM type switch never matches a RawInstruction as a return instruction."
resolution_type: "call bpf.Disassemble first, reject undecoded raw instructions, then pass the resulting []bpf.Instruction to bpf.NewVM"
applies_when:
  - "passing a compiled BPF program to golang.org/x/net/bpf.NewVM after bpf.Assemble or filter.Assemble returned []bpf.RawInstruction"
  - "a local-interface or mirror-receiver filter must run an assembled cBPF program through the userspace VM"
  - "debugging a BPF program must end with RetA or RetConstant error after manually placing RawInstruction values in []bpf.Instruction"
related_components: [rawsocket, engine]
tags: [bpf, golang.org/x/net/bpf, classic-bpf, packet-capture, gotcha]
---

# A bpf.RawInstruction Slice Cannot Be Passed to bpf.NewVM

## The situation

`src/modules/capture` compiles one `CaptureFilter` to one classic BPF program.
The local source and mirror receiver need to run that program through
`golang.org/x/net/bpf` after restoring or decapsulating the Ethernet frame.
`filter.Assemble` returns `[]bpf.RawInstruction`, which is the form a packet
socket accepts. `bpf.NewVM` instead takes `[]bpf.Instruction`.

Passing the raw slice directly does not compile. The two slice types are
different, even though one `bpf.RawInstruction` value implements the
`bpf.Instruction` interface through its `Assemble` method. Go does not convert
`[]bpf.RawInstruction` to `[]bpf.Instruction` element by element.

## The failure after an incorrect conversion

An element-by-element conversion can produce a `[]bpf.Instruction`, but the
interface values still contain `bpf.RawInstruction` as their concrete type:

```go
insts := make([]bpf.Instruction, len(raw))
for i, instruction := range raw {
	insts[i] = instruction
}
```

The pinned `bpf.NewVM` implementation checks the last interface value with a
type switch. Its accepted return types are `RetA` and `RetConstant`
(`golang.org/x/net@v0.58.0/bpf/vm.go:18,65-69`). A `RawInstruction` value
does not match either case, even when its opcode encodes a return instruction.
The VM's execution dispatch also switches on high-level instruction types, so
the raw value cannot run after construction.

## How to apply

Disassemble the raw program before calling `bpf.NewVM`:

```go
insts, allDecoded := bpf.Disassemble(raw)
if !allDecoded {
	return nil, errs.New().Code(ErrCodeSourceOpen).
		Msg("disassemble the filter program")
}
vm, err := bpf.NewVM(insts)
```

`bpf.Disassemble` returns `[]bpf.Instruction` and reports `allDecoded == false`
when an instruction remains a `RawInstruction`
(`golang.org/x/net@v0.58.0/bpf/asm.go:26-40`). Check that result before
constructing the VM. The capture implementation uses this conversion in
`src/modules/capture/rawsocket/mirror_linux.go` and
`src/modules/capture/rawsocket/local_linux.go`.

## Evidence

- `golang.org/x/net@v0.58.0/bpf/instructions.go:9-17` defines the interface and
  the raw instruction type. The interface applies to individual values.
- `golang.org/x/net@v0.58.0/bpf/vm.go:18-79` accepts a slice of interface
  values, type-switches its elements, and requires a high-level return value.
- `golang.org/x/net@v0.58.0/bpf/asm.go:9-40` defines the assembled raw form and
  the disassembly path back to high-level instructions.
- `src/modules/capture/rawsocket/mirror_linux_test.go` builds a real filter
  through `filter.Compile` and `filter.Assemble`, then confirms the receiver
  gets past VM construction.

`src/edge/netpen/link/bpf.go` has a separate BPF compiler and is outside this
solution. Apply the same conversion if it later sends assembled instructions to
`bpf.NewVM`.
