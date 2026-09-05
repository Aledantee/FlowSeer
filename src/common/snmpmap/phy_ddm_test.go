package snmpmap_test

import (
	"context"
	"math"
	"testing"

	"go.aledante.io/FlowSeer/generated/go/mib/dlinkswddmmib"
	"go.aledante.io/FlowSeer/generated/go/mib/dlinkswsfpinfomib"
	"go.aledante.io/FlowSeer/generated/go/mib/hh3ctransceiverinfomib"
	"go.aledante.io/FlowSeer/generated/go/mib/hpicftransceivermib"
	"go.aledante.io/FlowSeer/generated/go/mib/maumib"
	phyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/phy/v1"
	"go.aledante.io/FlowSeer/src/common/snmpmap"
)

// The vendor fixtures below are authored from the MIBs' own object
// descriptions, because no lab switch implements these modules. Each
// helper names the units the vendor encodes so a conversion can be
// checked against the MIB text.

func TestPhysical_DlinkEmptyCage(t *testing.T) {
	// A D-Link port with no module reports an empty laser
	// identifier, and nothing else about the cage survives.
	vbs := []vbFixture{
		stringAt(dlinkswsfpinfomib.DPortSfpInfoLaserIdentifier, []byte(""), 5),
		stringAt(dlinkswsfpinfomib.DPortSfpInfoVendorName, []byte("STALE"), 5),
	}

	facts, err := snmpmap.Physical(context.Background(), &fakeSession{vbs: vbs})
	if err != nil {
		t.Fatalf("Physical: %v", err)
	}

	module := facts.Facets[5].GetModule()
	if module == nil || module.GetPresent() {
		t.Fatalf("module = %v, want an explicitly empty cage", module)
	}

	mustValid(t, facts.Facets[5])

	if module.HasVendor() {
		t.Error("an empty cage kept a vendor name")
	}
}

func TestPhysical_DlinkDacOnCopperArm(t *testing.T) {
	// A direct-attach cable on a copper port keeps its
	// identity beside the copper arm.
	vbs := []vbFixture{
		objectIDAt(maumib.IfMauType, ianaMauType(54), 9, 1),
		stringAt(dlinkswsfpinfomib.DPortSfpInfoLaserIdentifier, []byte("SFP+"), 9),
		stringAt(dlinkswsfpinfomib.DPortSfpInfoConnectType, []byte("Copper Pigtail"), 9),
		stringAt(dlinkswsfpinfomib.DPortSfpInfoVendorName, []byte("CableCo         "), 9),
		stringAt(dlinkswsfpinfomib.DPortSfpInfoVendorSN, []byte("DAC-0001"), 9),
		integerAt(dlinkswsfpinfomib.DPortSfpInfoBitRate, 10300, 9),
		integerAt(dlinkswsfpinfomib.DPortSfpInfoWavelength, 0, 9),
	}

	facts, err := snmpmap.Physical(context.Background(), &fakeSession{vbs: vbs})
	if err != nil {
		t.Fatalf("Physical: %v", err)
	}

	facet := facts.Facets[9]
	mustValid(t, facet)

	if !facet.HasCopper() {
		t.Error("10GBASE-T did not select the copper arm")
	}

	module := facet.GetModule()
	if !module.GetPresent() || module.GetVendor() != "CableCo" || module.GetSerialNumber() != "DAC-0001" {
		t.Errorf("module = %v, want a present CableCo DAC-0001", module)
	}

	if module.GetFormFactor() != phyv1.ModuleFormFactor_MODULE_FORM_FACTOR_SFP {
		t.Errorf("form factor = %v, want SFP", module.GetFormFactor())
	}

	if module.GetConnector() != phyv1.ModuleConnector_MODULE_CONNECTOR_COPPER_PIGTAIL {
		t.Errorf("connector = %v, want COPPER_PIGTAIL", module.GetConnector())
	}

	if module.GetNominalBitRateMbps() != 10300 || len(module.GetLanes()) != 0 {
		t.Errorf("module = %v, want a 10300 Mb/s rate and no lane from a zero wavelength", module)
	}
}

func TestPhysical_DlinkDdmUnits(t *testing.T) {
	vbs := []vbFixture{
		stringAt(dlinkswsfpinfomib.DPortSfpInfoLaserIdentifier, []byte("SFP"), 3),
		stringAt(dlinkswsfpinfomib.DPortSfpInfoTransmissionMedia, []byte("Multi-mode"), 3),
		integerAt(dlinkswsfpinfomib.DPortSfpInfoWavelength, 850, 3),
		// milli-degrees Celsius
		integerAt(dlinkswddmmib.DDdmIfInfoCurrentTemperature, 36_500, 3),
		integerAt(dlinkswddmmib.DDdmIfInfoHighAlarmTemperature, 90_000, 3),
		integerAt(dlinkswddmmib.DDdmIfInfoHighWarnTemperature, 85_000, 3),
		integerAt(dlinkswddmmib.DDdmIfInfoLowWarnTemperature, -5_000, 3),
		integerAt(dlinkswddmmib.DDdmIfInfoLowAlarmTemperature, -10_000, 3),
		// centi-Volt
		integerAt(dlinkswddmmib.DDdmIfInfoCurrentVoltage, 330, 3),
		// milli-amperes
		integerAt(dlinkswddmmib.DDdmIfInfoCurrentBiasCurrent, 6, 3),
		// tenths of a microwatt
		integerAt(dlinkswddmmib.DDdmIfInfoCurrentTxPower, 1234, 3),
		integerAt(dlinkswddmmib.DDdmIfInfoCurrentRxPower, 0, 3),
	}

	facts, err := snmpmap.Physical(context.Background(), &fakeSession{vbs: vbs})
	if err != nil {
		t.Fatalf("Physical: %v", err)
	}

	facet := facts.Facets[3]
	mustValid(t, facet)

	if !facet.HasFiber() {
		t.Error("a multi-mode medium did not select the fiber arm")
	}

	module := facet.GetModule()

	temp := module.GetDiagnostics().GetTemperature()
	if temp.GetValueMillidegrees() != 36_500 || temp.GetLowAlarmMillidegrees() != -10_000 {
		t.Errorf("temperature = %v, want 36.5 degrees with a -10 degree low alarm", temp)
	}

	if got := module.GetDiagnostics().GetVoltage().GetValueMicrovolts(); got != 3_300_000 {
		t.Errorf("voltage = %d microvolts, want 3300000", got)
	}

	if len(module.GetLanes()) != 1 {
		t.Fatalf("lanes = %d, want the single channel", len(module.GetLanes()))
	}

	lane := module.GetLanes()[0]

	if lane.GetIndex() != 1 || lane.GetWavelengthNanometers() != 850 {
		t.Errorf("lane = %v, want index 1 at 850 nm", lane)
	}

	if got := lane.GetBias().GetValueMicroamperes(); got != 6_000 {
		t.Errorf("bias = %d microamperes, want 6000", got)
	}

	if got := lane.GetTxPower().GetValueNanowatts(); got != 123_400 {
		t.Errorf("tx power = %d nanowatts, want 123400 exactly", got)
	}

	if !lane.GetRxPower().HasValueNanowatts() || lane.GetRxPower().GetValueNanowatts() != 0 {
		t.Error("an explicit zero rx power is absent")
	}
}

func TestPhysical_HpDbmConversion(t *testing.T) {
	vbs := []vbFixture{
		stringAt(hpicftransceivermib.HpicfXcvrModel, []byte("J9150A"), 25),
		stringAt(hpicftransceivermib.HpicfXcvrSerial, []byte("CN12345678"), 25),
		stringAt(hpicftransceivermib.HpicfXcvrType, []byte("SFP+SR"), 25),
		stringAt(hpicftransceivermib.HpicfXcvrConnectorType, []byte("LC"), 25),
		stringAt(hpicftransceivermib.HpicfXcvrWavelength, []byte("850 nm"), 25),
		integerAt(hpicftransceivermib.HpicfXcvrDiagnostics, int32(hpicftransceivermib.HpicfXcvrDiagnosticsValueDom), 25),
		// thousandths of degrees Celsius
		integerAt(hpicftransceivermib.HpicfXcvrTemp, 49_120, 25),
		// hundreds of microvolts
		gauge32At(hpicftransceivermib.HpicfXcvrVoltage, 32_928, 25),
		// microamps
		gauge32At(hpicftransceivermib.HpicfXcvrBias, 7_500, 25),
		// thousandths of dBm
		integerAt(hpicftransceivermib.HpicfXcvrTxPower, -5_840, 25),
		integerAt(hpicftransceivermib.HpicfXcvrRxPower, -99_999_999, 25),
		// tenths of microwatts; zero means unsupported
		gauge32At(hpicftransceivermib.HpicfXcvrPwrOutHiAlarm, 10_000, 25),
		gauge32At(hpicftransceivermib.HpicfXcvrPwrOutHiWarn, 8_000, 25),
		gauge32At(hpicftransceivermib.HpicfXcvrPwrOutLoWarn, 1_000, 25),
		gauge32At(hpicftransceivermib.HpicfXcvrPwrOutLoAlarm, 0, 25),
	}

	facts, err := snmpmap.Physical(context.Background(), &fakeSession{vbs: vbs})
	if err != nil {
		t.Fatalf("Physical: %v", err)
	}

	facet := facts.Facets[25]
	mustValid(t, facet)

	if !facet.HasFiber() {
		t.Error("a wavelength did not select the fiber arm")
	}

	module := facet.GetModule()

	if module.GetPartNumber() != "J9150A" || module.GetConnector() != phyv1.ModuleConnector_MODULE_CONNECTOR_LC {
		t.Errorf("module = %v, want J9150A with an LC connector", module)
	}

	if got := module.GetDiagnostics().GetTemperature().GetValueMillidegrees(); got != 49_120 {
		t.Errorf("temperature = %d, want 49120 millidegrees", got)
	}

	if got := module.GetDiagnostics().GetVoltage().GetValueMicrovolts(); got != 3_292_800 {
		t.Errorf("voltage = %d microvolts, want 3292800", got)
	}

	lane := module.GetLanes()[0]

	want := uint32(math.Round(math.Pow(10, -0.584) * 1e6))
	if got := lane.GetTxPower().GetValueNanowatts(); got < want-1 || got > want+1 {
		t.Errorf("tx power = %d nanowatts, want within one of %d", got, want)
	}

	if !lane.GetRxPower().HasValueNanowatts() || lane.GetRxPower().GetValueNanowatts() != 0 {
		t.Error("the no-light sentinel did not map to zero")
	}

	tx := lane.GetTxPower()
	if tx.GetHighAlarmNanowatts() != 1_000_000 || tx.GetLowWarningNanowatts() != 100_000 || tx.HasLowAlarmNanowatts() {
		t.Errorf("tx thresholds = %v, want 1 mW high alarm, 0.1 mW low warning, no low alarm", tx)
	}
}

func TestPhysical_Hh3cChannelsOutOfOrder(t *testing.T) {
	// An H3C four-lane module whose channel rows arrive out of
	// order still maps to lanes 1 through 4 in order.
	vbs := []vbFixture{
		stringAt(hh3ctransceiverinfomib.Hh3cTransceiverHardwareType, []byte("QSFP28"), 49),
		stringAt(hh3ctransceiverinfomib.Hh3cTransceiverVendorName, []byte("H3C"), 49),
		stringAt(hh3ctransceiverinfomib.Hh3cTransceiverSerialNumber, []byte("210231A"), 49),
		integerAt(hh3ctransceiverinfomib.Hh3cTransceiverDiagnostic, 1, 49),
		// Celsius centigrade
		integerAt(hh3ctransceiverinfomib.Hh3cTransceiverTemperature, 41, 49),
		// hundredths of V
		integerAt(hh3ctransceiverinfomib.Hh3cTransceiverVoltage, 329, 49),
	}

	// hundredths of dBm per channel, delivered 2, 1, 4, 3
	for _, ch := range []uint32{2, 1, 4, 3} {
		vbs = append(vbs,
			integerAt(hh3ctransceiverinfomib.Hh3cTransceiverChannelCurTXPower, -100*int32(ch), 49, ch),
			// hundredths of mA
			integerAt(hh3ctransceiverinfomib.Hh3cTransceiverChannelBiasCurrent, 650, 49, ch),
			// microamps
			integerAt(hh3ctransceiverinfomib.Hh3cTransceiverChannelBiasHiAm, 12_000, 49, ch),
		)
	}

	facts, err := snmpmap.Physical(context.Background(), &fakeSession{vbs: vbs})
	if err != nil {
		t.Fatalf("Physical: %v", err)
	}

	facet := facts.Facets[49]
	mustValid(t, facet)

	module := facet.GetModule()

	if module.GetFormFactor() != phyv1.ModuleFormFactor_MODULE_FORM_FACTOR_QSFP28 {
		t.Errorf("form factor = %v, want QSFP28", module.GetFormFactor())
	}

	if got := module.GetDiagnostics().GetTemperature().GetValueMillidegrees(); got != 41_000 {
		t.Errorf("temperature = %d, want 41000 millidegrees", got)
	}

	if got := module.GetDiagnostics().GetVoltage().GetValueMicrovolts(); got != 3_290_000 {
		t.Errorf("voltage = %d microvolts, want 3290000", got)
	}

	lanes := module.GetLanes()
	if len(lanes) != 4 {
		t.Fatalf("lanes = %d, want 4", len(lanes))
	}

	for i, lane := range lanes {
		if lane.GetIndex() != uint32(i+1) {
			t.Errorf("lane %d has index %d", i, lane.GetIndex())
		}

		if got := lane.GetBias().GetValueMicroamperes(); got != 6_500 {
			t.Errorf("lane %d bias = %d microamperes, want 6500", i, got)
		}

		if got := lane.GetBias().GetHighAlarmMicroamperes(); got != 12_000 {
			t.Errorf("lane %d bias high alarm = %d, want 12000", i, got)
		}
	}

	// -1.00 dBm is 794328 nW; -4.00 dBm is 398107 nW.
	if got := lanes[0].GetTxPower().GetValueNanowatts(); got < 794_327 || got > 794_329 {
		t.Errorf("lane 1 tx power = %d, want about 794328", got)
	}

	if got := lanes[3].GetTxPower().GetValueNanowatts(); got < 398_106 || got > 398_108 {
		t.Errorf("lane 4 tx power = %d, want about 398107", got)
	}
}

func TestPhysical_NoVendorRowsNoModule(t *testing.T) {
	facts, err := snmpmap.Physical(context.Background(), &fakeSession{vbs: etherLikePort(3)})
	if err != nil {
		t.Fatalf("Physical: %v", err)
	}

	if facts.Facets[3].HasModule() {
		t.Error("a port with no vendor rows gained a module")
	}
}
