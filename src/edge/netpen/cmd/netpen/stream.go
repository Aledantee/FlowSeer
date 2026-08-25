package main

// stream.go provides the live-streaming output consumers for the runner's
// findings stream. Both the JSONL writer and the TUI program consume
// records AS THEY ARRIVE while the run is in flight — not buffered until
// the run completes (R11: live findings feed; KTD10: streaming JSONL).
//
// Both consumers accept a <-chan findings.Record: the caller starts the
// run (runner.Run or orchestrator.Run) in a goroutine, then calls the
// consumer in another goroutine to drain the channel live. The channel
// is closed when the run completes, so the consumer's range loop exits.

import (
	"fmt"
	"io"

	tea "charm.land/bubbletea/v2"

	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
	"go.aledante.io/FlowSeer/src/edge/netpen/output"
)

// streamJSON consumes the live record channel, writing each record as a
// JSONL line to stdout as it arrives (R11, KTD10). The meta header is
// written first, then each record immediately, then a closing summary.
// It blocks until the channel closes (run completes or context cancel).
func streamJSON(stdout, stderr io.Writer, ch <-chan findings.Record, meta findings.Meta) {
	w := output.NewJSONWriter(stdout, stderr, meta)

	// Write the meta header line first.
	if err := w.WriteMeta(); err != nil {
		fmt.Fprintf(stderr, "output error: %v\n", err)
		drainRecords(ch)
		return
	}

	count := 0
	sawSummary := false
	summary := findings.Summary{}
	for rec := range ch {
		count++
		switch rec.Kind {
		case findings.KindFinding:
			summary.Findings++
		case findings.KindResisted:
			summary.Resisted++
		case findings.KindSkipped:
			summary.Skipped++
		case findings.KindPending:
			summary.Pending++
		case findings.KindError:
			summary.Errors++
		case findings.KindSummary:
			sawSummary = true
		}
		if err := w.Write(rec); err != nil {
			fmt.Fprintf(stderr, "output error: %v\n", err)
			drainRecords(ch)
			return
		}
	}

	// Single-attack runs emit no summary, so the writer closes them with
	// a synthetic aggregate. Orchestrators emit their own richer summary;
	// returning here preserves that authoritative closing record.
	if sawSummary {
		return
	}
	summary.Attacks = count
	if err := w.WriteSummary(summary); err != nil {
		fmt.Fprintf(stderr, "output error: %v\n", err)
	}
}

// drainRecords consumes the remaining channel in the background so an
// early output failure cannot block the runner's producer to a hang.
func drainRecords(ch <-chan findings.Record) {
	go func() {
		for range ch {
		}
	}()
}

// streamTUI runs the bubbletea v2 TUI program, feeding findings records
// live from the record channel. A goroutine reads the channel and sends
// each record to the program via p.Send; the model's Update handles
// findings.Record messages. When the channel closes, p.Quit stops the
// program so main resumes and runs teardown (U7: teardown owned outside
// bubbletea, no quit hook).
//
// streamTUI is only called when stdout is a tty (ResolveMode selects TUI
// only on a tty). In tests the TUI model is exercised via Model.Update
// directly; streamTUI is never called without a real terminal.
func streamTUI(ch <-chan findings.Record, meta findings.Meta) error {
	m := output.NewModel(true, 80, 24)
	p := tea.NewProgram(m)
	// Goroutine: read the channel live and send records to the program.
	go func() {
		metaRec := findings.NewRecord(findings.KindMeta)
		metaRec.Time = meta.Started
		metaRec.Meta = &meta
		p.Send(metaRec)

		for rec := range ch {
			p.Send(rec)
		}
		p.Quit()
	}()

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("tui: %v", err)
	}
	return nil
}
