//go:build linux && netsimload_linktest

package packetio

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/modules/capture/rawsocket"
)

func TestRealInterface(t *testing.T) {
	tx := os.Getenv("NETSIMLOAD_LINK_TX_INTERFACE")
	rx := os.Getenv("NETSIMLOAD_LINK_RX_INTERFACE")
	if tx == "" || rx == "" {
		t.Skip("NETSIMLOAD_LINK_TX_INTERFACE and NETSIMLOAD_LINK_RX_INTERFACE are required")
	}
	if tx == rx {
		t.Fatal("transmit and receive interfaces must differ")
	}

	receiver, err := rawsocket.OpenLocalInterface(rx, false, nil)
	if err != nil {
		t.Fatalf("open receive interface: %v", err)
	}
	t.Cleanup(func() { _ = receiver.Close() })
	sender, err := OpenSender(tx)
	if err != nil {
		t.Fatalf("open transmit interface: %v", err)
	}
	t.Cleanup(func() { _ = sender.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	frames := receiver.Receive(ctx)
	wire := []byte{
		0x02, 0, 0, 0, 0, 2, 0x02, 0, 0, 0, 0, 1, 0x88, 0xb5,
		'N', 'E', 'T', 'S', 'I', 'M', 'L', 'O', 'A', 'D', '-', 'L', 'I', 'N', 'K',
		0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
		16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30,
	}
	if err := sender.Send(ctx, wire); err != nil {
		t.Fatalf("send frame: %v", err)
	}
	for {
		select {
		case frame, ok := <-frames:
			if !ok {
				t.Fatal("capture closed before receiving the sent frame")
			}
			if frame.Err != nil {
				t.Fatalf("capture frame: %v", frame.Err)
			}
			if bytes.Equal(frame.Data, wire) {
				return
			}
		case <-ctx.Done():
			t.Fatal("sent frame was not captured on the peer interface")
		}
	}
}
