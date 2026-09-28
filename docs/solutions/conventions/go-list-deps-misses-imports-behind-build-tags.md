---
title: An Untagged go list Misses Imports Made Only From Build-Tagged Files
date: 2026-09-28
last_verified: 2026-09-28
category: conventions
module: .claude/skills/verify-change
problem_type: bug
component: conformance-gates
severity: high
symptoms:
  - "A verifier run for a change to a root package does not select a nested module or package whose tagged tests import it"
  - "`verify-change.sh --print-selection -- src/protocol/ssh/buffer.go` prints no `dependent=src/edge/netpen` line"
root_cause: "`go list -deps`, `.Deps`, `.TestImports`, and `.XTestImports` cover only the files the default build selects. An import made only from a file under `//go:build <tag>` is absent, so an importer computed from that listing skips the package that vet_tagged would have compiled."
resolution_type: code_fix
applies_when:
  - "Changing how verify-change.sh picks targets, dependent modules, or importers from a go list listing"
  - "Writing any tool or gate that decides which packages depend on a changed package"
  - "Investigating a targeted verifier run that stayed green while a tagged integration or bench test stopped compiling"
related_components: [netpen, verify-change, integration-tests]
tags: [build-tags, go-list, verification, verify-change, dependency-selection]
---

# An untagged go list misses imports made only from build-tagged files

## The situation

FlowSeer puts integration tiers and benches behind build tags
(`netpen_t1`, `netpen_t2`, `yang_integration_t1`, `snmp_bench_*`). A
package often imports a root package only from those files. netpen reaches
`src/protocol/ssh` only through tagged code
(`src/edge/netpen/test/integration/t2_lab_ospf_test.go:1,12`):

```go
//go:build netpen_t2
...
	flowssh "go.aledante.io/FlowSeer/src/protocol/ssh"
```

`go list -deps -test ./...` in `src/edge/netpen` without tags does not name
`src/protocol/ssh` or `src/common/secret`. With
`-tags netpen_t1,netpen_t2,netpen_bench` it names both. A selection built on
the untagged listing therefore skipped netpen for an ssh change, while the
gate it skipped (`vet_tagged`) is the one that would have caught the break.

## How to apply it

Any "who depends on this package" question gets answered over the same
builds the gate then checks: the default build plus one listing per tag
the files carry. The verifier's dependent-module selection does this
(`.claude/skills/verify-change/scripts/verify-change.sh:338-345`):

```bash
module_deps() {
  local tag
  go list -e -deps -test -f '{{.ImportPath}}' ./... || return
  while IFS= read -r tag; do
    [[ -n $tag ]] || continue
    go list -e -deps -test -tags "$tag" -f '{{.ImportPath}}' ./... || return
  done < <(build_tags ./...)
}
```

`build_tags` (`verify-change.sh:326`) is the tag extraction `vet_tagged`
also uses, so the listing and the vet cover the same tags. The test is the "counts a nested module's
imports behind a build tag" case in `tools/hooks/tests/run.sh:765`. It
failed before `module_deps` existed.

## What this does not cover

The root module's targeted run has the same gap, and it is still open. Its
importer fixpoint reads an untagged listing
(`verify-change.sh:637`):

```bash
go list -e -f '{{.ImportPath}}|{{join .Deps " "}} {{join .TestImports " "}} {{join .XTestImports " "}}' ./... > "$build_dir/packages.txt"
```

`src/protocol/netconf/test/integration` imports `src/protocol/netconf` only
from `yang_integration_t1` files (`t1_main_test.go:1,14`), and that listing
does not name the import. So a change to `src/protocol/netconf` does not
target the integration package, and its tagged tests are not vetted. The
same holds for the restconf, gnmi, and netsimload integration packages.
Fixing it needs the per-tag listing in the fixpoint.
