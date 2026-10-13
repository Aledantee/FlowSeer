package historysink

import (
	"context"
	"errors"

	"github.com/ClickHouse/clickhouse-go/v2"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// ErrCodeRejected is an insert the server refuses for what the rows hold, so a
// retry of the same rows fails the same way. Any other insert error is
// transient: the server was unreachable, busy, or in a state that passes.
var ErrCodeRejected = errs.NewCode("historysink/rejected")

// IsRejected reports whether err is a permanent refusal of the rows.
func IsRejected(err error) bool {
	code, ok := errs.CodeOf(err)
	return ok && code == ErrCodeRejected
}

// Inserter writes one batch of rows to the syslog table. token identifies the
// batch for insert deduplication, so the same rows sent again with the same
// token are stored once while the server remembers the token.
//
// Insert returns an error for which [IsRejected] is true when the rows
// themselves cannot be stored, and any other error for a failure a later
// attempt may survive. It returns nil only after the server holds the rows.
type Inserter interface {
	Insert(ctx context.Context, rows []Row, token string) error
}

// ClickHouseInserter inserts into the syslog table of one database through a
// writer pool.
type ClickHouseInserter struct {
	conn  clickhouse.Conn
	query string
}

// NewInserter returns an inserter over conn for the syslog table of database.
// The database name must match the identifier form the service configuration
// enforces, since it is interpolated into the statement. The inserter is safe
// for concurrent use.
func NewInserter(conn clickhouse.Conn, database string) *ClickHouseInserter {
	return &ClickHouseInserter{
		conn: conn,
		query: "INSERT INTO `" + database + "`.syslog (tenant_id, device_id, ts, record_id, observed_at, sent_at, " +
			"severity, facility, present, hostname, app_name, proc_id, msg_id, sd_ids, sd_param_sd_id, sd_param_name, " +
			"sd_param_value, message, message_truncated, source_ip, location_id, retention_class)",
	}
}

// rejectedCodes are the server exception codes for a value the table cannot
// hold (ErrorCodes.cpp, ClickHouse v26.8.20.9): CANNOT_PARSE_TEXT,
// CANNOT_PARSE_INPUT_ASSERTION_FAILED, CANNOT_PARSE_DATETIME, TYPE_MISMATCH,
// ARGUMENT_OUT_OF_BOUND, CANNOT_CONVERT_TYPE, INCORRECT_DATA, and
// VALUE_IS_OUT_OF_RANGE_OF_DATA_TYPE. Every other code, including access and
// resource errors, is transient because the same rows may pass later.
var rejectedCodes = map[int32]bool{6: true, 27: true, 41: true, 53: true, 69: true, 70: true, 117: true, 321: true}

// Insert sends the rows in one synchronous insert carrying the deduplication
// token.
func (i *ClickHouseInserter) Insert(ctx context.Context, rows []Row, token string) error {
	ctx = clickhouse.Context(ctx, clickhouse.WithSettings(clickhouse.Settings{
		"async_insert":               0,
		"insert_deduplication_token": token,
	}))
	batch, err := i.conn.PrepareBatch(ctx, i.query)
	if err != nil {
		return classify(err, "prepare insert")
	}
	defer func() { _ = batch.Close() }()

	for n := range rows {
		r := &rows[n]
		if err := batch.Append(
			r.TenantID, r.DeviceID, r.ReceivedAt, r.RecordID, r.ObservedAt, r.SentAt,
			r.Severity, r.Facility, r.Present, r.Hostname, r.AppName, r.ProcID, r.MsgID,
			nonNil(r.SDIDs), nonNil(r.SDParamSDID), nonNil(r.SDParamName), nonNil(r.SDParamValue),
			r.Message, r.MessageTruncated, r.SourceIP, r.LocationID, r.RetentionClass,
		); err != nil {
			return errs.From(err).Code(ErrCodeRejected).Attr("record", r.RecordID.String()).Msg("append row")
		}
	}
	if err := batch.Send(); err != nil {
		return classify(err, "send insert")
	}
	return nil
}

// nonNil gives an empty array column value a non-nil slice, which the driver
// requires for an Array column.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func classify(err error, what string) error {
	var exception *clickhouse.Exception
	if errors.As(err, &exception) && rejectedCodes[exception.Code] {
		return errs.From(err).Code(ErrCodeRejected).Attr("clickhouse_code", int(exception.Code)).Msg(what)
	}
	return errs.From(err).Retryable().Msg(what)
}
