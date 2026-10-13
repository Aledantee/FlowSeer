package historysink

import (
	"net/netip"
	"time"

	"github.com/google/uuid"

	ingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/ingest/v1"
	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/tenant"
)

// Bits of [Row.Present]. A set bit says the message carried the field, since
// the table has no Nullable column and a severity of 0 is a real value.
const (
	PresentSeverity uint8 = 1 << iota
	PresentFacility
	PresentSentAt
	PresentSourceIP
)

// Row is one syslog record in the shape of the syslog table.
type Row struct {
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
	RetentionClass   uint8
}

// ErrCodeRecord is a record that cannot become a row. It is permanent: the
// same record fails the same way on every delivery.
var ErrCodeRecord = errs.NewCode("historysink/record")

// mapRecord converts one central ingest record into a row. tenantID is the
// tenant of the subject the record arrived on, location is the UUID of the
// device's location or empty, and class is the tenant's retention class
// number.
//
// The syslog message keeps its bytes: a Go string holds any byte sequence, and
// ClickHouse stores a String column without interpreting it.
func mapRecord(tenantID string, record *ingestv1.IngestRecord, location string, class uint8) (Row, error) {
	syslog := record.GetSyslog()
	if syslog == nil {
		return Row{}, errs.New().Code(ErrCodeRecord).Msg("record holds no syslog payload")
	}
	if err := tenant.Validate(tenantID); err != nil {
		return Row{}, errs.From(err).Code(ErrCodeRecord).Attr("tenant", tenantID).Msg("subject names an invalid tenant")
	}
	device, err := uuid.Parse(syslog.GetDevice().GetDevice().GetId())
	if err != nil {
		return Row{}, errs.From(err).Code(ErrCodeRecord).Msg("invalid device id")
	}
	recordID, err := uuid.Parse(record.GetRecordId())
	if err != nil {
		return Row{}, errs.From(err).Code(ErrCodeRecord).Msg("invalid record id")
	}
	var locationID uuid.UUID
	if location != "" {
		if locationID, err = uuid.Parse(location); err != nil {
			return Row{}, errs.From(err).Code(ErrCodeRecord).Msg("invalid location id")
		}
	}

	row := Row{
		TenantID:         tenantID,
		DeviceID:         device,
		ReceivedAt:       syslog.GetReceivedAt().AsTime(),
		RecordID:         recordID,
		ObservedAt:       record.GetProvenance().GetObservedAt().AsTime(),
		SentAt:           time.Unix(0, 0).UTC(),
		Hostname:         syslog.GetHostname(),
		AppName:          syslog.GetAppName(),
		ProcID:           syslog.GetProcId(),
		MsgID:            syslog.GetMsgId(),
		Message:          string(syslog.GetMessage()),
		MessageTruncated: syslog.GetMessageTruncated(),
		LocationID:       locationID,
		RetentionClass:   class,
	}
	if syslog.HasSeverity() {
		row.Severity = uint8(syslog.GetSeverity())
		row.Present |= PresentSeverity
	}
	if syslog.HasFacility() {
		row.Facility = uint8(syslog.GetFacility())
		row.Present |= PresentFacility
	}
	if syslog.HasSentAt() {
		row.SentAt = syslog.GetSentAt().AsTime()
		row.Present |= PresentSentAt
	}
	if ip, ok := sourceIP(syslog.GetSourceAddress()); ok {
		row.SourceIP = ip
		row.Present |= PresentSourceIP
	}
	for _, element := range syslog.GetStructuredData() {
		row.SDIDs = append(row.SDIDs, element.GetId())
		for _, param := range element.GetParams() {
			row.SDParamSDID = append(row.SDParamSDID, element.GetId())
			row.SDParamName = append(row.SDParamName, param.GetName())
			row.SDParamValue = append(row.SDParamValue, param.GetValue())
		}
	}
	return row, nil
}

// sourceIP returns the address as 16 octets, an IPv4 address in its
// IPv4-mapped form, which is how an IPv6 column holds one.
func sourceIP(address *addrv1.IpAddress) ([16]byte, bool) {
	if v4 := address.GetV4(); v4 != nil && len(v4.GetOctets()) == 4 {
		return netip.AddrFrom4([4]byte(v4.GetOctets())).As16(), true
	}
	if v6 := address.GetV6(); v6 != nil && len(v6.GetOctets()) == 16 {
		return [16]byte(v6.GetOctets()), true
	}
	return [16]byte{}, false
}
