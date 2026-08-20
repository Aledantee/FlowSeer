package yang_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/yang"
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
	drift := yang.DiffRevisions(vendored, advertised)
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
