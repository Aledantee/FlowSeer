---
title: Python Scripts Under uv Phase 6, Package-Local Scripts - Plan
type: refactor
date: 2026-10-05
artifact_contract: flowseer-plan/v2
execution: code
---

# Python Scripts Under uv Phase 6, Package-Local Scripts - Plan

## Goal

The scripts that live beside the package that runs them are standalone uv
scripts: `src/protocol/smi/bench/bench-gate.sh`,
`src/protocol/snmp/bench/bench-gate.sh`, the four `deploy/lab/write-*.sh`,
and `src/protocol/snmp/test/integration/scripts/capture-snmprec.sh`. The
means: each becomes `<name>.py` in place with its own metadata block, a Go
test in its package runs it, and the shell file is deleted in the same unit.

**Stop condition:** a Go test cannot start `uv run <script>` where the
verifier runs it. A package-local script then has no test beside it, since
the verifier compiles and tests Python under `tools/scripts/` and
`.claude/skills/*/scripts/` only
(`.claude/skills/verify-change/scripts/verify-change.sh:963` to `:988`).

## Decisions

- The parent plan's Decisions and the
  [repository scripting record](../architecture/2026-10-05-repository-scripting-direction.md)
  apply.
- Each stays in its directory as `<name>.py` with its own metadata block
  and imports nothing from `tools/scripts/`, as the record decides. The
  block is the one `tools/scripts/run.py:1` to `:4` carries:
  `requires-python = "==3.13.*"` and `dependencies = []`. No script gets a
  shebang or an executable bit. Why: the record starts every script as
  `uv run <path>`, and `uv help run` says a file ending in `.py` "will be
  treated as a script". uv 0.12.23 passes the environment and the exit
  status through and writes nothing of its own to either stream once the
  interpreter is installed: a script that ends in `sys.exit(3)` gives
  `uv run` status 3 and an empty standard error.
- The Go tests that run or read them change in the same unit:
  `src/protocol/smi/bench/gate_test.go:44` runs `sh bench-gate.sh`, and
  `src/services/device/test/integration/lab_fixtures_test.go` reads the
  text of `write-lab-secrets.sh` in three places (`:381`, `:401`, and
  `:422`, the last checking `deploy/lab/README.md` against the script) and
  the text of `write-openfga-store.sh` at `:438`.
- A script with no test gets one in its package, written to take the
  command under test, run against the shell file first and the Python file
  second. Why: the parent decides that each port is proven that way, and
  only Go tests reach a package-local script. The tests copy a lab script
  into a temporary tree and run it there, because the scripts write beside
  themselves (`write-lab-secrets.sh:8` to `:9`). The same test body then
  runs either file with no new flag.
- Where a script starts a tool the test cannot have (`go test -bench` for
  the SNMP gate, `snmpwalk` against a device), the test puts a stand-in
  first on `PATH`: a file it writes into `t.TempDir()` that prints a
  fixture. The stand-in is a POSIX `sh` file, so those tests skip on
  Windows. Why: it needs no change to the shell file before the port, and
  the alternative, a new input flag on each script, cannot be run against
  the shell.
- The `dex.env` assertion is restated against the written file. The test
  runs the secrets script, reads `secrets/dex.env`, and requires every line
  to match `^[A-Z_]+='[^']+'$`, each hash line to hold
  `$2y$10$` and 53 more characters, and `bcrypt.CompareHashAndPassword` to
  accept the hash with the password `credentials.txt` gives for that user.
  Why: the old test matched a here-document's source line. The new one
  holds the property itself, that a bcrypt hash reaches Dex unsubstituted.
  `golang.org/x/crypto` is already required (`go.mod:40`), and its
  `bcrypt` reads any minor version byte, `y` included
  (`bcrypt/bcrypt.go:274` to `:277` at v0.57.0).
- The task files (`src/protocol/smi/bench/Taskfile.yml:54`,
  `src/protocol/snmp/bench/Taskfile.yml:132`) call `uv run bench-gate.py`.
- `openssl` stays a subprocess for the certificate work, with the argument
  lists `write-lab-secrets.sh:47` to `:65` holds. Why: the script generates
  two RSA keys, a self-signed CA, and a server certificate signed by it,
  and Python 3.13 has no API for any of them. `dir(ssl)` holds no name that
  creates a key, a request, or a signature. A cryptography package is not
  added. The process substitution at `:65` becomes a file in a
  `tempfile.TemporaryDirectory()` outside `secrets/`.
- `htpasswd` stays a subprocess too, as `["htpasswd", "-niB", "-C", "10",
  "dummy"]` with the password on standard input. Why: Python 3.13 has no
  bcrypt. `import crypt` fails with `No module named 'crypt'`, and no name
  in `hashlib.algorithms_available` holds `bcrypt`. That Dex refuses a cost
  below 10 is carried from `write-lab-secrets.sh:81` to `:82` and is
  unverified here.
- `secrets.token_hex` replaces the four `openssl rand -hex` calls
  (`write-lab-secrets.sh:70`, `:74`, `:78`, `:79`). Why: its documentation
  reads "The string has *nbytes* random bytes, each byte converted to two
  hex digits", and the module's reads "cryptographically strong
  pseudo-random numbers suitable for managing secrets" (`pydoc secrets`
  under Python 3.13.2). `openssl` is then called 3 times, down from 7.
- `json` and `urllib` replace `jq` and `curl` in the lab scripts, and
  `base64` and `bytes` replace `base64 -d` and `xxd`.
- `write-openfga-store.py` verifies the server against `secrets/ca.crt`
  with `ssl.create_default_context(cafile=...)` and clears
  `ssl.VERIFY_X509_STRICT` on it. Why: Python 3.13 sets that flag by
  default, and with it the lab chain is refused. Against a server holding
  a certificate the three `openssl` calls above produce, `urlopen` fails
  with `CA cert does not include key usage extension` (OpenSSL 3.6.5) or
  `Missing Authority Key Identifier` (LibreSSL 3.3.6, the macOS
  `/usr/bin/openssl`). With the flag cleared both chains verify, and a
  different CA still fails with `unable to get local issuer certificate`.
  That is the check `curl --cacert` made. Changing the generated
  certificates was the alternative. It needs two extension sets that
  differ between the two `openssl` builds, and it breaks every existing
  `secrets/` directory. U3's test pins this with the script's own output.
- `deploy/lab/README.md`, `docs/runbooks/lab-icx7150-first-write.md`, and
  the snmp integration README name the new commands.
- The runbook's blocks stay shell, and only the two script paths in them
  change (`docs/runbooks/lab-icx7150-first-write.md:197`, `:223`). Why:
  `runbook_test.go:189` runs the blocks as one `sh -c` script because "an
  export in one is depended on by the next" (`runbook_test.go:144` to
  `:147`), and they hold `kill`, `wait`, and `until` loops over background
  jobs. They are documentation an operator pastes. The parent's
  requirement 3 counts tracked `.sh` files and its requirement 4 scans for
  `python3`, and a fenced block is neither. The same holds for the `curl`
  and `jq` lines an operator types from `deploy/lab/README.md`.
- A port keeps the verdicts and output of its shell file, defects included,
  except where a Change line names a difference. Why: the parent treats a
  changed verdict as a change nobody asked for. Two known defects stay and
  are listed under Open questions.

## Requirements

1. Each bench gate gives the same verdict. Example: `gate_test.go` passes
   with the command changed to `uv run bench-gate.py`, on its existing
   transcripts.
2. The lab secrets script writes owner-only files. Example: every file
   under the output directory has mode `0600` on POSIX.
3. `dex.env` holds each value literally. Example: a generated bcrypt hash
   `$2y$10$...` appears in the file unchanged and single-quoted.
4. The store script authenticates to a server the lab CA signed and prints
   the `authorization` block. Example: against a TLS server holding the
   generated `server.crt`, answering `{"id":"S1"}` and
   `{"authorization_model_id":"M1"}`, the output holds `store_id: "S1"` and
   `model_id: "M1"`, and both requests carried `Authorization: Bearer
   <openfga.key>`.
5. A trust anchor survives the provisioning script byte for byte. Example:
   an anchor whose JSON value is the base64 of bytes `0xe0` to `0xff`
   unmarshals from the output as those 32 bytes.
6. The registry script refuses the shipped template while it is unfilled.
   Example: `uv run deploy/lab/write-registry.py some-id` exits 1 and names
   `192.0.2.6`.
7. The capture script writes the lines the shell wrote for the same
   `snmpwalk` output. Example: `.1.3.6.1.2.1.1.5.0 = STRING: "sw1"` becomes
   `1.3.6.1.2.1.1.5.0|4|sw1`.

## Out of scope

- The verifier. It is a policy surface, and compiling package-local Python
  there belongs to the phase that ports it.
- The certificates the secrets script generates. They keep today's
  extensions.
- The operator's own `curl`, `jq`, and `buf curl` lines in
  `deploy/lab/README.md` and the runbook.
- The in-container script
  `src/protocol/snmp/test/integration/testdata/snmpd/pass-scripts/endofmibview.sh`,
  which the record exempts.
- The scripts read files a contributor or an operator wrote, a response
  from the lab's own OpenFGA, and `snmpwalk` output from a device the
  operator chose. All are trusted. A malformed value fails the script with
  its name on standard error.

## Units

Every Python file follows `docs/code-style-python.md`: subprocesses take an
argument list, paths are `pathlib`, a failure exits non-zero and names
itself on standard error, and the module does no work at import. A script
flushes its own output before it starts a child that writes to the same
stream.

### U1. SMI bench gate

Files: src/protocol/smi/bench/bench-gate.py, src/protocol/smi/bench/bench-gate.sh, src/protocol/smi/bench/gate_test.go, src/protocol/smi/bench/Taskfile.yml, src/protocol/smi/bench/doc.go
After: none
Change: `bench-gate.py` reads `COUNT`, `BENCH`, `GATE_NS`, `MIN_DELTA`,
`BASELINE`, and `RAW_IN` with the defaults at `bench-gate.sh:54` to `:59`.
It exits 2 on a missing baseline, a missing `benchstat`, or zero
`Benchmark` rows, and 1 on a hard regression, with the message texts the
shell prints. It keeps the row filter of `:84`, prints the plain `benchstat`
table, and reads `benchstat -format csv` with the `csv` module under the
rules of `:108` to `:132`. A delta is a regression only when it starts with
`+`. Its size is `float()` of the text before `%`, which reads `+Inf` as
infinity. The header comment's reasoning moves into the module docstring.
`LC_ALL=C` is dropped, since `float()` ignores the locale.
`gate_test.go` runs the command from a package variable, set to `sh
bench-gate.sh` for the first run and to `uv run bench-gate.py` after. The
comment at `gate_test.go:15` to `:24` describes a Python gate. The task
file and `doc.go:43` name `bench-gate.py`. `bench-gate.sh` is deleted.
Tests: the seven `TestGate*` cases in `gate_test.go`, unchanged in what
they assert, plus `TestGateRefusesAMissingBaseline` (`BASELINE` naming no
file exits 2).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/smi/bench/bench-gate.py src/protocol/smi/bench/bench-gate.sh src/protocol/smi/bench/gate_test.go src/protocol/smi/bench/Taskfile.yml src/protocol/smi/bench/doc.go`

### U2. SNMP bench gate

Files: src/protocol/snmp/bench/bench-gate.py, src/protocol/snmp/bench/bench-gate.sh, src/protocol/snmp/bench/gate_test.go, src/protocol/snmp/bench/Taskfile.yml, src/protocol/snmp/bench/doc.go, docs/solutions/architecture-patterns/snmp-collection-library-architecture-and-fast-path-conventions.md
After: none
Change: `bench-gate.py` reads `COUNT`, `BENCH`, and `GATE_NS`, runs `go
test -bench` with the arguments of `bench-gate.sh:51`, keeps the lines
matching `:53`, and applies the rules of `:62` to `:80`. It has no
`MIN_DELTA`, no `BASELINE`, and no `RAW_IN`, as the shell has none. A run
whose output matches no line exits 1, as `grep` under `set -e` does at
`:53`, and now says so. The new `gate_test.go` holds the command in a
package variable as U1 does and gives the gate a stand-in `go` that prints
a transcript. The task file, `doc.go:72`, and the solution document's
citations at lines 309 to 386 name `bench-gate.py` and its lines.
`bench-gate.sh` is deleted.
Tests: in `gate_test.go`, against `testdata/baseline-micro.txt` as the
transcript. `TestGatePassesAgainstBaseline` (exit 0, `perf-gate: PASS`).
`TestGateFailsOnAllocationRegression` (`allocs/op` of
`BenchmarkGet/impl=flowseer` times 1.5 exits 1 and names
`Get/impl=flowseer`). `TestGateTreatsWallTimeAsAdvisory` (`ns/op` times 2
exits 0 with `advisory`, and exits 1 under `GATE_NS=1`).
`TestGateFailsOnAnEmptyRun` (an empty transcript exits 1). Nothing covers
the `go test` argument list, which the stand-in ignores.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/snmp/bench/bench-gate.py src/protocol/snmp/bench/bench-gate.sh src/protocol/snmp/bench/gate_test.go src/protocol/snmp/bench/Taskfile.yml src/protocol/snmp/bench/doc.go docs/solutions/architecture-patterns/snmp-collection-library-architecture-and-fast-path-conventions.md`

### U3. Lab secrets and store scripts

Files: deploy/lab/write-lab-secrets.py, deploy/lab/write-lab-secrets.sh, deploy/lab/write-openfga-store.py, deploy/lab/write-openfga-store.sh, deploy/lab/README.md, deploy/lab/central.textproto, src/services/device/test/integration/lab_fixtures_test.go, src/services/device/test/integration/lab_scripts_test.go
After: none
Change: `write-lab-secrets.py` sets `os.umask(0o077)` before anything
else, requires `openssl` and `htpasswd` on `PATH`, refuses an existing
`secrets/` beside itself, and removes the directory when any step fails.
It writes the nine files the shell leaves, with the same line formats, and
creates the ones it writes itself with mode `0600` and `O_EXCL`. A value
holding a single quote fails the run before `dex.env` is written.
`write-openfga-store.py` keeps the environment variables, the file checks,
the two requests, the failure messages, and the printed block of the
shell, and starts no process. An HTTP error status is read as a response
body, as `curl -sS` reads it. `lab_scripts_test.go` holds
`runLabScript(t, tree, name, args...)`, which copies a script to
`<tree>/deploy/lab/` and starts the interpreter the first line of a `.sh`
file names (`bash` for the secrets and store scripts, which use
`BASH_SOURCE`) and `uv run` for a `.py` name. A `sync.Once` generates one
`secrets/` for the package in an `os.MkdirTemp` directory that a new
`TestMain` removes, since a `t.TempDir()` ends with the first test that
asked. The package has no `TestMain` today. The four tests at `lab_fixtures_test.go:377` to `:443` move
there, restated, and `heredocBody` is deleted. The README (`:13`, `:14`,
`:29`, `:43`, `:423`) and `central.textproto:4` name the Python files as
`uv run write-lab-secrets.py` and `uv run write-openfga-store.py`. Both
shell files are deleted.
Tests: in `lab_scripts_test.go`, skipping when `openssl` or `htpasswd` is
absent and on Windows.
`TestTheLabSecretsScriptWritesOwnerOnlyFiles`: `secrets/` holds exactly
`ca.crt`, `ca.key`, `server.crt`, `server.key`, `openfga.key`,
`dex_client.secret`, `openfga.env`, `dex.env`, and `credentials.txt`, each
`0600`. `TestTheLabSecretsScriptQuotesTheDexEnvFile`, as the Decision
states it. `TestTheLabSecretsScriptHashesAtDexsCostFromStdin`:
`bcrypt.Cost` of each hash is 10, and the script's text holds the
`htpasswd` argument list with no further element. The test finds the
line holding `htpasswd`, removes `[`, `]`, `"`, `,`, and `)`, and requires
the fields from `htpasswd` on to start with `htpasswd -niB -C 10 dummy`
and, in the Python file, to end there. One body then reads both files.
`TestTheLabReadmeReadsOnlyFilesTheSecretsScriptWrites`: every
`secrets/<name>` in the README exists in the generated directory.
`TestTheLabSecretsScriptRefusesAnExistingDirectory`: a second run exits 1
and changes no file. `TestTheLabStoreScriptPrintsTheAuthorizationBlock`:
requirement 4's example, with the model body equal to `model.json`
copied into the tree, served by an `httptest` server whose certificate is
the generated `server.crt`. This is the test that fails if the strict flag
is left set. `TestTheLabStoreScriptFailsWithoutAStoreId`: a `{}` answer
exits 1. `TestTheLabStoreScriptStartsNoProcess` replaces the `curl`
argument test: the script's text does not import `subprocess`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- deploy/lab/write-lab-secrets.py deploy/lab/write-lab-secrets.sh deploy/lab/write-openfga-store.py deploy/lab/write-openfga-store.sh deploy/lab/README.md deploy/lab/central.textproto src/services/device/test/integration/lab_fixtures_test.go src/services/device/test/integration/lab_scripts_test.go`

### U4. Lab registry and provisioning scripts

Files: deploy/lab/write-registry.py, deploy/lab/write-registry.sh, deploy/lab/write-provisioning.py, deploy/lab/write-provisioning.sh, deploy/lab/registry.textproto, deploy/lab/README.md, docs/runbooks/lab-icx7150-first-write.md, src/services/device/test/integration/lab_render_scripts_test.go
After: U3
Change: `write-registry.py` takes `<edge-id> [template]` and
`FLOWSEER_REGISTRY_TEMPLATE` in the order of `write-registry.sh:18`, exits
1 when the template lacks the placeholder, and refuses the shipped
template while it holds the placeholder address or digest. It compares
resolved paths, so a second spelling of the shipped path is refused too,
which the string comparison at `:41` let through. It replaces every
occurrence of the placeholder where `sed` replaced the first on a line.
The shipped template has one (`registry.textproto:17`).
`write-provisioning.py` takes `<created.json> <central-url>`, exits 1 on a
missing setup key, an empty anchor list, or an anchor that is not base64,
and renders every anchor before it prints a line. Each anchor byte is
written as `\xNN` in lower-case hex. It exits 1 when the setup key or the
URL holds a double quote or a backslash, since both are printed between
quotes unescaped. Both write bytes to standard output with `\n` line ends.
The runbook (`:163`, `:197`, `:223`), the README (`:18`, `:97`, `:99`), and
`registry.textproto:6` and `:8` name the Python files, and the two runbook
commands become `uv run "$FLOWSEER_REPO"/deploy/lab/<name>.py ...`. Both
shell files are deleted.
Tests: in `lab_render_scripts_test.go`, through U3's `runLabScript`.
`TestTheLabProvisioningScriptKeepsAnchorBytes`: requirement 5's example
with a second anchor of bytes `0x00` to `0x1f`, read back with `prototext`
into `EdgeProvisioning`, the message `lab_fixtures_test.go:90` already
reads. `TestTheLabProvisioningScriptWritesNothingOnBadInput`: a missing
setup key, an empty `trustAnchors`, and an anchor `!!!` each exit non-zero
with an empty standard output. The last case is run against the Python
only, since `write-provisioning.sh:47` takes the status of `tr` and can
print a truncated anchor. `TestTheLabRegistryScriptRendersTheEdge`: a
temporary template through `FLOWSEER_REGISTRY_TEMPLATE` yields the edge
identifier and no placeholder. `TestTheLabRegistryScriptRefusesTheShippedTemplate`:
requirement 6's example, run in place. `TestTheRunbooksBootstrapBringsUpADeployment` (`bootstrap_test.go:33`)
runs both scripts from the runbook's text, since its section at `:45`
holds the two blocks, and must pass before and after. It skips without
`buf`, `jq`, or `go` and now fails without `uv`, which the verifier
already requires (`verify-change.sh:559`).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- deploy/lab/write-registry.py deploy/lab/write-registry.sh deploy/lab/write-provisioning.py deploy/lab/write-provisioning.sh deploy/lab/registry.textproto deploy/lab/README.md docs/runbooks/lab-icx7150-first-write.md src/services/device/test/integration/lab_render_scripts_test.go`

### U5. SNMP capture script

Files: src/protocol/snmp/test/integration/scripts/capture-snmprec.py, src/protocol/snmp/test/integration/scripts/capture-snmprec.sh, src/protocol/snmp/test/integration/capture_script_test.go, src/protocol/snmp/test/integration/README.md, src/protocol/snmp/test/integration/testdata/snmprec/manifest.yaml
After: none
Change: `capture-snmprec.py` parses `--target` (required), `--community`,
`--root`, and `--output` with `argparse` and the defaults at
`capture-snmprec.sh:35` to `:38`, exits 1 when `snmpwalk` is not on
`PATH`, and runs it with the arguments of `:85`. It converts each line by
the rules of `:87` to `:129`, including the two the Open questions name:
the value is the second field of a split on ` = ` (empty for a line
without one, such as a `Hex-STRING` continuation, which then prints as
`<line>|4|`), the type is the text before the first colon, and the value
starts two characters after that colon whatever the skipped character is
(`:94`). A missing `--target` exits 2 with the `argparse` usage on
standard error, where the shell exits 1 and prints its header. With no `--output` it writes to standard
output. The README (`:154`) and the comment at `manifest.yaml:13` name the
Python file as `uv run .../capture-snmprec.py`. The verifier lists
`manifest.yaml` as a changed test fixture, and the change is that comment.
The shell file is deleted.
Tests: `capture_script_test.go`, untagged like `manifest_test.go`, with
the command in a package variable and a stand-in `snmpwalk` printing a
fixture. `TestCaptureConvertsSnmpwalkLines` expects, line for line:
`.1.3.6.1.2.1.1.5.0 = STRING: "sw1"` to `1.3.6.1.2.1.1.5.0|4|sw1`,
`.1.3.6.1.2.1.2.1.0 = INTEGER: 3` to `...|2|3`,
`.1.3.6.1.2.1.1.3.0 = Timeticks: (12345) 0:02:03.45` to `...|67|12345`,
`.1.3.6.1.2.1.31.1.1.1.6.1 = Counter64: 9` to `...|70|9`,
`.1.3.6.1.2.1.1.2.0 = OID: .1.3.6.1.4.1.9` to `...|6|.1.3.6.1.4.1.9`,
the untyped `.1.3.6.1.2.1.1.4.0 = admin` to `...|4|admin`,
`.1.3.6.1.2.1.1.9.0 = X:yz` to `...|4|z`, and the continuation line
`0A 0B` to `0A 0B|4|`.
`TestCaptureWritesTheOutputFile` (`--output` in a temporary directory holds
the same lines). `TestCaptureRequiresATarget` (no `--target` exits
2 for the Python, non-zero for the shell).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/snmp/test/integration/scripts/capture-snmprec.py src/protocol/snmp/test/integration/scripts/capture-snmprec.sh src/protocol/snmp/test/integration/capture_script_test.go src/protocol/snmp/test/integration/README.md src/protocol/snmp/test/integration/testdata/snmprec/manifest.yaml`

Waves: U1 U2 U3 U5 | U4

No unit names a file the phase 2 plan names in a `Files:` line.

## Verification

- `go test -C src/protocol/smi/bench -run TestGate .` and
  `go test -C src/protocol/snmp/bench -run TestGate .` pass, with
  `benchstat` on `PATH`.
- `go test ./src/services/device/test/integration/ -run
  'TestTheLab|TestTheRunbook'` passes with `openssl`, `htpasswd`, `buf`,
  and `jq` on `PATH`.
- `go test ./src/protocol/snmp/test/integration/ -run TestCapture` passes.
- `git ls-files '*.sh' -- src/protocol/smi/bench src/protocol/snmp/bench
  deploy/lab src/protocol/snmp/test/integration/scripts` prints nothing.
- `git grep -n -E 'bench-gate\.sh|write-[a-z-]+\.sh|capture-snmprec\.sh'
  -- ':!docs/plans'` prints nothing.
- Manual, on a host with Docker: the "Lab run" steps 1 to 3 of
  `deploy/lab/README.md`, ending with an `authorization` block from a real
  OpenFGA.

## Definition of done

- [ ] The verifier is green for every changed path.
- [ ] Each test ran against the shell file before that file was deleted,
      and the implement report names the command and its last line.
- [ ] Every line the verifier lists as a removed test, an added skip, or a
      changed fixture carries its reason: the four moved lab tests, the
      Windows and missing-tool skips, and `manifest.yaml`.
- [ ] `deploy/lab/README.md`, the runbook, and the snmp integration README
      name the Python commands.
- [ ] This plan's outcome is recorded with
      `.claude/skills/plan/scripts/plan_record.py implemented <plan>
      --units <n> --from <t> --to <t>` or `partial`, or with the command
      that replaces it if the phase that moves `plan_record.py` has landed.
- [ ] No plan labels in code.

## Open questions

- Does `capture-snmprec` ever see a type label? It runs `snmpwalk -OnQU`,
  and `man snmpcmd` says `-OQ` "Removes the type information when
  displaying varbind values". Every row would then be type 4, and a value
  holding a colon would lose its head to the type split. Unverified without
  a device. The port keeps the shell's output, and a fix is a separate
  change with a capture from a real agent as its fixture.
- The SNMP gate passes a run that holds the preamble and no benchmark row,
  the case `src/protocol/smi/bench/bench-gate.sh:86` to `:97` guards. The
  port keeps the verdict. Adding the guard changes a verdict and is left
  to a person.
- Dex's minimum bcrypt cost of 10 is unverified. The port keeps `-C 10`.
- The first `TestTheLab*` run generates a 4096-bit RSA key, which takes
  seconds. If that is too slow for the targeted verifier run, the
  implementer reports it and does not shrink the key.
