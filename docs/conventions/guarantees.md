# Package guarantees

A Go package's guaranteed behavior is collected in `GUARANTEES.md` beside its
`README.md`. Each guarantee describes an invariant or contract that callers
rely on and cites the top-level Go test that proves it.

## Scope and placement

`GUARANTEES.md` lives only in Go package directories (alongside `doc.go` or the
package's `.go` source files and `README.md`). It does not belong under
`spec/proto/`, `frontend/web/`, or skill directories. `spec/proto/` contains
only protobuf source files (`AGENTS.md`, Hard boundaries), and the citation
mechanism resolves Go test functions.

A guarantee specifies observable behavior of the exported package API: outcomes,
invariants, state transitions, and error conditions callers depend on. Internal
implementation details, private helper contracts, and transient code layout do
not belong in `GUARANTEES.md`.

## Rollout

Rollout is on touch. A package gains a `GUARANTEES.md` only when a plan touches
that package's behavior. The file initially records only the contracts the plan
introduces or alters. Backfilling guarantees across untouched packages is
avoided because ungrounded backfills create large diffs without review context.

## Relation to README and doc comments

`GUARANTEES.md`, `README.md`, and source doc comments divide responsibilities:

- `GUARANTEES.md` is the normative catalog of guaranteed behavior. It states the
  rules in RFC 2119 / RFC 8174 terms and binds each rule to a test function.
- `README.md` explains the package design, concepts, and reasons to a person
  learning the package. The README explains behavior and links to the relevant
  guarantee in `GUARANTEES.md` rather than restating the normative rule.
- Go doc comments on exported symbols remain the primary contract on the code
  itself (`docs/code-style.md`). Doc comments sit on the declarations and are
  reviewed alongside code changes. When behavior changes, both doc comments and
  `GUARANTEES.md` change in the same commit.

## Block format

Each guarantee is a markdown section identified by its `##` heading:

1. A `##` heading giving the guarantee's name. A rename represents a removal and
   an addition.
2. Exactly one normative sentence using MUST or MUST NOT (RFC 2119 as clarified
   by RFC 8174).
3. One or more scenario bullets in `- WHEN … THEN …` format.
4. Exactly one `Proved by:` line listing comma-separated top-level Go test
   names.

### Example block

```markdown
## Host-key verification has no default

Dial MUST fail immediately when neither WithInsecureHostKey nor WithHostKeyCallback
is provided.

- WHEN Dial is called with an empty Option slice THEN it returns ErrHostKeyVerificationMissing before opening a transport connection.

Proved by: TestHostKeyCallbackRequiresExplicitVerification
```

## Citation rules and test bounds

- Cited tests resolve only to top-level `func Test…(*testing.T)` functions in
  `*_test.go` files located in the exact same directory as `GUARANTEES.md`.
- Citations do not search subdirectories. Subdirectories are separate Go
  packages. Searching subdirectories would allow a subpackage test to mask the
  deletion of a parent package's test.
- Subtests (`t.Run`) cannot be cited because subtest names are runtime strings
  rather than static symbols.
- A guarantee MUST claim only what its cited tests assert. The verifier proves
  that cited test symbols exist; it cannot verify test semantics. A guarantee
  statement broader than its tests creates unproven prose.
