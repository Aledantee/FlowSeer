package conformance

import (
	"testing"

	"google.golang.org/protobuf/proto"

	cellularv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/cellular/v1"
)

// TestCellularSignalRules holds each signal field to its report-mapping
// bound, one past each endpoint failing, and leaves RSSI unbounded.
func TestCellularSignalRules(t *testing.T) {
	signal := func(b *cellularv1.CellularSignal_builder) *cellularv1.CellularSignal {
		return b.Build()
	}

	runFieldCases(t, []fieldCase{
		{name: "classic LTE RSRQ floor", message: signal(&cellularv1.CellularSignal_builder{RsrqMillidb: proto.Int32(-19500)})},
		{name: "lowest RSRP", message: signal(&cellularv1.CellularSignal_builder{RsrpMillidbm: proto.Int32(-156000)})},
		{name: "highest RSRP", message: signal(&cellularv1.CellularSignal_builder{RsrpMillidbm: proto.Int32(-30000)})},
		{name: "RSRP below range", message: signal(&cellularv1.CellularSignal_builder{RsrpMillidbm: proto.Int32(-156001)}), wantField: "rsrp_millidbm", wantText: "greater than or equal to -156000"},
		{name: "RSRP above range", message: signal(&cellularv1.CellularSignal_builder{RsrpMillidbm: proto.Int32(-29000)}), wantField: "rsrp_millidbm", wantText: "less than or equal to -30000"},
		{name: "lowest RSRQ", message: signal(&cellularv1.CellularSignal_builder{RsrqMillidb: proto.Int32(-43000)})},
		{name: "highest RSRQ", message: signal(&cellularv1.CellularSignal_builder{RsrqMillidb: proto.Int32(20000)})},
		{name: "RSRQ below range", message: signal(&cellularv1.CellularSignal_builder{RsrqMillidb: proto.Int32(-43500)}), wantField: "rsrq_millidb", wantText: "greater than or equal to -43000"},
		{name: "RSRQ above range", message: signal(&cellularv1.CellularSignal_builder{RsrqMillidb: proto.Int32(20001)}), wantField: "rsrq_millidb", wantText: "less than or equal to 20000"},
		{name: "lowest SINR", message: signal(&cellularv1.CellularSignal_builder{SinrMillidb: proto.Int32(-23000)})},
		{name: "highest SINR", message: signal(&cellularv1.CellularSignal_builder{SinrMillidb: proto.Int32(40000)})},
		{name: "SINR below range", message: signal(&cellularv1.CellularSignal_builder{SinrMillidb: proto.Int32(-23001)}), wantField: "sinr_millidb", wantText: "greater than or equal to -23000"},
		{name: "SINR above range", message: signal(&cellularv1.CellularSignal_builder{SinrMillidb: proto.Int32(40500)}), wantField: "sinr_millidb", wantText: "less than or equal to 40000"},
		{name: "unbounded RSSI", message: signal(&cellularv1.CellularSignal_builder{RssiMillidbm: proto.Int32(-120000)})},
	})
}

// TestCellularInterfaceRules holds the row to an interface name and its
// identifiers to their TS 23.003 digit counts, keeping MNC leading zeros.
func TestCellularInterfaceRules(t *testing.T) {
	row := func() *cellularv1.CellularInterface_builder {
		return &cellularv1.CellularInterface_builder{
			InterfaceName: proto.String("Cellular0/1/0"),
			Technology:    cellularv1.RadioAccessTechnology_RADIO_ACCESS_TECHNOLOGY_LTE.Enum(),
			Band:          proto.Uint32(20),
		}
	}
	with := func(set func(*cellularv1.CellularInterface_builder)) *cellularv1.CellularInterface {
		b := row()
		set(b)
		return b.Build()
	}

	runFieldCases(t, []fieldCase{
		{name: "row", message: row().Build()},
		{name: "interface name absent", message: with(func(b *cellularv1.CellularInterface_builder) { b.InterfaceName = nil }), wantField: "interface_name", wantText: "value is required"},
		{name: "15-digit IMEI", message: with(func(b *cellularv1.CellularInterface_builder) { b.Imei = proto.String("352099001761481") })},
		{name: "14-digit IMEI", message: with(func(b *cellularv1.CellularInterface_builder) { b.Imei = proto.String("35209900176148") }), wantField: "imei", wantText: "match regex"},
		{name: "two-digit MNC with leading zero", message: with(func(b *cellularv1.CellularInterface_builder) { b.Mnc = proto.String("01") })},
		{name: "one-digit MNC", message: with(func(b *cellularv1.CellularInterface_builder) { b.Mnc = proto.String("1") }), wantField: "mnc", wantText: "match regex"},
		{name: "two-digit MCC", message: with(func(b *cellularv1.CellularInterface_builder) { b.Mcc = proto.String("26") }), wantField: "mcc", wantText: "match regex"},
		{name: "IMSI", message: with(func(b *cellularv1.CellularInterface_builder) { b.Imsi = proto.String("262011234567890") })},
		{name: "ICCID", message: with(func(b *cellularv1.CellularInterface_builder) { b.Iccid = proto.String("8949020000012345678") })},
		{name: "signal out of range", message: with(func(b *cellularv1.CellularInterface_builder) {
			b.Signal = cellularv1.CellularSignal_builder{SinrMillidb: proto.Int32(40500)}.Build()
		}), wantField: "signal.sinr_millidb", wantText: "less than or equal to 40000"},
	})
}
