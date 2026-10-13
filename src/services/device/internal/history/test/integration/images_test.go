package integration_test

import (
	"os"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/services/device/internal/history/historytest"
)

func TestClickHouseImageMatchesTheDeviceServiceSuite(t *testing.T) {
	data, err := os.ReadFile("../../../../test/integration/clickhouse_images_test.go")
	if err != nil {
		t.Fatalf("read the device service image declaration: %v", err)
	}
	want := `const clickHouseImage = "` + historytest.Image + `"`
	if !strings.Contains(string(data), want) {
		t.Errorf("the device service suite does not declare %q", want)
	}
}
