package netconf_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/netconf"
	"go.aledante.io/FlowSeer/src/common/yang"
)

type contextTransport struct {
	*fakeTransport
	exec func(context.Context, any, any) error
}

func (f *contextTransport) Exec(ctx context.Context, op, reply any) error {
	return f.exec(ctx, op, reply)
}

func TestRPCTimeoutBoundsCallerDeadline(t *testing.T) {
	for _, tc := range []struct {
		name    string
		timeout time.Duration
		want    time.Duration
	}{
		{name: "no caller deadline", want: time.Hour},
		{name: "later caller deadline", timeout: 2 * time.Hour, want: time.Hour},
		{name: "earlier caller deadline", timeout: 30 * time.Minute, want: 30 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tc.timeout)
				t.Cleanup(cancel)
			}
			f := &contextTransport{fakeTransport: newFake()}
			f.exec = func(ctx context.Context, _, _ any) error {
				deadline, ok := ctx.Deadline()
				if !ok {
					t.Fatal("RPC context has no deadline")
				}
				if remaining := time.Until(deadline); remaining > tc.want || remaining < tc.want-time.Minute {
					t.Errorf("RPC time remaining = %v, want approximately %v", remaining, tc.want)
				}
				return nil
			}
			s := netconf.NewSession(f, netconf.Options{RPCTimeout: time.Hour})
			t.Cleanup(func() {
				if err := s.Close(context.Background()); err != nil {
					t.Error(err)
				}
			})
			if _, err := s.Get(ctx, yang.Path{}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestApplyCancellationStillCleansUp(t *testing.T) {
	for _, tc := range []struct {
		name string
		cap  string
		want []string
	}{
		{name: "candidate", cap: capCandidate, want: []string{"lock", "discard-changes", "unlock"}},
		{name: "running", cap: capWritableRunning, want: []string{"lock", "unlock"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			f := &contextTransport{fakeTransport: newFake(tc.cap)}
			f.exec = func(ctx context.Context, op, reply any) error {
				if opName(op) == "edit-config" {
					cancel()
					return ctx.Err()
				}
				if err := ctx.Err(); err != nil {
					return err
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Error("cleanup RPC context has no deadline")
				}
				return f.fakeTransport.Exec(ctx, op, reply)
			}
			s := netconf.NewSession(f, netconf.Options{})
			t.Cleanup(func() {
				if err := s.Close(context.Background()); err != nil {
					t.Error(err)
				}
			})
			if err := s.Apply(ctx, []byte("<hostname>edge</hostname>")); !errors.Is(err, context.Canceled) {
				t.Errorf("Apply error = %v, want context.Canceled", err)
			}
			if got := f.callLog(); !slices.Equal(got, tc.want) {
				t.Errorf("completed RPCs = %v, want %v", got, tc.want)
			}
			if err := s.Err(); err != nil {
				t.Errorf("terminal session error = %v, want nil", err)
			}
		})
	}
}
