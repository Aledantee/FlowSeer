package syslogsource

import (
	"sync"
	"time"
)

// RawPolicy governs whether verbatim datagram payload evidence is retained
// for parse-failing syslog records. It limits retained raw evidence to a
// configured per-minute burst per device and samples thereafter, tracking the
// count of suppressed failures between kept samples. RawPolicy is safe for
// concurrent use.
type RawPolicy struct {
	failuresPerMinute uint32
	sampleEvery       uint32
	clock             func() time.Time

	mu      sync.Mutex // guards devices
	devices map[string]*deviceWindow
}

type deviceWindow struct {
	windowStart         time.Time
	failuresInMinute    uint32
	suppressedSinceLast uint64
}

// NewRawPolicy constructs a raw suppression policy. Zero values take the
// production defaults: 20 failures per device per minute, sampling 1 in 100
// thereafter, on the wall clock.
func NewRawPolicy(failuresPerMinute, sampleEvery uint32, clock func() time.Time) *RawPolicy {
	if failuresPerMinute == 0 {
		failuresPerMinute = 20
	}
	if sampleEvery == 0 {
		sampleEvery = 100
	}
	if clock == nil {
		clock = time.Now
	}
	return &RawPolicy{
		failuresPerMinute: failuresPerMinute,
		sampleEvery:       sampleEvery,
		clock:             clock,
		devices:           make(map[string]*deviceWindow),
	}
}

// Evaluate evaluates whether a parse failure for deviceID retains raw evidence.
// When true, suppressed reports the number of failures suppressed since the
// last kept sample for this device.
func (p *RawPolicy) Evaluate(deviceID string) (keep bool, suppressed uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := p.clock()
	win, ok := p.devices[deviceID]
	if !ok {
		win = &deviceWindow{windowStart: now}
		p.devices[deviceID] = win
	}

	if now.Sub(win.windowStart) >= time.Minute {
		win.windowStart = now
		win.failuresInMinute = 0
	}

	win.failuresInMinute++
	if win.failuresInMinute <= p.failuresPerMinute {
		supp := win.suppressedSinceLast
		win.suppressedSinceLast = 0
		return true, supp
	}

	if (win.failuresInMinute-p.failuresPerMinute)%p.sampleEvery == 0 {
		supp := win.suppressedSinceLast
		win.suppressedSinceLast = 0
		return true, supp
	}

	win.suppressedSinceLast++
	return false, 0
}
