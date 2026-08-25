package output

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
)

// feedLineLimit caps the number of feed lines retained in memory. The feed is
// a bounded viewport: older lines scroll off as new findings arrive.
const feedLineLimit = 1000

// progressEntry is one attack's progress state in the TUI's progress map.
type progressEntry struct {
	attack string
	mode   string
	phase  string
	rollup string // non-empty when a rollup verdict has arrived
}

// Model is the bubbletea v2 model for netpen's TUI. It holds a live findings
// feed (a bounded, scrollable viewport) and a per-attack progress map driven
// by progress and rollup records from the findings stream.
//
// The model never owns signal handling or teardown. Quitting (q or Ctrl-C)
// transitions the model to a finished state that main reads to drive its own
// teardown — the TUI's responsibility ends at signaling intent to quit.
type Model struct {
	feed        []string
	feedContent string // cached strings.Join(feed, "\n"); rebuilt when feedDirty
	feedDirty   bool
	viewport    viewport.Model
	progress    map[string]*progressEntry
	width       int
	height      int
	finished    bool
	isDark      bool
}

// NewModel constructs a TUI model with the given dark-background flag
// (lipgloss v2 explicit isDark) and an initial terminal size. The model is
// ready for tea.NewProgram.
func NewModel(isDark bool, width, height int) Model {
	m := Model{
		feed:      make([]string, 0, feedLineLimit),
		feedDirty: true,
		progress:  make(map[string]*progressEntry),
		isDark:    isDark,
		width:     width,
		height:    height,
	}
	m.viewport = viewport.New(viewport.WithWidth(m.contentWidth()), viewport.WithHeight(m.contentHeight()))
	return m
}

// Init satisfies the tea.Model interface. It returns nil — the stream-reading
// Cmd is armed by the caller through [Model.WaitForRecord].
func (m Model) Init() tea.Cmd {
	return nil
}

// Update drives the model from findings-stream messages and terminal events.
// Findings records append to the feed and update the progress map; a quit key
// (q or Ctrl-C) transitions the model to the finished state.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.SetWidth(m.contentWidth())
		m.viewport.SetHeight(m.contentHeight())
		return m, nil

	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.finished = true
			return m, tea.Quit
		case "up", "k":
			m.viewport.ScrollUp(1)
		case "down", "j":
			m.viewport.ScrollDown(1)
		case "pgup":
			m.viewport.HalfPageUp()
		case "pgdown":
			m.viewport.HalfPageDown()
		}

	case findings.Record:
		m.applyRecord(msg)
	}

	return m, nil
}

// applyRecord updates the feed and progress map from one findings record. It
// is the shared ingestion path for both live stream messages and synthetic
// test injection.
func (m *Model) applyRecord(r findings.Record) {
	switch r.Kind {
	case findings.KindMeta:
		if r.Meta != nil {
			m.appendFeed(fmt.Sprintf("netpen %s — attack: %s", r.Meta.Tool, r.Meta.AttackLeg))
			if r.Meta.WatchLeg != "" {
				m.appendFeed(fmt.Sprintf("  watch leg: %s", r.Meta.WatchLeg))
			}
		}

	case findings.KindFinding:
		if r.Finding != nil {
			m.appendFeed(fmt.Sprintf("[%s] %s: %s", r.Attack, r.Finding.Module, string(r.Finding.Detail)))
		}

	case findings.KindProgress:
		if r.Progress != nil {
			key := attackKey(r.Attack, r.Mode)
			entry := m.getOrCreate(key, r.Attack, r.Mode)
			entry.phase = r.Progress.Phase
		}

	case findings.KindResisted, findings.KindSkipped, findings.KindPending:
		if r.Rollup != nil {
			key := attackKey(r.Attack, r.Mode)
			entry := m.getOrCreate(key, r.Attack, r.Mode)
			entry.rollup = string(r.Kind)
			m.appendFeed(fmt.Sprintf("[%s] %s: %s", r.Attack, r.Kind, r.Rollup.Detail))
		}

	case findings.KindError:
		if r.Error != nil {
			m.appendFeed(fmt.Sprintf("[%s] error: %s (%s)", r.Attack, r.Error.Message, r.Error.Code))
		}

	case findings.KindRefusal:
		if r.Refusal != nil {
			m.appendFeed(fmt.Sprintf("[%s] refused: %s", r.Attack, r.Refusal.Reason))
		}

	case findings.KindSummary:
		if r.Summary != nil {
			s := r.Summary
			m.appendFeed(fmt.Sprintf("summary: %d attacks, %d findings, %d resisted, %d skipped, %d pending, %d errors",
				s.Attacks, s.Findings, s.Resisted, s.Skipped, s.Pending, s.Errors))
		}
	}

	m.refreshViewport()
}

// appendFeed adds a line to the feed, evicting the oldest when at capacity.
func (m *Model) appendFeed(line string) {
	if len(m.feed) >= feedLineLimit {
		m.feed = m.feed[1:]
	}
	m.feed = append(m.feed, line)
	m.feedDirty = true
}

// getOrCreate fetches or creates a progress entry for an attack.
func (m *Model) getOrCreate(key, attack, mode string) *progressEntry {
	entry, ok := m.progress[key]
	if !ok {
		entry = &progressEntry{attack: attack, mode: mode}
		m.progress[key] = entry
	}
	return entry
}

// refreshViewport syncs the viewport's content from the feed. The join
// is cached and only rebuilt when the feed changed since the last sync.
func (m *Model) refreshViewport() {
	if m.feedDirty {
		m.feedContent = strings.Join(m.feed, "\n")
		m.feedDirty = false
	}
	m.viewport.SetContent(m.feedContent)
}

// View renders the TUI: a findings feed viewport and a per-attack progress
// panel. A terminal narrower than 4 columns clamps to a minimal layout rather
// than crashing.
func (m Model) View() tea.View {
	if m.width < 4 {
		return clampView(m.width, m.height)
	}

	var sb strings.Builder

	sb.WriteString(m.headerStyle().Render("netpen — live findings feed"))
	sb.WriteString("\n")

	feedHeight := m.contentHeight() - progressPanelHeight(m.progress) - 1
	if feedHeight < 1 {
		feedHeight = 1
	}
	m.viewport.SetHeight(feedHeight)
	m.refreshViewport()
	sb.WriteString(m.viewport.View())
	sb.WriteString("\n")

	if len(m.progress) > 0 {
		sb.WriteString(m.renderProgress())
	}

	v := tea.NewView(sb.String())
	v.AltScreen = true
	return v
}

// renderProgress renders the per-attack progress panel.
func (m Model) renderProgress() string {
	var sb strings.Builder
	sb.WriteString(m.headerStyle().Render("progress"))
	sb.WriteString("\n")

	for _, key := range m.sortedProgressKeys() {
		entry := m.progress[key]
		line := entry.attack
		if entry.mode != "" {
			line += " (" + entry.mode + ")"
		}
		if entry.rollup != "" {
			line += " → " + entry.rollup
		} else if entry.phase != "" {
			line += " → " + entry.phase
		}
		sb.WriteString(line)
		sb.WriteString("\n")
	}

	return sb.String()
}

// sortedProgressKeys returns progress map keys in a stable order.
func (m Model) sortedProgressKeys() []string {
	keys := make([]string, 0, len(m.progress))
	for k := range m.progress {
		keys = append(keys, k)
	}
	// Simple insertion sort for deterministic output without importing sort.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// Finished reports whether the model has transitioned to the finished state
// (the user pressed q or Ctrl-C). Main reads this to drive its own teardown.
func (m Model) Finished() bool {
	return m.finished
}

// Feed returns the current feed lines (for testing).
func (m Model) Feed() []string {
	return m.feed
}

// progressEntries returns the current progress entries (for testing).
func (m Model) progressEntries() map[string]*progressEntry {
	return m.progress
}

// contentWidth returns the usable width for content, reserving space for
// borders/padding. Clamped to at least 1.
func (m Model) contentWidth() int {
	if m.width < 4 {
		return 1
	}
	return m.width - 2
}

// contentHeight returns the usable height for content. Clamped to at least 1.
func (m Model) contentHeight() int {
	if m.height < 2 {
		return 1
	}
	return m.height - 1
}

// headerStyle returns a lipgloss style for section headers, using the
// model's isDark flag for color selection.
func (m Model) headerStyle() lipgloss.Style {
	style := lipgloss.NewStyle().Bold(true)
	if m.isDark {
		style = style.Foreground(lipgloss.Color("117"))
	} else {
		style = style.Foreground(lipgloss.Color("21"))
	}
	return style
}

// attackKey builds the progress-map key for an (attack, mode) pair.
func attackKey(attack, mode string) string {
	return attack + "\x00" + mode
}

// progressPanelHeight returns the height the progress panel occupies.
func progressPanelHeight(progress map[string]*progressEntry) int {
	if len(progress) == 0 {
		return 0
	}
	return len(progress) + 1 // header + entries
}

// clampView renders a minimal view for a terminal too narrow for the full
// layout. It never panics.
func clampView(width, height int) tea.View {
	if width < 1 || height < 1 {
		return tea.NewView("")
	}
	return tea.NewView("netpen")
}
