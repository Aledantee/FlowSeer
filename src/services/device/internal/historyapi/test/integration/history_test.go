//go:build history_integration

package integration_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	connect "connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	historyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/history/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/history/v1/historyv1connect"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	logv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/log/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/tenant"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz"
	"go.aledante.io/FlowSeer/src/services/device/internal/history"
	"go.aledante.io/FlowSeer/src/services/device/internal/history/historytest"
	"go.aledante.io/FlowSeer/src/services/device/internal/historyapi"
	"go.aledante.io/FlowSeer/src/services/device/internal/historyquery"
	"go.aledante.io/FlowSeer/src/services/device/internal/historysink"
)

const (
	tenantA1 = "018f0000-0000-7000-8000-0000000000a1"
	tenantA2 = "018f0000-0000-7000-8000-0000000000a2"
	tenantA3 = "018f0000-0000-7000-8000-0000000000a3"
	device1  = "018f0000-0000-7000-8000-0000000000d1"
	device2  = "018f0000-0000-7000-8000-0000000000d2"
)

var dayOne = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

// principalInterceptor stands in for authentication.
type principalInterceptor struct{}

func (principalInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		return next(authn.NewContext(ctx, authn.Principal{ID: "operator"}), req)
	}
}

func (principalInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (principalInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

// viewerChecker grants every check except the viewer relation on tenants the
// test lists as denied.
type viewerChecker struct {
	mu     sync.Mutex
	denied map[string]bool
}

func (c *viewerChecker) deny(tenantID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.denied == nil {
		c.denied = map[string]bool{}
	}
	c.denied[tenantID] = true
}

func (c *viewerChecker) Check(_ context.Context, q authz.Query) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return q.Relation != "viewer" || !c.denied[q.Object[len("tenant:"):]], nil
}

func (c *viewerChecker) BatchCheck(ctx context.Context, queries []authz.Query) ([]bool, error) {
	answers := make([]bool, len(queries))
	for i, q := range queries {
		answers[i], _ = c.Check(ctx, q)
	}
	return answers, nil
}

type env struct {
	server  *historytest.Server
	store   *history.Store
	client  historyv1connect.HistoryServiceClient
	checker *viewerChecker
}

func start(t *testing.T) *env {
	t.Helper()
	server := historytest.Start(t, false)
	store := server.Migrated(t)
	checker := &viewerChecker{}
	mux := http.NewServeMux()
	mux.Handle(historyv1connect.NewHistoryServiceHandler(
		historyapi.New(historyquery.NewReader(store.Reader(), "flowseer")),
		connect.WithInterceptors(principalInterceptor{}, authz.NewInterceptor(checker)),
	))
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)
	return &env{
		server:  server,
		store:   store,
		client:  historyv1connect.NewHistoryServiceClient(httpServer.Client(), httpServer.URL),
		checker: checker,
	}
}

func syslogRow(tenantID, deviceID string, recordID uuid.UUID, ts time.Time, severity uint8, message string) historysink.Row {
	return historysink.Row{
		TenantID:       tenantID,
		DeviceID:       uuid.MustParse(deviceID),
		ReceivedAt:     ts,
		RecordID:       recordID,
		ObservedAt:     ts,
		SentAt:         time.Unix(0, 0),
		Severity:       severity,
		Present:        historysink.PresentSeverity,
		Message:        message,
		RetentionClass: 2,
	}
}

// insert stores rows through the sink's inserter, as the service does.
func (e *env) insert(t *testing.T, rows ...historysink.Row) {
	t.Helper()
	inserter := historysink.NewInserter(e.store.Writer(), "flowseer")
	if err := inserter.Insert(context.Background(), rows, uuid.NewString()); err != nil {
		t.Fatalf("insert: %v", err)
	}
}

func deviceRequest(from, to time.Time, tenants string) *connect.Request[historyv1.ListSyslogRecordsRequest] {
	req := connect.NewRequest(historyv1.ListSyslogRecordsRequest_builder{
		Device: inventoryv1.DeviceGlobalRef_builder{Device: inventoryv1.DeviceLocalRef_builder{Id: proto.String(device1)}.Build()}.Build(),
		From:   timestamppb.New(from),
		To:     timestamppb.New(to),
	}.Build())
	req.Header().Set("X-FlowSeer-Tenant", tenants)
	return req
}

func TestARecordInsertedTwiceFifteenMinutesApartIsListedOnceBeforeAndAfterMerges(t *testing.T) {
	e := start(t)
	record := uuid.New()
	first := syslogRow(tenantA1, device1, record, dayOne.Add(time.Hour), 3, "%LINK-3-UPDOWN: Gi1/0/1 down")
	e.insert(t, first)
	e.insert(t, first)
	if got := e.server.Scalar(t, "SELECT toString(count()) FROM flowseer.syslog WHERE record_id = '"+record.String()+"'"); got != "2" {
		t.Fatalf("rows stored before any merge = %s, want 2: the redelivery must really be stored twice", got)
	}

	list := func(stage string) {
		resp, err := e.client.ListSyslogRecords(context.Background(), deviceRequest(dayOne, dayOne.Add(24*time.Hour), tenantA1))
		if err != nil {
			t.Fatalf("%s: list: %v", stage, err)
		}
		if n := len(resp.Msg.GetEntries()); n != 1 {
			t.Errorf("%s: the record is listed %d times, want once", stage, n)
		}
	}
	list("before the merge")

	if err := e.server.Admin.Exec(context.Background(), "OPTIMIZE TABLE flowseer.syslog FINAL"); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if got := e.server.Scalar(t, "SELECT toString(count()) FROM flowseer.syslog WHERE record_id = '"+record.String()+"'"); got != "1" {
		t.Fatalf("rows stored after the merge = %s, want 1: the merge must have collapsed the repeat", got)
	}
	list("after the merge")
}

func TestAHeaderOfTwoTenantsReturnsBothTenantsRowsEachNamingItsOwner(t *testing.T) {
	e := start(t)
	a1, a2 := uuid.New(), uuid.New()
	e.insert(t,
		syslogRow(tenantA1, device1, a1, dayOne.Add(time.Hour), 3, "from the first tenant"),
		syslogRow(tenantA2, device1, a2, dayOne.Add(2*time.Hour), 3, "from the second tenant"),
		syslogRow(tenantA3, device1, uuid.New(), dayOne.Add(3*time.Hour), 3, "from a tenant the call does not name"),
	)

	resp, err := e.client.ListSyslogRecords(context.Background(), deviceRequest(dayOne, dayOne.Add(24*time.Hour), tenantA1+","+tenantA2))
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if err := protovalidate.Validate(resp.Msg); err != nil {
		t.Fatalf("the response fails its schema rules: %v", err)
	}
	entries := resp.Msg.GetEntries()
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if entries[0].GetRecordId() != a2.String() || entries[0].GetTenant().GetId() != tenantA2 ||
		entries[1].GetRecordId() != a1.String() || entries[1].GetTenant().GetId() != tenantA1 {
		t.Errorf("entries = %v, want the second tenant's newer row, then the first tenant's", entries)
	}
	if got := string(entries[1].GetRecord().GetMessage()); got != "from the first tenant" {
		t.Errorf("message = %q", got)
	}
}

func TestATenantTheCallerCannotViewFailsTheCallAndReturnsNoRows(t *testing.T) {
	e := start(t)
	e.insert(t,
		syslogRow(tenantA1, device1, uuid.New(), dayOne.Add(time.Hour), 3, "readable"),
		syslogRow(tenantA3, device1, uuid.New(), dayOne.Add(2*time.Hour), 3, "not readable"),
	)
	e.checker.deny(tenantA3)

	resp, err := e.client.ListSyslogRecords(context.Background(), deviceRequest(dayOne, dayOne.Add(24*time.Hour), tenantA1+","+tenantA3))

	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v (err %v), want PermissionDenied", connect.CodeOf(err), err)
	}
	if resp != nil {
		t.Errorf("a denied call returned %d entries", len(resp.Msg.GetEntries()))
	}
}

func TestALocationReadFindsTheRecordsOfEveryDeviceStampedWithIt(t *testing.T) {
	e := start(t)
	location := uuid.MustParse("018f0000-0000-7000-8000-0000000000e1")
	at := func(deviceID string, ts time.Time, loc uuid.UUID, msg string) historysink.Row {
		row := syslogRow(tenantA1, deviceID, uuid.New(), ts, 4, msg)
		row.LocationID = loc
		return row
	}
	e.insert(t,
		at(device1, dayOne.Add(time.Hour), location, "device one in the location"),
		at(device2, dayOne.Add(2*time.Hour), location, "device two in the location"),
		at(device2, dayOne.Add(3*time.Hour), uuid.New(), "device two after it moved"),
	)
	req := connect.NewRequest(historyv1.ListSyslogRecordsRequest_builder{
		Location: inventoryv1.LocationGlobalRef_builder{Location: inventoryv1.LocationLocalRef_builder{Id: proto.String(location.String())}.Build()}.Build(),
		From:     timestamppb.New(dayOne),
		To:       timestamppb.New(dayOne.Add(24 * time.Hour)),
	}.Build())
	req.Header().Set("X-FlowSeer-Tenant", tenantA1)

	resp, err := e.client.ListSyslogRecords(context.Background(), req)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	entries := resp.Msg.GetEntries()
	if len(entries) != 2 || string(entries[0].GetRecord().GetMessage()) != "device two in the location" || string(entries[1].GetRecord().GetMessage()) != "device one in the location" {
		t.Errorf("entries = %v, want the two records stamped with the location, newest first", entries)
	}
}

func TestPagingWalksTheWindowNewestFirstWithoutRepeatsOrGaps(t *testing.T) {
	e := start(t)
	var rows []historysink.Row
	for i := range 25 {
		rows = append(rows, syslogRow(tenantA1, device1, uuid.New(), dayOne.Add(time.Duration(i)*time.Minute), uint8(i%8), "m"))
	}
	e.insert(t, rows...)
	var seen []time.Time
	token := ""

	for page := 1; ; page++ {
		req := deviceRequest(dayOne, dayOne.Add(24*time.Hour), tenantA1)
		req.Msg.SetPageSize(10)
		if token != "" {
			req.Msg.SetPageToken(token)
		}
		resp, err := e.client.ListSyslogRecords(context.Background(), req)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		for _, entry := range resp.Msg.GetEntries() {
			seen = append(seen, entry.GetRecord().GetReceivedAt().AsTime())
		}
		if !resp.Msg.HasNextPageToken() {
			if page != 3 {
				t.Errorf("the walk ended on page %d, want 3 for 25 entries at 10 a page", page)
			}
			break
		}
		token = resp.Msg.GetNextPageToken()
		if page > 5 {
			t.Fatal("the walk never ended")
		}
	}

	if len(seen) != 25 {
		t.Fatalf("entries seen = %d, want 25", len(seen))
	}
	for i := 1; i < len(seen); i++ {
		if !seen[i].Before(seen[i-1]) {
			t.Errorf("entry %d at %v is not older than entry %d at %v", i, seen[i], i-1, seen[i-1])
		}
	}
}

func TestPagingTwoTenantsThatShareARecordIdAtOneTimeLosesNoRow(t *testing.T) {
	e := start(t)
	shared := uuid.New()
	at := dayOne.Add(time.Hour)
	e.insert(t,
		syslogRow(tenantA1, device1, shared, at, 3, "from the first tenant"),
		syslogRow(tenantA2, device1, shared, at, 3, "from the second tenant"),
	)
	var tenants []string
	token := ""

	for page := 1; ; page++ {
		req := deviceRequest(dayOne, dayOne.Add(24*time.Hour), tenantA1+","+tenantA2)
		req.Msg.SetPageSize(1)
		if token != "" {
			req.Msg.SetPageToken(token)
		}
		resp, err := e.client.ListSyslogRecords(context.Background(), req)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if n := len(resp.Msg.GetEntries()); n != 1 {
			t.Fatalf("page %d holds %d entries, want 1", page, n)
		}
		tenants = append(tenants, resp.Msg.GetEntries()[0].GetTenant().GetId())
		if !resp.Msg.HasNextPageToken() {
			break
		}
		token = resp.Msg.GetNextPageToken()
		if page > 3 {
			t.Fatal("the walk never ended")
		}
	}

	if len(tenants) != 2 || tenants[0] != tenantA2 || tenants[1] != tenantA1 {
		t.Errorf("tenants by page = %v, want [%s %s]: one row each, the larger tenant id first", tenants, tenantA2, tenantA1)
	}
}

func TestAMinimumSeverityKeepsOnlyRecordsAtThatSeverityOrWorse(t *testing.T) {
	e := start(t)
	var rows []historysink.Row
	for severity := range 8 {
		rows = append(rows, syslogRow(tenantA1, device1, uuid.New(), dayOne.Add(time.Duration(severity)*time.Minute), uint8(severity), "m"))
	}
	noPriority := syslogRow(tenantA1, device1, uuid.New(), dayOne.Add(time.Hour), 0, "no priority")
	noPriority.Present = 0
	e.insert(t, append(rows, noPriority)...)
	req := deviceRequest(dayOne, dayOne.Add(24*time.Hour), tenantA1)
	req.Msg.SetMinSeverity(logv1.SyslogSeverity_SYSLOG_SEVERITY_ERROR)

	resp, err := e.client.ListSyslogRecords(context.Background(), req)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if n := len(resp.Msg.GetEntries()); n != 4 {
		t.Errorf("entries = %d, want 4 (emergency, alert, critical, error) and not the message without a priority", n)
	}
	for _, entry := range resp.Msg.GetEntries() {
		if !entry.GetRecord().HasSeverity() || entry.GetRecord().GetSeverity() > logv1.SyslogSeverity_SYSLOG_SEVERITY_ERROR {
			t.Errorf("entry with severity %v present %v passed the filter", entry.GetRecord().GetSeverity(), entry.GetRecord().HasSeverity())
		}
	}
}

func TestARecordsFieldsSurviveTheRoundTripThroughTheStore(t *testing.T) {
	e := start(t)
	row := syslogRow(tenantA1, device1, uuid.New(), dayOne.Add(time.Hour), 0, "%LINK-3-UPDOWN: \xff\xfe down")
	row.Present = historysink.PresentSeverity | historysink.PresentFacility | historysink.PresentSentAt | historysink.PresentSourceIP
	row.Facility = 23
	row.SentAt = dayOne.Add(time.Hour - time.Second)
	row.Hostname = "sw1"
	row.SDIDs = []string{"origin", "empty"}
	row.SDParamSDID = []string{"origin"}
	row.SDParamName = []string{"ip"}
	row.SDParamValue = []string{"10.0.0.1"}
	row.SourceIP = [16]byte{10: 0xff, 11: 0xff, 12: 10, 13: 1, 14: 2, 15: 3}
	e.insert(t, row)

	resp, err := e.client.ListSyslogRecords(context.Background(), deviceRequest(dayOne, dayOne.Add(24*time.Hour), tenantA1))
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if err := protovalidate.Validate(resp.Msg); err != nil {
		t.Fatalf("the response fails its schema rules: %v", err)
	}
	r := resp.Msg.GetEntries()[0].GetRecord()
	if string(r.GetMessage()) != row.Message || !r.HasSeverity() || r.GetSeverity() != logv1.SyslogSeverity_SYSLOG_SEVERITY_EMERGENCY ||
		r.GetFacility() != logv1.SyslogFacility_SYSLOG_FACILITY_LOCAL7 || r.GetHostname() != "sw1" ||
		!r.GetSentAt().AsTime().Equal(row.SentAt) || r.GetSourceAddress().GetV4().GetOctets()[3] != 3 ||
		len(r.GetStructuredData()) != 2 || r.GetStructuredData()[0].GetParams()[0].GetValue() != "10.0.0.1" {
		t.Errorf("record = %v, want the stored fields back", r)
	}
}

func TestAThirtyDayReadPastTheReaderRowLimitFailsWhileInsertsKeepCompleting(t *testing.T) {
	e := start(t)
	// 12,000,000 rows of one device over 30 days. The reader profile caps a
	// read at 10,000,000 rows.
	seed := `INSERT INTO flowseer.syslog (tenant_id, device_id, ts, record_id, severity, present, message, retention_class)
SELECT '` + tenantA1 + `', toUUID('` + device1 + `'), toDateTime64('2026-09-01 00:00:00', 3, 'UTC') + toIntervalMillisecond(intDiv(number * 2592000000, 12000000)),
       generateUUIDv4(), 7, 1, 'seed', 2
FROM numbers(12000000)`
	if err := e.server.Admin.Exec(context.Background(), seed); err != nil {
		t.Fatalf("seed 12,000,000 rows: %v", err)
	}
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	inserted := make(chan error, 1)
	go func() {
		rows := make([]historysink.Row, 10000)
		for i := range rows {
			rows[i] = syslogRow(tenantA2, device2, uuid.New(), dayOne.Add(time.Duration(i)*time.Second), 3, "concurrent insert")
		}
		inserted <- historysink.NewInserter(e.store.Writer(), "flowseer").Insert(context.Background(), rows, uuid.NewString())
	}()
	to := from.Add(30 * 24 * time.Hour)

	_, err := e.client.ListSyslogRecords(context.Background(), deviceRequest(from, to, tenantA1))
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Errorf("the 30-day read gave %v (err %v), want ResourceExhausted", connect.CodeOf(err), err)
	}

	// The same read straight through the reader names the limit that stopped it.
	ctx := tenant.WithSet(context.Background(), []string{tenantA1})
	_, readErr := historyquery.NewReader(e.store.Reader(), "flowseer").ListSyslog(ctx, historyquery.Query{
		Scope: historyquery.DeviceScope(uuid.MustParse(device1)), From: from, To: to,
	})
	if code, _ := errs.CodeOf(readErr); code != historyquery.ErrCodeLimit || !strings.Contains(readErr.Error(), "max_rows_to_read") {
		t.Errorf("the reader failed with %v, want the max_rows_to_read limit of the reader profile", readErr)
	}

	// A day of the same device is a read the profile allows.
	day, err := e.client.ListSyslogRecords(context.Background(), deviceRequest(from, from.Add(24*time.Hour), tenantA1))
	if err != nil {
		t.Fatalf("the one-day read: %v", err)
	}
	if n := len(day.Msg.GetEntries()); n != 100 {
		t.Errorf("the one-day read gave %d entries, want a page of 100", n)
	}
	select {
	case insertErr := <-inserted:
		if insertErr != nil {
			t.Errorf("the concurrent insert of 10,000 rows failed: %v", insertErr)
		}
	case <-time.After(60 * time.Second):
		t.Error("the concurrent insert of 10,000 rows did not complete")
	}
}
