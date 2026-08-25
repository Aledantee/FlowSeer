// linktest_helpers_test.go provides the getenv helper for the linktest build
// tag, kept separate so the default build does not import "os" into the test
// package's non-tagged files.
//
//go:build linux && linktest

package link

import "os"

func getenv(key string) string { return os.Getenv(key) }
