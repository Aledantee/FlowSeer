package identityapi

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/accessstore"
	"go.aledante.io/FlowSeer/src/services/device/internal/tenantstore"
)

func TestConnectErrMarksStoreFailuresRetryable(t *testing.T) {
	tests := []struct {
		name string
		code errs.Code
	}{
		{name: "access store", code: accessstore.ErrCodeStore},
		{name: "access conflict", code: accessstore.ErrCodeConflict},
		{name: "tenant store", code: tenantstore.ErrCodeStore},
		{name: "tenant conflict", code: tenantstore.ErrCodeConflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := connectErr(context.Background(), errs.New().Code(tt.code).Msg("store failure"))
			if got := connect.CodeOf(err); got != connect.CodeUnavailable {
				t.Fatalf("status = %v, want %v", got, connect.CodeUnavailable)
			}
			if !errs.Retryable(err) {
				t.Fatalf("error = %v, want retryable", err)
			}
		})
	}
}
