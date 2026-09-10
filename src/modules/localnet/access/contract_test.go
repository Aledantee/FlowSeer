package access_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/modules/localnet/access"
)

func TestHostCanNameConfigurationContracts(t *testing.T) {
	policy := access.EvidencePolicy{
		access.EvidenceKindInterfaceRead:              time.Minute,
		access.EvidenceKindInterfaceDescriptionChange: 2 * time.Minute,
	}
	cfg := access.Config{EvidencePolicy: policy}
	if got := cfg.EvidencePolicy[access.EvidenceKindInterfaceDescriptionChange]; got != 2*time.Minute {
		t.Errorf("description-change evidence lifetime = %v, want %v", got, 2*time.Minute)
	}

	opts := access.SubmitOptions{Priority: access.PriorityHigh}
	if opts.Priority != access.PriorityHigh {
		t.Errorf("priority = %v, want %v", opts.Priority, access.PriorityHigh)
	}

	if got := access.ErrCodeOverload.String(); got != "lane/overload" {
		t.Errorf("overload code = %q, want %q", got, "lane/overload")
	}
}
