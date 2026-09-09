// Package integration proves the mirror receiver against independent
// senders it does not control (a Linux erspan tunnel, an Open vSwitch
// erspan port); see erspan_test.go and README.md. This file carries no
// build constraint so the package is never "build constraints exclude all
// Go files" on a default build; the suite itself is behind
// linux && capture_mirror_integration.
package integration
