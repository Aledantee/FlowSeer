// Package historyquery reads stored history from ClickHouse for a set of
// tenants. It is the one place a history read statement is built: every
// statement is scoped by the tenant set the interceptor admitted, which a
// caller cannot leave out.
package historyquery

import (
	"context"
	"encoding/base64"
	"errors"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/google/uuid"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/tenant"
)

// Error codes the package returns.
var (
	// ErrCodeNoTenants is a read whose context carries no admitted tenant.
	ErrCodeNoTenants = errs.NewCode("historyquery/no-tenants")
	// ErrCodeBadToken is a page token this package did not issue or that was
	// changed.
	ErrCodeBadToken = errs.NewCode("historyquery/bad-token")
	// ErrCodeLimit is a read that reached a limit of the reader's profile: rows,
	// bytes, time, memory, or concurrent queries.
	ErrCodeLimit = errs.NewCode("historyquery/limit")
	// ErrCodeStore is any other failure of the read, including a store that is
	// unreachable.
	ErrCodeStore = errs.NewCode("historyquery/store")
)

const (
	defaultPageSize = 100
	maxPageSize     = 1000
)

type scopeKind int

const (
	scopeDevice scopeKind = iota + 1
	scopeLocation
)

// Scope is what a read is about: one device or one location.
type Scope struct {
	kind scopeKind
	id   uuid.UUID
}

// DeviceScope reads the records of one device.
func DeviceScope(id uuid.UUID) Scope { return Scope{kind: scopeDevice, id: id} }

// LocationScope reads the records of the devices stamped with one location.
func LocationScope(id uuid.UUID) Scope { return Scope{kind: scopeLocation, id: id} }

// Query is one page of a syslog read. From is inclusive and To exclusive, both
// against the receive time.
type Query struct {
	Scope Scope
	From  time.Time
	To    time.Time
	// MinSeverity keeps records at this severity number or lower, which is
	// more severe. Nil keeps every record, including those with no priority.
	MinSeverity *uint8
	// PageSize is the most entries returned. Zero means 100 and the value is
	// capped at 1000.
	PageSize int
	// Cursor is the NextCursor of the previous page, with the rest of the
	// query unchanged. Empty means the first page.
	Cursor string
}

// Entry is one stored syslog record with the tenant that owns it.
type Entry struct {
	TenantID         string
	DeviceID         uuid.UUID
	ReceivedAt       time.Time
	RecordID         uuid.UUID
	ObservedAt       time.Time
	SentAt           time.Time
	Severity         uint8
	Facility         uint8
	Present          uint8
	Hostname         string
	AppName          string
	ProcID           string
	MsgID            string
	SDIDs            []string
	SDParamSDID      []string
	SDParamName      []string
	SDParamValue     []string
	Message          string
	MessageTruncated bool
	SourceIP         [16]byte
	LocationID       uuid.UUID
}

// Bits of [Entry.Present], matching the syslog table.
const (
	PresentSeverity uint8 = 1 << iota
	PresentFacility
	PresentSentAt
	PresentSourceIP
)

// Result is one page.
type Result struct {
	// Entries are newest first by receive time, then by record id.
	Entries []Entry
	// NextCursor is empty on the last page.
	NextCursor string
}

// Reader runs history reads through the service's reader pool. It is safe for
// concurrent use.
type Reader struct {
	conn     clickhouse.Conn
	database string
}

// NewReader returns a reader over conn for database. The database name must
// match the identifier form the service configuration enforces, since it is
// quoted into every statement.
func NewReader(conn clickhouse.Conn, database string) *Reader {
	return &Reader{conn: conn, database: database}
}

// ListSyslog returns one page of syslog records. The tenants are the set the
// authorization interceptor put on ctx ([tenant.SetFrom]), so a read without an
// admitted tenant fails with [ErrCodeNoTenants] before any statement is built.
//
// A record the store holds twice, as a redelivery past the insert
// deduplication window leaves it, is returned once.
func (r *Reader) ListSyslog(ctx context.Context, q Query) (Result, error) {
	tenants, err := tenant.SetFrom(ctx)
	if err != nil || len(tenants) == 0 {
		return Result{}, errs.From(err).Code(ErrCodeNoTenants).Msg("history read without an admitted tenant")
	}
	if q.Scope.kind == 0 {
		return Result{}, errs.New().Code(ErrCodeStore).Msg("history read without a scope")
	}
	size := q.PageSize
	if size <= 0 {
		size = defaultPageSize
	}
	size = min(size, maxPageSize)

	var after *cursor
	if q.Cursor != "" {
		position, err := decodeToken(q.Cursor)
		if err != nil {
			return Result{}, err
		}
		after = &position
	}
	// One extra row says whether a next page exists.
	stmt := buildStatement(r.database, q, tenants, after, uint32(size+1))

	rows, err := r.conn.Query(ctx, stmt.text, stmt.args...)
	if err != nil {
		return Result{}, classify(ctx, err)
	}
	defer func() { _ = rows.Close() }()

	entries := make([]Entry, 0, size+1)
	for rows.Next() {
		var e Entry
		var ip netip.Addr
		if err := rows.Scan(&e.TenantID, &e.DeviceID, &e.ReceivedAt, &e.RecordID, &e.ObservedAt, &e.SentAt,
			&e.Severity, &e.Facility, &e.Present, &e.Hostname, &e.AppName, &e.ProcID, &e.MsgID,
			&e.SDIDs, &e.SDParamSDID, &e.SDParamName, &e.SDParamValue,
			&e.Message, &e.MessageTruncated, &ip, &e.LocationID); err != nil {
			return Result{}, classify(ctx, err)
		}
		e.SourceIP = ip.As16()
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return Result{}, classify(ctx, err)
	}

	result := Result{Entries: entries}
	if len(entries) > size {
		result.Entries = entries[:size]
		last := result.Entries[size-1]
		result.NextCursor = encodeToken(cursor{ts: last.ReceivedAt, recordID: last.RecordID})
	}
	return result, nil
}

// limitCodes are the server exception codes for a limit of the reader's
// profile or the server (ErrorCodes.cpp, ClickHouse v26.8.20.9): TOO_MANY_ROWS,
// TIMEOUT_EXCEEDED, TOO_SLOW, TOO_MANY_SIMULTANEOUS_QUERIES,
// MEMORY_LIMIT_EXCEEDED, TOO_MANY_BYTES, and TOO_MANY_ROWS_OR_BYTES.
var limitCodes = map[int32]bool{158: true, 159: true, 160: true, 202: true, 241: true, 307: true, 396: true}

func classify(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	var exception *clickhouse.Exception
	if errors.As(err, &exception) && limitCodes[exception.Code] {
		return errs.From(err).Code(ErrCodeLimit).Attr("clickhouse_code", int(exception.Code)).
			UserMsg("the read matches more data than one call may read").
			Hint("narrow the time window or the scope").
			Msg("history read reached a reader limit")
	}
	return errs.From(err).Code(ErrCodeStore).Retryable().Msg("history read failed")
}

// encodeToken is the opaque page token of a keyset position.
func encodeToken(c cursor) string {
	raw := strconv.FormatInt(c.ts.UnixMilli(), 10) + "." + c.recordID.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeToken returns the position a non-empty token names.
func decodeToken(token string) (cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return cursor{}, errs.From(err).Code(ErrCodeBadToken).Msg("page token is not valid")
	}
	millis, id, _ := strings.Cut(string(raw), ".")
	ms, convErr := strconv.ParseInt(millis, 10, 64)
	record, idErr := uuid.Parse(id)
	if convErr != nil || idErr != nil {
		return cursor{}, errs.New().Code(ErrCodeBadToken).Msg("page token is not valid")
	}
	return cursor{ts: time.UnixMilli(ms).UTC(), recordID: record}, nil
}
