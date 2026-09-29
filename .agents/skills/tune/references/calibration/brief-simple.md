Ensure the Go package `src/common/net/ethernet` (module `go.aledante.io/FlowSeer`) gives `Frame` a `WireOctets` method meeting the contract below. If the method already exists, verify it against the contract and the package's tests, change only what fails, and report; do not rewrite working code. If it does not exist, add it to `ethernet.go` and cover it in `ethernet_test.go`. Either way the test file must exercise every clause of the contract. Done means: `go test -race -count=3 ./src/common/net/ethernet/` passes from the repository root, `gofumpt -l src/common/net/ethernet` prints nothing, and any change is committed on the current branch with a short message. If nothing needed changing, say so and name the commit that holds the existing implementation.

Read first: `src/common/net/ethernet/ethernet.go` (the whole file, for the `Frame` shape and its `Encode` method), `src/common/net/ethernet/ethernet_test.go` (test style: table cases, `t.Run`), `docs/code-style.md`.

Contract:

```go
// WireOctets returns the octets f occupies on the wire.
func (f Frame) WireOctets() int
```

- The figure is `f`'s encoded length raised to a minimum, plus a fixed overhead.
- The minimum is the IEEE 802.3 floor of 60 octets, plus 4 octets for each 802.1Q tag in `f.Tags`. An encoded length at or above that minimum is used as is; a shorter one is raised to the minimum.
- The fixed overhead is 24 octets, for the preamble, start-of-frame delimiter, interpacket gap, and frame check sequence. It is always added, on top of the encoded length or the minimum, whichever applied.
- A frame whose `Encode` fails is still measured. Its encoded length counts as zero, so it lands on the minimum for its tag count rather than reporting the failure. The error belongs to the caller that asks `Encode` for bytes, not to this accessor.

Worked figures: an untagged frame with a 46-octet payload is 84; a 1518-octet frame is 1542; a single-tagged frame below the tagged minimum is 88.

Return, outcome first and nothing else: the changed paths (or "no change"), the exact test command with its last lines of output, and the commit hash. Do not edit files outside `src/common/net/ethernet/`, do not touch `AGENTS.md`, `buf.yaml`, `tools/hooks/`, or `.claude/`, and do not cite plan or ticket identifiers in code or comments. Run only the package's own tests; the coordinator runs the repository verifier after the merge, so skip it even where repository guidance asks for it before handoff. Do not ask questions; if something blocks you, state the blocker and stop. Do not spawn subagents that edit files; read-only subagents are fine. No narration while working; terse register in the report: fragments fine, identifiers and errors exact, code unchanged.
