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
	"encoding/json"
	"fmt"
	"io"

	tea "charm.land/bubbletea/v2"

	"go.aledante.io/FlowSeer/src/netpen/findings"
	"go.aledante.io/FlowSeer/src/netpen/output"
)

// streamJSON consumes the live record channel, writing each record as a
// JSONL line to stdout as it arrives (R11, KTD10). The meta header is
// written first, then each record immediately, then a closing summary.
// It blocks until the channel closes (run completes or context cancel).
func streamJSON(stdout, stderr io.Writer, ch <-chan findings.Record, meta findings.Meta) {
	// Write the meta header line first.
	metaRec := findings.NewRecord(findings.KindMeta)
	metaRec.Time = meta.Started
	metaRec.Meta = &meta
	if err := writeJSONL(stdout, metaRec); err != nil {
		fmt.Fprintf(stderr, "output error: %v\n", err)
		return
	}

	count := 0
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
		}
		if err := writeJSONL(stdout, rec); err != nil {
			fmt.Fprintf(stderr, "output error: %v\n", err)
			return
		}
	}

	// Write the closing summary.
	summary.Attacks = count
	sumRec := findings.NewRecord(findings.KindSummary)
	sumRec.Summary = &summary
	if err := writeJSONL(stdout, sumRec); err != nil {
		fmt.Fprintf(stderr, "output error: %v\n", err)
	}
}

// writeJSONL marshals one record and writes it as a single JSONL line.
func writeJSONL(stdout io.Writer, rec findings.Record) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("json marshal: %v", err)
	}
	if _, err := stdout.Write(data); err != nil {
		return fmt.Errorf("stdout write failed")
	}
	if _, err := stdout.Write([]byte("\n")); err != nil {
		return fmt.Errorf("stdout write failed")
	}
	return nil
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
