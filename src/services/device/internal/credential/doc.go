//go:build linux || darwin

// Package credential reads device credentials from a mounted secret
// volume, the shape a Kubernetes secret or an operator-managed directory
// both take. [Provider] opens the mount root once and holds its file
// descriptor for its own lifetime; every [Provider.Get] resolves the
// credential's key relative to that held descriptor with a true
// O_NOFOLLOW open, so a symlink swapped into the mount after the
// directory was opened is refused at the kernel call itself rather than
// raced between a check and a read.
package credential
