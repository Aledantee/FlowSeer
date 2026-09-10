package access_test

import (
	"context"
	"testing"
	"time"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	"go.aledante.io/FlowSeer/src/modules/localnet/access"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
)

// A mutation's observation is read under the policy version its own intent
// was admitted under, not the one the device was onboarded with.
//
// The version is what this asserts on, and it is the whole point of the test.
// The device session carries a handle too, and passing that one makes the
// symptom this test was written for disappear: the acquisition succeeds, the
// mutation verifies, and every other test in this package passes. The two
// handles share a key here — a deployment reuses a policy key and bumps its
// version — so nothing but the version separates the right source from the
// plausible one.
//
// Central decides per operation which policy admitted an intent. An
// observation labeled with the session's version would claim the mutation
// was seen under a policy that had moved on, and would do it silently.
func TestAMutationIsObservedUnderItsOwnPolicyVersion(t *testing.T) {
	source := &recordingCredentials{}
	l := laneWithCredentials(t, source)

	const onboarded, admitted = uint64(1), uint64(4)

	err := l.AddDevice(context.Background(), "dev-1", access.DeviceSession{
		DelayedEffect: interfaces.DelayedEffect{Horizon: time.Minute},
		OpenSNMP:      probeFactory(),
		AccessPolicy:  policyHandle("icx7150", onboarded),
		BindingID:     "binding-1",
		ReadOverride: func(context.Context, string) (*accessv1.InterfaceObservation, error) {
			return completeObservation("uplink to core"), nil
		},
		SubmitOverride: func(context.Context, *accessv1.InterfaceDescriptionChange) error { return nil },
	})
	if err != nil {
		t.Fatalf("AddDevice() error: %v", err)
	}

	if got := source.handleVersions(); len(got) != 1 || got[0] != onboarded {
		t.Fatalf("after onboarding, handle versions = %v, want one acquisition at version %d", got, onboarded)
	}

	req := mutationRequest(1)
	req.GetMutation().SetAccessPolicy(policyHandle("icx7150", admitted))
	if _, err := runMutation(t, l, req); err != nil {
		t.Fatalf("Submit() error: %v", err)
	}

	// Two acquisition sites exist: the identity probe, which acquires under
	// the device session's handle because that is what DeviceSession's
	// AccessPolicy is for, and the observation, which must acquire under the
	// operation's. A mutation interleaves them — a probe, the baseline read,
	// the post-write observation, the epoch re-probe — so a run produces
	// several of each.
	//
	// Asserted as "every acquisition is one of the two, and at least one is
	// the intent's" rather than as a count. How many observations a mutation
	// takes is the lane's business and changes with the route it picks; which
	// handle an observation acquires under is this test's subject, and the
	// failure it exists for is zero.
	versions := source.handleVersions()
	intents := 0
	for _, got := range versions {
		switch got {
		case admitted:
			intents++
		case onboarded:
		default:
			t.Errorf("an acquisition used policy version %d, which is neither the intent's %d nor the session's %d",
				got, admitted, onboarded)
		}
	}
	if intents == 0 {
		t.Errorf("no acquisition used the intent's policy version %d (versions: %v); "+
			"the observation read under the session's handle instead of the one the mutation was admitted under",
			admitted, versions)
	}
}
