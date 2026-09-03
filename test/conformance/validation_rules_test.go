package conformance

import (
	"testing"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"
)

type validationCase struct {
	name      string
	message   proto.Message
	wantValid bool
}

func runValidationCases(t *testing.T, cases []validationCase) {
	t.Helper()

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			err := protovalidate.Validate(tt.message)
			gotValid := err == nil
			if gotValid != tt.wantValid {
				t.Errorf("got valid=%t, want %t: %v", gotValid, tt.wantValid, err)
			}
		})
	}
}
