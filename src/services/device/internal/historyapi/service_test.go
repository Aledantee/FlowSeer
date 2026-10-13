package historyapi_test

import (
	"context"
	"errors"
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
	errsv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/errs/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	logv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/log/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/tenant"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz"
	"go.aledante.io/FlowSeer/src/services/device/internal/historyapi"
	"go.aledante.io/FlowSeer/src/services/device/internal/historyquery"
)

const (
	tenantA  = "018f0000-0000-7000-8000-0000000000a1"
	tenantB  = "018f0000-0000-7000-8000-0000000000a2"
	deviceID = "018f0000-0000-7000-8000-0000000000d1"
	siteID   = "018f0000-0000-7000-8000-0000000000e1"
	recordID = "018f0000-0000-7000-8000-000000000101"
)

// fakeReader records the query and the tenant set it was called with.
type fakeReader struct {
	mu      sync.Mutex
	calls   int
	query   historyquery.Query
	tenants []string
	result  historyquery.Result
	err     error
}

func (f *fakeReader) ListSyslog(ctx context.Context, q historyquery.Query) (historyquery.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.query = q
	f.tenants, _ = tenant.SetFrom(ctx)
	return f.result, f.err
}

func (f *fakeReader) snapshot() (calls int, q historyquery.Query, tenants []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.query, f.tenants
}

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

// denyingChecker answers every check true except those deny matches.
type denyingChecker struct {
	deny    func(authz.Query) bool
	mu      sync.Mutex
	queries []authz.Query
}

func (c *denyingChecker) Check(_ context.Context, q authz.Query) (bool, error) {
	c.mu.Lock()
	c.queries = append(c.queries, q)
	c.mu.Unlock()
	return c.deny == nil || !c.deny(q), nil
}

func (c *denyingChecker) BatchCheck(ctx context.Context, queries []authz.Query) ([]bool, error) {
	answers := make([]bool, len(queries))
	for i, q := range queries {
		answers[i], _ = c.Check(ctx, q)
	}
	return answers, nil
}

type harness struct {
	client  historyv1connect.HistoryServiceClient
	reader  *fakeReader
	checker *denyingChecker
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	reader := &fakeReader{}
	checker := &denyingChecker{}
	mux := http.NewServeMux()
	mux.Handle(historyv1connect.NewHistoryServiceHandler(historyapi.New(reader),
		connect.WithInterceptors(principalInterceptor{}, authz.NewInterceptor(checker))))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return &harness{client: historyv1connect.NewHistoryServiceClient(server.Client(), server.URL), reader: reader, checker: checker}
}

func deviceRequest(header ...string) *connect.Request[historyv1.ListSyslogRecordsRequest] {
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	req := connect.NewRequest(historyv1.ListSyslogRecordsRequest_builder{
		Device: inventoryv1.DeviceGlobalRef_builder{Device: inventoryv1.DeviceLocalRef_builder{Id: proto.String(deviceID)}.Build()}.Build(),
		From:   timestamppb.New(from),
		To:     timestamppb.New(from.Add(24 * time.Hour)),
	}.Build())
	for _, line := range header {
		req.Header().Add("X-FlowSeer-Tenant", line)
	}
	return req
}

func errorCode(t *testing.T, err error) string {
	t.Helper()
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("error %T is not a connect error: %v", err, err)
	}
	for _, detail := range connectErr.Details() {
		if msg, valErr := detail.Value(); valErr == nil {
			if payload, ok := msg.(*errsv1.ErrorPayload); ok {
				return payload.GetCode()
			}
		}
	}
	return ""
}

func TestTheServiceReadsTheTenantsTheInterceptorAdmittedAsASet(t *testing.T) {
	h := newHarness(t)

	if _, err := h.client.ListSyslogRecords(context.Background(), deviceRequest(tenantA+","+tenantB)); err != nil {
		t.Fatalf("call: %v", err)
	}

	calls, query, tenants := h.reader.snapshot()
	if calls != 1 || len(tenants) != 2 || tenants[0] != tenantA || tenants[1] != tenantB {
		t.Errorf("reader called %d times for tenants %v, want once for [%s %s]", calls, tenants, tenantA, tenantB)
	}
	if query.Scope != historyquery.DeviceScope(uuid.MustParse(deviceID)) {
		t.Errorf("scope = %+v, want the device", query.Scope)
	}
	if !query.From.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) || !query.To.Equal(time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("window = %v to %v", query.From, query.To)
	}
}

func TestATenantTheCallerCannotViewFailsTheWholeCallWithNoReader(t *testing.T) {
	h := newHarness(t)
	h.checker.deny = func(q authz.Query) bool { return q.Object == "tenant:"+tenantB && q.Relation == "viewer" }

	_, err := h.client.ListSyslogRecords(context.Background(), deviceRequest(tenantA+","+tenantB))

	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v (err %v), want PermissionDenied", connect.CodeOf(err), err)
	}
	if calls, _, _ := h.reader.snapshot(); calls != 0 {
		t.Errorf("the reader ran %d times after a denied tenant", calls)
	}
}

func TestAListOfTwentySixTenantsIsRefusedAndAMissingHeaderIsRefused(t *testing.T) {
	h := newHarness(t)
	ids := make([]string, 26)
	for i := range ids {
		ids[i] = uuid.NewString()
	}

	_, tooMany := h.client.ListSyslogRecords(context.Background(), deviceRequest(strings.Join(ids, ",")))
	_, none := h.client.ListSyslogRecords(context.Background(), deviceRequest())

	for name, err := range map[string]error{"26 tenants": tooMany, "no header": none} {
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: code = %v (err %v), want InvalidArgument", name, connect.CodeOf(err), err)
		}
	}
	if calls, _, _ := h.reader.snapshot(); calls != 0 {
		t.Errorf("the reader ran %d times for a refused header", calls)
	}
}

func TestRequestFieldsReachTheQuery(t *testing.T) {
	h := newHarness(t)
	req := connect.NewRequest(historyv1.ListSyslogRecordsRequest_builder{
		Location:    inventoryv1.LocationGlobalRef_builder{Location: inventoryv1.LocationLocalRef_builder{Id: proto.String(siteID)}.Build()}.Build(),
		From:        timestamppb.New(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)),
		To:          timestamppb.New(time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)),
		MinSeverity: logv1.SyslogSeverity_SYSLOG_SEVERITY_EMERGENCY.Enum(),
		PageSize:    proto.Int32(25),
		PageToken:   proto.String("token-from-before"),
	}.Build())
	req.Header().Set("X-FlowSeer-Tenant", tenantA)

	if _, err := h.client.ListSyslogRecords(context.Background(), req); err != nil {
		t.Fatalf("call: %v", err)
	}

	_, query, _ := h.reader.snapshot()
	if query.Scope != historyquery.LocationScope(uuid.MustParse(siteID)) {
		t.Errorf("scope = %+v, want the location", query.Scope)
	}
	if query.MinSeverity == nil || *query.MinSeverity != 0 {
		t.Errorf("min severity = %v, want a pointer to 0 (EMERGENCY is a real severity)", query.MinSeverity)
	}
	if query.PageSize != 25 || query.PageToken != "token-from-before" {
		t.Errorf("page = %d %q, want 25 and the token", query.PageSize, query.PageToken)
	}
}

func TestAMissingSeverityIsNoFilter(t *testing.T) {
	h := newHarness(t)

	if _, err := h.client.ListSyslogRecords(context.Background(), deviceRequest(tenantA)); err != nil {
		t.Fatalf("call: %v", err)
	}

	if _, query, _ := h.reader.snapshot(); query.MinSeverity != nil {
		t.Errorf("min severity = %d, want none when the request names none", *query.MinSeverity)
	}
}

func fullEntry() historyquery.Entry {
	return historyquery.Entry{
		TenantID:         tenantA,
		DeviceID:         uuid.MustParse(deviceID),
		ReceivedAt:       time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC),
		RecordID:         uuid.MustParse(recordID),
		SentAt:           time.Date(2026, 10, 2, 9, 59, 59, 0, time.UTC),
		Severity:         0,
		Facility:         23,
		Present:          historyquery.PresentSeverity | historyquery.PresentFacility | historyquery.PresentSentAt | historyquery.PresentSourceIP,
		Hostname:         "sw1",
		AppName:          "link",
		ProcID:           "42",
		MsgID:            "ID47",
		SDIDs:            []string{"origin", "empty", "meta"},
		SDParamSDID:      []string{"origin", "origin", "meta"},
		SDParamName:      []string{"ip", "ip", "seq"},
		SDParamValue:     []string{"10.0.0.1", "10.0.0.2", ""},
		Message:          "%LINK-3-UPDOWN: \xff down",
		MessageTruncated: true,
		SourceIP:         [16]byte{10: 0xff, 11: 0xff, 12: 10, 13: 1, 14: 2, 15: 3},
	}
}

func TestAStoredRowBecomesAValidRecordWithItsEntryFields(t *testing.T) {
	h := newHarness(t)
	h.reader.result = historyquery.Result{Entries: []historyquery.Entry{fullEntry()}, NextToken: "next"}

	resp, err := h.client.ListSyslogRecords(context.Background(), deviceRequest(tenantA))
	if err != nil {
		t.Fatalf("call: %v", err)
	}

	if err := protovalidate.Validate(resp.Msg); err != nil {
		t.Fatalf("the response fails its schema rules: %v", err)
	}
	if got := resp.Msg.GetNextPageToken(); got != "next" {
		t.Errorf("next page token = %q, want next", got)
	}
	entry := resp.Msg.GetEntries()[0]
	if entry.GetRecordId() != recordID || entry.GetTenant().GetId() != tenantA {
		t.Errorf("entry names record %q and tenant %q, want %s and %s", entry.GetRecordId(), entry.GetTenant().GetId(), recordID, tenantA)
	}
	r := entry.GetRecord()
	if !r.HasSeverity() || r.GetSeverity() != logv1.SyslogSeverity_SYSLOG_SEVERITY_EMERGENCY {
		t.Errorf("severity = %v present %v, want EMERGENCY present", r.GetSeverity(), r.HasSeverity())
	}
	if r.GetFacility() != logv1.SyslogFacility_SYSLOG_FACILITY_LOCAL7 {
		t.Errorf("facility = %v, want LOCAL7", r.GetFacility())
	}
	if string(r.GetMessage()) != "%LINK-3-UPDOWN: \xff down" {
		t.Errorf("message = %q, want the stored bytes", r.GetMessage())
	}
	if got := r.GetSourceAddress().GetV4().GetOctets(); len(got) != 4 || got[0] != 10 || got[3] != 3 {
		t.Errorf("source address v4 octets = %v, want 10.1.2.3", got)
	}
	if r.GetHostname() != "sw1" || r.GetAppName() != "link" || r.GetProcId() != "42" || r.GetMsgId() != "ID47" || !r.GetMessageTruncated() {
		t.Errorf("header fields = %v", r)
	}
	elements := r.GetStructuredData()
	if len(elements) != 3 || elements[0].GetId() != "origin" || len(elements[0].GetParams()) != 2 ||
		elements[1].GetId() != "empty" || len(elements[1].GetParams()) != 0 ||
		elements[2].GetParams()[0].GetName() != "seq" || elements[2].GetParams()[0].GetValue() != "" {
		t.Errorf("structured data = %v, want origin with 2 params, empty with none, meta with seq=", elements)
	}
}

func TestARowWithoutOptionalFieldsLeavesThemUnset(t *testing.T) {
	h := newHarness(t)
	row := historyquery.Entry{
		TenantID:   tenantA,
		DeviceID:   uuid.MustParse(deviceID),
		ReceivedAt: time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC),
		RecordID:   uuid.MustParse(recordID),
		Message:    "plain",
	}
	h.reader.result = historyquery.Result{Entries: []historyquery.Entry{row}}

	resp, err := h.client.ListSyslogRecords(context.Background(), deviceRequest(tenantA))
	if err != nil {
		t.Fatalf("call: %v", err)
	}

	if err := protovalidate.Validate(resp.Msg); err != nil {
		t.Fatalf("the response fails its schema rules: %v", err)
	}
	r := resp.Msg.GetEntries()[0].GetRecord()
	if r.HasSeverity() || r.HasFacility() || r.HasSentAt() || r.HasSourceAddress() || r.HasHostname() || r.HasAppName() || r.HasProcId() || r.HasMsgId() || r.HasMessageTruncated() {
		t.Errorf("record = %v, want every optional field unset", r)
	}
	if resp.Msg.HasNextPageToken() {
		t.Errorf("next page token = %q, want unset on the last page", resp.Msg.GetNextPageToken())
	}
}

func TestAnIPv6SourceAddressStaysIPv6(t *testing.T) {
	h := newHarness(t)
	row := fullEntry()
	row.SourceIP = [16]byte{0: 0x20, 1: 0x01, 2: 0x0d, 3: 0xb8, 15: 1}
	h.reader.result = historyquery.Result{Entries: []historyquery.Entry{row}}

	resp, err := h.client.ListSyslogRecords(context.Background(), deviceRequest(tenantA))
	if err != nil {
		t.Fatalf("call: %v", err)
	}

	v6 := resp.Msg.GetEntries()[0].GetRecord().GetSourceAddress().GetV6().GetOctets()
	if len(v6) != 16 || v6[0] != 0x20 || v6[15] != 1 {
		t.Errorf("source address v6 octets = %v, want 2001:db8::1", v6)
	}
}

func TestStoreFailuresMapToTheCodesACallerBranchesOn(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode connect.Code
	}{
		{"a reader limit", errs.New().Code(historyquery.ErrCodeLimit).Msg("too many rows"), connect.CodeResourceExhausted},
		{"a bad page token", errs.New().Code(historyquery.ErrCodeBadToken).Msg("token"), connect.CodeInvalidArgument},
		{"an unreachable store", errs.New().Code(historyquery.ErrCodeStore).Retryable().Msg("dial tcp: refused"), connect.CodeUnavailable},
		{"a read with no admitted tenant", errs.New().Code(historyquery.ErrCodeNoTenants).Msg("no tenant"), connect.CodeInternal},
		{"an unclassified failure", errors.New("boom"), connect.CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.reader.err = tt.err

			_, err := h.client.ListSyslogRecords(context.Background(), deviceRequest(tenantA))

			if connect.CodeOf(err) != tt.wantCode {
				t.Errorf("code = %v (err %v), want %v", connect.CodeOf(err), err, tt.wantCode)
			}
			if strings.Contains(err.Error(), "dial tcp") || strings.Contains(err.Error(), "boom") {
				t.Errorf("the internal cause reached the caller: %v", err)
			}
		})
	}
}

func TestAReaderLimitCarriesItsUserMessageAndCode(t *testing.T) {
	h := newHarness(t)
	h.reader.err = errs.New().Code(historyquery.ErrCodeLimit).UserMsg("narrow the window").Msg("Limit for rows exceeded")

	_, err := h.client.ListSyslogRecords(context.Background(), deviceRequest(tenantA))

	if got := errorCode(t, err); got != historyquery.ErrCodeLimit.String() {
		t.Errorf("error code = %q, want %q", got, historyquery.ErrCodeLimit)
	}
	if !strings.Contains(err.Error(), "narrow the window") {
		t.Errorf("error %q does not carry the user message", err)
	}
}

func TestACancelledCallReturnsTheContextErrorNotAnInternalOne(t *testing.T) {
	reader := &fakeReader{err: context.Canceled}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := historyapi.New(reader).ListSyslogRecords(ctx, deviceRequest(tenantA))

	if !errors.Is(err, context.Canceled) || connect.CodeOf(err) == connect.CodeInternal {
		t.Errorf("err = %v with code %v, want the cancellation unwrapped", err, connect.CodeOf(err))
	}
}

func TestARequestTheSchemaLetThroughButTheHandlerCannotReadIsInvalidArgument(t *testing.T) {
	req := deviceRequest(tenantA)
	req.Msg.GetDevice().GetDevice().SetId("not-a-uuid")

	_, err := historyapi.New(&fakeReader{}).ListSyslogRecords(context.Background(), req)

	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("code = %v (err %v), want InvalidArgument", connect.CodeOf(err), err)
	}
	if got := errorCode(t, err); got != historyapi.ErrCodeBadRequest.String() {
		t.Errorf("error code = %q, want %q", got, historyapi.ErrCodeBadRequest)
	}
}
