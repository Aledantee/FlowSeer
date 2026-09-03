package full

import (
	"context"
	"slices"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
)

func TestDefaultScanWaitsForWatchEmitter(t *testing.T) {
	frame := readPcap(t, fixturePath(t, "arp_reply.pcap"))[0]
	synctest.Test(t, func(t *testing.T) {
		leg := newScriptableLeg()
		leg.PushRX(frame)
		t.Cleanup(func() { _ = leg.Close() })

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		emitting := make(chan struct{})
		release := make(chan struct{})
		done := make(chan struct{})
		go func() {
			_ = defaultScanRun(ctx, ScanConfig{
				WatchLeg: leg,
				Time:     time.Hour,
				NoProbe:  true,
			}, func(findings.Record) {
				close(emitting)
				<-release
			})
			close(done)
		}()

		<-emitting
		cancel()
		synctest.Wait()
		select {
		case <-done:
			t.Error("scan returned while the watch emitter was still active")
		default:
		}

		close(release)
		<-done
	})
}

func TestParseCandidateVLANBounds(t *testing.T) {
	cases := []struct {
		name string
		spec string
		want []int
	}{
		{name: "valid range", spec: "1, 10-12,4094", want: []int{1, 10, 11, 12, 4094}},
		{name: "overflow", spec: "18446744073709551617"},
		{name: "reversed range", spec: "20-10"},
		{name: "upper bound", spec: "4093-4100", want: []int{4093, 4094}},
		{name: "wide range", spec: "4093-" + strconv.Itoa(int(^uint(0)>>1)), want: []int{4093, 4094}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseCandidateVLANs(tc.spec); !slices.Equal(got, tc.want) {
				t.Errorf("parseCandidateVLANs(%q) = %v, want %v", tc.spec, got, tc.want)
			}
		})
	}
}
