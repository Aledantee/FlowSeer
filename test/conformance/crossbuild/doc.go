// Package crossbuild gates FreeBSD compilation of packages that contain
// platform-specific networking, service, and agent code.
//
// Build constraints exclude FreeBSD source files from macOS and Linux host
// builds and test passes. The gate runs go vet with GOOS=freebsd to type-check
// production and test code for amd64 and arm64, testing the default build tags
// and the FreeBSD VM suite tags without requiring a FreeBSD host. A link step
// builds the agent binary for each architecture to catch missing function
// bodies and symbols that type-checking alone does not report. Reading every
// vetted package directory, go.mod, and go.sum makes those files cache inputs
// of the test, so any source change reruns the gate.
package crossbuild
