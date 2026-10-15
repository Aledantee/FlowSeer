package bpfdev

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// kernelTicks reports whether the kernel stores a nonzero bd_rtout for d at
// hz: usec*hz >= 1000000 after BIOCSRTIMEOUT's timeval conversion.
func kernelTicks(d time.Duration, hz uint32) bool {
	tv := unix.NsecToTimeval(d.Nanoseconds())
	return (int64(tv.Sec)*1_000_000+int64(tv.Usec))*int64(hz) >= 1_000_000
}

func TestMinReadTimeoutReachesOneTick(t *testing.T) {
	for _, hz := range []uint32{1001, 11111} {
		t.Run(fmt.Sprintf("hz%d", hz), func(t *testing.T) {
			if got := minReadTimeout(hz); !kernelTicks(got, hz) {
				t.Errorf("minReadTimeout(%d) = %v, kernel stores a zero bd_rtout", hz, got)
			}
		})
	}
	for hz := uint32(8); hz <= 137438; hz++ {
		if got := minReadTimeout(hz); !kernelTicks(got, hz) {
			t.Fatalf("minReadTimeout(%d) = %v, kernel stores a zero bd_rtout", hz, got)
		}
	}
}

func TestResolveRefusesReadTimeoutOverOneHour(t *testing.T) {
	for _, d := range []time.Duration{time.Hour + 1, time.Duration(1<<63 - 1)} {
		_, _, err := Config{ReadTimeout: d}.resolve()
		if !errors.Is(err, errs.New().Code(ErrCodeOpen).Msg("")) {
			t.Errorf("resolve(%v) error = %v, want code %q", d, err, ErrCodeOpen)
		}
	}
	if _, _, err := (Config{ReadTimeout: time.Hour}).resolve(); err != nil {
		t.Errorf("resolve(1h) error = %v, want nil", err)
	}
}

func TestResolveRefusesNegativeReadTimeout(t *testing.T) {
	_, _, err := Config{ReadTimeout: -time.Second}.resolve()
	if !errors.Is(err, errs.New().Code(ErrCodeOpen).Msg("")) {
		t.Errorf("resolve error = %v, want code %q", err, ErrCodeOpen)
	}
}
