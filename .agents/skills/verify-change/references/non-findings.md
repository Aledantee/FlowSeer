# Failures that are not findings

| Symptom | What it is | What to do |
| --- | --- | --- |
| `required tools are not on PATH: gofumpt goimports` (the whole list, no gate name) | setup, not a finding; every command-line tool the selected gates invoke is checked before the first gate, and Go tools are looked up in `$(go env GOPATH)/bin` whether or not the session's PATH carries it | report it as setup, not as a finding about the change |
| `parallel golangci-lint is running` from a hand run | `golangci-lint` takes one file lock per machine; the gate passes `--allow-serial-runners` and waits for a concurrent instance, a hand run without that flag fails | repeat the hand run once the verifier has finished |
| a test timeout under `--full` | `--full` bounds `go test -p` to half the CPUs, because packages that start listeners or walk a corpus time out under one test binary per CPU and pass alone | see below |

The Docker daemon is not a PATH tool: when it is unavailable, that stays a
failed gate.

A timeout is a finding only once it reproduces with the package run alone,
and a pass alone does not clear the change either. Attribute it in one step:
the same failure on a detached checkout of the base, with the change absent.
`go list -deps` on the failing package says where to look first, but it
answers a compile-time question only: a change also reaches a test through a
shared port, a `testdata/` directory, or an environment variable.
