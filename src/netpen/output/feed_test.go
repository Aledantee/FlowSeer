package output

import (
	"encoding/json"
	"time"

	"go.aledante.io/FlowSeer/src/netpen/findings"
)

// syntheticFeed returns a deterministic set of records covering every record
// kind the machine contract carries. It is the shared input for mode-parity
// tests: the same feed injected through the JSON writer produces JSONL lines,
// and through the TUI model produces feed and progress updates.
func syntheticFeed() []findings.Record {
	t0 := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

	meta := findings.NewRecord(findings.KindMeta)
	meta.Time = t0
	meta.Meta = &findings.Meta{
		Tool:      "netpen",
		Version:   "test",
		AttackLeg: "eth0",
		WatchLeg:  "eth1",
		Started:   t0,
	}

	finding := findings.NewRecord(findings.KindFinding)
	finding.Time = t0
	finding.Attack = "arpsweep"
	finding.Finding = &findings.Finding{
		Module: "arp",
		Detail: json.RawMessage(`{"host":"10.0.0.1","mac":"aa:bb:cc:dd:ee:ff"}`),
	}

	progress := findings.NewRecord(findings.KindProgress)
	progress.Time = t0
	progress.Attack = "arpsweep"
	progress.Progress = &findings.Progress{
		Phase:  "sweep",
		Detail: "scanning 10.0.0.0/24",
	}

	resisted := findings.NewRecord(findings.KindResisted)
	resisted.Time = t0
	resisted.Attack = "stproot"
	resisted.Rollup = &findings.Rollup{
		Verdict: findings.KindResisted,
		Detail:  "root guard blocked the BPDU",
	}

	skipped := findings.NewRecord(findings.KindSkipped)
	skipped.Time = t0
	skipped.Attack = "vtp"
	skipped.Rollup = &findings.Rollup{
		Verdict: findings.KindSkipped,
		Detail:  "no VTP domain detected",
	}

	pending := findings.NewRecord(findings.KindPending)
	pending.Time = t0
	pending.Attack = "dtp"
	pending.Rollup = &findings.Rollup{
		Verdict: findings.KindPending,
		Detail:  "no watch-leg traversal evidence",
	}

	errRec := findings.NewRecord(findings.KindError)
	errRec.Time = t0
	errRec.Attack = "roguedhcp"
	errRec.Error = &findings.ErrorRecord{
		Code:    "netpen/runtime",
		Message: "dhcp socket bind failed",
		Attack:  "roguedhcp",
	}

	refusal := findings.NewRecord(findings.KindRefusal)
	refusal.Time = t0
	refusal.Attack = "vtp"
	refusal.Mode = "wipe"
	refusal.Refusal = &findings.Refusal{
		Reason: "permanent-destructive without acknowledgment",
	}

	summary := findings.NewRecord(findings.KindSummary)
	summary.Time = t0
	summary.Summary = &findings.Summary{
		Attacks:  3,
		Findings: 1,
		Resisted: 1,
		Skipped:  1,
		Pending:  1,
		Errors:   1,
	}

	return []findings.Record{
		meta, finding, progress, resisted, skipped, pending, errRec, refusal, summary,
	}
}

// emptyFeed returns the minimal record set for an empty run: meta header plus
// a zero summary.
func emptyFeed() []findings.Record {
	t0 := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

	meta := findings.NewRecord(findings.KindMeta)
	meta.Time = t0
	meta.Meta = &findings.Meta{
		Tool:      "netpen",
		Version:   "test",
		AttackLeg: "eth0",
		Started:   t0,
	}

	summary := findings.NewRecord(findings.KindSummary)
	summary.Time = t0
	summary.Summary = &findings.Summary{}

	return []findings.Record{meta, summary}
}

// secretCarryingFeed returns a record set where a finding carries a Secret in
// its detail. The secret value must never appear in stdout bytes.
func secretCarryingFeed() []findings.Record {
	t0 := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

	meta := findings.NewRecord(findings.KindMeta)
	meta.Time = t0
	meta.Meta = &findings.Meta{
		Tool:      "netpen",
		Version:   "test",
		AttackLeg: "eth0",
		Started:   t0,
	}

	// A finding whose detail JSON includes a Secret. The Secret's
	// MarshalJSON emits only protocol and length, never the value.
	secret := findings.NewSecret("snmp", []byte("supersecretcommunity"))
	detail, _ := json.Marshal(struct {
		Host   string          `json:"host"`
		Secret findings.Secret `json:"secret"`
	}{
		Host:   "10.0.0.1",
		Secret: secret,
	})

	finding := findings.NewRecord(findings.KindFinding)
	finding.Time = t0
	finding.Attack = "snmp-recon"
	finding.Finding = &findings.Finding{
		Module: "snmp",
		Detail: detail,
	}

	summary := findings.NewRecord(findings.KindSummary)
	summary.Time = t0
	summary.Summary = &findings.Summary{
		Attacks:  1,
		Findings: 1,
	}

	return []findings.Record{meta, finding, summary}
}
