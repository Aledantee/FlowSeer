package conformance

import (
	"errors"
	"strings"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	measurev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/measure/v1"
)

func TestTemperatureThresholdsOrdered(t *testing.T) {
	errsOn(t, measurev1.Temperature_builder{
		HighWarningMillidegreesCelsius: proto.Int32(70_000),
		HighAlarmMillidegreesCelsius:   proto.Int32(60_000),
	}.Build(), "temperature.thresholds_ordered")

	runValidationCases(t, []validationCase{
		{name: "ordered thresholds pass", message: validTemperature(), wantValid: true},
		{name: "a single threshold passes", message: measurev1.Temperature_builder{
			LowAlarmMillidegreesCelsius: proto.Int32(-40_000),
		}.Build(), wantValid: true},
		{name: "no measurement passes", message: measurev1.Temperature_builder{}.Build(), wantValid: true},
	})
}

func TestVoltageThresholdsOrdered(t *testing.T) {
	errsOn(t, measurev1.Voltage_builder{
		HighWarningMicrovolts: proto.Int32(52_000_000),
		HighAlarmMicrovolts:   proto.Int32(48_000_000),
	}.Build(), "voltage.thresholds_ordered")

	runValidationCases(t, []validationCase{
		{name: "a negative rail value passes", message: validVoltage(), wantValid: true},
		{name: "no measurement passes", message: measurev1.Voltage_builder{}.Build(), wantValid: true},
	})
}

func TestCurrentThresholdsOrdered(t *testing.T) {
	errsOn(t, measurev1.Current_builder{
		HighWarningMicroamperes: proto.Int32(6_000),
		HighAlarmMicroamperes:   proto.Int32(5_000),
	}.Build(), "current.thresholds_ordered")

	runValidationCases(t, []validationCase{
		{name: "ordered thresholds pass", message: validCurrent(), wantValid: true},
		{name: "no measurement passes", message: measurev1.Current_builder{}.Build(), wantValid: true},
	})
}

func TestPowerThresholdsOrdered(t *testing.T) {
	errsOn(t, measurev1.Power_builder{
		HighWarningNanowatts: proto.Uint64(900_000_000_000),
		HighAlarmNanowatts:   proto.Uint64(800_000_000_000),
	}.Build(), "power.thresholds_ordered")

	runValidationCases(t, []validationCase{
		{name: "zero sensed power is a real reading", message: validPower(), wantValid: true},
		{name: "no measurement passes", message: measurev1.Power_builder{}.Build(), wantValid: true},
	})
}

func TestRotationSpeedThresholdsOrdered(t *testing.T) {
	errsOn(t, measurev1.RotationSpeed_builder{
		HighWarningRpm: proto.Uint32(12_000),
		HighAlarmRpm:   proto.Uint32(11_000),
	}.Build(), "rotation_speed.thresholds_ordered")

	runValidationCases(t, []validationCase{
		{name: "ordered thresholds pass", message: validRotationSpeed(), wantValid: true},
		{name: "no measurement passes", message: measurev1.RotationSpeed_builder{}.Build(), wantValid: true},
	})
}

func TestRelativeHumidityThresholdsOrdered(t *testing.T) {
	errsOn(t, measurev1.RelativeHumidity_builder{
		LowAlarmBasisPoints:   proto.Uint32(4_000),
		LowWarningBasisPoints: proto.Uint32(3_000),
	}.Build(), "relative_humidity.thresholds_ordered")

	runValidationCases(t, []validationCase{
		{name: "ordered thresholds pass", message: validRelativeHumidity(), wantValid: true},
		{name: "no measurement passes", message: measurev1.RelativeHumidity_builder{}.Build(), wantValid: true},
	})
}

// TestBasisPointsRule exercises the ratio rule through the relative-humidity
// carrier: the value and every threshold carry it.
func TestBasisPointsRule(t *testing.T) {
	runValidationCases(t, []validationCase{
		{name: "a full scale value is accepted", message: measurev1.RelativeHumidity_builder{
			ValueBasisPoints: proto.Uint32(10_000),
		}.Build(), wantValid: true},
		{name: "a value above full scale is rejected", message: measurev1.RelativeHumidity_builder{
			ValueBasisPoints: proto.Uint32(10_001),
		}.Build(), wantValid: false},
		{name: "a threshold above full scale is rejected", message: measurev1.RelativeHumidity_builder{
			HighAlarmBasisPoints: proto.Uint32(10_001),
		}.Build(), wantValid: false},
	})
}

// TestSensorReadingQuantity holds the required-oneof contract: no arm is an
// error naming the oneof, and each of the six arms is a full valid reading.
func TestSensorReadingQuantity(t *testing.T) {
	t.Run("no arm fails naming the oneof", func(t *testing.T) {
		err := protovalidate.Validate(measurev1.SensorReading_builder{}.Build())
		if err == nil {
			t.Fatal("SensorReading with no arm was valid, want a violation naming quantity")
		}
		var validationErr *protovalidate.ValidationError
		if !errors.As(err, &validationErr) {
			t.Fatalf("got %T, want *protovalidate.ValidationError: %v", err, err)
		}
		var found bool
		for _, violation := range validationErr.Violations {
			if protovalidate.FieldPathString(violation.Proto.GetField()) == "quantity" &&
				strings.Contains(violation.Proto.GetMessage(), "exactly one field is required") {
				found = true
			}
		}
		if !found {
			t.Errorf("no violation named quantity with the required-oneof message; got %v", err)
		}
	})

	readings := map[string]*measurev1.SensorReading{
		"temperature":       measurev1.SensorReading_builder{Temperature: validTemperature()}.Build(),
		"voltage":           measurev1.SensorReading_builder{Voltage: validVoltage()}.Build(),
		"current":           measurev1.SensorReading_builder{Current: validCurrent()}.Build(),
		"power":             measurev1.SensorReading_builder{Power: validPower()}.Build(),
		"rotation speed":    measurev1.SensorReading_builder{RotationSpeed: validRotationSpeed()}.Build(),
		"relative humidity": measurev1.SensorReading_builder{RelativeHumidity: validRelativeHumidity()}.Build(),
	}
	for arm, reading := range readings {
		t.Run("the "+arm+" arm is a full valid reading", func(t *testing.T) {
			if err := protovalidate.Validate(reading); err != nil {
				t.Errorf("got invalid, want valid: %v", err)
			}
		})
	}
}

func TestPathQualityRules(t *testing.T) {
	errsOn(t, measurev1.PathQuality_builder{
		Latency: durationpb.New(-time.Second),
	}.Build(), "duration.gte")

	runValidationCases(t, []validationCase{
		{name: "a negative jitter is rejected", message: measurev1.PathQuality_builder{
			Jitter: durationpb.New(-time.Millisecond),
		}.Build(), wantValid: false},
		{name: "a loss above full scale is rejected", message: measurev1.PathQuality_builder{
			LossBasisPoints: proto.Uint32(10_001),
		}.Build(), wantValid: false},
		{name: "a full report passes", message: measurev1.PathQuality_builder{
			Latency:         durationpb.New(42 * time.Millisecond),
			Jitter:          durationpb.New(3 * time.Millisecond),
			LossBasisPoints: proto.Uint32(150),
		}.Build(), wantValid: true},
		{name: "an unmeasured path passes", message: measurev1.PathQuality_builder{}.Build(), wantValid: true},
	})
}

func validTemperature() *measurev1.Temperature {
	return measurev1.Temperature_builder{
		ValueMillidegreesCelsius:       proto.Int32(42_000),
		HighAlarmMillidegreesCelsius:   proto.Int32(75_000),
		HighWarningMillidegreesCelsius: proto.Int32(70_000),
		LowWarningMillidegreesCelsius:  proto.Int32(10_000),
		LowAlarmMillidegreesCelsius:    proto.Int32(5_000),
	}.Build()
}

func validVoltage() *measurev1.Voltage {
	// A -48 V telecom rail: 48 V below ground, ordered low to high.
	return measurev1.Voltage_builder{
		ValueMicrovolts:       proto.Int32(-48_000_000),
		HighAlarmMicrovolts:   proto.Int32(-44_000_000),
		HighWarningMicrovolts: proto.Int32(-47_000_000),
		LowWarningMicrovolts:  proto.Int32(-53_000_000),
		LowAlarmMicrovolts:    proto.Int32(-57_000_000),
	}.Build()
}

func validCurrent() *measurev1.Current {
	return measurev1.Current_builder{
		ValueMicroamperes:       proto.Int32(4_000),
		HighAlarmMicroamperes:   proto.Int32(8_000),
		HighWarningMicroamperes: proto.Int32(6_000),
		LowWarningMicroamperes:  proto.Int32(3_000),
		LowAlarmMicroamperes:    proto.Int32(2_000),
	}.Build()
}

func validPower() *measurev1.Power {
	return measurev1.Power_builder{
		ValueNanowatts:       proto.Uint64(0),
		HighAlarmNanowatts:   proto.Uint64(800_000_000_000),
		HighWarningNanowatts: proto.Uint64(700_000_000_000),
		LowWarningNanowatts:  proto.Uint64(100_000),
		LowAlarmNanowatts:    proto.Uint64(10_000),
	}.Build()
}

func validRotationSpeed() *measurev1.RotationSpeed {
	return measurev1.RotationSpeed_builder{
		ValueRpm:       proto.Uint32(6_000),
		HighAlarmRpm:   proto.Uint32(12_000),
		HighWarningRpm: proto.Uint32(10_000),
		LowWarningRpm:  proto.Uint32(3_000),
		LowAlarmRpm:    proto.Uint32(1_000),
	}.Build()
}

func validRelativeHumidity() *measurev1.RelativeHumidity {
	return measurev1.RelativeHumidity_builder{
		ValueBasisPoints:       proto.Uint32(5_000),
		HighAlarmBasisPoints:   proto.Uint32(9_000),
		HighWarningBasisPoints: proto.Uint32(8_000),
		LowWarningBasisPoints:  proto.Uint32(2_000),
		LowAlarmBasisPoints:    proto.Uint32(1_000),
	}.Build()
}

// errsOn asserts msg fails validation with a violation of the named rule,
// which is how an out-of-contract reading is told apart from a valid one:
// requiring a rule id keeps a pass where every rule was dropped from reading
// as proof of anything.
func errsOn(t *testing.T, msg proto.Message, ruleID string) {
	t.Helper()

	err := protovalidate.Validate(msg)
	if err == nil {
		t.Fatalf("message was valid, want a violation of %s", ruleID)
	}
	var validationErr *protovalidate.ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("got %T, want *protovalidate.ValidationError: %v", err, err)
	}
	for _, violation := range validationErr.Violations {
		if violation.Proto.GetRuleId() == ruleID {
			return
		}
	}
	t.Errorf("no violation of rule %s; got %v", ruleID, err)
}
