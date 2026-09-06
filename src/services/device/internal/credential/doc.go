//go:build linux || darwin

// Package credential reads device credentials from an operator-managed
// directory: one regular file per credential key, mode 0600, plus a
// "<key>.meta.json" sidecar naming the version. This is not the layout a
// Kubernetes secret volume mounts (kubelet publishes each key as a symlink
// through a "..data" directory, which this package's O_NOFOLLOW opens
// refuse by design); a Kubernetes-backed deployment needs its own adapter
// that resolves those symlinks before handing the resulting regular files
// to a mount this package can read.
//
// [Provider] opens the mount root once and holds its file descriptor for
// its own lifetime; every [Provider.Get] resolves the credential's key
// relative to that held descriptor with a true O_NOFOLLOW open, so a
// symlink swapped into the mount after the directory was opened is
// refused at the kernel call itself rather than raced between a check and
// a read.
//
// Get returns the file parsed into a CredentialMaterial, but parses it only
// after every security, version, and rotation check has passed. A malformed
// file therefore surfaces as a parse error only when nothing else was wrong;
// it never masks a symlink, an insecure mode, a version mismatch, or a
// mid-rotation read, so the reported cause is the one to act on.
package credential
