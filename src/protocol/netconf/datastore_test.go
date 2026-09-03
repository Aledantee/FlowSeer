package netconf_test

import (
	"context"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/protocol/netconf"
	"go.aledante.io/FlowSeer/src/protocol/yang"
)

func TestInvalidDatastoreDoesNotSendRPC(t *testing.T) {
	for _, ds := range []netconf.Datastore{"", "canddiate", "startup"} {
		t.Run(string(ds), func(t *testing.T) {
			f := newFake(capCandidate, capValidate, capWritableRunning)
			s := netconf.NewSession(f, netconf.Options{})
			t.Cleanup(func() {
				if err := s.Close(context.Background()); err != nil {
					t.Error(err)
				}
			})
			ctx := context.Background()
			for _, op := range []struct {
				name string
				run  func() error
			}{
				{"get-config", func() error { _, err := s.GetConfig(ctx, ds, yang.Path{}); return err }},
				{"edit-config", func() error { return s.EditConfig(ctx, ds, []byte(`<x/>`)) }},
				{"lock", func() error { return s.Lock(ctx, ds) }},
				{"unlock", func() error { return s.Unlock(ctx, ds) }},
				{"validate", func() error { return s.Validate(ctx, ds) }},
			} {
				t.Run(op.name, func(t *testing.T) {
					err := op.run()
					if code, _ := errs.CodeOf(err); code != netconf.ErrCodeUnsupported {
						t.Errorf("got %v, want unsupported datastore", err)
					}
				})
			}
			if calls := f.callLog(); len(calls) != 0 {
				t.Errorf("got RPCs %v, want none", calls)
			}
		})
	}
}
