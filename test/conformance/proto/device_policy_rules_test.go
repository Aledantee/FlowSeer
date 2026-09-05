package conformance

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	policyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/policy/v1"
)

func accessPolicyHandle(key string, version uint64) *policyv1.AccessPolicyHandle {
	return policyv1.AccessPolicyHandle_builder{
		Key:     proto.String(key),
		Version: proto.Uint64(version),
	}.Build()
}

func TestAccessPolicyHandleRules(t *testing.T) {
	tests := []validationCase{
		{
			name:      "key and version are valid",
			message:   accessPolicyHandle("icx7150-lab", 3),
			wantValid: true,
		},
		{
			name:      "dots and underscores are valid",
			message:   accessPolicyHandle("site_1.default", 1),
			wantValid: true,
		},
		{
			name:      "empty key is rejected",
			message:   accessPolicyHandle("", 1),
			wantValid: false,
		},
		{
			name:      "uppercase key is rejected",
			message:   accessPolicyHandle("Lab", 1),
			wantValid: false,
		},
		{
			name:      "traversal key is rejected",
			message:   accessPolicyHandle("../root", 1),
			wantValid: false,
		},
		{
			name:      "separator in key is rejected",
			message:   accessPolicyHandle("site/lab", 1),
			wantValid: false,
		},
		{
			name:      "key longer than 128 is rejected",
			message:   accessPolicyHandle(strings.Repeat("a", 129), 1),
			wantValid: false,
		},
		{
			name:      "version zero is rejected",
			message:   accessPolicyHandle("icx7150-lab", 0),
			wantValid: false,
		},
		{
			name:      "absent version is rejected",
			message:   policyv1.AccessPolicyHandle_builder{Key: proto.String("icx7150-lab")}.Build(),
			wantValid: false,
		},
		{
			name:      "absent key is rejected",
			message:   policyv1.AccessPolicyHandle_builder{Version: proto.Uint64(1)}.Build(),
			wantValid: false,
		},
	}

	runValidationCases(t, tests)
}
