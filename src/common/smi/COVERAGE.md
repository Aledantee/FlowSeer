# SMI diagnostic coverage

Generated from the diagnostic catalog in `internal/catalog` and the
fixtures under `testdata/malformed`. Do not edit by hand — run
`go test ./src/common/smi/ -run TestCoverageMatrixIsCurrent -update-coverage`.

Every code the catalog declares has a fixture that provokes it, and every
fixture asserts the exact set of codes its load produces. A code with no
fixture, and a fixture naming a code the catalog does not carry, both fail
the build.

**33 codes, 33 fixtures.**

| code | severity | fixture | asserted by |
|---|---|---|---|
| `smi/binary-string-not-octets` | 3 | `testdata/malformed/binary-string-not-octets` | `TestMalformedFixtures/binary-string-not-octets` |
| `smi/clause-out-of-order` | 3 | `testdata/malformed/clause-out-of-order` | `TestMalformedFixtures/clause-out-of-order` |
| `smi/content-after-end` | 2 | `testdata/malformed/content-after-end` | `TestMalformedFixtures/content-after-end` |
| `smi/curly-quoted-string` | 3 | `testdata/malformed/curly-quoted-string` | `TestMalformedFixtures/curly-quoted-string` |
| `smi/default-not-permitted` | 2 | `testdata/malformed/default-not-permitted` | `TestMalformedFixtures/default-not-permitted` |
| `smi/dialect-value-mismatch` | 3 | `testdata/malformed/dialect-value-mismatch` | `TestMalformedFixtures/dialect-value-mismatch` |
| `smi/display-hint-malformed` | 2 | `testdata/malformed/display-hint-malformed` | `TestMalformedFixtures/display-hint-malformed` |
| `smi/display-hint-not-permitted` | 2 | `testdata/malformed/display-hint-not-permitted` | `TestMalformedFixtures/display-hint-not-permitted` |
| `smi/display-hint-separator` | 2 | `testdata/malformed/display-hint-separator` | `TestMalformedFixtures/display-hint-separator` |
| `smi/duplicate-clause` | 3 | `testdata/malformed/duplicate-clause` | `TestMalformedFixtures/duplicate-clause` |
| `smi/duplicate-module` | 5 | `testdata/malformed/duplicate-module` | `TestMalformedFixtures/duplicate-module` |
| `smi/hyphen-separator` | 5 | `testdata/malformed/hyphen-separator` | `TestMalformedFixtures/hyphen-separator` |
| `smi/import-cycle` | 3 | `testdata/malformed/import-cycle` | `TestMalformedFixtures/import-cycle` |
| `smi/limit-exceeded` | 1 | `testdata/malformed/limit-exceeded` | `TestMalformedFixtures/limit-exceeded` |
| `smi/missing-clause` | 2 | `testdata/malformed/missing-clause` | `TestMalformedFixtures/missing-clause` |
| `smi/missing-import` | 3 | `testdata/malformed/missing-import` | `TestMalformedFixtures/missing-import` |
| `smi/missing-module-header` | 1 | `testdata/malformed/missing-module-header` | `TestMalformedFixtures/missing-module-header` |
| `smi/module-not-found` | 2 | `testdata/malformed/module-not-found` | `TestMalformedFixtures/module-not-found` |
| `smi/negative-size` | 2 | `testdata/malformed/negative-size` | `TestMalformedFixtures/negative-size` |
| `smi/non-conforming-oid-default` | 3 | `testdata/malformed/non-conforming-oid-default` | `TestMalformedFixtures/non-conforming-oid-default` |
| `smi/odd-hex-string` | 3 | `testdata/malformed/odd-hex-string` | `TestMalformedFixtures/odd-hex-string` |
| `smi/overlapping-range` | 3 | `testdata/malformed/overlapping-range` | `TestMalformedFixtures/overlapping-range` |
| `smi/paired-comment-mode` | 6 | `testdata/malformed/paired-comment-mode`<br>`testdata/malformed/unterminated-comment` | `TestMalformedFixtures/paired-comment-mode`<br>`TestMalformedFixtures/unterminated-comment` |
| `smi/range-not-ascending` | 2 | `testdata/malformed/range-not-ascending` | `TestMalformedFixtures/range-not-ascending` |
| `smi/range-outside-base-type` | 2 | `testdata/malformed/range-outside-base-type` | `TestMalformedFixtures/range-outside-base-type` |
| `smi/trailing-hyphen-identifier` | 3 | `testdata/malformed/trailing-hyphen-identifier` | `TestMalformedFixtures/trailing-hyphen-identifier` |
| `smi/underscore-in-descriptor` | 3 | `testdata/malformed/underscore-in-descriptor` | `TestMalformedFixtures/underscore-in-descriptor` |
| `smi/unexpected-token` | 2 | `testdata/malformed/unexpected-token` | `TestMalformedFixtures/unexpected-token` |
| `smi/unknown-clause` | 2 | `testdata/malformed/unknown-clause` | `TestMalformedFixtures/unknown-clause` |
| `smi/unrecognized-declaration` | 2 | `testdata/malformed/unrecognized-declaration` | `TestMalformedFixtures/unrecognized-declaration` |
| `smi/unresolved-declaration` | 2 | `testdata/malformed/unresolved-declaration` | `TestMalformedFixtures/unresolved-declaration` |
| `smi/unterminated-comment` | 1 | `testdata/malformed/unterminated-comment` | `TestMalformedFixtures/unterminated-comment` |
| `smi/unterminated-string` | 1 | `testdata/malformed/unterminated-string` | `TestMalformedFixtures/unterminated-string` |

## Deliberate tolerances

Each row is a place the parser knowingly reads something the RFCs forbid,
because refusing it costs the corpus more than it reports. Tightening one
means deleting the test beside it, which is the point: a tolerance nobody
wrote down reads as an oversight to the next person who finds it.

| tolerance | pinned by |
|---|---|
| an underscore inside a descriptor is read as part of the name | `TestUnderscoreDescriptorKeepsItsMembersThoughRFC2578ForbidsIt` in `internal/lex/lex_test.go` |
| Windows-1252 curly quotes delimit a string | `TestCurlyQuotedFileKeepsItsDeclarationsThoughSMIWritesASCIIQuotes` in `internal/lex/lex_test.go` |
| a curly-quoted DESCRIPTION still reaches the resolved model | `TestMalformedCurlyQuotedDescriptionKeepsItsDeclarationThoughSMIWritesASCIIQuotes` in `malformed_test.go` |
| an underscored descriptor keeps every member of its enumeration | `TestMalformedUnderscoreDescriptorResolvesItsMembersThoughRFC2578ForbidsIt` in `malformed_test.go` |
| a descriptor ending in a hyphen is read whole | `TestMalformedTrailingHyphenNameIsReadWholeThoughRFC2578ForbidsIt` in `malformed_test.go` |
| clauses written out of their macro's order are kept | `TestMalformedOutOfOrderClauseKeepsItsDeclarationThoughRFC2578FixesTheOrder` in `malformed_test.go` |
| a repeated clause keeps its first occurrence | `TestMalformedDuplicateClauseKeepsTheFirstThoughRFC2578AllowsOne` in `malformed_test.go` |
| a clause value belonging to the other SMI dialect is kept | `TestMalformedDialectValueIsKeptThoughItBelongsToTheOtherSMI` in `malformed_test.go` |
| overlapping range alternatives are both kept | `TestMalformedOverlappingRangeKeepsBothAlternativesThoughRFC2578ForbidsThem` in `malformed_test.go` |
| an OID default written as sub-identifiers keeps its declaration | `TestMalformedOIDDefaultAsSubIdentifiersKeepsItsDeclarationThoughRFC2578HasNoSuchForm` in `malformed_test.go` |
| a symbol used without an IMPORTS clause naming it still resolves | `TestMalformedMissingImportStillResolvesThoughRFC2578RequiresTheImport` in `malformed_test.go` |
| an IMPORTS cycle is broken at the back edge and costs neither module | `TestMalformedImportCycleKeepsBothModulesThoughItHasNoAcyclicOrder` in `malformed_test.go` |
| text after the final END is dropped and the module before it kept | `TestMalformedContentAfterEndKeepsTheModuleBeforeItThoughItBelongsToNoModule` in `malformed_test.go` |
| a file written for paired comments is re-read under that rule | `TestMalformedPairedCommentReparseKeepsTheDeclarationsThoughEndOfLineIsTheDefault` in `malformed_test.go` |
