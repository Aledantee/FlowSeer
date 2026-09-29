Give the Go package `go.aledante.io/FlowSeer/src/edge/netpen/attacks/routing` a correct OSPF LSA checksum. `craftLSAUpdate` and `craftLSAFlush` in `ospf.go` build OSPFv2 Link State Advertisements whose checksum field is left zero; a valid packet carries the checksum defined in RFC 2328 section 12.1.7. Add the function that computes it and populate the field of the crafted LSAs. Done means: `go test -race -count=3 ./src/edge/netpen/attacks/routing/` passes from the repository root, `gofumpt -l src/edge/netpen/attacks/routing` prints nothing, and the change is committed on the current branch with a short message.

Read first: `src/edge/netpen/attacks/routing/ospf.go` (whole file; note the existing `ospfChecksum` for the OSPF packet header and the two LSA-crafting functions), `src/edge/netpen/attacks/routing/routing_test.go` (test style and how crafted frames are asserted), `docs/code-style.md`.

Contract:

```go
// ospfLSAChecksum computes the RFC 2328 section 12.1.7 checksum over an LSA.
func ospfLSAChecksum(lsa []byte) uint16
```

- The checksum is the Fletcher checksum specified in RFC 2328 section 12.1.7, computed over the whole LSA (header and body) starting at the options field.
- The LS age field (the first two octets of the LSA header, offset 16-17 from the LSA start) is excluded from the sum, because routers rewrite it in transit; the two checksum octets themselves count as zero while the sum runs.
- Each LSA that `craftLSAUpdate` and `craftLSAFlush` build carries this value in its checksum field after its length field is set, replacing the zero left there today.

Return, outcome first and nothing else: the changed paths (or "no change"), the exact test command with its last lines of output, and the commit hash. Do not edit files outside `src/edge/netpen/attacks/routing/`, do not touch `AGENTS.md`, `buf.yaml`, `tools/hooks/`, or `.claude/`, and do not cite plan or ticket identifiers in code or comments. Run only the package's own tests; the coordinator runs the repository verifier after the merge, so skip it even where repository guidance asks for it before handoff. Do not ask questions; if something blocks you, state the blocker and stop. Do not spawn subagents that edit files; read-only subagents are fine. No narration while working; terse register in the report: fragments fine, identifiers and errors exact, code unchanged.
