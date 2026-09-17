---
title: A Third Kind Joins a Two-Kind System Silently, So Enumerate the Sites Before Writing One
date: 2026-09-17
last_verified: 2026-09-17
category: architecture-patterns
module: src/common/netsim/fabric
problem_type: architecture_pattern
component: fabric
severity: high
applies_when:
  - "Adding a node, endpoint, or participant kind to a package that holds exactly two and classifies by asking each one's map in turn"
  - "Planning work whose stop condition is whether a new kind can be added without reshaping how the package classifies"
  - "Reviewing such a change, to decide whether every site was found"
related_components: [netsim, vswitch]
tags: [node-kind, classification, exhaustiveness, enumeration, fabric]
---

# A third kind joins a two-kind system silently

## The situation

`src/common/netsim/fabric` held two node kinds, switches and hosts, as two maps
on its configuration. Nothing dispatched through an interface or an enum. Every
place that needed to know what a node was asked the two maps in turn, and
everything that walked nodes walked both maps in sequence.

Adding a reflector as a third kind touched roughly a dozen such sites across
three files. The compiler found none of them: `.golangci.yml` enables no
exhaustive-struct check, and a two-map question with a third answer is still
valid Go. Three distinct failure shapes, each silent in its own way:

- **A classifier that falls through.** A site asking "host? switch? else
  unknown" rejects a valid reflector with an error about the cable that names
  it, not about the missing map.
- **An iteration site that drops the map.** The construction path assembled its
  specification field by field, so a missed field meant every construction
  silently lost the reflectors, surfacing three calls later as an
  endpoint-not-found error.
- **A lookup that is dereferenced immediately.** The step function reads
  `f.switches[arr.Device]` and calls a method on the result, which reads a field
  on its receiver. For a reflector that is a nil dereference: a panic on the
  feature's own happy path, in a repository whose panic gate walks the syntax
  tree for explicit panic calls and cannot see this one.

## What to do

Enumerate the sites before writing the kind, and put the enumeration in the
plan rather than in the implementer's head. Separate the two lists, because
they fail differently and a reader uses them differently: the sites that
classify and fall through, and the sites that iterate and drop. Name the
lookup that panics on its own.

Then have the implementer grep independently and reconcile against the list,
reporting both what it changed and what it deliberately did not. One site here
is unreachable by the obvious grep, because it keys on the switch map rather
than the host map; the plan named it precisely so the grep's silence would not
be read as absence.

The assertion that catches the worst of the three is a round trip: build from a
configuration carrying the new kind and confirm the built object's specification
still carries it. Write that first and watch it fail before touching the
construction path.

## Why the usual signals are absent

- **The compiler.** Two maps plus a third is not a type error. An `if/else if`
  chain over map lookups has no exhaustiveness to check.
- **The package suite.** Every existing test uses the two existing kinds, so a
  complete suite passes with the third kind entirely unwired.
- **The panic gate.** It reports `panic` call expressions outside `Must`
  functions. A nil-pointer dereference through a map miss is invisible to it.

## Evidence

- The two-map shape and its classification sites:
  `src/common/netsim/fabric/config.go`, `fabric.go`, and `run.go`. After the
  change the reflector map is read at twenty non-test sites across the package.
- The panicking lookup: `src/common/netsim/fabric/run.go` reads
  `f.switches[arr.Device]` and dereferences it two lines later through
  `Ports()`, which returns `s.ports.Clone()`
  (`src/common/netsim/vswitch/switch.go:390-392`).
- The dropped map: removing the reflector field from the construction-spec
  assembler fails the round-trip test with
  `cable 2 endpoint A: endpoint node "r1" not found`, watched on 2026-09-17.
  The plan had predicted that message, which is what made it recognisable.
- The grep's blind spot: the panicking lookup keys on the switch map, so a grep
  for the host map's name does not reach it.

## What this does not cover

It says nothing about whether a third kind is the right design; that is a
question for a `docs/architecture/` record, and here one had already decided it.
It also assumes the package is small enough to enumerate. Past that size the
answer is to introduce the dispatch the package never had, which is a refactor
with its own plan rather than an addition.
