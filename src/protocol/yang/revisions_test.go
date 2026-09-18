package yang_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/yang"
)

const lockfileFixture = `{
  "generator_version": "yanggen-1",
  "modules": {
    "cisco-iosxe/Cisco-IOS-XE-native": {"revision": "2023-11-01", "source_sha256": "aa", "closure_sha256": "bb"},
    "cisco-iosxe/ietf-interfaces": {"revision": "2014-05-08", "source_sha256": "cc", "closure_sha256": "dd"},
    "aruba-cx/openconfig-system": {"revision": "2021-01-18", "source_sha256": "ee", "closure_sha256": "ff"}
  }
}`

func TestParseLockfileRevisionsAndDiff(t *testing.T) {
	vendored, err := yang.ParseLockfileRevisions([]byte(lockfileFixture), "cisco-iosxe")
	if err != nil {
		t.Fatalf("ParseLockfileRevisions: %v", err)
	}
	if len(vendored) != 2 || vendored["Cisco-IOS-XE-native"] != "2023-11-01" {
		t.Fatalf("vendored = %v", vendored)
	}

	advertised := map[string]string{
		"Cisco-IOS-XE-native": "2024-07-01", // drifted
		"ietf-interfaces":     "2014-05-08", // matches
		"Cisco-IOS-XE-bgp":    "2024-07-01", // not vendored: not drift
	}
	drift, incomparable := yang.DiffRevisions(vendored, advertised)
	if len(incomparable) != 0 {
		t.Errorf("incomparable = %+v, want none: every revision here is a date", incomparable)
	}
	if len(drift) != 1 {
		t.Fatalf("drift = %+v, want exactly the native module", drift)
	}
	if drift[0].Module != "Cisco-IOS-XE-native" || drift[0].Advertised != "2024-07-01" || drift[0].Vendored != "2023-11-01" {
		t.Errorf("drift[0] = %+v", drift[0])
	}
}

func TestParseLockfileRevisionsUnknownVendor(t *testing.T) {
	if _, err := yang.ParseLockfileRevisions([]byte(lockfileFixture), "nokia-sros"); err == nil {
		t.Fatal("unknown vendor parsed without error")
	}
}

// TestDiffRevisionsSeparatesIncomparableVocabularies pins the gNMI
// case: OpenConfig modules carry both an RFC 7950 revision date and an
// openconfig-version semantic version, and gNMI Capabilities reports
// the latter. Comparing a semver against a vendored date is not
// evidence of drift, so it must not be reported as drift.
func TestDiffRevisionsSeparatesIncomparableVocabularies(t *testing.T) {
	vendored := map[string]string{
		"openconfig-bgp":      "2023-12-28",
		"openconfig-aaa":      "2022-07-29",
		"Cisco-IOS-XE-native": "2023-11-01",
	}
	advertised := map[string]string{
		"openconfig-bgp":      "9.8.0",      // semver against a date: incomparable
		"openconfig-aaa":      "2022-07-29", // same date: neither drift nor incomparable
		"Cisco-IOS-XE-native": "2020-07-02", // date against a date: real drift
	}
	drift, incomparable := yang.DiffRevisions(vendored, advertised)
	if len(drift) != 1 || drift[0].Module != "Cisco-IOS-XE-native" {
		t.Errorf("drift = %+v, want only the native module", drift)
	}
	if len(incomparable) != 1 || incomparable[0].Module != "openconfig-bgp" {
		t.Errorf("incomparable = %+v, want only openconfig-bgp", incomparable)
	}
	if len(incomparable) == 1 && incomparable[0].Advertised != "9.8.0" {
		t.Errorf("incomparable[0] = %+v, want the advertised semver preserved", incomparable[0])
	}
}
