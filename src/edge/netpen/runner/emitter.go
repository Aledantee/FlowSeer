package runner

import (
	"encoding/json"

	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
)

// emitter is the [check.Emitter] the runner hands a behavior. It wraps the
// run's [Stream] so a behavior emits typed [findings.Record] values stamped
// with the current attack and mode without holding the stream itself.
//
// An emitter is safe for use from a single goroutine (the behavior's run).
// A behavior that spawns concurrent workers must serialize its calls.
type emitter struct {
	stream *Stream
}

func (e *emitter) Emit(r findings.Record) bool {
	return e.stream.send(r)
}

func (e *emitter) Finding(module string, detail json.RawMessage) bool {
	r := findings.NewRecord(findings.KindFinding)
	r.Attack = e.stream.attackName()
	r.Mode = e.stream.attackMode()
	r.Finding = &findings.Finding{Module: module, Detail: detail}
	return e.Emit(r)
}

func (e *emitter) Progress(phase, detail string) bool {
	r := findings.NewRecord(findings.KindProgress)
	r.Attack = e.stream.attackName()
	r.Mode = e.stream.attackMode()
	r.Progress = &findings.Progress{Phase: phase, Detail: detail}
	return e.Emit(r)
}
