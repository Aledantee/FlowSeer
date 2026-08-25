package output

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
)

// TestTUIUpdateAppendsFeed verifies Update with findings records appends to
// the feed model.
func TestTUIUpdateAppendsFeed(t *testing.T) {
	recs := syntheticFeed()
	m := NewModel(true, 80, 24)

	for _, r := range recs {
		model, _ := m.Update(r)
		m = model.(Model)
	}

	feed := m.Feed()
	if len(feed) == 0 {
		t.Fatal("feed is empty after processing records")
	}

	// The meta header should produce a feed line mentioning the tool.
	foundMeta := false
	for _, line := range feed {
		if strings.Contains(line, "netpen") {
			foundMeta = true
			break
		}
	}
	if !foundMeta {
		t.Error("feed does not contain the meta header line")
	}

	// A finding record should produce a feed line.
	foundFinding := false
	for _, line := range feed {
		if strings.Contains(line, "arp") {
			foundFinding = true
			break
		}
	}
	if !foundFinding {
		t.Error("feed does not contain the finding line")
	}
}

// TestTUIUpdateProgress verifies Update with progress and rollup records
// updates the per-attack progress map.
func TestTUIUpdateProgress(t *testing.T) {
	recs := syntheticFeed()
	m := NewModel(true, 80, 24)

	for _, r := range recs {
		model, _ := m.Update(r)
		m = model.(Model)
	}

	progress := m.progressEntries()
	if len(progress) == 0 {
		t.Fatal("progress map is empty after processing records")
	}

	// arpsweep had a progress record.
	entry, ok := progress[attackKey("arpsweep", "")]
	if !ok {
		t.Fatal("progress map missing arpsweep entry")
	}
	if entry.phase != "sweep" {
		t.Errorf("arpsweep phase = %q, want sweep", entry.phase)
	}

	// stproot had a resisted rollup.
	entry, ok = progress[attackKey("stproot", "")]
	if !ok {
		t.Fatal("progress map missing stproot entry")
	}
	if entry.rollup != "resisted" {
		t.Errorf("stproot rollup = %q, want resisted", entry.rollup)
	}
}

// TestTUIQuitFinishedState verifies quitting (q) transitions the model to the
// finished state and returns a Quit cmd.
func TestTUIQuitFinishedState(t *testing.T) {
	m := NewModel(true, 80, 24)

	msg := tea.KeyPressMsg{}
	msg.Text = "q"
	msg.Code = 'q'

	model, cmd := m.Update(msg)
	m = model.(Model)

	if !m.Finished() {
		t.Error("model not finished after q key")
	}
	if cmd == nil {
		t.Error("cmd is nil after q key, expected tea.Quit")
	}
}

// TestTUICtrlCFinishedState verifies Ctrl-C transitions the model to the
// finished state.
func TestTUICtrlCFinishedState(t *testing.T) {
	m := NewModel(true, 80, 24)

	// Ctrl-C is a KeyPressMsg with mod=ModCtrl and code='c'.
	msg := tea.KeyPressMsg{}
	msg.Code = 'c'
	// Set the mod field to ModCtrl through the underlying Key.
	// KeyPressMsg is a type alias for Key, so we set Mod directly.
	msg.Mod = tea.ModCtrl

	model, cmd := m.Update(msg)
	m = model.(Model)

	if !m.Finished() {
		t.Error("model not finished after ctrl+c")
	}
	if cmd == nil {
		t.Error("cmd is nil after ctrl+c, expected tea.Quit")
	}
}

// TestTUIEmptyRun verifies an empty run renders an empty-feed state without
// panic.
func TestTUIEmptyRun(t *testing.T) {
	recs := emptyFeed()
	m := NewModel(true, 80, 24)

	for _, r := range recs {
		model, _ := m.Update(r)
		m = model.(Model)
	}

	// View must not panic on an empty feed.
	v := m.View()
	if v.Content == "" {
		t.Error("View returned empty content for empty run")
	}
}

// TestTUI3ColumnClamp verifies a 3-column terminal clamps the layout instead
// of crashing.
func TestTUI3ColumnClamp(t *testing.T) {
	m := NewModel(true, 3, 10)

	// Feed some records so there's content.
	for _, r := range syntheticFeed() {
		model, _ := m.Update(r)
		m = model.(Model)
	}

	// View must not panic on a 3-column terminal.
	v := m.View()
	if v.Content == "" {
		t.Error("View returned empty content on 3-column terminal")
	}
}

// TestTUI1ColumnClamp verifies a 1-column terminal clamps without panic.
func TestTUI1ColumnClamp(t *testing.T) {
	m := NewModel(true, 1, 1)
	v := m.View()
	if v.Content == "" {
		t.Error("View returned empty content on 1x1 terminal")
	}
}

// TestTUIWindowSizeUpdate verifies a WindowSizeMsg updates the model's
// dimensions.
func TestTUIWindowSizeUpdate(t *testing.T) {
	m := NewModel(true, 80, 24)

	model, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = model.(Model)

	if m.width != 120 {
		t.Errorf("width = %d, want 120", m.width)
	}
	if m.height != 40 {
		t.Errorf("height = %d, want 40", m.height)
	}
}

// TestTUIRollupParity verifies resisted/skipped/pending rollup records appear
// as rollup entries in the TUI model.
func TestTUIRollupParity(t *testing.T) {
	recs := syntheticFeed()
	m := NewModel(true, 80, 24)

	for _, r := range recs {
		model, _ := m.Update(r)
		m = model.(Model)
	}

	progress := m.progressEntries()
	wantRollups := map[string]string{
		"stproot": "resisted",
		"vtp":     "skipped",
		"dtp":     "pending",
	}
	for attack, wantRollup := range wantRollups {
		entry, ok := progress[attackKey(attack, "")]
		if !ok {
			t.Errorf("progress map missing %s", attack)
			continue
		}
		if entry.rollup != wantRollup {
			t.Errorf("%s rollup = %q, want %q", attack, entry.rollup, wantRollup)
		}
	}
}

// TestTUIParityWithJSON verifies the same synthetic record set produces
// equivalent content in both modes: JSON lines in JSON mode and feed/progress
// updates in TUI mode.
func TestTUIParityWithJSON(t *testing.T) {
	recs := syntheticFeed()

	// JSON mode: produce JSONL lines.
	jsonStdout := newBuffer()
	jsonStderr := newBuffer()
	jw := NewJSONWriter(jsonStdout, jsonStderr, *recs[0].Meta)
	if err := jw.Run(recs[1:]); err != nil {
		t.Fatalf("JSON Run: %v", err)
	}

	// TUI mode: produce feed + progress.
	m := NewModel(true, 80, 24)
	for _, r := range recs {
		model, _ := m.Update(r)
		m = model.(Model)
	}

	// Both modes should have processed the same number of records.
	// JSON: meta + 8 records = 9 lines.
	// TUI: feed should have content from the same records.
	feed := m.Feed()
	if len(feed) == 0 {
		t.Fatal("TUI feed is empty; parity check failed")
	}

	// Both modes should carry the rollup verdicts.
	jsonOut := jsonStdout.String()
	for _, rollup := range []string{"resisted", "skipped", "pending"} {
		if !strings.Contains(jsonOut, rollup) {
			t.Errorf("JSON output missing %s rollup", rollup)
		}
		progress := m.progressEntries()
		found := false
		for _, entry := range progress {
			if entry.rollup == rollup {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("TUI progress missing %s rollup", rollup)
		}
	}
}

// TestTUIInitReturnsNil verifies Init returns nil (no initial command).
func TestTUIInitReturnsNil(t *testing.T) {
	m := NewModel(true, 80, 24)
	if cmd := m.Init(); cmd != nil {
		t.Error("Init returned non-nil cmd")
	}
}

// TestTUIMetaRecordInFeed verifies the meta record produces feed lines
// identifying the tool and legs.
func TestTUIMetaRecordInFeed(t *testing.T) {
	recs := syntheticFeed()
	m := NewModel(true, 80, 24)

	model, _ := m.Update(recs[0]) // meta record
	m = model.(Model)

	feed := m.Feed()
	if len(feed) == 0 {
		t.Fatal("feed empty after meta record")
	}
	if !strings.Contains(feed[0], "netpen") {
		t.Errorf("first feed line = %q, want it to contain netpen", feed[0])
	}
}

// TestTUISummaryInFeed verifies the summary record produces a feed line with
// aggregate counts.
func TestTUISummaryInFeed(t *testing.T) {
	recs := syntheticFeed()
	m := NewModel(true, 80, 24)

	// Find and send the summary record (last in the feed).
	for _, r := range recs {
		if r.Kind == findings.KindSummary {
			model, _ := m.Update(r)
			m = model.(Model)
		}
	}

	feed := m.Feed()
	found := false
	for _, line := range feed {
		if strings.Contains(line, "summary") && strings.Contains(line, "attacks") {
			found = true
			break
		}
	}
	if !found {
		t.Error("feed does not contain summary line with counts")
	}
}

// TestTUIViewAltScreen verifies View sets AltScreen.
func TestTUIViewAltScreen(t *testing.T) {
	m := NewModel(true, 80, 24)
	v := m.View()
	if !v.AltScreen {
		t.Error("View did not set AltScreen")
	}
}

// buffer is a minimal io.Writer for tests.
type buffer struct {
	data []byte
}

func newBuffer() *buffer { return &buffer{} }

func (b *buffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *buffer) String() string { return string(b.data) }
