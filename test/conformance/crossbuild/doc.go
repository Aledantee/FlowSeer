// Package crossbuild gates the FreeBSD compilation of packages that contain
// platform-specific networking, service, and agent code.
//
// FreeBSD source files do not compile on macOS or Linux host environments
// during ordinary builds and test passes. Running go vet with GOOS=freebsd
// type-checks both production code and test files across supported architectures
// and build tags without requiring a FreeBSD host.
package crossbuild
