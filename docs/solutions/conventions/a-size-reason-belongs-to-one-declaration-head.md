---
title: A size reason belongs to one declaration head
date: 2026-10-13
last_verified: 2026-10-13
category: conventions
module: test/conformance/sim
problem_type: convention
component: conformance-gates
severity: medium
applies_when:
  - "Writing or reviewing a source-size gate that accepts a justification comment on a function declaration or callback."
  - "Associating comments with grouped declarations, nested callbacks, or multiline initializers."
related_components: [testing, sim]
tags: [go-ast, conformance-gate, static-analysis, test-design]
---

# A size reason belongs to one declaration head

A size gate that finds a nearby justification can excuse an oversized callback
with a comment written for its sibling. Commit `10b4e2f7` records that the
previous gate accepted group, trailing and sibling reasons. Its new fixtures
failed before the predicate changed:

```text
--- FAIL: TestSizeGateLiteralHeadReasons/group_reason_does_not_cover_beta (0.00s)
--- FAIL: TestSizeGateLiteralHeadReasons/trailing_declaration_comment (0.00s)
```

Attach a declaration head to its sole literal initializer. Require a standalone
comment immediately above that head or the literal, with only whitespace in
between. A grouped declaration's comment cannot justify each member.
`test/conformance/sim/size_test.go:49-75` limits head ownership:

```go
// A declaration head belongs to a literal only when it initializes that
// literal alone. Group heads cannot document siblings.
```

At `:95-97` the check also rejects trailing and intervening code:

```go
// Lead comments occupy their own lines with no intervening
// tokens. A trailing reason belongs to the preceding code.
if strings.TrimSpace(string(source[lineStart:start])) == "" && strings.TrimSpace(string(source[end:positions.Position(head).Offset])) == "" {
```

For a grouped variable, put the reason on the member it explains:

```go
var (
    alpha = func() {}
    // one unit because its cases share one decoder state.
    beta = func() {
        // The callback body is justified here.
    }
)
```

`TestSizeGateLiteralHeadReasons` (`size_test.go:231-269`) pairs unrelated,
shared and trailing comments with accepted member, local and multiline heads.
Its rejection asserts the exact offending literal's line, so a sibling finding
cannot substitute for the missing one (`:264-266`).

This checks comment ownership and the gate's required reason syntax. It cannot
judge whether a reason makes the oversized body necessary.
