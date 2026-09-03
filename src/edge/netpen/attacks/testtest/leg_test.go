package testtest_test

import (
	"context"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/testtest"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
)

func TestReceiveAfterQueueDrains(t *testing.T) {
	leg := testtest.New()
	t.Cleanup(func() { _ = leg.Close() })
	frames := leg.Receive(t.Context())
	time.Sleep(20 * time.Millisecond)
	leg.PushRX([]byte{42})
	select {
	case frame := <-frames:
		if len(frame.Data) != 1 || frame.Data[0] != 42 {
			t.Fatalf("got frame %v, want [42]", frame.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("receiver did not wake for a newly queued frame")
	}
}

func TestPushRXFrameOwnsData(t *testing.T) {
	leg := testtest.New()
	t.Cleanup(func() { _ = leg.Close() })
	data := []byte{42}
	leg.PushRXFrame(link.Frame{Data: data})
	data[0] = 99
	frame := <-leg.Receive(t.Context())
	if len(frame.Data) != 1 || frame.Data[0] != 42 {
		t.Errorf("got frame %v, want [42]", frame.Data)
	}
}

func TestSendLifecycle(t *testing.T) {
	for _, tt := range []struct {
		name   string
		cancel bool
		close  bool
	}{
		{name: "open"},
		{name: "canceled", cancel: true},
		{name: "closed", close: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			leg := testtest.New()
			t.Cleanup(func() { _ = leg.Close() })
			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)
			if tt.cancel {
				cancel()
			}
			if tt.close {
				if err := leg.Close(); err != nil {
					t.Fatal(err)
				}
			}
			err := leg.Send(ctx, []byte{42})
			switch {
			case tt.cancel:
				if err != context.Canceled {
					t.Errorf("got error %v, want context.Canceled", err)
				}
			case tt.close:
				if code, ok := errs.CodeOf(err); !ok || code != link.ErrCodeLegOpen {
					t.Errorf("got error %v, want leg-open code", err)
				}
			case err != nil:
				t.Error(err)
			}
			wantSends := 1
			if tt.cancel || tt.close {
				wantSends = 0
			}
			if got := leg.SendCount(); got != wantSends {
				t.Errorf("got %d sends, want %d", got, wantSends)
			}
		})
	}
}
