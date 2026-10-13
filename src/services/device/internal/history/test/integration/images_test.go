package integration_test

import (
	"os"
	"strings"
	"testing"
)

// clickHouseImage is the server the history integration tests run. The device
// service suite and the lab declare the same digest.
const clickHouseImage = "clickhouse/clickhouse-server:26.8@sha256:9b61e3c635c04ad5bb521eb4f6e61ce7585b5580814e51c25bb9e8292ce43364"

func TestClickHouseImageMatchesTheDeviceServiceSuite(t *testing.T) {
	data, err := os.ReadFile("../../../../test/integration/clickhouse_images_test.go")
	if err != nil {
		t.Fatalf("read the device service image declaration: %v", err)
	}
	want := `const clickHouseImage = "` + clickHouseImage + `"`
	if !strings.Contains(string(data), want) {
		t.Errorf("the device service suite does not declare %q", want)
	}
}
