---
title: yanggen Output Depends on goyang's Map Order When Two Augments Add the Same Child
date: 2026-09-28
last_verified: 2026-09-28
category: conventions
module: src/protocol/yang/cmd/yanggen
problem_type: bug
component: yang
severity: medium
symptoms:
  - "`yanggen -update` on an unchanged tree rewrites files under generated/go/yang/cisco-iosxe/ciscoiosxenative"
  - "Two regenerations from the same sources produce different diffs against the committed bindings"
  - "A field's Module flips between two modules, e.g. Macsec between moduleCiscoIOSXEEthernet (TBool) and moduleCiscoIOSXESwitch (TEmpty)"
root_cause: "goyang's Modules.Process collects modules for augment resolution by ranging over the ms.Modules map. When two modules augment one node with a same-named child, the one applied first wins, and Go map order changes per run."
resolution_type: workaround
applies_when:
  - "Running `go run ./src/protocol/yang/cmd/yanggen -update` and reviewing or committing the diff under generated/go/yang"
  - "Investigating a generated YANG binding diff in ciscoiosxenative, cisco-iosxe/openconfigsystem, or ruckus-icx/openconfiginterfaces that no source change explains"
  - "Adding a vendored YANG tree or a module that augments a node another module already augments"
related_components: [goyang, generated-bindings]
tags: [yang, yanggen, goyang, determinism, augment, code-generation]
---

# yanggen output depends on goyang's map order when two augments add the same child

## The situation

Some vendored modules augment the same node with a child of the same name:

- `Cisco-IOS-XE-ethernet` and `Cisco-IOS-XE-switch` both add `macsec`.
- `openconfig-if-poe` and `icx-openconfig-if-poe-aug` both add `poe` under
  `/interfaces/interface/ethernet`.

Only one of each pair ends up in the schema, and which one changes from run
to run. Two `yanggen -update` runs on 2026-09-28 (darwin/arm64, goyang
v1.6.3), with no source change, produced a 3,964-line diff and a 185-line
diff against the committed tree. The first touched `ciscoiosxenative`
(16 files), `cisco-iosxe/openconfigsystem`, and
`ruckus-icx/openconfiginterfaces`. The second touched two
`ciscoiosxenative` chunks and `ruckus-icx/openconfiginterfaces`. One line
from the second run:

```diff
-		{GoName: "Macsec", Module: moduleCiscoIOSXEEthernet, Name: "macsec", Type: yang.TBool},
+		{GoName: "Macsec", Module: moduleCiscoIOSXESwitch, Name: "macsec", Type: yang.TEmpty},
```

## Why

goyang resolves augments over a slice it fills by ranging over a map, so
the processing order follows Go's randomized map iteration
(`github.com/openconfig/goyang@v1.6.3/pkg/yang/modules.go:346-352`):

```go
	// Now handle all the augments.  We don't have a good way to know
	// what order to process them in, so repeat until no progress is made

	mods := make([]*Module, 0, len(ms.Modules)+len(ms.SubModules))
	for _, m := range ms.Modules {
		mods = append(mods, m)
	}
```

The lockfile records source and closure hashes, not output
(`src/protocol/yang/cmd/yanggen/lockfile.go:36-40`), so `yanggen -check`
passes after either outcome
(`src/protocol/yang/cmd/yanggen/doc.go:47-49` states the limitation).

## How to apply it

- After `yanggen -update`, commit only the binding diffs a source or
  generator change explains. When the only change is in the packages
  above, it is this noise: put those files back to their committed content.
  The `generated/` hooks deny `git restore` from an agent's Bash, so ask a
  person to run
  `git restore --source=HEAD -- generated/go/yang/cisco-iosxe generated/go/yang/ruckus-icx`.
- Do not treat "regenerate, then no diff" as a gate over the binding
  files. It holds for `generated/go/yang/go.mod` and `go.sum`, which do not
  depend on schema order.
- A new module that augments a node another module already augments can
  add a new pair. Check its package with two regenerations before
  committing.

## What this does not cover

The fix is still open: a rule for which augment wins, a skip entry for one
side of each pair, or feeding goyang a deterministic order. The plan
`docs/plans/2026-09-26-1108-perf-yang-nested-module-plan.md` (Open questions)
records it. Once it lands, this solution is removed.
