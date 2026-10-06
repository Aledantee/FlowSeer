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
  `requires-python = "==3.13.*"` and `dependencies = []`, except in
  `write-lab-secrets.py`, whose block the Decisions below give. No script
  gets a shebang or an executable bit. Why: the record starts every script as
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
  `$2b$10$` and 53 more characters, and `bcrypt.CompareHashAndPassword` to
  accept the hash with the password `credentials.txt` gives for that user.
  Why: the old test matched a here-document's source line. The new one
  holds the property itself, that a bcrypt hash reaches Dex unsubstituted.
  `golang.org/x/crypto` is already required (`go.mod:40`), and its
  `bcrypt` reads any minor version byte, `b` and `y` included
  (`bcrypt/bcrypt.go:274` to `:277` at v0.57.0). The shell file writes
  `$2y$`, so the run against it takes the prefix `$2y$10$`. That is the
  one expectation that differs between the two runs.
- The task files (`src/protocol/smi/bench/Taskfile.yml:54`,
  `src/protocol/snmp/bench/Taskfile.yml:132`) call `uv run bench-gate.py`.
- The lab scripts start neither `openssl` nor `htpasswd`. Third-party
  packages declared in the script's metadata block do that work. Why: uv
  exists so a script can add dependencies. This replaces two earlier
  Decisions of this plan, that `openssl` and `htpasswd` each stay a
  subprocess. (decided by the user, 2026-10-06)
- The packages are `cryptography==50.0.1` for the keys and certificates and
  `bcrypt==5.0.0` for the password hashes, both in the block of
  `write-lab-secrets.py` and nowhere else. Why: Python 3.13 has neither.
  `dir(ssl)` holds no name that creates a key or a signature, and `import
  crypt` fails with `No module named 'crypt'`. `cryptography` has no
  password-hashing bcrypt of its own. Its only use of the name is the
  extra `bcrypt>=3.1.5; extra == "ssh"`
  (https://pypi.org/pypi/cryptography/50.0.1/json, `requires_dist`), so one
  package cannot do both jobs. The versions are the newest past the 14-day
  wait of the
  [dependency admission record](../architecture/2026-10-01-dependency-admission-direction.md)
  on 2026-10-06. PyPI gives these upload times
  (`https://pypi.org/pypi/<name>/json`, `releases`): `cryptography` 50.0.1
  on 2026-08-25 and 50.0.2 on 2026-09-30, `bcrypt` 5.0.0 on 2025-09-25.
  50.0.2 is six days old and leaves the wait on 2026-10-14. Taking it then
  is a pin move with its own reason, as that record requires, and is not
  part of this plan.
- The dependency tree is four versions: `cryptography` 50.0.1, `bcrypt`
  5.0.0, `cffi` 2.1.1 (uploaded 2026-08-03), and `pycparser` 3.0 (uploaded
  2026-01-21). `cryptography` requires `cffi>=2.0.0` on CPython and `cffi`
  requires `pycparser` (the same PyPI records). The OSV batch query
  (https://api.osv.dev/v1/querybatch, ecosystem `PyPI`) returned no
  advisory for any of the four on 2026-10-06, and none for `cryptography`
  50.0.2.
- The pin is a lock file beside the script, `write-lab-secrets.py.lock`,
  written by `uv lock --script deploy/lab/write-lab-secrets.py`. The block
  also holds `[tool.uv]` with `exclude-newer` set to the day 14 days before
  the lock is written. Why: the
  [repository scripting record](../architecture/2026-10-05-repository-scripting-direction.md)
  names that command as the hash pin, and `uv help lock` says it locks the
  script "to a `.lock` file adjacent to the script itself". Under uv
  0.12.23 the file holds a `sha256` for the source archive and every wheel
  of all four packages, and a run from an empty cache with one digest
  altered exits 1 with `Hash mismatch`. `exclude-newer` makes uv enforce
  the wait as pnpm's `minimumReleaseAge` does: with the cutoff
  `2026-09-22T00:00:00Z`, `uv lock --script` refuses `cryptography==50.0.2`
  and names its upload time. The lock records the cutoff under `[options]`.
- The secrets script is started as `uv run --locked write-lab-secrets.py`,
  in the README and in the test. Why: a plain `uv run` rewrites a lock that
  no longer matches the block and exits 0, and it resolves without a pin
  when the lock file is missing. With `--locked`, uv 0.12.23 exits 1 on a
  stale lock ("The lockfile at `uv.lock` needs to be updated") and 2 on a
  missing one. The other scripts have no dependency and no lock, and
  `--locked` fails for them, so they keep the plain `uv run`. One more
  property of that uv version: `uv lock --script` keeps the digests of an
  existing lock without checking them, so a lock is regenerated by deleting
  the file first.
- Each of the two direct dependencies gets a statement at
  `docs/dependencies/statements/pypi/<name>.md` with `ecosystem: pypi`,
  `required_by` naming `deploy/lab/write-lab-secrets.py`, `criteria: run`,
  `verdict: keep`, and an empty `approved`. Why: the admission record gives
  every direct dependency a statement and names no directory. The shape
  `statements/<ecosystem>/<name>.md` is the one the gate builds
  (`tools/deps/inventory/statements.go`, `statementPath`), and `go` and
  `npm` are the lower-case OSV ecosystem names, which makes this one
  `pypi`. The criterion is `run` because nothing under `deploy/lab/` is in
  the non-test build of a package under `src/`. `approved` stays empty
  because a person rules on a statement, and
  `docs/dependencies/README.md` says an empty value "means that the verdict
  still needs that ruling". Unconfirmed, repeated under Open questions.
  `cffi` and `pycparser` are transitive. Their per-version records belong
  to `docs/dependencies/records/`, which does not exist yet, so each
  statement names the four versions and says the source is not yet
  reviewed, as the existing statements do.
Ruled: Python statements live at `docs/dependencies/statements/pypi/<package>.md`. Why: the user answered the open question on 2026-10-06. Cost if wrong: a move of two files and the `required_by` of nothing else.
Ruled: the statements are written and a person approves each one before the script's dependency block or lock exists. Why: the user answered the open question on 2026-10-06, which makes the earlier order (statements and block in one unit) obsolete. Cost if wrong: none beyond the wait for the approval.
- The certificates keep the shell's keys, names, and lifetime and gain the
  extensions a strict verifier asks for. The CA is RSA 4096 with subject
  `CN=FlowSeer Lab CA`, and the server is RSA 2048 with subject
  `CN=localhost`. Both are signed with SHA-256, valid 365 days, with a
  random serial. The CA carries a critical `basicConstraints` of `CA:TRUE`,
  a critical `keyUsage` of `keyCertSign` and `cRLSign`, a subject key
  identifier, and an authority key identifier. The server carries a
  critical `basicConstraints` of `CA:FALSE`, a critical `keyUsage` of
  `digitalSignature` and `keyEncipherment`, the subject alternative names
  `DNS:localhost` and `IP:127.0.0.1`, the extended key usages `serverAuth`
  and `clientAuth`, and both key identifiers. Keys are written as
  unencrypted PKCS #8 PEM (`-----BEGIN PRIVATE KEY-----`), which is what
  `openssl req -nodes` writes under OpenSSL 3.6.5. Why: the builder calls are the ones the package's own
  tutorial uses for a root and a leaf
  (https://cryptography.io/en/50.0.1/x509/tutorial/, "Creating a CA
  hierarchy"). A chain built this way under `cryptography` 50.0.1 verifies
  for `localhost` and `127.0.0.1` in Python 3.13.2 (OpenSSL 3.0.16, the
  interpreter uv installs) with `ssl.VERIFY_X509_STRICT` set, and in Go
  1.27.1 through `x509.Certificate.Verify` and `tls.LoadX509KeyPair`, whose
  `parsePrivateKey` tries PKCS #8 first (`crypto/tls/tls.go:361`). No CSR
  file and no serial file is written, so nothing needs removing afterwards.
- The hash is `bcrypt.hashpw(password, bcrypt.gensalt(rounds=10))`, which
  writes `$2b$10$` and 53 more characters. Why: the wheel's stub declares
  `gensalt(rounds: int = 12, prefix: bytes = b"2b")`
  (`bcrypt/__init__.pyi` in 5.0.0), and `gensalt(prefix=b"2y")` raises
  `Supported prefixes are b'2a' or b'2b'`, so the `$2y$` of `htpasswd`
  cannot be kept. Dex reads either. At the tag of the lab's image,
  `dexidp/dex:v2.45.1` (`deploy/lab/compose.yaml:62`), a static password
  login calls `checkCost` and then `bcrypt.CompareHashAndPassword` from
  `golang.org/x/crypto` (`server/server.go:568` to `:575`), and `checkCost`
  refuses a cost below `bcrypt.DefaultCost`, which is 10
  (`server/api.go:175` to `:187`,
  https://raw.githubusercontent.com/dexidp/dex/v2.45.1/server/api.go).
  That source is the tag on the forge. The image digest was not read.
- After this phase the lab scripts start no process. `json`, `urllib`,
  `base64`, and `secrets` cover what `jq`, `curl`, `base64 -d`, `xxd`, and
  `openssl rand` did, as the next Decisions state. The processes that
  remain in the phase are `go test -bench` and `benchstat` in the SMI
  gate, `go test -bench` in the SNMP gate, and `snmpwalk` in the capture
  script.
- `secrets.token_hex` replaces the four `openssl rand -hex` calls
  (`write-lab-secrets.sh:70`, `:74`, `:78`, `:79`). Why: its documentation
  reads "The string has *nbytes* random bytes, each byte converted to two
  hex digits", and the module's reads "cryptographically strong
  pseudo-random numbers suitable for managing secrets" (`pydoc secrets`
  under Python 3.13.2).
- `json` and `urllib` replace `jq` and `curl` in the lab scripts, and
  `base64` and `bytes` replace `base64 -d` and `xxd`.
- `write-openfga-store.py` verifies the server against `secrets/ca.crt`
  with `ssl.create_default_context(cafile=...)` and changes no flag on it.
  Why: Python 3.13 sets `ssl.VERIFY_X509_STRICT` by default, and the chain
  the secrets script now writes passes it. A different CA still fails with
  `unable to get local issuer certificate`, which is the check
  `curl --cacert` made. Clearing the flag was this plan's earlier answer,
  taken because two `openssl` builds needed two extension sets. One
  generator removes that reason.
- A `secrets/` directory the shell script wrote is not accepted by the
  store script. Its CA has no key usage and its server certificate no
  authority key identifier, and the strict default refuses the chain with
  `CA cert does not include key usage extension` or `Missing Authority Key
  Identifier`. The store script prints that reason and exits 1. The remedy
  is to delete `secrets/`, run the secrets script, and recreate the
  containers, since the preshared key, the client secret, and the hashes
  change with the directory. Why: `AGENTS.md`, Agent behavior, rules out a
  path that preserves a landed shape, and the directory is untracked lab
  state. Running containers and the operator's `curl --cacert` lines are
  not affected by the old chain.
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
   `$2b$10$...` appears in the file unchanged and single-quoted.
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
- The statement gate. `tools/deps/inventory/statements.go` walks
  `statements/go/` and `statements/npm/` and reads Go manifests and the
  pnpm lockfile, so it neither checks nor rejects a file under
  `statements/pypi/`. Reading script metadata blocks there is dependency
  admission work.
- `src/edge/netpen/layers/harvest.py`, which declares `scapy>=2.5` with no
  lock and no statement.
- Moving `cryptography` to 50.0.2 once its wait ends.
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

Files: deploy/lab/write-lab-secrets.py, deploy/lab/write-lab-secrets.py.lock, deploy/lab/write-lab-secrets.sh, deploy/lab/write-openfga-store.py, deploy/lab/write-openfga-store.sh, deploy/lab/README.md, deploy/lab/central.textproto, docs/dependencies/statements/pypi/cryptography.md, docs/dependencies/statements/pypi/bcrypt.md, docs/dependencies/README.md, src/services/device/test/integration/lab_fixtures_test.go, src/services/device/test/integration/lab_scripts_test.go
After: none
Change: `write-lab-secrets.py` carries the block the Decisions give:
`requires-python = "==3.13.*"`, `dependencies = ["cryptography==50.0.1",
"bcrypt==5.0.0"]`, and `[tool.uv]` with `exclude-newer`. Its lock file is
committed beside it. The script sets `os.umask(0o077)` before anything
else, refuses an existing `secrets/` beside itself, and removes the
directory when any step fails. It starts no process and checks for no tool
on `PATH`. It builds the two keys and the two certificates the Decisions
describe, and hashes each password with `bcrypt` at cost 10. It writes the
nine files the shell leaves, with the same line formats, and creates every
one with mode `0600` and `O_EXCL`. A value holding a single quote fails
the run before `dex.env` is written. The two statements are written from
the facts in the Decisions, with the three sections
`docs/dependencies/README.md` lists, and that README names
`statements/pypi/` and says the gate does not read it.
`write-openfga-store.py` keeps the environment variables, the file checks,
the two requests, the failure messages, and the printed block of the
shell, and starts no process. It verifies with the default context. A
certificate the context refuses exits 1 with the verifier's reason and one
line naming `write-lab-secrets.py` as the way to a new `secrets/`. An HTTP
error status is read as a response body, as `curl -sS` reads it.
`lab_scripts_test.go` holds
`runLabScript(t, tree, name, args...)`, which copies a script to
`<tree>/deploy/lab/` and starts the interpreter the first line of a `.sh`
file names (`bash` for the secrets and store scripts, which use
`BASH_SOURCE`) and `uv run` for a `.py` name. A `.py` with a
`<name>.py.lock` beside it has the lock copied with it and is started as
`uv run --locked`, so a lock that no longer matches the block fails every
test that runs the script. A `sync.Once` generates one
`secrets/` for the package in an `os.MkdirTemp` directory that a new
`TestMain` removes, since a `t.TempDir()` ends with the first test that
asked. The package has no `TestMain` today. The four tests at `lab_fixtures_test.go:377` to `:443` move
there, restated, and `heredocBody` is deleted. The README (`:13`, `:14`,
`:29`, `:43`, `:423`) and `central.textproto:4` name the Python files as
`uv run --locked write-lab-secrets.py` and `uv run write-openfga-store.py`.
The README has no prerequisite list that names `openssl` or `htpasswd`, so
none is edited. Step 3 gains one sentence: the store script refuses a
`secrets/` whose CA has no key usage extension. Both shell files are
deleted.
Tests: in `lab_scripts_test.go`, skipping on Windows. The run against the
shell file also skips when `openssl` or `htpasswd` is absent, and for the
store tests when `curl` or `jq` is. The Python run has no tool skip. A
test that changes `secrets/` copies the generated directory into its own
tree first, since the other tests read the shared one in parallel. It downloads the four packages from PyPI the first
time a host runs it and reads uv's cache after that.
`TestTheLabSecretsScriptWritesOwnerOnlyFiles`: `secrets/` holds exactly
`ca.crt`, `ca.key`, `server.crt`, `server.key`, `openfga.key`,
`dex_client.secret`, `openfga.env`, `dex.env`, and `credentials.txt`, each
`0600`. `TestTheLabSecretsScriptQuotesTheDexEnvFile`, as the Decision
states it. `TestTheLabSecretsScriptHashesAtDexsCost` replaces
`TestTheLabSecretsScriptHashesAtDexsCostFromStdin`: `bcrypt.Cost` of each
hash is 10. The `htpasswd` argument check goes with the process it read.
`TestTheLabSecretsScriptStartsNoProcess`, which has no shell run: the
Python file's text does not import `subprocess`, which is what keeps a password out of a process
list. `TestTheLabSecretsScriptWritesAStrictChain`, against the Python
output only: `crypto/x509` parses `ca.crt` as a CA with valid basic
constraints, `KeyUsageCertSign`, a 4096-bit RSA key, and a subject key
identifier. `server.crt` has a 2048-bit RSA key, the DNS name `localhost`,
the address `127.0.0.1`, `ExtKeyUsageServerAuth` and
`ExtKeyUsageClientAuth`, an authority key identifier equal to the CA's
subject key identifier, and a lifetime of 365 days. It verifies against a
pool holding `ca.crt` for both names, and `tls.LoadX509KeyPair` loads it
with `server.key`.
`TestTheLabReadmeReadsOnlyFilesTheSecretsScriptWrites`: every
`secrets/<name>` in the README exists in the generated directory.
`TestTheLabSecretsScriptRefusesAnExistingDirectory`: a second run exits 1
and changes no file. `TestTheLabStoreScriptPrintsTheAuthorizationBlock`:
requirement 4's example, with the model body equal to `model.json`
copied into the tree, served by an `httptest` server whose certificate is
the generated `server.crt`. The script's context keeps the strict
default, so this test fails if the chain lacks an extension Python asks
for. `TestTheLabStoreScriptRefusesAnotherCa`: with `secrets/ca.crt`
replaced by a CA the test builds with `crypto/x509`, the script exits 1
(non-zero for the shell, which leaves with the status of `curl`) and the
server's handler is never called. `TestTheLabStoreScriptFailsWithoutAStoreId`: a `{}` answer
exits 1. `TestTheLabStoreScriptStartsNoProcess`, which has no shell run, replaces
the `curl` argument test: the script's text does not import `subprocess`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- deploy/lab/write-lab-secrets.py deploy/lab/write-lab-secrets.py.lock deploy/lab/write-lab-secrets.sh deploy/lab/write-openfga-store.py deploy/lab/write-openfga-store.sh deploy/lab/README.md deploy/lab/central.textproto docs/dependencies/statements/pypi/cryptography.md docs/dependencies/statements/pypi/bcrypt.md docs/dependencies/README.md src/services/device/test/integration/lab_fixtures_test.go src/services/device/test/integration/lab_scripts_test.go`

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
Neither script has a dependency or a lock file, so both keep
`dependencies = []` and the plain `uv run`.
Tests: in `lab_render_scripts_test.go`, through U3's `runLabScript`, which
adds `--locked` only for a script with a lock beside it.
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
  'TestTheLab|TestTheRunbook'` passes with `buf` and `jq` on `PATH`, and
  with PyPI reachable or the four packages in uv's cache.
- `uv lock --check --script deploy/lab/write-lab-secrets.py` exits 0.
- `go test ./src/protocol/snmp/test/integration/ -run TestCapture` passes.
- `git ls-files -- 'src/protocol/smi/bench/*.sh' 'src/protocol/snmp/bench/*.sh'
  'deploy/lab/*.sh' 'src/protocol/snmp/test/integration/scripts/*.sh'` prints
  nothing.
- `git grep -n -E 'bench-gate\.sh|write-[a-z-]+\.sh|capture-snmprec\.sh'
  -- ':!docs/plans'` prints nothing.
- Manual, on a host with Docker: the "Lab run" steps 1 to 3 of
  `deploy/lab/README.md`, ending with an `authorization` block from a real
  OpenFGA.

## Definition of done

- [ ] The verifier is green for every changed path.
- [ ] Each test that has a shell run ran against the shell file before
      that file was deleted, and the implement report names the command
      and its last line.
- [ ] Every line the verifier lists as a removed test, an added skip, or a
      changed fixture carries its reason: the four moved lab tests, the
      Windows and missing-tool skips, and `manifest.yaml`.
- [ ] `deploy/lab/README.md`, the runbook, and the snmp integration README
      name the Python commands.
- [ ] A person has set `approved` in both `statements/pypi/` files before
      the phase lands. The implementer leaves it empty and says so in the
      report.
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
- The SMI gate compares a `MIN_DELTA` that is not a decimal number as text,
  as awk did under `-v`, so `MIN_DELTA=abc` never fails a run. The port
  keeps the verdict (`src/protocol/smi/bench/bench-gate.py:70`).
  Refusing such a value changes a verdict and is left to a person.
- Whether the Dex and OpenFGA containers accept the new chain is
  unverified. Both are Go programs and Go 1.27.1 loads and verifies it,
  but neither image was started. The manual lab run under Verification
  settles it.
- Where the verifier runs U3's tests without network and with an empty uv
  cache, `uv run --locked` cannot fetch the packages and the tests fail.
  `uv run --locked --offline` passes once the cache holds them. If that
  blocks the verifier, the implementer reports it and does not add a skip.
- uv installs the platform wheels, which hold compiled extensions. The
  lock pins their digests and the digest of each source archive. Reading
  that source is the review the admission record asks for and is not part
  of this plan.
- The first `TestTheLab*` run generates a 4096-bit RSA key, which takes
  seconds. If that is too slow for the targeted verifier run, the
  implementer reports it and does not shrink the key.

## Review gaps

Follow-ups of the review. None holds the verdict.

- deploy/lab/write-openfga-store.py:25: `Path(__file__).resolve()` prints `preshared_key_file` and `ca_file` with symbolic links resolved, where the shell printed the directory as it was invoked, and `write-lab-secrets.py:204` and `write-registry.py:50` do the same in their messages; fails: a checkout under a linked directory, such as `/var/folders` on macOS, prints `/private/var/...`; class: convention
- docs/plans/2026-10-05-2216-refactor-uv-python-scripts-phase6-plan.md:418: U4's Change line names no difference for `argparse`, which exits 2 on a missing argument where `sh` exited 1 and refuses an extra argument the shell ignored (`deploy/lab/write-registry.py:46`, `deploy/lab/write-provisioning.py:94`); fails: a caller that tests for status 1 or passes a third argument; class: convention
- src/protocol/smi/bench/bench-gate.py:63: `AWK_NUMBER` reads `+inf`, `+nan`, `-nan`, `nan`, and `0x10` as text, and the two awk builds on the development host (`/usr/bin/awk` 20200816, Homebrew 20260426) disagree with each other on each of them; fails: `MIN_DELTA=+inf` on a 5% regression exits 1, where Homebrew awk gave 0 and `/usr/bin/awk` gave 1; class: hardening
