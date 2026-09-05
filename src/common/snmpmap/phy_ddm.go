package snmpmap

import (
	"context"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"

	"go.aledante.io/FlowSeer/generated/go/mib/dlinkswddmmib"
	"go.aledante.io/FlowSeer/generated/go/mib/dlinkswsfpinfomib"
	"go.aledante.io/FlowSeer/generated/go/mib/hh3ctransceiverinfomib"
	"go.aledante.io/FlowSeer/generated/go/mib/hpicftransceivermib"
	phyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/phy/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// The vendor transceiver tables are keyed by ifIndex and each vendor
// reports units of its own. Every conversion below lands in the schema's
// linear units: nanowatts, microamperes, microvolts, and thousandths of a
// degree Celsius. A device implements at most one vendor's tables, so the
// mappers do not reconcile a device that answers more than one: each
// overwrites what the previous one set.

// dlinkSfpColumns are the D-Link SFP identity columns [Physical] requests.
var dlinkSfpColumns = []snmp.AnyColumn{
	dlinkswsfpinfomib.DPortSfpInfoLaserIdentifier,
	dlinkswsfpinfomib.DPortSfpInfoConnectType,
	dlinkswsfpinfomib.DPortSfpInfoVendorName,
	dlinkswsfpinfomib.DPortSfpInfoVendorPN,
	dlinkswsfpinfomib.DPortSfpInfoVendorRev,
	dlinkswsfpinfomib.DPortSfpInfoVendorSN,
	dlinkswsfpinfomib.DPortSfpInfoDateCode,
	dlinkswsfpinfomib.DPortSfpInfoTransmissionMedia,
	dlinkswsfpinfomib.DPortSfpInfoBitRate,
	dlinkswsfpinfomib.DPortSfpInfoWavelength,
}

// dlinkDdmColumns are the D-Link DDM measurement columns [Physical]
// requests: the linear power columns, never the dBm duplicates.
var dlinkDdmColumns = []snmp.AnyColumn{
	dlinkswddmmib.DDdmIfInfoCurrentTemperature,
	dlinkswddmmib.DDdmIfInfoHighAlarmTemperature,
	dlinkswddmmib.DDdmIfInfoHighWarnTemperature,
	dlinkswddmmib.DDdmIfInfoLowWarnTemperature,
	dlinkswddmmib.DDdmIfInfoLowAlarmTemperature,
	dlinkswddmmib.DDdmIfInfoCurrentVoltage,
	dlinkswddmmib.DDdmIfInfoHighAlarmVoltage,
	dlinkswddmmib.DDdmIfInfoHighWarnVoltage,
	dlinkswddmmib.DDdmIfInfoLowWarnVoltage,
	dlinkswddmmib.DDdmIfInfoLowAlarmVoltage,
	dlinkswddmmib.DDdmIfInfoCurrentBiasCurrent,
	dlinkswddmmib.DDdmIfInfoHighAlarmBiasCurrent,
	dlinkswddmmib.DDdmIfInfoHighWarnBiasCurrent,
	dlinkswddmmib.DDdmIfInfoLowWarnBiasCurrent,
	dlinkswddmmib.DDdmIfInfoLowAlarmBiasCurrent,
	dlinkswddmmib.DDdmIfInfoCurrentTxPower,
	dlinkswddmmib.DDdmIfInfoHighAlarmTxPower,
	dlinkswddmmib.DDdmIfInfoHighWarnTxPower,
	dlinkswddmmib.DDdmIfInfoLowWarnTxPower,
	dlinkswddmmib.DDdmIfInfoLowAlarmTxPower,
	dlinkswddmmib.DDdmIfInfoCurrentRxPower,
	dlinkswddmmib.DDdmIfInfoHighAlarmRxPower,
	dlinkswddmmib.DDdmIfInfoHighWarnRxPower,
	dlinkswddmmib.DDdmIfInfoLowWarnRxPower,
	dlinkswddmmib.DDdmIfInfoLowAlarmRxPower,
}

// hpXcvrColumns are the HP ProCurve transceiver columns [Physical] requests.
var hpXcvrColumns = []snmp.AnyColumn{
	hpicftransceivermib.HpicfXcvrModel,
	hpicftransceivermib.HpicfXcvrSerial,
	hpicftransceivermib.HpicfXcvrType,
	hpicftransceivermib.HpicfXcvrConnectorType,
	hpicftransceivermib.HpicfXcvrWavelength,
	hpicftransceivermib.HpicfXcvrDiagnostics,
	hpicftransceivermib.HpicfXcvrTemp,
	hpicftransceivermib.HpicfXcvrVoltage,
	hpicftransceivermib.HpicfXcvrBias,
	hpicftransceivermib.HpicfXcvrTxPower,
	hpicftransceivermib.HpicfXcvrRxPower,
	hpicftransceivermib.HpicfXcvrTempHiAlarm,
	hpicftransceivermib.HpicfXcvrTempLoAlarm,
	hpicftransceivermib.HpicfXcvrTempHiWarn,
	hpicftransceivermib.HpicfXcvrTempLoWarn,
	hpicftransceivermib.HpicfXcvrVccHiAlarm,
	hpicftransceivermib.HpicfXcvrVccLoAlarm,
	hpicftransceivermib.HpicfXcvrVccHiWarn,
	hpicftransceivermib.HpicfXcvrVccLoWarn,
	hpicftransceivermib.HpicfXcvrBiasHiAlarm,
	hpicftransceivermib.HpicfXcvrBiasLoAlarm,
	hpicftransceivermib.HpicfXcvrBiasHiWarn,
	hpicftransceivermib.HpicfXcvrBiasLoWarn,
	hpicftransceivermib.HpicfXcvrPwrOutHiAlarm,
	hpicftransceivermib.HpicfXcvrPwrOutLoAlarm,
	hpicftransceivermib.HpicfXcvrPwrOutHiWarn,
	hpicftransceivermib.HpicfXcvrPwrOutLoWarn,
	hpicftransceivermib.HpicfXcvrRcvPwrHiAlarm,
	hpicftransceivermib.HpicfXcvrRcvPwrLoAlarm,
	hpicftransceivermib.HpicfXcvrRcvPwrHiWarn,
	hpicftransceivermib.HpicfXcvrRcvPwrLoWarn,
	hpicftransceivermib.HpicfXcvrManufacDate,
}

// hh3cXcvrColumns are the H3C transceiver-info columns [Physical] requests.
var hh3cXcvrColumns = []snmp.AnyColumn{
	hh3ctransceiverinfomib.Hh3cTransceiverHardwareType,
	hh3ctransceiverinfomib.Hh3cTransceiverType,
	hh3ctransceiverinfomib.Hh3cTransceiverWaveLength,
	hh3ctransceiverinfomib.Hh3cTransceiverVendorName,
	hh3ctransceiverinfomib.Hh3cTransceiverSerialNumber,
	hh3ctransceiverinfomib.Hh3cTransceiverDiagnostic,
	hh3ctransceiverinfomib.Hh3cTransceiverCurTXPower,
	hh3ctransceiverinfomib.Hh3cTransceiverCurRXPower,
	hh3ctransceiverinfomib.Hh3cTransceiverTemperature,
	hh3ctransceiverinfomib.Hh3cTransceiverVoltage,
	hh3ctransceiverinfomib.Hh3cTransceiverBiasCurrent,
	hh3ctransceiverinfomib.Hh3cTransceiverTempHiAlarm,
	hh3ctransceiverinfomib.Hh3cTransceiverTempLoAlarm,
	hh3ctransceiverinfomib.Hh3cTransceiverTempHiWarn,
	hh3ctransceiverinfomib.Hh3cTransceiverTempLoWarn,
	hh3ctransceiverinfomib.Hh3cTransceiverVccHiAlarm,
	hh3ctransceiverinfomib.Hh3cTransceiverVccLoAlarm,
	hh3ctransceiverinfomib.Hh3cTransceiverVccHiWarn,
	hh3ctransceiverinfomib.Hh3cTransceiverVccLoWarn,
	hh3ctransceiverinfomib.Hh3cTransceiverBiasHiAlarm,
	hh3ctransceiverinfomib.Hh3cTransceiverBiasLoAlarm,
	hh3ctransceiverinfomib.Hh3cTransceiverBiasHiWarn,
	hh3ctransceiverinfomib.Hh3cTransceiverBiasLoWarn,
	hh3ctransceiverinfomib.Hh3cTransceiverPwrOutHiAlarm,
	hh3ctransceiverinfomib.Hh3cTransceiverPwrOutLoAlarm,
	hh3ctransceiverinfomib.Hh3cTransceiverPwrOutHiWarn,
	hh3ctransceiverinfomib.Hh3cTransceiverPwrOutLoWarn,
	hh3ctransceiverinfomib.Hh3cTransceiverRcvPwrHiAlarm,
	hh3ctransceiverinfomib.Hh3cTransceiverRcvPwrLoAlarm,
	hh3ctransceiverinfomib.Hh3cTransceiverRcvPwrHiWarn,
	hh3ctransceiverinfomib.Hh3cTransceiverRcvPwrLoWarn,
	hh3ctransceiverinfomib.Hh3cTransceiverRevisionNumber,
	hh3ctransceiverinfomib.Hh3cTransceiverPartNumber,
}

// hh3cChannelColumns are the H3C per-channel columns [Physical] requests.
var hh3cChannelColumns = []snmp.AnyColumn{
	hh3ctransceiverinfomib.Hh3cTransceiverChannelCurTXPower,
	hh3ctransceiverinfomib.Hh3cTransceiverChannelCurRXPower,
	hh3ctransceiverinfomib.Hh3cTransceiverChannelBiasCurrent,
	hh3ctransceiverinfomib.Hh3cTransceiverChannelBiasHiAm,
	hh3ctransceiverinfomib.Hh3cTransceiverChannelBiasLoAm,
	hh3ctransceiverinfomib.Hh3cTransceiverChannelTXPwrHiAm,
	hh3ctransceiverinfomib.Hh3cTransceiverChannelTXPwrLoAm,
}

// hpNoLightDbm is the value HP reports for zero optical power, where the
// logarithm has no answer.
const hpNoLightDbm = -99999999

// walkModules maps the vendor transceiver tables onto each facet's
// pluggable module. Each vendor's tables are optional; a device answers
// at most one vendor, and a subtree the device does not implement is a
// decline, not an error.
func walkModules(ctx context.Context, sess snmp.Session, facets map[uint32]*phyv1.EthernetFacet) error {
	var walkErrs []error

	walkErrs = append(walkErrs, walkDlinkModules(ctx, sess, facets))
	walkErrs = append(walkErrs, walkHpModules(ctx, sess, facets))
	walkErrs = append(walkErrs, walkHh3cModules(ctx, sess, facets))

	for _, facet := range facets {
		module := facet.GetModule()
		if module == nil {
			continue
		}

		// A lane is created before its row's columns are known to hold
		// anything, and the schema reads an empty list as "no lane
		// diagnostics", so a lane that gathered nothing is not a fact.
		lanes := slices.DeleteFunc(module.GetLanes(), func(lane *phyv1.ModuleLane) bool {
			return !lane.HasWavelengthNanometers() && !lane.HasBias() && !lane.HasTxPower() && !lane.HasRxPower()
		})

		slices.SortFunc(lanes, func(a, b *phyv1.ModuleLane) int {
			return int(a.GetIndex()) - int(b.GetIndex())
		})

		module.SetLanes(lanes)
	}

	return errors.Join(walkErrs...)
}

// moduleAt returns the facet's module, creating a present one on first
// use. An explicitly empty cage is set by the identity table and yields
// nil: a measurement row for that cage is stale agent state, and the
// schema forbids attributes on an empty cage.
func moduleAt(facets map[uint32]*phyv1.EthernetFacet, ifIndex uint32) *phyv1.PluggableModule {
	facet := facetAt(facets, ifIndex)

	module := facet.GetModule()
	if module == nil {
		module = &phyv1.PluggableModule{}
		module.SetPresent(true)
		facet.SetModule(module)
	}

	if !module.GetPresent() {
		return nil
	}

	return module
}

// populated reports whether any field of a measurement message was set,
// which is the one rule for attaching it: a threshold without a reading
// and a reading without thresholds are both facts.
func populated(m proto.Message) bool {
	return proto.Size(m) > 0
}

// laneAt returns the module's lane with the given index, creating it on
// first use.
func laneAt(module *phyv1.PluggableModule, index uint32) *phyv1.ModuleLane {
	for _, lane := range module.GetLanes() {
		if lane.GetIndex() == index {
			return lane
		}
	}

	lane := &phyv1.ModuleLane{}
	lane.SetIndex(index)
	module.SetLanes(append(module.GetLanes(), lane))

	return lane
}

// diagnosticsAt returns the module's module-level diagnostics, creating
// them on first use.
func diagnosticsAt(module *phyv1.PluggableModule) *phyv1.ModuleDiagnostics {
	d := module.GetDiagnostics()
	if d == nil {
		d = &phyv1.ModuleDiagnostics{}
		module.SetDiagnostics(d)
	}

	return d
}

// setString sets a string field from a vendor string, trimming the
// padding modules carry and leaving an empty value absent.
func setString(set func(string), value string) {
	if v := strings.TrimSpace(value); v != "" {
		set(v)
	}
}

// walkDlinkModules maps the D-Link SFP identity and DDM tables.
func walkDlinkModules(ctx context.Context, sess snmp.Session, facets map[uint32]*phyv1.EthernetFacet) error {
	var walkErrs []error

	sfpWalk := dlinkswsfpinfomib.DPortSfpInfoTable.Walk(ctx, sess, dlinkSfpColumns...)
	for idx, row := range sfpWalk.Iter() {
		ifIndex, ok := singleIndex(idx)
		if !ok {
			continue
		}

		mapDlinkSfp(facets, ifIndex, row)
	}

	if err := sfpWalk.Err(); err != nil {
		walkErrs = append(walkErrs, errs.From(err).Code(ErrCodePhysicalWalk).Msg("walk dPortSfpInfoTable"))
	}

	ddmWalk := dlinkswddmmib.DDdmIfInfoTable.Walk(ctx, sess, dlinkDdmColumns...)
	for idx, row := range ddmWalk.Iter() {
		ifIndex, ok := singleIndex(idx)
		if !ok {
			continue
		}

		mapDlinkDdm(facets, ifIndex, row)
	}

	if err := ddmWalk.Err(); err != nil {
		walkErrs = append(walkErrs, errs.From(err).Code(ErrCodePhysicalWalk).Msg("walk dDdmIfInfoTable"))
	}

	return errors.Join(walkErrs...)
}

// mapDlinkSfp maps one D-Link SFP identity row. The MIB reports an empty
// laser identifier for an empty cage, which is the one explicit
// empty-cage signal any vendor table gives.
func mapDlinkSfp(facets map[uint32]*phyv1.EthernetFacet, ifIndex uint32, r dlinkswsfpinfomib.DPortSfpInfoTableRow) {
	if r.Observed(dlinkswsfpinfomib.DPortSfpInfoLaserIdentifier) && strings.TrimSpace(r.DPortSfpInfoLaserIdentifier) == "" {
		empty := &phyv1.PluggableModule{}
		empty.SetPresent(false)
		facetAt(facets, ifIndex).SetModule(empty)

		return
	}

	module := moduleAt(facets, ifIndex)
	if module == nil {
		return
	}

	if r.Observed(dlinkswsfpinfomib.DPortSfpInfoLaserIdentifier) {
		if ff, ok := formFactorFromText(r.DPortSfpInfoLaserIdentifier); ok {
			module.SetFormFactor(ff)
		}
	}

	if r.Observed(dlinkswsfpinfomib.DPortSfpInfoConnectType) {
		if c, ok := connectorFromText(r.DPortSfpInfoConnectType); ok {
			module.SetConnector(c)
		}
	}

	if r.Observed(dlinkswsfpinfomib.DPortSfpInfoVendorName) {
		setString(module.SetVendor, r.DPortSfpInfoVendorName)
	}

	if r.Observed(dlinkswsfpinfomib.DPortSfpInfoVendorPN) {
		setString(module.SetPartNumber, r.DPortSfpInfoVendorPN)
	}

	if r.Observed(dlinkswsfpinfomib.DPortSfpInfoVendorRev) {
		setString(module.SetRevision, r.DPortSfpInfoVendorRev)
	}

	if r.Observed(dlinkswsfpinfomib.DPortSfpInfoVendorSN) {
		setString(module.SetSerialNumber, r.DPortSfpInfoVendorSN)
	}

	if r.Observed(dlinkswsfpinfomib.DPortSfpInfoDateCode) {
		setString(module.SetDateCode, r.DPortSfpInfoDateCode)
	}

	// The MIB reports the rate in megabaud, which is megabits per second
	// for the NRZ signaling every module in its range uses.
	if r.Observed(dlinkswsfpinfomib.DPortSfpInfoBitRate) && r.DPortSfpInfoBitRate > 0 {
		module.SetNominalBitRateMbps(uint32(r.DPortSfpInfoBitRate))
	}

	if r.Observed(dlinkswsfpinfomib.DPortSfpInfoWavelength) && r.DPortSfpInfoWavelength > 0 {
		laneAt(module, 1).SetWavelengthNanometers(uint32(r.DPortSfpInfoWavelength))
	}

	if r.Observed(dlinkswsfpinfomib.DPortSfpInfoTransmissionMedia) {
		mediumFromText(facetAt(facets, ifIndex), r.DPortSfpInfoTransmissionMedia)
	}
}

// mapDlinkDdm maps one D-Link DDM row: millidegrees, centivolts,
// milliamperes, and tenths of a microwatt into the schema's units.
func mapDlinkDdm(facets map[uint32]*phyv1.EthernetFacet, ifIndex uint32, r dlinkswddmmib.DDdmIfInfoTableRow) {
	module := moduleAt(facets, ifIndex)
	if module == nil {
		return
	}

	temp := &phyv1.ModuleTemperature{}
	setSigned(temp.SetValueMillidegrees, r.Observed(dlinkswddmmib.DDdmIfInfoCurrentTemperature), r.DDdmIfInfoCurrentTemperature, 1)
	setSigned(temp.SetHighAlarmMillidegrees, r.Observed(dlinkswddmmib.DDdmIfInfoHighAlarmTemperature), r.DDdmIfInfoHighAlarmTemperature, 1)
	setSigned(temp.SetHighWarningMillidegrees, r.Observed(dlinkswddmmib.DDdmIfInfoHighWarnTemperature), r.DDdmIfInfoHighWarnTemperature, 1)
	setSigned(temp.SetLowWarningMillidegrees, r.Observed(dlinkswddmmib.DDdmIfInfoLowWarnTemperature), r.DDdmIfInfoLowWarnTemperature, 1)
	setSigned(temp.SetLowAlarmMillidegrees, r.Observed(dlinkswddmmib.DDdmIfInfoLowAlarmTemperature), r.DDdmIfInfoLowAlarmTemperature, 1)

	if populated(temp) {
		diagnosticsAt(module).SetTemperature(temp)
	}

	volt := &phyv1.SupplyVoltage{}
	setUnsigned(volt.SetValueMicrovolts, r.Observed(dlinkswddmmib.DDdmIfInfoCurrentVoltage), int64(r.DDdmIfInfoCurrentVoltage), 10_000)
	setUnsigned(volt.SetHighAlarmMicrovolts, r.Observed(dlinkswddmmib.DDdmIfInfoHighAlarmVoltage), int64(r.DDdmIfInfoHighAlarmVoltage), 10_000)
	setUnsigned(volt.SetHighWarningMicrovolts, r.Observed(dlinkswddmmib.DDdmIfInfoHighWarnVoltage), int64(r.DDdmIfInfoHighWarnVoltage), 10_000)
	setUnsigned(volt.SetLowWarningMicrovolts, r.Observed(dlinkswddmmib.DDdmIfInfoLowWarnVoltage), int64(r.DDdmIfInfoLowWarnVoltage), 10_000)
	setUnsigned(volt.SetLowAlarmMicrovolts, r.Observed(dlinkswddmmib.DDdmIfInfoLowAlarmVoltage), int64(r.DDdmIfInfoLowAlarmVoltage), 10_000)

	if populated(volt) {
		diagnosticsAt(module).SetVoltage(volt)
	}

	lane := laneAt(module, 1)

	bias := &phyv1.BiasCurrent{}
	setUnsigned(bias.SetValueMicroamperes, r.Observed(dlinkswddmmib.DDdmIfInfoCurrentBiasCurrent), int64(r.DDdmIfInfoCurrentBiasCurrent), 1_000)
	setUnsigned(bias.SetHighAlarmMicroamperes, r.Observed(dlinkswddmmib.DDdmIfInfoHighAlarmBiasCurrent), int64(r.DDdmIfInfoHighAlarmBiasCurrent), 1_000)
	setUnsigned(bias.SetHighWarningMicroamperes, r.Observed(dlinkswddmmib.DDdmIfInfoHighWarnBiasCurrent), int64(r.DDdmIfInfoHighWarnBiasCurrent), 1_000)
	setUnsigned(bias.SetLowWarningMicroamperes, r.Observed(dlinkswddmmib.DDdmIfInfoLowWarnBiasCurrent), int64(r.DDdmIfInfoLowWarnBiasCurrent), 1_000)
	setUnsigned(bias.SetLowAlarmMicroamperes, r.Observed(dlinkswddmmib.DDdmIfInfoLowAlarmBiasCurrent), int64(r.DDdmIfInfoLowAlarmBiasCurrent), 1_000)

	if populated(bias) {
		lane.SetBias(bias)
	}

	tx := &phyv1.OpticalPower{}
	setUnsigned(tx.SetValueNanowatts, r.Observed(dlinkswddmmib.DDdmIfInfoCurrentTxPower), int64(r.DDdmIfInfoCurrentTxPower), 100)
	setUnsigned(tx.SetHighAlarmNanowatts, r.Observed(dlinkswddmmib.DDdmIfInfoHighAlarmTxPower), int64(r.DDdmIfInfoHighAlarmTxPower), 100)
	setUnsigned(tx.SetHighWarningNanowatts, r.Observed(dlinkswddmmib.DDdmIfInfoHighWarnTxPower), int64(r.DDdmIfInfoHighWarnTxPower), 100)
	setUnsigned(tx.SetLowWarningNanowatts, r.Observed(dlinkswddmmib.DDdmIfInfoLowWarnTxPower), int64(r.DDdmIfInfoLowWarnTxPower), 100)
	setUnsigned(tx.SetLowAlarmNanowatts, r.Observed(dlinkswddmmib.DDdmIfInfoLowAlarmTxPower), int64(r.DDdmIfInfoLowAlarmTxPower), 100)

	if populated(tx) {
		lane.SetTxPower(tx)
	}

	rx := &phyv1.OpticalPower{}
	setUnsigned(rx.SetValueNanowatts, r.Observed(dlinkswddmmib.DDdmIfInfoCurrentRxPower), int64(r.DDdmIfInfoCurrentRxPower), 100)
	setUnsigned(rx.SetHighAlarmNanowatts, r.Observed(dlinkswddmmib.DDdmIfInfoHighAlarmRxPower), int64(r.DDdmIfInfoHighAlarmRxPower), 100)
	setUnsigned(rx.SetHighWarningNanowatts, r.Observed(dlinkswddmmib.DDdmIfInfoHighWarnRxPower), int64(r.DDdmIfInfoHighWarnRxPower), 100)
	setUnsigned(rx.SetLowWarningNanowatts, r.Observed(dlinkswddmmib.DDdmIfInfoLowWarnRxPower), int64(r.DDdmIfInfoLowWarnRxPower), 100)
	setUnsigned(rx.SetLowAlarmNanowatts, r.Observed(dlinkswddmmib.DDdmIfInfoLowAlarmRxPower), int64(r.DDdmIfInfoLowAlarmRxPower), 100)

	if populated(rx) {
		lane.SetRxPower(rx)
	}
}

// walkHpModules maps the HP ProCurve transceiver table.
func walkHpModules(ctx context.Context, sess snmp.Session, facets map[uint32]*phyv1.EthernetFacet) error {
	walk := hpicftransceivermib.HpicfXcvrInfoTable.Walk(ctx, sess, hpXcvrColumns...)
	for idx, row := range walk.Iter() {
		ifIndex, ok := singleIndex(idx)
		if !ok {
			continue
		}

		mapHpXcvr(facets, ifIndex, row)
	}

	if err := walk.Err(); err != nil {
		return errs.From(err).Code(ErrCodePhysicalWalk).Msg("walk hpicfXcvrInfoTable")
	}

	return nil
}

// mapHpXcvr maps one HP transceiver row. HP reports current power in
// thousandths of dBm with a sentinel for no light, and thresholds in
// tenths of a microwatt; a zero threshold means the module has none.
func mapHpXcvr(facets map[uint32]*phyv1.EthernetFacet, ifIndex uint32, r hpicftransceivermib.HpicfXcvrInfoTableRow) {
	model := strings.TrimSpace(string(r.HpicfXcvrModel))
	serial := strings.TrimSpace(string(r.HpicfXcvrSerial))

	if r.Observed(hpicftransceivermib.HpicfXcvrModel) && r.Observed(hpicftransceivermib.HpicfXcvrSerial) && model == "" && serial == "" {
		empty := &phyv1.PluggableModule{}
		empty.SetPresent(false)
		facetAt(facets, ifIndex).SetModule(empty)

		return
	}

	module := moduleAt(facets, ifIndex)
	if module == nil {
		return
	}

	if r.Observed(hpicftransceivermib.HpicfXcvrModel) {
		setString(module.SetPartNumber, model)
	}

	if r.Observed(hpicftransceivermib.HpicfXcvrSerial) {
		setString(module.SetSerialNumber, serial)
	}

	if r.Observed(hpicftransceivermib.HpicfXcvrType) {
		if ff, ok := formFactorFromText(string(r.HpicfXcvrType)); ok {
			module.SetFormFactor(ff)
		}
	}

	if r.Observed(hpicftransceivermib.HpicfXcvrConnectorType) {
		if c, ok := connectorFromText(string(r.HpicfXcvrConnectorType)); ok {
			module.SetConnector(c)
		}
	}

	if r.Observed(hpicftransceivermib.HpicfXcvrManufacDate) {
		setString(module.SetDateCode, string(r.HpicfXcvrManufacDate))
	}

	if r.Observed(hpicftransceivermib.HpicfXcvrWavelength) {
		text := strings.TrimSpace(string(r.HpicfXcvrWavelength))

		if nm, ok := leadingNumber(text); ok && nm > 0 {
			laneAt(module, 1).SetWavelengthNanometers(nm)
			fiberIfUnset(facetAt(facets, ifIndex))
		} else if strings.EqualFold(text, "n/a") {
			copperIfUnset(facetAt(facets, ifIndex))
		}
	}

	if !r.Observed(hpicftransceivermib.HpicfXcvrDiagnostics) || r.HpicfXcvrDiagnostics != hpicftransceivermib.HpicfXcvrDiagnosticsValueDom {
		return
	}

	temp := &phyv1.ModuleTemperature{}
	setSigned(temp.SetValueMillidegrees, r.Observed(hpicftransceivermib.HpicfXcvrTemp), r.HpicfXcvrTemp, 1)
	setSigned(temp.SetHighAlarmMillidegrees, r.Observed(hpicftransceivermib.HpicfXcvrTempHiAlarm), r.HpicfXcvrTempHiAlarm, 1)
	setSigned(temp.SetHighWarningMillidegrees, r.Observed(hpicftransceivermib.HpicfXcvrTempHiWarn), r.HpicfXcvrTempHiWarn, 1)
	setSigned(temp.SetLowWarningMillidegrees, r.Observed(hpicftransceivermib.HpicfXcvrTempLoWarn), r.HpicfXcvrTempLoWarn, 1)
	setSigned(temp.SetLowAlarmMillidegrees, r.Observed(hpicftransceivermib.HpicfXcvrTempLoAlarm), r.HpicfXcvrTempLoAlarm, 1)

	if populated(temp) {
		diagnosticsAt(module).SetTemperature(temp)
	}

	// HP reports a zero supply voltage for a module that has none to
	// report, the same way its thresholds do.
	volt := &phyv1.SupplyVoltage{}
	setNonzero(volt.SetValueMicrovolts, r.Observed(hpicftransceivermib.HpicfXcvrVoltage), int64(r.HpicfXcvrVoltage), 100)
	setNonzero(volt.SetHighAlarmMicrovolts, r.Observed(hpicftransceivermib.HpicfXcvrVccHiAlarm), int64(r.HpicfXcvrVccHiAlarm), 100)
	setNonzero(volt.SetHighWarningMicrovolts, r.Observed(hpicftransceivermib.HpicfXcvrVccHiWarn), int64(r.HpicfXcvrVccHiWarn), 100)
	setNonzero(volt.SetLowWarningMicrovolts, r.Observed(hpicftransceivermib.HpicfXcvrVccLoWarn), int64(r.HpicfXcvrVccLoWarn), 100)
	setNonzero(volt.SetLowAlarmMicrovolts, r.Observed(hpicftransceivermib.HpicfXcvrVccLoAlarm), int64(r.HpicfXcvrVccLoAlarm), 100)

	if populated(volt) {
		diagnosticsAt(module).SetVoltage(volt)
	}

	lane := laneAt(module, 1)

	bias := &phyv1.BiasCurrent{}
	setUnsigned(bias.SetValueMicroamperes, r.Observed(hpicftransceivermib.HpicfXcvrBias), int64(r.HpicfXcvrBias), 1)
	setNonzero(bias.SetHighAlarmMicroamperes, r.Observed(hpicftransceivermib.HpicfXcvrBiasHiAlarm), int64(r.HpicfXcvrBiasHiAlarm), 1)
	setNonzero(bias.SetHighWarningMicroamperes, r.Observed(hpicftransceivermib.HpicfXcvrBiasHiWarn), int64(r.HpicfXcvrBiasHiWarn), 1)
	setNonzero(bias.SetLowWarningMicroamperes, r.Observed(hpicftransceivermib.HpicfXcvrBiasLoWarn), int64(r.HpicfXcvrBiasLoWarn), 1)
	setNonzero(bias.SetLowAlarmMicroamperes, r.Observed(hpicftransceivermib.HpicfXcvrBiasLoAlarm), int64(r.HpicfXcvrBiasLoAlarm), 1)

	if populated(bias) {
		lane.SetBias(bias)
	}

	tx := &phyv1.OpticalPower{}

	setHpDbm(tx.SetValueNanowatts, r.Observed(hpicftransceivermib.HpicfXcvrTxPower), r.HpicfXcvrTxPower)

	setNonzero(tx.SetHighAlarmNanowatts, r.Observed(hpicftransceivermib.HpicfXcvrPwrOutHiAlarm), int64(r.HpicfXcvrPwrOutHiAlarm), 100)
	setNonzero(tx.SetHighWarningNanowatts, r.Observed(hpicftransceivermib.HpicfXcvrPwrOutHiWarn), int64(r.HpicfXcvrPwrOutHiWarn), 100)
	setNonzero(tx.SetLowWarningNanowatts, r.Observed(hpicftransceivermib.HpicfXcvrPwrOutLoWarn), int64(r.HpicfXcvrPwrOutLoWarn), 100)
	setNonzero(tx.SetLowAlarmNanowatts, r.Observed(hpicftransceivermib.HpicfXcvrPwrOutLoAlarm), int64(r.HpicfXcvrPwrOutLoAlarm), 100)

	if populated(tx) {
		lane.SetTxPower(tx)
	}

	rx := &phyv1.OpticalPower{}

	setHpDbm(rx.SetValueNanowatts, r.Observed(hpicftransceivermib.HpicfXcvrRxPower), r.HpicfXcvrRxPower)

	setNonzero(rx.SetHighAlarmNanowatts, r.Observed(hpicftransceivermib.HpicfXcvrRcvPwrHiAlarm), int64(r.HpicfXcvrRcvPwrHiAlarm), 100)
	setNonzero(rx.SetHighWarningNanowatts, r.Observed(hpicftransceivermib.HpicfXcvrRcvPwrHiWarn), int64(r.HpicfXcvrRcvPwrHiWarn), 100)
	setNonzero(rx.SetLowWarningNanowatts, r.Observed(hpicftransceivermib.HpicfXcvrRcvPwrLoWarn), int64(r.HpicfXcvrRcvPwrLoWarn), 100)
	setNonzero(rx.SetLowAlarmNanowatts, r.Observed(hpicftransceivermib.HpicfXcvrRcvPwrLoAlarm), int64(r.HpicfXcvrRcvPwrLoAlarm), 100)

	if populated(rx) {
		lane.SetRxPower(rx)
	}
}

// walkHh3cModules maps the H3C transceiver-info and per-channel tables.
func walkHh3cModules(ctx context.Context, sess snmp.Session, facets map[uint32]*phyv1.EthernetFacet) error {
	var walkErrs []error

	infoWalk := hh3ctransceiverinfomib.Hh3cTransceiverInfoTable.Walk(ctx, sess, hh3cXcvrColumns...)
	for idx, row := range infoWalk.Iter() {
		ifIndex, ok := singleIndex(idx)
		if !ok {
			continue
		}

		mapHh3cXcvr(facets, ifIndex, row)
	}

	if err := infoWalk.Err(); err != nil {
		walkErrs = append(walkErrs, errs.From(err).Code(ErrCodePhysicalWalk).Msg("walk hh3cTransceiverInfoTable"))
	}

	chanWalk := hh3ctransceiverinfomib.Hh3cTransceiverChannelTable.Walk(ctx, sess, hh3cChannelColumns...)
	for idx, row := range chanWalk.Iter() {
		if idx.Len() != 2 || idx.At(1) == 0 {
			continue
		}

		mapHh3cChannel(facets, idx.At(0), idx.At(1), row)
	}

	if err := chanWalk.Err(); err != nil {
		walkErrs = append(walkErrs, errs.From(err).Code(ErrCodePhysicalWalk).Msg("walk hh3cTransceiverChannelTable"))
	}

	return errors.Join(walkErrs...)
}

// mapHh3cXcvr maps one H3C transceiver row. H3C reports current power in
// hundredths of dBm, temperature in whole degrees, voltage in hundredths
// of a volt, and bias in hundredths of a milliampere; its thresholds use
// the same units HP does.
func mapHh3cXcvr(facets map[uint32]*phyv1.EthernetFacet, ifIndex uint32, r hh3ctransceiverinfomib.Hh3cTransceiverInfoTableRow) {
	vendor := strings.TrimSpace(string(r.Hh3cTransceiverVendorName))
	hardware := strings.TrimSpace(string(r.Hh3cTransceiverHardwareType))

	if r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverVendorName) && r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverHardwareType) && vendor == "" && hardware == "" {
		empty := &phyv1.PluggableModule{}
		empty.SetPresent(false)
		facetAt(facets, ifIndex).SetModule(empty)

		return
	}

	module := moduleAt(facets, ifIndex)
	if module == nil {
		return
	}

	if r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverHardwareType) {
		if ff, ok := formFactorFromText(hardware); ok {
			module.SetFormFactor(ff)
		}
	}

	if r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverType) && !module.HasFormFactor() {
		if ff, ok := formFactorFromText(string(r.Hh3cTransceiverType)); ok {
			module.SetFormFactor(ff)
		}
	}

	if r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverVendorName) {
		setString(module.SetVendor, vendor)
	}

	if r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverSerialNumber) {
		setString(module.SetSerialNumber, string(r.Hh3cTransceiverSerialNumber))
	}

	if r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverPartNumber) {
		setString(module.SetPartNumber, string(r.Hh3cTransceiverPartNumber))
	}

	if r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverRevisionNumber) {
		setString(module.SetRevision, string(r.Hh3cTransceiverRevisionNumber))
	}

	if r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverWaveLength) && r.Hh3cTransceiverWaveLength > 0 {
		laneAt(module, 1).SetWavelengthNanometers(uint32(r.Hh3cTransceiverWaveLength))
		fiberIfUnset(facetAt(facets, ifIndex))
	}

	if r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverDiagnostic) && !r.Hh3cTransceiverDiagnostic {
		return
	}

	temp := &phyv1.ModuleTemperature{}
	setSigned(temp.SetValueMillidegrees, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverTemperature), r.Hh3cTransceiverTemperature, 1000)
	setSigned(temp.SetHighAlarmMillidegrees, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverTempHiAlarm), r.Hh3cTransceiverTempHiAlarm, 1)
	setSigned(temp.SetHighWarningMillidegrees, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverTempHiWarn), r.Hh3cTransceiverTempHiWarn, 1)
	setSigned(temp.SetLowWarningMillidegrees, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverTempLoWarn), r.Hh3cTransceiverTempLoWarn, 1)
	setSigned(temp.SetLowAlarmMillidegrees, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverTempLoAlarm), r.Hh3cTransceiverTempLoAlarm, 1)

	if populated(temp) {
		diagnosticsAt(module).SetTemperature(temp)
	}

	volt := &phyv1.SupplyVoltage{}
	setUnsigned(volt.SetValueMicrovolts, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverVoltage), int64(r.Hh3cTransceiverVoltage), 10_000)
	setNonzero(volt.SetHighAlarmMicrovolts, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverVccHiAlarm), int64(r.Hh3cTransceiverVccHiAlarm), 100)
	setNonzero(volt.SetHighWarningMicrovolts, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverVccHiWarn), int64(r.Hh3cTransceiverVccHiWarn), 100)
	setNonzero(volt.SetLowWarningMicrovolts, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverVccLoWarn), int64(r.Hh3cTransceiverVccLoWarn), 100)
	setNonzero(volt.SetLowAlarmMicrovolts, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverVccLoAlarm), int64(r.Hh3cTransceiverVccLoAlarm), 100)

	if populated(volt) {
		diagnosticsAt(module).SetVoltage(volt)
	}

	lane := laneAt(module, 1)

	bias := &phyv1.BiasCurrent{}
	setUnsigned(bias.SetValueMicroamperes, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverBiasCurrent), int64(r.Hh3cTransceiverBiasCurrent), 10)
	setNonzero(bias.SetHighAlarmMicroamperes, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverBiasHiAlarm), int64(r.Hh3cTransceiverBiasHiAlarm), 1)
	setNonzero(bias.SetHighWarningMicroamperes, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverBiasHiWarn), int64(r.Hh3cTransceiverBiasHiWarn), 1)
	setNonzero(bias.SetLowWarningMicroamperes, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverBiasLoWarn), int64(r.Hh3cTransceiverBiasLoWarn), 1)
	setNonzero(bias.SetLowAlarmMicroamperes, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverBiasLoAlarm), int64(r.Hh3cTransceiverBiasLoAlarm), 1)

	if populated(bias) {
		lane.SetBias(bias)
	}

	tx := &phyv1.OpticalPower{}

	setDbm(tx.SetValueNanowatts, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverCurTXPower), r.Hh3cTransceiverCurTXPower, 100)

	setNonzero(tx.SetHighAlarmNanowatts, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverPwrOutHiAlarm), int64(r.Hh3cTransceiverPwrOutHiAlarm), 100)
	setNonzero(tx.SetHighWarningNanowatts, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverPwrOutHiWarn), int64(r.Hh3cTransceiverPwrOutHiWarn), 100)
	setNonzero(tx.SetLowWarningNanowatts, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverPwrOutLoWarn), int64(r.Hh3cTransceiverPwrOutLoWarn), 100)
	setNonzero(tx.SetLowAlarmNanowatts, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverPwrOutLoAlarm), int64(r.Hh3cTransceiverPwrOutLoAlarm), 100)

	if populated(tx) {
		lane.SetTxPower(tx)
	}

	rx := &phyv1.OpticalPower{}

	setDbm(rx.SetValueNanowatts, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverCurRXPower), r.Hh3cTransceiverCurRXPower, 100)

	setNonzero(rx.SetHighAlarmNanowatts, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverRcvPwrHiAlarm), int64(r.Hh3cTransceiverRcvPwrHiAlarm), 100)
	setNonzero(rx.SetHighWarningNanowatts, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverRcvPwrHiWarn), int64(r.Hh3cTransceiverRcvPwrHiWarn), 100)
	setNonzero(rx.SetLowWarningNanowatts, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverRcvPwrLoWarn), int64(r.Hh3cTransceiverRcvPwrLoWarn), 100)
	setNonzero(rx.SetLowAlarmNanowatts, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverRcvPwrLoAlarm), int64(r.Hh3cTransceiverRcvPwrLoAlarm), 100)

	if populated(rx) {
		lane.SetRxPower(rx)
	}
}

// mapHh3cChannel maps one H3C per-channel row onto the lane of the same
// index. The channel's bias and transmit readings replace what the info
// table put on the lane, alarms included: the info table's warning
// thresholds describe the module as a whole and need not order against a
// channel's own alarms. The channel table has no receive thresholds, so
// the receive reading joins the module-level thresholds already there.
func mapHh3cChannel(facets map[uint32]*phyv1.EthernetFacet, ifIndex, channel uint32, r hh3ctransceiverinfomib.Hh3cTransceiverChannelTableRow) {
	module := moduleAt(facets, ifIndex)
	if module == nil {
		return
	}

	lane := laneAt(module, channel)

	bias := &phyv1.BiasCurrent{}
	setUnsigned(bias.SetValueMicroamperes, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverChannelBiasCurrent), int64(r.Hh3cTransceiverChannelBiasCurrent), 10)
	setNonzero(bias.SetHighAlarmMicroamperes, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverChannelBiasHiAm), int64(r.Hh3cTransceiverChannelBiasHiAm), 1)
	setNonzero(bias.SetLowAlarmMicroamperes, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverChannelBiasLoAm), int64(r.Hh3cTransceiverChannelBiasLoAm), 1)

	if populated(bias) {
		lane.SetBias(bias)
	}

	tx := &phyv1.OpticalPower{}
	setDbm(tx.SetValueNanowatts, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverChannelCurTXPower), r.Hh3cTransceiverChannelCurTXPower, 100)
	setNonzero(tx.SetHighAlarmNanowatts, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverChannelTXPwrHiAm), int64(r.Hh3cTransceiverChannelTXPwrHiAm), 100)
	setNonzero(tx.SetLowAlarmNanowatts, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverChannelTXPwrLoAm), int64(r.Hh3cTransceiverChannelTXPwrLoAm), 100)

	if populated(tx) {
		lane.SetTxPower(tx)
	}

	rx := lane.GetRxPower()
	if rx == nil {
		rx = &phyv1.OpticalPower{}
	}

	setDbm(rx.SetValueNanowatts, r.Observed(hh3ctransceiverinfomib.Hh3cTransceiverChannelCurRXPower), r.Hh3cTransceiverChannelCurRXPower, 100)

	if populated(rx) {
		lane.SetRxPower(rx)
	}
}

// setSigned sets a signed schema field from an observed vendor value
// scaled by factor, saturating on overflow.
func setSigned(set func(int32), observed bool, value int32, factor int64) {
	if !observed {
		return
	}

	scaled := int64(value) * factor
	if scaled > math.MaxInt32 {
		scaled = math.MaxInt32
	} else if scaled < math.MinInt32 {
		scaled = math.MinInt32
	}

	set(int32(scaled))
}

// setUnsigned sets an unsigned schema field from an observed vendor
// value scaled by factor. A negative reading is outside every vendor's
// documented range and reads as unreported; an overflow saturates.
func setUnsigned(set func(uint32), observed bool, value, factor int64) {
	if !observed || value < 0 {
		return
	}

	scaled := value * factor
	if scaled > math.MaxUint32 {
		scaled = math.MaxUint32
	}

	set(uint32(scaled))
}

// setNonzero is [setUnsigned] for a vendor value whose zero means the
// module reports none: every HP and H3C threshold, and HP's supply
// voltage.
func setNonzero(set func(uint32), observed bool, value, factor int64) {
	if value == 0 {
		return
	}

	setUnsigned(set, observed, value, factor)
}

// setDbm sets a nanowatt field from an observed power in dBm scaled by
// perDbm (1000 for thousandths, 100 for hundredths), rounding to the
// nearest. A power too strong to fit the field is outside every module's
// range and reads as unreported, which also covers an agent that answers
// an unmeasurable reading with the largest integer; one too weak to reach
// a nanowatt reads as zero.
func setDbm(set func(uint32), observed bool, value int32, perDbm float64) {
	if !observed {
		return
	}

	nw := math.Round(math.Pow(10, float64(value)/perDbm/10) * 1e6)
	if nw > math.MaxUint32 {
		return
	}

	set(uint32(nw))
}

// setHpDbm is [setDbm] for HP's thousandths of dBm, whose documented
// sentinel for no light is zero nanowatts.
func setHpDbm(set func(uint32), observed bool, value int32) {
	if observed && value == hpNoLightDbm {
		set(0)

		return
	}

	setDbm(set, observed, value, 1000)
}

// leadingNumber reads the unsigned integer a vendor string starts with,
// as in "850 nm" or "1310nm".
func leadingNumber(text string) (uint32, bool) {
	digits := 0
	for digits < len(text) && text[digits] >= '0' && text[digits] <= '9' {
		digits++
	}

	if digits == 0 {
		return 0, false
	}

	n, err := strconv.ParseUint(text[:digits], 10, 32)
	if err != nil {
		return 0, false
	}

	return uint32(n), true
}

// formFactorFromText reads a form factor out of the free text every
// vendor table uses, matching the longest family name first so "QSFP28"
// is not read as "QSFP". Text naming no known family yields nothing.
func formFactorFromText(text string) (phyv1.ModuleFormFactor, bool) {
	t := strings.ToUpper(strings.TrimSpace(text))
	t = strings.ReplaceAll(t, "_", "-")

	for _, f := range formFactorNames {
		if strings.Contains(t, f.name) {
			return f.value, true
		}
	}

	return phyv1.ModuleFormFactor_MODULE_FORM_FACTOR_UNKNOWN, false
}

// formFactorNames is ordered longest and most specific first.
var formFactorNames = []struct {
	name  string
	value phyv1.ModuleFormFactor
}{
	{"QSFP-DD", phyv1.ModuleFormFactor_MODULE_FORM_FACTOR_QSFP_DD},
	{"QSFPDD", phyv1.ModuleFormFactor_MODULE_FORM_FACTOR_QSFP_DD},
	{"QSFP28", phyv1.ModuleFormFactor_MODULE_FORM_FACTOR_QSFP28},
	{"QSFP+", phyv1.ModuleFormFactor_MODULE_FORM_FACTOR_QSFP_PLUS},
	{"QSFP", phyv1.ModuleFormFactor_MODULE_FORM_FACTOR_QSFP},
	{"SFP-DD", phyv1.ModuleFormFactor_MODULE_FORM_FACTOR_SFP_DD},
	{"OSFP", phyv1.ModuleFormFactor_MODULE_FORM_FACTOR_OSFP},
	{"DSFP", phyv1.ModuleFormFactor_MODULE_FORM_FACTOR_DSFP},
	{"XENPAK", phyv1.ModuleFormFactor_MODULE_FORM_FACTOR_XENPAK},
	{"XPAK", phyv1.ModuleFormFactor_MODULE_FORM_FACTOR_XPAK},
	{"XFP", phyv1.ModuleFormFactor_MODULE_FORM_FACTOR_XFP},
	{"GBIC", phyv1.ModuleFormFactor_MODULE_FORM_FACTOR_GBIC},
	{"SFP", phyv1.ModuleFormFactor_MODULE_FORM_FACTOR_SFP},
}

// connectorFromText reads a connector out of vendor free text, longest
// name first. Text naming no known connector yields nothing.
func connectorFromText(text string) (phyv1.ModuleConnector, bool) {
	t := strings.ToUpper(strings.TrimSpace(text))

	for _, c := range connectorNames {
		if strings.Contains(t, c.name) {
			return c.value, true
		}
	}

	return phyv1.ModuleConnector_MODULE_CONNECTOR_UNKNOWN, false
}

// connectorNames is ordered longest and most specific first.
var connectorNames = []struct {
	name  string
	value phyv1.ModuleConnector
}{
	{"COPPER PIGTAIL", phyv1.ModuleConnector_MODULE_CONNECTOR_COPPER_PIGTAIL},
	{"OPTICAL PIGTAIL", phyv1.ModuleConnector_MODULE_CONNECTOR_OPTICAL_PIGTAIL},
	{"NO SEPARABLE", phyv1.ModuleConnector_MODULE_CONNECTOR_NONE},
	{"MPO 2X16", phyv1.ModuleConnector_MODULE_CONNECTOR_MPO_DUAL_ROW16},
	{"MPO 1X16", phyv1.ModuleConnector_MODULE_CONNECTOR_MPO_SINGLE_ROW16},
	{"MPO 2X12", phyv1.ModuleConnector_MODULE_CONNECTOR_MPO_DUAL_ROW12},
	{"MPO", phyv1.ModuleConnector_MODULE_CONNECTOR_MPO_SINGLE_ROW12},
	{"MT-RJ", phyv1.ModuleConnector_MODULE_CONNECTOR_MT_RJ},
	{"MTRJ", phyv1.ModuleConnector_MODULE_CONNECTOR_MT_RJ},
	{"RJ45", phyv1.ModuleConnector_MODULE_CONNECTOR_RJ45},
	{"RJ-45", phyv1.ModuleConnector_MODULE_CONNECTOR_RJ45},
	{"LC", phyv1.ModuleConnector_MODULE_CONNECTOR_LC},
	{"SC", phyv1.ModuleConnector_MODULE_CONNECTOR_SC},
	{"MU", phyv1.ModuleConnector_MODULE_CONNECTOR_MU},
	{"CS", phyv1.ModuleConnector_MODULE_CONNECTOR_CS},
	{"SN", phyv1.ModuleConnector_MODULE_CONNECTOR_SN},
}

// mediumFromText selects a transport arm from a vendor's media
// description when the MAU type left it undecided.
func mediumFromText(facet *phyv1.EthernetFacet, text string) {
	t := strings.ToLower(text)

	switch {
	case strings.Contains(t, "copper"):
		copperIfUnset(facet)
	case strings.Contains(t, "mode"), strings.Contains(t, "fib"):
		fiberIfUnset(facet)
	}
}

// copperIfUnset selects the copper arm unless a stronger source already
// chose a transport.
func copperIfUnset(facet *phyv1.EthernetFacet) {
	if !facet.HasTransport() {
		copperArm(facet)
	}
}

// fiberIfUnset selects the fiber arm unless a stronger source already
// chose a transport.
func fiberIfUnset(facet *phyv1.EthernetFacet) {
	if !facet.HasTransport() {
		fiberArm(facet)
	}
}
