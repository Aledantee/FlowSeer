---
title: A Mounted Credential Read Needs True O_NOFOLLOW, and Its Sidecar Metadata Must Be Pinned to the Same File
date: 2026-09-05
category: architecture-patterns
module: src/services/device/internal/credential
problem_type: architecture_pattern
component: filesystem-security
severity: high
applies_when:
  - "Reading a secret, key, or credential from a file a mount, an operator,
    or another process can replace after the reading process starts."
  - "Choosing between Go's os.Root/os.OpenInRoot and a raw O_NOFOLLOW open
    for a security-sensitive file read."
  - "Splitting a credential's material and its declared version into two
    separate files (a data file plus a sidecar), and validating the
    version after the material has already been read."
related_components: [credential-provider, edge-service]
tags: [symlink-race, toctou, o-nofollow, os-root, credential-rotation]
---

## Situation

`src/services/device/internal/credential.Provider` reads a device credential
by key from a mounted directory it does not control the writer of. Two
separate mistakes are easy to make here, and both slipped past the author
before an independent review caught them.

## What is true, and why

**`os.Root` (Go 1.24+) refuses a symlink that escapes the root, but it does
not refuse a symlink that stays inside it.** The stdlib documents this
directly: "Methods on Root will follow symbolic links, but symbolic links may
not reference a location outside the root." That is the right rule for
walking a directory tree — it is the wrong rule for a single credential file,
where the requirement is "this exact path must never be a symlink," not
"nothing may escape this directory." Using `os.Root.Open`/`os.OpenInRoot` for
a credential leaf would let an attacker who can write inside the mount (a
compromised sidecar, a misconfigured secret manager) point the credential's
name at a different, still-in-mount file, and the read would silently follow
it.

The fix is a real `O_NOFOLLOW` open, which most platforms' `openat(2)`
supports as a flag: the kernel fails the call with `ELOOP` when the final
path component is a symlink, atomically, with no window between a check and
the open. Go's `os` package does not expose this flag portably, but
`golang.org/x/sys/unix.Openat` does:

```go
// src/services/device/internal/credential/provider.go:128-137
func (p *Provider) openNoFollow(name string) (*os.File, error) {
	fd, err := unix.Openat(int(p.root.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	...
}
```

confirmed by `TestProviderRefusesSymlinkedCredential` and
`TestProviderConcurrentSwapNeverLeaksTheReplacedTarget` in
`src/services/device/internal/credential/provider_test.go`, the latter racing
a goroutine that swaps the entry between a regular file and a symlink while
`Get` runs, asserting every successful read returns the original content and
every failure is a refusal rather than a leak.

**A material file and its version-declaring sidecar, opened independently,
can be read across a rotation.** If `Get` opens `<key>`, reads it, then opens
`<key>.meta.json` and trusts its declared version, a rotation landing between
the two opens pairs version-N material with version-(N+1) metadata (or the
reverse), and a caller pinned to N+1 gets stale material certified as
current. Neither open alone can see this; the fix is to re-resolve the
material path once more after the metadata is read and refuse if it no
longer identifies the same file:

```go
// src/services/device/internal/credential/provider.go, after the version check
recheck, err := p.openNoFollow(key)
...
recheckInfo, err := checkSecure(recheck, key)
...
if !os.SameFile(materialInfo, recheckInfo) {
	return nil, errs.New().Code(ErrCodeRotatedDuringRead)...
}
```

confirmed by `TestProviderRefusesRotationBetweenMaterialAndMetadata`, which
uses an injected `afterMaterialRead` test hook to rotate the file
deterministically inside the race window rather than relying on goroutine
timing.

## How to apply

- Default to `os.Root`/`os.OpenInRoot` for walking an untrusted directory
  tree where the concern is path traversal. Reach for a raw `O_NOFOLLOW`
  open (`golang.org/x/sys/unix.Openat`, `//go:build linux || darwin` or
  narrower) only when the concern is specifically "this leaf must not be a
  symlink," and say so in the package doc — including which real-world mount
  layouts that then cannot be read directly, since a Kubernetes secret
  volume's native layout is exactly this shape (each key is a symlink
  through a rotated `..data` directory) and needs its own adapter in front
  of an `O_NOFOLLOW` reader.
- When a value and its integrity/version metadata live in two files, treat
  every read as a two-open, one-identity-check sequence: open A, do work,
  open B, do work, then re-open A and confirm `os.SameFile` against the
  first open's `os.FileInfo` before trusting the pairing. A single combined
  file is simpler when the format is yours to choose; the recheck is the
  fix when the two-file split is already the contract.

## What it does not cover

- Windows: `unix.Openat`/`ELOOP` semantics assume a POSIX `openat(2)`. A
  Windows credential reader needs its own mechanism.
- The narrow window between the recheck's own open and the function
  returning the already-read `data` — the recheck closes the two-open race
  the metadata read created; it does not make the whole `Get` call one
  atomic operation.
