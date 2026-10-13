// Package integration holds the history read's tests against a real ClickHouse
// server. They carry the history_integration build tag and start the pinned
// image through history/historytest:
//
//	TESTCONTAINERS_RYUK_DISABLED=true go test -race -tags=history_integration ./src/services/device/internal/historyapi/test/integration/
package integration
