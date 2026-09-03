package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/smi"
)

// updateBaselineGolden refreshes the committed golden the refresh flag
// is compared against. Run with
//
//	go test ./src/protocol/snmp/cmd/mibgen -run TestBaselineRefresh -update-baseline
//
// after an intentional change to the file's shape.
var updateBaselineGolden = flag.Bool("update-baseline", false,
	"rewrite testdata/baseline/refreshed.golden.yaml from the current refresh output")

// ietfSearchPath is the bundled IETF tree the fixture module imports
// SNMPv2-SMI and SNMPv2-TC from.
func ietfSearchPath(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "..", "spec", "mib", "ietf"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("spec/mib/ietf not available: %v", err)
	}

	return dir
}

// loadFixture loads the BASELINE-MIB variant living in
// testdata/baseline/mibs/<variant>. Each variant sits in its own
// directory because they all declare the same module name: the baseline
// is keyed on that name, so the point of the fixtures is that only the
// source differs.
func loadFixture(t *testing.T, variant string) (*Config, *smi.ModuleSet) {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("testdata", "baseline", "mibs", variant))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}

	cfg := &Config{
		SearchPaths: []string{dir, ietfSearchPath(t)},
		Modules:     []Module{{Name: "BASELINE-MIB", Package: "baselinemib"}},
	}
	set, err := LoadModules(cfg)
	if err != nil {
		t.Fatalf("load %s fixture: %v", variant, err)
	}

	return cfg, set
}

// loadFixtureBaseline reads one of the committed baseline fixtures.
func loadFixtureBaseline(t *testing.T, name string) *Baseline {
	t.Helper()
	bl, err := LoadBaseline(filepath.Join("testdata", "baseline", name))
	if err != nil {
		t.Fatalf("load baseline %s: %v", name, err)
	}

	return bl
}

// checkFixture runs the gate over one variant with one baseline.
func checkFixture(t *testing.T, variant, baseline string) error {
	t.Helper()
	cfg, set := loadFixture(t, variant)

	return CheckBaseline(cfg, set, loadFixtureBaseline(t, baseline))
}

// TestBaselineCoversTheRecordedFour is the happy path: the fixture
// raises exactly the four diagnostics four.yaml records, so nothing
// stands in the way of rendering.
func TestBaselineCoversTheRecordedFour(t *testing.T) {
	if err := checkFixture(t, "base", "four.yaml"); err != nil {
		t.Fatalf("gate rejected a fully baselined module: %v", err)
	}
}

// TestBaselineNamesTheFifthDiagnostic is the gate biting. The fifth
// variant adds a third hinted gauge; the failure has to name it, because
// a failure that only says "something changed" leaves the reader to
// diff the corpus by hand.
func TestBaselineNamesTheFifthDiagnostic(t *testing.T) {
	err := checkFixture(t, "fifth", "four.yaml")
	if err == nil {
		t.Fatal("gate accepted a diagnostic the baseline does not record")
	}
	if !strings.Contains(err.Error(), "ThirdHintedGauge") {
		t.Errorf("failure does not name the new declaration: %v", err)
	}
	if !strings.Contains(err.Error(), "smi/display-hint-not-permitted") {
		t.Errorf("failure does not name the code: %v", err)
	}
}

// TestBaselineSurvivesAMovedLine is the reason file, line and column are
// left out of the key. The shifted variant is the base variant with a
// paragraph of comment inserted and one clause re-indented, which moves
// every diagnostic's line and one diagnostic's column.
func TestBaselineSurvivesAMovedLine(t *testing.T) {
	base, baseSet := loadFixture(t, "base")
	shifted, shiftedSet := loadFixture(t, "shifted")

	before, err := observe(base, baseSet)
	if err != nil {
		t.Fatalf("observe base: %v", err)
	}
	after, err := observe(shifted, shiftedSet)
	if err != nil {
		t.Fatalf("observe shifted: %v", err)
	}
	if len(before) != len(after) {
		t.Fatalf("fixtures disagree on diagnostic count: %d vs %d", len(before), len(after))
	}

	// Prove the positions really did move; otherwise the test would
	// pass without exercising anything.
	moved := false
	for i := range before {
		if before[i].rendered != after[i].rendered {
			moved = true

			break
		}
	}
	if !moved {
		t.Fatal("the shifted fixture renders identically to the base one, so it proves nothing")
	}

	if err := checkFixture(t, "shifted", "four.yaml"); err != nil {
		t.Errorf("a diagnostic that only moved failed the gate: %v", err)
	}
}

// TestBaselineFailsOnAMovedDeclaration is the other half of the key: the
// declaration name is load-bearing, so the same code arriving under a
// different name is a new condition.
func TestBaselineFailsOnAMovedDeclaration(t *testing.T) {
	err := checkFixture(t, "moved", "four.yaml")
	if err == nil {
		t.Fatal("gate accepted a diagnostic that moved to a different declaration")
	}
	if !strings.Contains(err.Error(), "OtherRange") {
		t.Errorf("failure does not name the declaration it moved to: %v", err)
	}
}

// TestBaselineFailsOnAnUnknownCode keeps a renamed or retired code from
// silently covering nothing. A row naming a code the catalog does not
// define looks exactly like a row that is doing its job.
func TestBaselineFailsOnAnUnknownCode(t *testing.T) {
	err := checkFixture(t, "base", "unknown-code.yaml")
	if err == nil {
		t.Fatal("gate accepted a baseline naming a code the catalog does not define")
	}
	if !strings.Contains(err.Error(), "smi/no-such-condition") {
		t.Errorf("failure does not name the unknown code: %v", err)
	}
}

// TestBaselineDemandsAReasonForAnErrorGrade covers the obligation the
// severity scale imposes: fatal and error mean a file is unusable or a
// definition is lost, so waving one through is a decision somebody has
// to defend in writing.
func TestBaselineDemandsAReasonForAnErrorGrade(t *testing.T) {
	err := checkFixture(t, "base", "missing-reason.yaml")
	if err == nil {
		t.Fatal("gate accepted an error-graded group with no recorded reason")
	}
	if !strings.Contains(err.Error(), "needs a recorded reason") {
		t.Errorf("failure does not say a reason is missing: %v", err)
	}

	// The minor-graded duplicate-clause group in the same file carries
	// no reason and must not be complained about, or the obligation
	// would apply to the whole scale.
	if strings.Contains(err.Error(), "smi/duplicate-clause") {
		t.Errorf("a minor-graded group was asked for a reason: %v", err)
	}
}

// TestBaselinePendingPassesTheAlwaysOnGate pins the first of the two
// tiers. Pending is the state of a group somebody has recorded but not
// yet read, and a branch mid-triage still has to build.
func TestBaselinePendingPassesTheAlwaysOnGate(t *testing.T) {
	if err := checkFixture(t, "base", "pending.yaml"); err != nil {
		t.Fatalf("the always-on gate rejected a pending group: %v", err)
	}

	// The completeness gate's body is what forbids it; the build-tagged
	// test in baseline_complete_test.go runs it against the committed
	// baseline, and this is the same predicate against the fixture.
	pending := loadFixtureBaseline(t, "pending.yaml").PendingGroups()
	if len(pending) != 1 {
		t.Fatalf("PendingGroups() = %v, want exactly the one pending group", pending)
	}
	if !strings.Contains(pending[0], "smi/display-hint-not-permitted") {
		t.Errorf("PendingGroups() = %v, want it to name the pending code", pending)
	}
}

// TestBaselineAcceptedRiskNeedsReasonAndAllowlist makes a waiver cost a
// diff in two places. One of them alone is a string somebody can type
// without anyone else reading it.
func TestBaselineAcceptedRiskNeedsReasonAndAllowlist(t *testing.T) {
	tests := []struct {
		name     string
		baseline string
		wantErr  string
	}{
		{name: "reason and allowlist", baseline: "accepted-risk-ok.yaml"},
		{name: "no reason", baseline: "accepted-risk-no-reason.yaml", wantErr: "accepted-risk with no reason"},
		{name: "not allowlisted", baseline: "accepted-risk-no-allowlist.yaml", wantErr: "not on the allowlist"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := checkFixture(t, "base", tc.baseline)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("gate rejected a well-formed accepted-risk group: %v", err)
			case tc.wantErr == "":
				return
			case err == nil:
				t.Fatalf("gate accepted an accepted-risk group missing %q", tc.wantErr)
			case !strings.Contains(err.Error(), tc.wantErr):
				t.Errorf("failure = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

// TestBaselineRefusesAnUnbaselinedUnresolvedDeclaration is the relaxed
// form of the renderer's refusal. An unresolved declaration still has an
// OID and a name, so emitting one produces an accessor that compiles and
// decodes the wrong thing — but a declaration the baseline records is
// one somebody has already signed off, and it must not cost the build
// twice.
func TestBaselineRefusesAnUnbaselinedUnresolvedDeclaration(t *testing.T) {
	err := checkFixture(t, "unresolved", "four.yaml")
	if err == nil {
		t.Fatal("gate rendered a module carrying an unresolved declaration")
	}
	if !strings.Contains(err.Error(), "baselineBroken") {
		t.Errorf("failure does not name the unresolved declaration: %v", err)
	}

	if err := checkFixture(t, "unresolved", "unresolved.yaml"); err != nil {
		t.Errorf("gate refused a module whose unresolved declaration is baselined: %v", err)
	}
}

// TestRefuseUnresolvedTreatsABaselinedNameAsSignedOff is the relaxation
// this baseline buys, stated at the unit level. The refusal itself is
// pinned by TestRefuseUnresolved_FailsRatherThanEmittingPartially.
func TestRefuseUnresolvedTreatsABaselinedNameAsSignedOff(t *testing.T) {
	mod := &smi.Module{
		Name:  "TEST-MIB",
		Nodes: []*smi.Node{{Name: "fine"}, {Name: "broken", Unresolved: true}},
	}

	if err := refuseUnresolved(mod, map[string]bool{"broken": true}); err != nil {
		t.Errorf("refuseUnresolved refused a baselined name: %v", err)
	}
	if err := refuseUnresolved(mod, map[string]bool{"somethingElse": true}); err == nil {
		t.Error("refuseUnresolved accepted an unresolved name the baseline does not record")
	}
}

// TestBaselineRefreshRegeneratesAReviewableFile pins what the refresh
// flag writes. The file is reviewed as a diff, so its shape has to be
// stable: sorted declarations, sorted codes, and modules in config
// order.
func TestBaselineRefreshRegeneratesAReviewableFile(t *testing.T) {
	cfg, set := loadFixture(t, "base")

	fresh, err := RefreshBaseline(cfg, set, nil)
	if err != nil {
		t.Fatalf("RefreshBaseline: %v", err)
	}

	path := filepath.Join(t.TempDir(), "baseline.yaml")
	if err := WriteBaseline(path, fresh); err != nil {
		t.Fatalf("WriteBaseline: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written baseline: %v", err)
	}

	goldenPath := filepath.Join("testdata", "baseline", "refreshed.golden.yaml")
	if *updateBaselineGolden {
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("updated %s (%d bytes)", goldenPath, len(got))

		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden (rerun with -update-baseline if first time): %v", err)
	}
	if string(want) != string(got) {
		t.Errorf("refresh output differs from %s; rerun with -update-baseline after auditing the diff", goldenPath)
		candidate := goldenPath + ".got"
		_ = os.WriteFile(candidate, got, 0o644)
		t.Logf("wrote candidate to %s", candidate)
	}

	// A freshly discovered group arrives pending with no reason, which
	// is what keeps the completeness gate meaningful.
	if pending := fresh.PendingGroups(); len(pending) != 3 {
		t.Errorf("PendingGroups() = %v, want all three fresh groups pending", pending)
	}
}

// TestBaselineRefreshCarriesReasonsForward keeps a refresh from throwing
// away the sentences somebody wrote. A refresh that reset every status
// would make the flag unusable on a baseline anybody had triaged.
func TestBaselineRefreshCarriesReasonsForward(t *testing.T) {
	cfg, set := loadFixture(t, "fifth")

	fresh, err := RefreshBaseline(cfg, set, loadFixtureBaseline(t, "four.yaml"))
	if err != nil {
		t.Fatalf("RefreshBaseline: %v", err)
	}

	entries, err := fresh.entries()
	if err != nil {
		t.Fatalf("entries: %v", err)
	}

	added := baselineKey{module: "BASELINE-MIB", code: "smi/display-hint-not-permitted", decl: "ThirdHintedGauge"}
	g, ok := entries[added]
	if !ok {
		t.Fatalf("refresh did not pick up the new declaration; entries = %v", entries)
	}
	if g.Status != BaselineRecorded {
		t.Errorf("status = %q, want the group's recorded status carried over", g.Status)
	}
	if g.Reason == "" {
		t.Error("the group's reason was dropped by the refresh")
	}

	// The refreshed file is what a reviewer reads, so the added name has
	// to be visible in it.
	path := filepath.Join(t.TempDir(), "baseline.yaml")
	if err := WriteBaseline(path, fresh); err != nil {
		t.Fatalf("WriteBaseline: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(body), "ThirdHintedGauge") {
		t.Errorf("refreshed file does not name the added declaration:\n%s", body)
	}
}

// TestBaselineGateNeverWritesTheBaseline is the rule that keeps the gate
// worth having. A gate that repairs itself reports nothing.
func TestBaselineGateNeverWritesTheBaseline(t *testing.T) {
	path := filepath.Join("testdata", "baseline", "four.yaml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if err := checkFixture(t, "fifth", "four.yaml"); err == nil {
		t.Fatal("expected the gate to fail on the fifth diagnostic")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(before) != string(after) {
		t.Error("the gate rewrote the baseline it was failing against")
	}
}

// TestScanDeclarationHeadsIgnoresCommentsAndStrings covers the one part
// of the attribution that can be wrong on its own. A DESCRIPTION that
// wraps to the left margin, or a commented-out declaration, would
// otherwise invent a head and shift every name after it.
func TestScanDeclarationHeadsIgnoresCommentsAndStrings(t *testing.T) {
	src := []byte(`TEST-MIB DEFINITIONS ::= BEGIN

realDecl OBJECT-TYPE
    DESCRIPTION "a description that wraps
notADeclaration but prose inside the string"
    ::= { 1 2 }

-- commentedOut OBJECT-TYPE

secondDecl OBJECT-TYPE
    ::= { 1 3 }

END
`)

	var got []string
	for _, h := range scanDeclarationHeads(src) {
		got = append(got, h.name)
	}

	want := []string{"TEST-MIB", "realDecl", "secondDecl"}
	if len(got) != len(want) {
		t.Fatalf("heads = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("heads = %v, want %v", got, want)
		}
	}
}

// TestDeclarationAtFallsBackToFileScope covers a diagnostic raised
// before anything was declared, such as a missing module header.
func TestDeclarationAtFallsBackToFileScope(t *testing.T) {
	heads := []declarationHead{{offset: 40, name: "first"}, {offset: 90, name: "second"}}

	tests := []struct {
		offset int
		want   string
	}{
		{offset: 0, want: FileScopeDeclaration},
		{offset: 39, want: FileScopeDeclaration},
		{offset: 40, want: "first"},
		{offset: 89, want: "first"},
		{offset: 90, want: "second"},
		{offset: 4000, want: "second"},
	}
	for _, tc := range tests {
		if got := declarationAt(heads, tc.offset); got != tc.want {
			t.Errorf("declarationAt(%d) = %q, want %q", tc.offset, got, tc.want)
		}
	}
}

// committedBaseline reads the baseline the repository ships, the one the
// configured modules are actually gated against.
func committedBaseline(t *testing.T) (*Config, *smi.ModuleSet, *Baseline) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "..", ".."))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	cfgPath := filepath.Join(root, defaultConfigPath)
	if _, err := os.Stat(cfgPath); err != nil {
		t.Skipf("%s not available: %v", cfgPath, err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	bl, err := LoadBaseline(filepath.Join(root, defaultBaselineName))
	if err != nil {
		t.Fatalf("LoadBaseline: %v", err)
	}
	set, err := LoadModules(cfg)
	if err != nil {
		t.Fatalf("LoadModules: %v", err)
	}

	return cfg, set, bl
}

// TestCommittedBaselineIsCurrent is the always-on integrity gate over
// the file the repository ships: it is structurally valid, and it covers
// everything the configured modules raise today.
func TestCommittedBaselineIsCurrent(t *testing.T) {
	cfg, set, bl := committedBaseline(t)

	if err := CheckBaseline(cfg, set, bl); err != nil {
		t.Errorf("the committed baseline no longer matches the corpus: %v", err)
	}
}
