//go:build history_integration

package integration_test

import (
	"context"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/services/device/internal/history"
	"go.aledante.io/FlowSeer/src/services/device/internal/history/historytest"
)

// showCreate returns SHOW CREATE output for every table and view of a
// database, in name order.
func showCreate(t *testing.T, s *historytest.Server, database string) map[string]string {
	t.Helper()
	ctx := context.Background()
	rows, err := s.Admin.Query(ctx, "SELECT name FROM system.tables WHERE database = ? ORDER BY name", database)
	if err != nil {
		t.Fatalf("list tables of %s: %v", database, err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list tables of %s: %v", database, err)
	}
	_ = rows.Close()

	out := make(map[string]string, len(names))
	for _, name := range names {
		out[name] = s.Scalar(t, "SHOW CREATE TABLE `"+database+"`.`"+name+"`")
	}
	return out
}

func TestMigrateBringsAnEmptyServerToTheCurrentSchemaAndASecondRunChangesNothing(t *testing.T) {
	s := historytest.Start(t, false)
	store := s.Open(t)
	ctx := context.Background()

	first, err := history.Migrate(ctx, store)
	if err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if len(first) != 7 {
		t.Fatalf("first run applied %d steps (%v), want 7", len(first), first)
	}
	before := showCreate(t, s, "flowseer")
	beforeControl := showCreate(t, s, "flowseer_control")
	wantTables := []string{"device_dim", "syslog", "syslog_by_location", "syslog_by_location_mv", "syslog_daily", "syslog_daily_mv"}
	for _, name := range wantTables {
		if _, ok := before[name]; !ok {
			t.Errorf("table %q missing after the first run; have %v", name, keys(before))
		}
	}

	second, err := history.Migrate(ctx, store)
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if len(second) != 0 {
		t.Errorf("second run applied %v, want nothing", second)
	}

	for name, ddl := range before {
		if got := showCreate(t, s, "flowseer")[name]; got != ddl {
			t.Errorf("SHOW CREATE %s changed on the second run:\n got %s\nwant %s", name, got, ddl)
		}
	}
	for name, ddl := range beforeControl {
		if got := showCreate(t, s, "flowseer_control")[name]; got != ddl {
			t.Errorf("SHOW CREATE %s changed on the second run:\n got %s\nwant %s", name, got, ddl)
		}
	}
	if got := s.Scalar(t, "SELECT toString(count()) FROM flowseer_control.schema_steps"); got != "7" {
		t.Errorf("schema_steps rows after two runs = %s, want 7", got)
	}
	if got := s.Scalar(t, "SELECT toString(count(DISTINCT step)) FROM flowseer_control.schema_steps"); got != "7" {
		t.Errorf("distinct steps recorded = %s, want 7", got)
	}
}

func TestMigrateOnAClusterNodeCreatesReplicatedObjects(t *testing.T) {
	s := historytest.Start(t, true)
	store := s.Open(t)

	if _, err := history.Migrate(context.Background(), store); err != nil {
		t.Fatalf("cluster migrate: %v", err)
	}

	if got := s.Scalar(t, "SELECT engine FROM system.databases WHERE name = 'flowseer'"); got != "Replicated" {
		t.Errorf("database engine = %q, want Replicated", got)
	}
	for _, table := range []string{"syslog", "syslog_by_location", "syslog_daily", "device_dim"} {
		got := s.Scalar(t, "SELECT engine FROM system.tables WHERE database = 'flowseer' AND name = '"+table+"'")
		if !strings.HasPrefix(got, "Replicated") {
			t.Errorf("engine of %s = %q, want a Replicated engine", table, got)
		}
	}
	if got := s.Scalar(t, "SELECT toString(count()) FROM system.workloads WHERE name IN ('ingestion', 'monitoring', 'analytics')"); got != "3" {
		t.Errorf("workloads on the cluster node = %s, want 3", got)
	}

	again, err := history.Migrate(context.Background(), store)
	if err != nil {
		t.Fatalf("second cluster migrate: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("second cluster run applied %v, want nothing", again)
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
