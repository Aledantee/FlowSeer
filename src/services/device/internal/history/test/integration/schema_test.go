//go:build history_integration

package integration_test

import (
	"context"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/google/uuid"

	"go.aledante.io/FlowSeer/src/services/device/internal/history"
)

// migrated starts a single-node server and brings it to the current schema.
func migrated(t *testing.T) (*server, *history.Store) {
	t.Helper()
	s := startClickHouse(t, false)
	store := s.open(t)
	if _, err := history.Migrate(context.Background(), store); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s, store
}

// seedSyslog inserts perDevice rows for each of tenants t<first>..t<first+tenants-1>
// and devices d0..d<devices-1>, as the admin user so the volume costs one
// statement.
func (s *server) seedSyslog(t *testing.T, first, tenants, devices, perDevice int) {
	t.Helper()
	query := `INSERT INTO flowseer.syslog (tenant_id, device_id, ts, record_id, severity, present, message, location_id, retention_class)
SELECT
    concat('t', toString(tenant)),
    toUUID(concat('00000000-0000-7000-8000-', leftPad(toString(device), 12, '0'))),
    toDateTime64('2026-10-01 00:00:00', 3, 'UTC') + toIntervalSecond(n),
    generateUUIDv4(),
    toUInt8(n % 8),
    3,
    concat('%LINK-3-UPDOWN: Interface Gi1/0/', toString(n % 48), ', changed state to down'),
    toUUID(concat('00000000-0000-7000-8000-', leftPad(toString(device % 4), 12, '0'))),
    2
FROM (SELECT number AS n FROM numbers(?)) AS rows
CROSS JOIN (SELECT number + ? AS tenant FROM numbers(?)) AS tenants
CROSS JOIN (SELECT number AS device FROM numbers(?)) AS devices`
	if err := s.admin.Exec(context.Background(), query, uint64(perDevice), uint64(first), uint64(tenants), uint64(devices)); err != nil {
		t.Fatalf("seed syslog: %v", err)
	}
	// One part per partition, so the plan counts key ranges of the data and
	// not of the insert blocks that have not merged yet.
	if err := s.admin.Exec(context.Background(), "OPTIMIZE TABLE flowseer.syslog FINAL"); err != nil {
		t.Fatalf("optimize syslog: %v", err)
	}
}

func TestEveryTableTheAnalystReadsHasARowPolicyInTheSubqueryForm(t *testing.T) {
	s, _ := migrated(t)

	rows, err := s.admin.Query(context.Background(), `
SELECT name FROM system.tables WHERE database = 'flowseer' AND engine != 'MaterializedView' ORDER BY name`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		tables = append(tables, name)
	}
	_ = rows.Close()
	if len(tables) != 4 {
		t.Fatalf("tables of the history database = %v, want device_dim, syslog, syslog_by_location, syslog_daily", tables)
	}

	for _, table := range tables {
		got := s.scalar(t, `SELECT toString(count()) FROM system.row_policies
WHERE database = 'flowseer' AND table = '`+table+`'
  AND has(apply_to_list, 'flowseer_analyst')
  AND select_filter LIKE '%tenant_id IN (SELECT tenant_id FROM flowseer_control.user_tenants WHERE%'
  AND select_filter NOT LIKE '%dictHas%'`)
		if got != "1" {
			t.Errorf("table %s has %s analyst row policies of the subquery form, want 1", table, got)
		}
	}
}

var (
	primaryKeyGranules = regexp.MustCompile(`(?s)PrimaryKey.*?Granules: (\d+)/(\d+)`)
	selectedRanges     = regexp.MustCompile(`Ranges: (\d+)`)
)

// deviceScopeIndex returns how many granules the primary key keeps for one
// device's day of rows, and how many key ranges that read scans.
func (s *server) deviceScopeIndex(t *testing.T, tenant, device string) (granules, ranges int) {
	t.Helper()
	plan := s.scalar(t, `SELECT arrayStringConcat(groupArray(explain), '\n') FROM (EXPLAIN indexes = 1
SELECT record_id FROM flowseer.syslog
WHERE tenant_id IN ('`+tenant+`') AND device_id = toUUID('`+device+`')
  AND ts >= toDateTime64('2026-10-01 00:00:00', 3, 'UTC') AND ts < toDateTime64('2026-10-02 00:00:00', 3, 'UTC')
ORDER BY ts DESC LIMIT 100)`)
	g := primaryKeyGranules.FindStringSubmatch(plan)
	r := selectedRanges.FindStringSubmatch(plan)
	if g == nil || r == nil {
		t.Fatalf("EXPLAIN holds no primary key granules or ranges:\n%s", plan)
	}
	granules, _ = strconv.Atoi(g[1])
	ranges, _ = strconv.Atoi(r[1])
	return granules, ranges
}

func TestADeviceScopeReadSelectsOneKeyRangeThatOtherTenantsDataDoesNotGrow(t *testing.T) {
	s, _ := migrated(t)
	const device = "00000000-0000-7000-8000-000000000004"

	// 2000 rows of one device span at most two granules of 8192 rows.
	const maxGranules = 2

	s.seedSyslog(t, 0, 20, 10, 2000)
	granules, ranges := s.deviceScopeIndex(t, "t3", device)
	if ranges != 1 || granules > maxGranules {
		t.Errorf("with 400,000 rows: granules %d, key ranges %d; want at most %d and 1", granules, ranges, maxGranules)
	}

	if err := s.admin.Exec(context.Background(), "TRUNCATE TABLE flowseer.syslog"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	s.seedSyslog(t, 0, 80, 10, 2000)
	granules, ranges = s.deviceScopeIndex(t, "t3", device)
	if ranges != 1 || granules > maxGranules {
		t.Errorf("with 1,600,000 rows of other tenants: granules %d, key ranges %d; want at most %d and 1", granules, ranges, maxGranules)
	}
}

func insertAsWriter(t *testing.T, store *history.Store, rows ...syslogRow) {
	t.Helper()
	batch, err := store.Writer().PrepareBatch(context.Background(),
		"INSERT INTO flowseer.syslog (tenant_id, device_id, ts, record_id, severity, present, message, location_id, retention_class)")
	if err != nil {
		t.Fatalf("prepare batch: %v", err)
	}
	for _, r := range rows {
		if err := batch.Append(r.tenant, r.device, r.ts, uuid.New(), r.severity, r.present, r.message, r.location, uint8(2)); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatalf("send: %v", err)
	}
}

type syslogRow struct {
	tenant   string
	device   uuid.UUID
	location uuid.UUID
	ts       time.Time
	severity uint8
	present  uint8
	message  string
}

func TestAnInsertAsTheWriterFeedsTheLocationTableAndTheDailyCounts(t *testing.T) {
	s, store := migrated(t)
	device := uuid.MustParse("00000000-0000-7000-8000-0000000000d1")
	location := uuid.MustParse("00000000-0000-7000-8000-0000000000e1")
	day := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)

	insertAsWriter(t, store,
		syslogRow{tenant: "a1", device: device, location: location, ts: day, severity: 3, present: 3, message: "%LINK-3-UPDOWN: Gi1/0/1 down"},
		syslogRow{tenant: "a1", device: device, location: location, ts: day.Add(time.Minute), severity: 3, present: 3, message: "%LINK-3-UPDOWN: Gi1/0/2 down"},
		syslogRow{tenant: "a1", device: device, location: location, ts: day.Add(2 * time.Minute), severity: 0, present: 0, message: "plain text"},
	)

	if got := s.scalar(t, "SELECT toString(count()) FROM flowseer.syslog_by_location WHERE tenant_id = 'a1' AND location_id = '"+location.String()+"' AND vendor_tag = 'LINK-3-UPDOWN'"); got != "2" {
		t.Errorf("tagged rows by location = %s, want 2", got)
	}
	if got := s.scalar(t, "SELECT toString(sum(messages)) FROM flowseer.syslog_daily WHERE tenant_id = 'a1' AND severity = 3 AND vendor_tag = 'LINK-3-UPDOWN' AND day = '2026-10-02'"); got != "2" {
		t.Errorf("daily count of severity 3 = %s, want 2", got)
	}
	if got := s.scalar(t, "SELECT toString(sum(messages)) FROM flowseer.syslog_daily WHERE tenant_id = 'a1' AND severity = 255 AND day = '2026-10-02'"); got != "1" {
		t.Errorf("daily count of the message without a priority = %s, want 1 under severity 255", got)
	}
	if got := s.scalar(t, "SELECT toString(any(location_id)) FROM flowseer.syslog_daily WHERE tenant_id = 'a1'"); got != location.String() {
		t.Errorf("daily location_id = %s, want %s", got, location)
	}
}

func TestTheWriterOnlyInsertsAndTheReaderOnlyReads(t *testing.T) {
	_, store := migrated(t)
	ctx := context.Background()

	if err := store.Reader().Exec(ctx, "SELECT count() FROM flowseer.syslog"); err != nil {
		t.Errorf("reader select: %v", err)
	}
	if err := store.Reader().Exec(ctx, "INSERT INTO flowseer.syslog (tenant_id, device_id, ts, record_id) SELECT 'a1', generateUUIDv4(), now(), generateUUIDv4()"); err == nil {
		t.Error("reader insert succeeded, want a refusal")
	}
	if err := store.Writer().Exec(ctx, "INSERT INTO flowseer.syslog (tenant_id, device_id, ts, record_id) SELECT 'a1', generateUUIDv4(), now(), generateUUIDv4()"); err != nil {
		t.Errorf("writer insert: %v", err)
	}
	if err := store.Writer().Exec(ctx, "SELECT count() FROM flowseer.syslog"); err == nil {
		t.Error("writer select succeeded, want a refusal")
	}
}

func TestTheServiceReaderProfileLimitsCannotBeRaised(t *testing.T) {
	_, store := migrated(t)
	ctx := context.Background()

	var limit string
	if err := store.Reader().QueryRow(ctx, "SELECT toString(getSetting('max_rows_to_read'))").Scan(&limit); err != nil {
		t.Fatalf("read the limit: %v", err)
	}
	if limit != "10000000" {
		t.Errorf("max_rows_to_read as the reader = %s, want 10000000", limit)
	}
	raised := clickhouse.Context(ctx, clickhouse.WithSettings(clickhouse.Settings{"max_rows_to_read": 20_000_000}))
	if err := store.Reader().Exec(raised, "SELECT 1"); err == nil {
		t.Error("the reader raised max_rows_to_read, want a constraint violation")
	}
}

func TestTheAnalystReadsOnlyTheTenantsMappedToIt(t *testing.T) {
	s, _ := migrated(t)
	ctx := context.Background()
	s.seedSyslog(t, 0, 3, 2, 10)
	analyst := s.connectAsAnalyst(t, "analyst-secret")

	tenants := func() []string {
		rows, err := analyst.Query(ctx, "SELECT DISTINCT tenant_id FROM flowseer.syslog ORDER BY tenant_id")
		if err != nil {
			t.Fatalf("analyst select: %v", err)
		}
		defer func() { _ = rows.Close() }()
		var out []string
		for rows.Next() {
			var tenant string
			if err := rows.Scan(&tenant); err != nil {
				t.Fatalf("scan: %v", err)
			}
			out = append(out, tenant)
		}
		return out
	}

	if got := tenants(); len(got) != 0 {
		t.Fatalf("analyst without a mapping reads tenants %v, want none", got)
	}

	if err := s.admin.Exec(ctx, "INSERT INTO flowseer_control.user_tenants (user, tenant_id) VALUES ('flowseer_analyst', 't0'), ('flowseer_analyst', 't2'), ('someone_else', 't1')"); err != nil {
		t.Fatalf("map tenants: %v", err)
	}
	got := tenants()
	if len(got) != 2 || got[0] != "t0" || got[1] != "t2" {
		t.Errorf("analyst reads tenants %v, want [t0 t2]", got)
	}

	var mappedUsers uint64
	if err := analyst.QueryRow(ctx, "SELECT count(DISTINCT user) FROM flowseer_control.user_tenants").Scan(&mappedUsers); err != nil {
		t.Fatalf("analyst reads the mapping: %v", err)
	}
	if mappedUsers != 1 {
		t.Errorf("analyst sees %d users in the mapping, want only itself", mappedUsers)
	}
}

func TestMigrateAppliesARotatedPasswordFile(t *testing.T) {
	s := startClickHouse(t, false)
	store := s.open(t)
	ctx := context.Background()
	if _, err := history.Migrate(ctx, store); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := s.connectAsAnalyst(t, "analyst-secret").Ping(ctx); err != nil {
		t.Fatalf("analyst with the first password: %v", err)
	}

	s.user(t, "flowseer_analyst", "rotated-secret")
	if _, err := history.Migrate(ctx, store); err != nil {
		t.Fatalf("migrate after rotation: %v", err)
	}

	if err := s.connectAsAnalyst(t, "analyst-secret").Ping(ctx); err == nil {
		t.Error("the old analyst password still works after the rotation")
	}
	if err := s.connectAsAnalyst(t, "rotated-secret").Ping(ctx); err != nil {
		t.Errorf("the rotated analyst password: %v", err)
	}
}

func TestMirrorDevicesKeepsTheNewestRowOfADevice(t *testing.T) {
	s, store := migrated(t)
	ctx := context.Background()
	const tenantID = "018f0000-0000-7000-8000-0000000000a1"
	device := "00000000-0000-7000-8000-0000000000d1"
	location := "00000000-0000-7000-8000-0000000000e1"

	if err := store.MirrorDevices(ctx, tenantID, []history.Device{{ID: device, Name: "sw1"}, {ID: "00000000-0000-7000-8000-0000000000d2", Name: "sw2", LocationID: location}}); err != nil {
		t.Fatalf("first mirror: %v", err)
	}
	if err := store.MirrorDevices(ctx, tenantID, []history.Device{{ID: device, Name: "sw1-renamed", LocationID: location}}); err != nil {
		t.Fatalf("second mirror: %v", err)
	}

	if got := s.scalar(t, "SELECT name FROM flowseer.device_dim FINAL WHERE tenant_id = '"+tenantID+"' AND device_id = '"+device+"'"); got != "sw1-renamed" {
		t.Errorf("name after the second mirror = %q, want sw1-renamed", got)
	}
	if got := s.scalar(t, "SELECT toString(location_id) FROM flowseer.device_dim FINAL WHERE tenant_id = '"+tenantID+"' AND device_id = '"+device+"'"); got != location {
		t.Errorf("location after the second mirror = %q, want %q", got, location)
	}
	if got := s.scalar(t, "SELECT toString(count()) FROM flowseer.device_dim FINAL WHERE tenant_id = '"+tenantID+"'"); got != "2" {
		t.Errorf("devices mirrored = %s, want 2", got)
	}
}
