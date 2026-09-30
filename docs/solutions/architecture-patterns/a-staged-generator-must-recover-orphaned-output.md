---
title: A Staged Generator Must Recover Orphaned Output Before Rendering Again
date: 2026-09-30
category: architecture-patterns
module: src/protocol/yang/cmd/yanggen
problem_type: architecture_pattern
component: code_generation
severity: medium
applies_when:
  - "Writing or reviewing a generator that swaps complete output directories into place"
  - "A generator can be interrupted after moving current output aside and before installing its replacement"
  - "A failed or restarted generator must distinguish incomplete staging from recoverable prior output"
related_components: [conformance-gates, test_fixtures]
tags: [code-generation, atomic-replacement, crash-recovery, staging, rollback, yanggen]
---

# A staged generator must recover orphaned output before rendering again

## The situation

`yanggen` renders each vendor into a sibling temporary directory, then swaps
that directory into the output tree. The swap first moves the current vendor
directory to an `-old-*` path. A process can stop after that rename and leave
the output without its last complete tree. A later run that deletes every
staging directory has no safe output to restore.

## The rule

Give each staging name one meaning and make startup repair the interrupted
states:

- `.<vendor>-tmp-*` is incomplete work and can be removed.
- `.<vendor>-old-*` is a complete prior tree. If the vendor directory is
  missing, restore the newest old tree, then remove older backups. If the
  vendor directory exists, remove stale old trees after the current output is
  known to be present.
- When a replacement rename fails, attempt rollback. If rollback also fails,
  keep the old path out of deferred cleanup and include its path in the error.

The temporary root also needs an explicit mode before it is renamed into the
output tree. A temporary directory's default mode is otherwise carried into
the committed tree.

## Working example

After an interrupted swap, a restart may see:

```text
out/.fixture-tmp-crash
out/.fixture-old-1
out/.fixture-old-2
```

When `out/fixture` is absent, compare the old directories by modification
time, restore `.fixture-old-2` to `out/fixture`, and delete the stale
temporary directory and older backup. Rendering can then fail without leaving
the output empty.

## Evidence

- `src/protocol/yang/cmd/yanggen/emit.go:150-176` prunes staging before a
  run, creates a sibling temporary directory, and sets its mode before use.
- `src/protocol/yang/cmd/yanggen/emit.go:242-303` removes incomplete temporary
  paths, restores the newest old tree when the vendor is missing, and removes
  the remaining old paths.
- `src/protocol/yang/cmd/yanggen/emit.go:217-223` preserves an old path when
  rollback fails and reports that path in the returned error.
- `src/protocol/yang/cmd/yanggen/emit_test.go:633-739` proves stale staging is
  pruned and the newest old tree is restored after a failed render.
- `src/protocol/yang/cmd/yanggen/emit_test.go:742-798` proves a failed
  rollback leaves the old tree readable and names it in the error.

## What this does not cover

This protocol does not make a rename durable across power loss. Durable
replacement also needs the filesystem's file and directory synchronization
rules. It does not replace validating all generated outputs before the first
swap.
