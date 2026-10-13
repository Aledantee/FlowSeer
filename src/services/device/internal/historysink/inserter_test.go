package historysink

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// stubConn answers PrepareBatch from a script and leaves every other method
// to the nil embedded interface, which a test that reaches it would fail on.
type stubConn struct {
	clickhouse.Conn
	query      string
	prepareErr error
	batch      *stubBatch
}

func (c *stubConn) PrepareBatch(_ context.Context, query string, _ ...driver.PrepareBatchOption) (driver.Batch, error) {
	c.query = query
	if c.prepareErr != nil {
		return nil, c.prepareErr
	}
	return c.batch, nil
}

type stubBatch struct {
	driver.Batch
	appended  [][]any
	appendErr error
	sendErr   error
}

func (b *stubBatch) Append(v ...any) error {
	b.appended = append(b.appended, v)
	return b.appendErr
}
func (b *stubBatch) Send() error  { return b.sendErr }
func (b *stubBatch) Close() error { return nil }

func oneRow() []Row {
	return []Row{{TenantID: testTenant, DeviceID: uuid.New(), RecordID: uuid.New(), ReceivedAt: time.Now()}}
}

func TestTheInsertStatementNamesAsManyColumnsAsARowAppends(t *testing.T) {
	batch := &stubBatch{}
	conn := &stubConn{batch: batch}

	if err := NewInserter(conn, "flowseer").Insert(context.Background(), oneRow(), "token"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	open, closing := strings.Index(conn.query, "("), strings.LastIndex(conn.query, ")")
	columns := strings.Split(conn.query[open+1:closing], ",")
	if len(batch.appended) != 1 || len(batch.appended[0]) != len(columns) {
		t.Fatalf("appended %d values for the %d columns of %q", len(batch.appended[0]), len(columns), conn.query)
	}
	if !strings.HasPrefix(conn.query, "INSERT INTO `flowseer`.syslog (tenant_id,") {
		t.Errorf("statement = %q, want an insert into the syslog table of the configured database", conn.query)
	}
}

func TestAnEmptyArrayColumnIsAppendedAsANonNilSlice(t *testing.T) {
	batch := &stubBatch{}

	if err := NewInserter(&stubConn{batch: batch}, "flowseer").Insert(context.Background(), oneRow(), "token"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	for _, i := range []int{13, 14, 15, 16} {
		if got, ok := batch.appended[0][i].([]string); !ok || got == nil {
			t.Errorf("array value %d = %#v, want a non-nil []string", i, batch.appended[0][i])
		}
	}
}

func TestInsertSplitsServerErrorsIntoRejectedAndTransient(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		wantRejected bool
	}{
		{"type mismatch", &clickhouse.Exception{Code: 53}, true},
		{"cannot parse text", &clickhouse.Exception{Code: 6}, true},
		{"value out of range", &clickhouse.Exception{Code: 321}, true},
		{"wrapped rejection", fmt.Errorf("send: %w", &clickhouse.Exception{Code: 117}), true},
		{"access denied", &clickhouse.Exception{Code: 497}, false},
		{"memory limit", &clickhouse.Exception{Code: 241}, false},
		{"too many parts", &clickhouse.Exception{Code: 252}, false},
		{"connection closed", errors.New("write: broken pipe"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := &stubConn{batch: &stubBatch{sendErr: tt.err}}

			err := NewInserter(conn, "flowseer").Insert(context.Background(), oneRow(), "token")

			if err == nil {
				t.Fatal("insert returned nil for a failed send")
			}
			if got := IsRejected(err); got != tt.wantRejected {
				t.Errorf("IsRejected = %v, want %v for %v", got, tt.wantRejected, err)
			}
			if !tt.wantRejected && !errs.Retryable(err) {
				t.Errorf("a transient error is not marked retryable: %v", err)
			}
		})
	}
}

func TestAFailedPrepareIsTransientAndAFailedAppendIsRejected(t *testing.T) {
	prepare := NewInserter(&stubConn{prepareErr: errors.New("dial tcp: refused")}, "flowseer").Insert(context.Background(), oneRow(), "token")
	if prepare == nil || IsRejected(prepare) {
		t.Errorf("a failed prepare = %v, want a transient error", prepare)
	}

	appended := NewInserter(&stubConn{batch: &stubBatch{appendErr: errors.New("converting value")}}, "flowseer").Insert(context.Background(), oneRow(), "token")
	if !IsRejected(appended) {
		t.Errorf("a failed append = %v, want a rejection", appended)
	}
}
