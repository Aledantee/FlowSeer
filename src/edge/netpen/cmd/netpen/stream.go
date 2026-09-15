package main

import (
	"context"
	"fmt"
	"io"

	tea "charm.land/bubbletea/v2"

	"go.aledante.io/FlowSeer/src/common/spawn"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
	"go.aledante.io/FlowSeer/src/edge/netpen/output"
)

// streamJSON returns the first output error. The caller must cancel the run
// and drain the producer so cleanup can finish after an output failure.
func streamJSON(stdout, stderr io.Writer, ch <-chan findings.Record, meta findings.Meta) error {
	w := output.NewJSONWriter(stdout, stderr, meta)

	if err := w.WriteMeta(); err != nil {
		return err
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
			return err
		}
	}

	// Single-attack runs emit no summary, so the writer closes them with
	// a synthetic aggregate. Orchestrators emit their own richer summary;
	// returning here preserves that authoritative closing record.
	if sawSummary {
		return nil
	}
	summary.Attacks = count
	return w.WriteSummary(summary)
}

// streamTUI drains the producer even when the user quits or the terminal fails.
// On terminal failure cancel stops the run before this function waits for
// cleanup records. The caller reports the returned terminal error.
func streamTUI(ctx context.Context, ch <-chan findings.Record, meta findings.Meta, cancel context.CancelFunc) error {
	m := output.NewModel(true, 80, 24)
	p := tea.NewProgram(m)
	done := make(chan struct{})
	// close(done) is fn's own deferred call, so it still runs on a panic
	// unwind: the <-done wait below never hangs on a feed that panics.
	spawn.Go(ctx, "netpen stream TUI feed", func() {
		defer close(done)
		metaRec := findings.NewRecord(findings.KindMeta)
		metaRec.Time = meta.Started
		metaRec.Meta = &meta
		p.Send(metaRec)

		for rec := range ch {
			p.Send(rec)
		}
		p.Quit()
	})

	_, err := p.Run()
	if err != nil {
		cancel()
	}
	<-done
	if err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	return nil
}
