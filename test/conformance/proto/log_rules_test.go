package conformance

import (
	"strings"
	"testing"

	logv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/log/v1"
)

// TestSyslogRegistriesKeepTheirIntegers validates that SyslogSeverity and
// SyslogFacility match the RFC 5424 §6.2.1 integer assignments exactly,
// have real zero values, and define no UNSPECIFIED sentinels.
func TestSyslogRegistriesKeepTheirIntegers(t *testing.T) {
	if got := int32(logv1.SyslogSeverity_SYSLOG_SEVERITY_EMERGENCY); got != 0 {
		t.Errorf("SYSLOG_SEVERITY_EMERGENCY = %d, want 0", got)
	}
	if got := int32(logv1.SyslogFacility_SYSLOG_FACILITY_KERN); got != 0 {
		t.Errorf("SYSLOG_FACILITY_KERN = %d, want 0", got)
	}

	wantSeverities := map[string]int32{
		"SYSLOG_SEVERITY_EMERGENCY":     0,
		"SYSLOG_SEVERITY_ALERT":         1,
		"SYSLOG_SEVERITY_CRITICAL":      2,
		"SYSLOG_SEVERITY_ERROR":         3,
		"SYSLOG_SEVERITY_WARNING":       4,
		"SYSLOG_SEVERITY_NOTICE":        5,
		"SYSLOG_SEVERITY_INFORMATIONAL": 6,
		"SYSLOG_SEVERITY_DEBUG":         7,
	}

	sevDesc := logv1.SyslogSeverity(0).Descriptor()
	if sevDesc.Values().Len() != len(wantSeverities) {
		t.Errorf("SyslogSeverity count = %d, want %d", sevDesc.Values().Len(), len(wantSeverities))
	}
	for i := 0; i < sevDesc.Values().Len(); i++ {
		v := sevDesc.Values().Get(i)
		name := string(v.Name())
		if strings.HasSuffix(name, "_UNSPECIFIED") {
			t.Errorf("SyslogSeverity has unexpected UNSPECIFIED value: %s", name)
		}
		wantNum, ok := wantSeverities[name]
		if !ok {
			t.Errorf("unexpected SyslogSeverity value: %s", name)
			continue
		}
		if int32(v.Number()) != wantNum {
			t.Errorf("SyslogSeverity %s = %d, want %d", name, v.Number(), wantNum)
		}
	}

	wantFacilities := map[string]int32{
		"SYSLOG_FACILITY_KERN":     0,
		"SYSLOG_FACILITY_USER":     1,
		"SYSLOG_FACILITY_MAIL":     2,
		"SYSLOG_FACILITY_DAEMON":   3,
		"SYSLOG_FACILITY_AUTH":     4,
		"SYSLOG_FACILITY_SYSLOG":   5,
		"SYSLOG_FACILITY_LPR":      6,
		"SYSLOG_FACILITY_NEWS":     7,
		"SYSLOG_FACILITY_UUCP":     8,
		"SYSLOG_FACILITY_CRON":     9,
		"SYSLOG_FACILITY_AUTHPRIV": 10,
		"SYSLOG_FACILITY_FTP":      11,
		"SYSLOG_FACILITY_NTP":      12,
		"SYSLOG_FACILITY_AUDIT":    13,
		"SYSLOG_FACILITY_ALERT":    14,
		"SYSLOG_FACILITY_CLOCK":    15,
		"SYSLOG_FACILITY_LOCAL0":   16,
		"SYSLOG_FACILITY_LOCAL1":   17,
		"SYSLOG_FACILITY_LOCAL2":   18,
		"SYSLOG_FACILITY_LOCAL3":   19,
		"SYSLOG_FACILITY_LOCAL4":   20,
		"SYSLOG_FACILITY_LOCAL5":   21,
		"SYSLOG_FACILITY_LOCAL6":   22,
		"SYSLOG_FACILITY_LOCAL7":   23,
	}

	facDesc := logv1.SyslogFacility(0).Descriptor()
	if facDesc.Values().Len() != len(wantFacilities) {
		t.Errorf("SyslogFacility count = %d, want %d", facDesc.Values().Len(), len(wantFacilities))
	}
	for i := 0; i < facDesc.Values().Len(); i++ {
		v := facDesc.Values().Get(i)
		name := string(v.Name())
		if strings.HasSuffix(name, "_UNSPECIFIED") {
			t.Errorf("SyslogFacility has unexpected UNSPECIFIED value: %s", name)
		}
		wantNum, ok := wantFacilities[name]
		if !ok {
			t.Errorf("unexpected SyslogFacility value: %s", name)
			continue
		}
		if int32(v.Number()) != wantNum {
			t.Errorf("SyslogFacility %s = %d, want %d", name, v.Number(), wantNum)
		}
	}
}
