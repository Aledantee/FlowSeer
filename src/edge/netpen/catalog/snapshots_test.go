package catalog

import (
	"reflect"
	"testing"
)

// TestRegistrationMetadataIsDetached prevents callers from changing frozen gates
// through the slices they register or receive from Behaviors.
func TestRegistrationMetadataIsDetached(t *testing.T) {
	mu.Lock()
	savedBehaviors, savedRegistered := behaviors, registered
	behaviors, registered = nil, false
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		behaviors, registered = savedBehaviors, savedRegistered
		mu.Unlock()
	})

	b := Behavior{
		Name: "snapshot", Protocols: []string{"arp"}, Preconditions: []string{"watch-leg"},
		Legs: WatchRequired, Class: NonDestructive, Help: "snapshot fixture",
		Modes: []Mode{{Flag: "persist", Class: PermanentDestructive, Help: "persistent fixture"}},
	}
	if err := Register(b); err != nil {
		t.Fatalf("Register(%q): %v", b.Name, err)
	}
	want := Entries()

	b.Protocols[0] = "changed"
	b.Preconditions[0] = "changed"
	b.Modes[0].Class = NonDestructive
	if got := Entries(); !reflect.DeepEqual(got, want) {
		t.Errorf("entries after input mutation = %+v, want %+v", got, want)
	}

	snapshot := Behaviors()[0]
	snapshot.Protocols[0] = "changed again"
	snapshot.Preconditions[0] = "changed again"
	snapshot.Modes[0].Flag = "changed"
	if got := Entries(); !reflect.DeepEqual(got, want) {
		t.Errorf("entries after snapshot mutation = %+v, want %+v", got, want)
	}
}
