package service

import (
	"sync"
	"sync/atomic"
)

// admissionState serializes immutable admission revisions published by
// concurrent supervisors. Readers linearize against one revision without
// sharing mutable state with supervisor goroutines.
type admissionState struct {
	// mu serializes writers that derive and publish a new immutable revision.
	mu      sync.Mutex
	current atomic.Pointer[admissionRevision]
}

func newAdmissionState(initial *admissionRevision) *admissionState {
	state := &admissionState{}
	state.current.Store(initial)
	return state
}

func (s *admissionState) load() *admissionRevision {
	return s.current.Load()
}

func (s *admissionState) setPhase(phase admissionPhase) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current.Store(s.current.Load().withPhase(phase))
}

// setModuleState publishes state for module and all its descendants.
func (s *admissionState) setModuleState(module plannedModule, state moduleState) {
	s.mu.Lock()
	defer s.mu.Unlock()

	revision := s.current.Load()
	var update func(plannedModule)
	update = func(item plannedModule) {
		revision = revision.withModuleState(item.path, state)
		for _, child := range item.children {
			update(child)
		}
	}
	update(module)
	s.current.Store(revision)
}

// setModuleSnapshot atomically publishes running or disabled states for every
// module in the snapshot. A disabled parent forces all descendants disabled.
func (s *admissionState) setModuleSnapshot(modules []plannedModule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	revision := s.current.Load()
	for _, module := range modules {
		var update func(plannedModule, bool)
		update = func(item plannedModule, parentEnabled bool) {
			enabled := parentEnabled && item.enabled
			state := moduleDisabled
			if enabled {
				state = moduleRunning
			}
			revision = revision.withModuleState(item.path, state)
			for _, child := range item.children {
				update(child, enabled)
			}
		}
		update(module, true)
	}
	s.current.Store(revision)
}
