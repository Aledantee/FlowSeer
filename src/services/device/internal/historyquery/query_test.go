package historyquery

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/tenant"
)

const (
	tenantA = "018f0000-0000-7000-8000-0000000000a1"
	tenantB = "018f0000-0000-7000-8000-0000000000a2"
)

var (
	deviceID   = uuid.MustParse("018f0000-0000-7000-8000-0000000000d1")
	locationID = uuid.MustParse("018f0000-0000-7000-8000-0000000000e1")
	windowFrom = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	windowTo   = time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
)

func deviceQuery() Query {
	return Query{Scope: DeviceScope(deviceID), From: windowFrom, To: windowTo}
}

func locationQuery() Query {
	return Query{Scope: LocationScope(locationID), From: windowFrom, To: windowTo}
}

func TestEveryWhereOfEveryStatementStartsWithTheTenantCondition(t *testing.T) {
	severity := uint8(3)
	filtered := func(q Query) Query {
		q.MinSeverity = &severity
		return q
	}
	tests := []struct {
		name  string
		query Query
		after bool
	}{
		{"device", deviceQuery(), false},
		{"location", locationQuery(), false},
		{"device with a severity", filtered(deviceQuery()), false},
		{"location with a severity", filtered(locationQuery()), false},
		{"device after a cursor", deviceQuery(), true},
		{"location after a cursor", locationQuery(), true},
		{"location with a severity after a cursor", filtered(locationQuery()), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var after *cursor
			if tt.after {
				after = &cursor{ts: windowTo, recordID: uuid.New()}
			}

			stmt := buildStatement("flowseer", tt.query, []string{tenantA, tenantB}, after, 101)

			wheres := strings.Count(stmt.text, "WHERE ")
			led := strings.Count(stmt.text, "WHERE "+tenantCondition)
			if wheres == 0 || wheres != led {
				t.Errorf("%d WHERE clauses and %d lead with %q in:\n%s", wheres, led, tenantCondition, stmt.text)
			}
			if strings.Contains(stmt.text, tenantA) || strings.Contains(stmt.text, tenantB) {
				t.Errorf("a tenant id is part of the statement text, want it bound as a parameter:\n%s", stmt.text)
			}
		})
	}
}

func TestEveryStatementKeepsOneRowPerRecord(t *testing.T) {
	for name, q := range map[string]Query{"device": deviceQuery(), "location": locationQuery()} {
		t.Run(name, func(t *testing.T) {
			stmt := buildStatement("flowseer", q, []string{tenantA}, nil, 101)

			if !strings.Contains(stmt.text, "LIMIT 1 BY tenant_id, record_id") {
				t.Errorf("statement has no LIMIT 1 BY on the record key:\n%s", stmt.text)
			}
			if !strings.Contains(stmt.text, "ORDER BY ts DESC, record_id DESC") {
				t.Errorf("statement does not order newest first:\n%s", stmt.text)
			}
		})
	}
}

func TestADeviceScopeReadsSyslogAndALocationScopeReadsTheLocationTable(t *testing.T) {
	device := buildStatement("flowseer", deviceQuery(), []string{tenantA}, nil, 101).text
	location := buildStatement("flowseer", locationQuery(), []string{tenantA}, nil, 101).text

	if strings.Contains(device, "syslog_by_location") || !strings.Contains(device, "FROM `flowseer`.syslog\n") {
		t.Errorf("device statement:\n%s\nwant a read of syslog only", device)
	}
	if !strings.Contains(location, "FROM `flowseer`.syslog_by_location") || !strings.Contains(location, "(tenant_id, device_id, ts, record_id) IN (") {
		t.Errorf("location statement:\n%s\nwant the keys from syslog_by_location and the rows from syslog", location)
	}
}

func TestASeverityFilterLeavesOutMessagesWithoutAPriority(t *testing.T) {
	severity := uint8(3)
	q := deviceQuery()
	without := buildStatement("flowseer", q, []string{tenantA}, nil, 101).text
	q.MinSeverity = &severity
	with := buildStatement("flowseer", q, []string{tenantA}, nil, 101).text

	if strings.Contains(without, "severity") && strings.Contains(without, "bitAnd(present, 1)") {
		t.Errorf("a statement without a filter mentions the severity bit:\n%s", without)
	}
	if !strings.Contains(with, "bitAnd(present, 1) = 1 AND severity <= {severity:UInt8}") {
		t.Errorf("statement with a filter:\n%s\nwant the presence bit tested with the comparison", with)
	}
}

func TestACursorAddsTheKeysetConditionAndItsParameters(t *testing.T) {
	record := uuid.New()
	stmt := buildStatement("flowseer", deviceQuery(), []string{tenantA}, &cursor{ts: windowTo, recordID: record}, 101)

	if !strings.Contains(stmt.text, "(ts, record_id) < ({after_ts:DateTime64(3)}, {after_id:UUID})") {
		t.Errorf("statement:\n%s\nwant the keyset condition", stmt.text)
	}
	if got := namedValue(t, stmt.args, "after_id"); got != record.String() {
		t.Errorf("after_id = %v, want %s", got, record)
	}
}

func namedValue(t *testing.T, args []any, name string) any {
	t.Helper()
	for _, a := range args {
		switch v := a.(type) {
		case driver.NamedValue:
			if v.Name == name {
				return v.Value
			}
		case driver.NamedDateValue:
			if v.Name == name {
				return v.Value
			}
		}
	}
	t.Fatalf("no argument named %q in %v", name, args)
	return nil
}

func TestAReadWithoutAnAdmittedTenantFailsBeforeAnyStatement(t *testing.T) {
	conn := &stubConn{}
	reader := NewReader(conn, "flowseer")

	for name, ctx := range map[string]context.Context{
		"no tenant":    context.Background(),
		"an empty set": tenant.WithSet(context.Background(), nil),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := reader.ListSyslog(ctx, deviceQuery())

			if code, _ := errs.CodeOf(err); code != ErrCodeNoTenants {
				t.Errorf("code = %v (err %v), want %v", code, err, ErrCodeNoTenants)
			}
			if conn.queries != 0 {
				t.Errorf("a statement ran for a read with no tenant")
			}
		})
	}
}

// stubConn answers Query from a script and leaves the rest of the interface
// to the nil embedded value.
type stubConn struct {
	clickhouse.Conn
	queries int
	text    string
	args    []any
	rows    int
	err     error
}

func (c *stubConn) Query(_ context.Context, query string, args ...any) (driver.Rows, error) {
	c.queries++
	c.text, c.args = query, args
	if c.err != nil {
		return nil, c.err
	}
	return &stubRows{remaining: c.rows}, nil
}

type stubRows struct {
	driver.Rows
	remaining int
	served    int
}

func (r *stubRows) Next() bool {
	if r.remaining == 0 {
		return false
	}
	r.remaining--
	r.served++
	return true
}

func (r *stubRows) Scan(dest ...any) error {
	i := r.served
	*dest[0].(*string) = tenantA
	*dest[1].(*uuid.UUID) = deviceID
	*dest[2].(*time.Time) = windowTo.Add(-time.Duration(i) * time.Second)
	*dest[3].(*uuid.UUID) = uuid.MustParse(fmt.Sprintf("018f0000-0000-7000-8000-%012d", i))
	*dest[19].(*netip.Addr) = netip.MustParseAddr("::ffff:10.1.2.3")
	return nil
}

func (r *stubRows) Err() error   { return nil }
func (r *stubRows) Close() error { return nil }

func tenantContext() context.Context {
	return tenant.WithSet(context.Background(), []string{tenantA, tenantB})
}

func TestAPageHoldsPageSizeEntriesAndATokenWhenMoreRemain(t *testing.T) {
	conn := &stubConn{rows: 4}
	q := deviceQuery()
	q.PageSize = 3

	result, err := NewReader(conn, "flowseer").ListSyslog(tenantContext(), q)
	if err != nil {
		t.Fatalf("ListSyslog: %v", err)
	}

	if got := namedValue(t, conn.args, "limit"); got != uint32(4) {
		t.Errorf("limit = %v, want 4: one more than the page to find out whether another follows", got)
	}
	if len(result.Entries) != 3 || result.NextToken == "" {
		t.Fatalf("page = %d entries and token %q, want 3 and a token", len(result.Entries), result.NextToken)
	}
	position, err := decodeToken(result.NextToken)
	if err != nil {
		t.Fatalf("decode the token: %v", err)
	}
	last := result.Entries[2]
	if !position.ts.Equal(last.ReceivedAt) || position.recordID != last.RecordID {
		t.Errorf("token position = %v, want the last entry %v %v", position, last.ReceivedAt, last.RecordID)
	}
}

func TestTheLastPageHasNoToken(t *testing.T) {
	conn := &stubConn{rows: 3}
	q := deviceQuery()
	q.PageSize = 3

	result, err := NewReader(conn, "flowseer").ListSyslog(tenantContext(), q)
	if err != nil {
		t.Fatalf("ListSyslog: %v", err)
	}

	if len(result.Entries) != 3 || result.NextToken != "" {
		t.Errorf("page = %d entries and token %q, want 3 and no token", len(result.Entries), result.NextToken)
	}
}

func TestThePageSizeDefaultsToOneHundredAndIsCappedAtOneThousand(t *testing.T) {
	for size, want := range map[int]uint32{0: 101, -5: 101, 50: 51, 1000: 1001, 5000: 1001} {
		conn := &stubConn{}
		q := deviceQuery()
		q.PageSize = size

		if _, err := NewReader(conn, "flowseer").ListSyslog(tenantContext(), q); err != nil {
			t.Fatalf("size %d: %v", size, err)
		}

		if got := namedValue(t, conn.args, "limit"); got != want {
			t.Errorf("page size %d bound limit %v, want %d", size, got, want)
		}
	}
}

func TestTheTenantSetIsBoundAsAParameter(t *testing.T) {
	conn := &stubConn{}

	if _, err := NewReader(conn, "flowseer").ListSyslog(tenantContext(), deviceQuery()); err != nil {
		t.Fatalf("ListSyslog: %v", err)
	}

	got, ok := namedValue(t, conn.args, "tenants").([]string)
	if !ok || len(got) != 2 || got[0] != tenantA || got[1] != tenantB {
		t.Errorf("tenants parameter = %v, want [%s %s]", got, tenantA, tenantB)
	}
}

func TestAServerLimitIsAResourceLimitAndOtherFailuresAreRetryableStoreErrors(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantCode  errs.Code
		retryable bool
	}{
		{"too many rows", &clickhouse.Exception{Code: 158, Message: "Limit for rows exceeded"}, ErrCodeLimit, false},
		{"timeout", &clickhouse.Exception{Code: 159}, ErrCodeLimit, false},
		{"memory", &clickhouse.Exception{Code: 241}, ErrCodeLimit, false},
		{"wrapped limit", fmt.Errorf("query: %w", &clickhouse.Exception{Code: 307}), ErrCodeLimit, false},
		{"unknown table", &clickhouse.Exception{Code: 60}, ErrCodeStore, true},
		{"connection refused", errors.New("dial tcp: refused"), ErrCodeStore, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := &stubConn{err: tt.err}

			_, err := NewReader(conn, "flowseer").ListSyslog(tenantContext(), deviceQuery())

			if code, _ := errs.CodeOf(err); code != tt.wantCode {
				t.Errorf("code = %v (err %v), want %v", code, err, tt.wantCode)
			}
			if errs.Retryable(err) != tt.retryable {
				t.Errorf("retryable = %v, want %v", errs.Retryable(err), tt.retryable)
			}
			if tt.wantCode == ErrCodeLimit && errs.UserMessage(err) == "" {
				t.Error("a limit error has no user message")
			}
		})
	}
}

func TestACancelledReadReturnsTheContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(tenantContext())
	cancel()
	conn := &stubConn{err: errors.New("write: broken pipe")}

	_, err := NewReader(conn, "flowseer").ListSyslog(ctx, deviceQuery())

	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled unwrapped", err)
	}
}

func TestATokenRoundTripsAndABadOneIsRefused(t *testing.T) {
	want := cursor{ts: time.UnixMilli(1790899200123).UTC(), recordID: uuid.MustParse("018f0000-0000-7000-8000-000000000101")}

	got, err := decodeToken(encodeToken(want))
	if err != nil || !got.ts.Equal(want.ts) || got.recordID != want.recordID {
		t.Fatalf("round trip = %v, %v, want %v", got, err, want)
	}

	for name, token := range map[string]string{
		"not base64":     "***",
		"no separator":   encodeRaw("1790899200123"),
		"bad millis":     encodeRaw("x." + want.recordID.String()),
		"bad record id":  encodeRaw("1790899200123.not-a-uuid"),
		"empty payload":  encodeRaw(""),
		"truncated uuid": encodeRaw("1790899200123.018f0000-0000"),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := decodeToken(token)

			if code, _ := errs.CodeOf(err); code != ErrCodeBadToken {
				t.Errorf("code = %v (err %v), want %v", code, err, ErrCodeBadToken)
			}
		})
	}
}

func encodeRaw(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}
