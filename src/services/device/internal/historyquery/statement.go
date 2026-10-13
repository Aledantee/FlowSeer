package historyquery

import (
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/google/uuid"
)

// syslogColumns are the columns a read selects, in the order [Reader.scan]
// reads them.
const syslogColumns = "tenant_id, device_id, ts, record_id, observed_at, sent_at, severity, facility, present, " +
	"hostname, app_name, proc_id, msg_id, sd_ids, sd_param_sd_id, sd_param_name, sd_param_value, " +
	"message, message_truncated, source_ip, location_id"

// tenantCondition leads every WHERE of every statement: the tenant column leads
// the primary key of both tables, so the condition prunes by key, and a read
// can never be written without it.
const tenantCondition = "tenant_id IN {tenants:Array(String)}"

// statement is a query text and the named parameters it binds.
type statement struct {
	text string
	args []any
}

// buildStatement returns the SELECT for one page: limit rows, newest first.
// The tenants are bound as a parameter and are never part of the text. database
// is quoted here and must match the identifier form the service configuration
// enforces.
//
// A device scope reads syslog directly. A location scope selects the newest
// keys from syslog_by_location, which holds no message text, and fetches their
// rows from syslog by the full key.
func buildStatement(database string, q Query, tenants []string, after *cursor, limit uint32) statement {
	args := []any{
		clickhouse.Named("tenants", tenants),
		clickhouse.DateNamed("from", q.From, clickhouse.MilliSeconds),
		clickhouse.DateNamed("to", q.To, clickhouse.MilliSeconds),
		clickhouse.Named("limit", limit),
	}
	filters := windowAndFilters(q, &args, after)

	db := "`" + database + "`"
	var text string
	if q.Scope.kind == scopeDevice {
		args = append(args, clickhouse.Named("device", q.Scope.id.String()))
		text = "SELECT " + syslogColumns + " FROM " + db + ".syslog\n" +
			"WHERE " + tenantCondition + "\n" +
			"  AND device_id = {device:UUID}\n" + filters +
			"ORDER BY ts DESC, record_id DESC\n" +
			"LIMIT 1 BY tenant_id, record_id\n" +
			"LIMIT {limit:UInt32}"
	} else {
		args = append(args, clickhouse.Named("location", q.Scope.id.String()))
		text = "SELECT " + syslogColumns + " FROM " + db + ".syslog\n" +
			"WHERE " + tenantCondition + "\n" +
			"  AND ts >= {from:DateTime64(3)} AND ts < {to:DateTime64(3)}\n" +
			"  AND (tenant_id, device_id, ts, record_id) IN (\n" +
			"    SELECT tenant_id, device_id, ts, record_id FROM " + db + ".syslog_by_location\n" +
			"    WHERE " + tenantCondition + "\n" +
			"      AND location_id = {location:UUID}\n" + indent(filters) +
			"    ORDER BY ts DESC, record_id DESC\n" +
			"    LIMIT 1 BY tenant_id, record_id\n" +
			"    LIMIT {limit:UInt32}\n" +
			"  )\n" +
			"ORDER BY ts DESC, record_id DESC\n" +
			"LIMIT 1 BY tenant_id, record_id"
	}
	return statement{text: text, args: args}
}

// windowAndFilters returns the time window, the severity filter, and the
// keyset condition as AND clauses, appending the parameters they bind.
func windowAndFilters(q Query, args *[]any, after *cursor) string {
	var b strings.Builder
	b.WriteString("  AND ts >= {from:DateTime64(3)} AND ts < {to:DateTime64(3)}\n")
	if q.MinSeverity != nil {
		// A message without a priority has no severity to compare, so a
		// severity filter leaves it out.
		*args = append(*args, clickhouse.Named("severity", *q.MinSeverity))
		b.WriteString("  AND bitAnd(present, 1) = 1 AND severity <= {severity:UInt8}\n")
	}
	if after != nil {
		*args = append(*args,
			clickhouse.DateNamed("after_ts", after.ts, clickhouse.MilliSeconds),
			clickhouse.Named("after_id", after.recordID.String()),
		)
		b.WriteString("  AND (ts, record_id) < ({after_ts:DateTime64(3)}, {after_id:UUID})\n")
	}
	return b.String()
}

func indent(lines string) string {
	return "    " + strings.ReplaceAll(strings.TrimSuffix(lines, "\n"), "\n", "\n    ") + "\n"
}

// cursor is the keyset position after the last row of a page.
type cursor struct {
	ts       time.Time
	recordID uuid.UUID
}
