package rawsocket

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/net/bpf"
)

type localReadResult struct {
	data       []byte
	wireLength int
	capturedAt time.Time
	retryable  bool
	err        error
}

type fakeFrameReader struct {
	results     []localReadResult
	closeCalls  int
	closeErr    error
	pollStarted chan struct{}
}

func (r *fakeFrameReader) readFrame(buf []byte) ([]byte, int, time.Time, bool, error) {
	if len(r.results) == 0 {
		if r.pollStarted != nil {
			close(r.pollStarted)
			r.pollStarted = nil
		}
		// Model the reader's blocking poll so cancellation is tested mid-read.
		time.Sleep(pollTimeout)
		return nil, 0, time.Time{}, true, errors.New("poll timeout")
	}
	result := r.results[0]
	r.results = r.results[1:]
	copy(buf, result.data)
	return bytes.Clone(buf[:len(result.data)]), result.wireLength, result.capturedAt, result.retryable, result.err
}

func (r *fakeFrameReader) stats() (uint64, uint64, error) {
	return 100, 3, nil
}

func (r *fakeFrameReader) close() error {
	r.closeCalls++
	return r.closeErr
}

func TestLocalSource_DeliversFrame(t *testing.T) {
	capturedAt := time.Date(2026, time.October, 12, 10, 0, 0, 123, time.UTC)
	r := &fakeFrameReader{results: []localReadResult{{
		data: []byte{1, 2, 3, 4}, wireLength: 9000, capturedAt: capturedAt,
	}}}
	s := newLocalSource(r)
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	select {
	case f, ok := <-s.Receive(ctx):
		if !ok || f.Err != nil || !bytes.Equal(f.Data, []byte{1, 2, 3, 4}) {
			t.Fatalf("frame = %+v, open = %t, want 01020304", f, ok)
		}
		if f.OriginalLength != 9000 || !f.CapturedAt.Equal(capturedAt) {
			t.Errorf("metadata = (%d, %v), want (9000, %v)", f.OriginalLength, f.CapturedAt, capturedAt)
		}
	case <-ctx.Done():
		t.Fatal("frame delivery timed out")
	}
	received, dropped, err := s.Stats()
	if err != nil || received != 100 || dropped != 3 {
		t.Errorf("Stats = (%d, %d, %v), want (100, 3, nil)", received, dropped, err)
	}
}

func TestLocalSource_RetryableErrorIsRetried(t *testing.T) {
	r := &fakeFrameReader{results: []localReadResult{
		{retryable: true, err: errors.New("interrupted")},
		{data: []byte{7}, wireLength: 1},
	}}
	s := newLocalSource(r)
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	select {
	case f, ok := <-s.Receive(ctx):
		if !ok || f.Err != nil || !bytes.Equal(f.Data, []byte{7}) {
			t.Fatalf("frame = %+v, open = %t, want 07 after retry", f, ok)
		}
	case <-ctx.Done():
		t.Fatal("retry timed out")
	}
}

func TestLocalSource_ReadErrorIsTerminal(t *testing.T) {
	wantErr := errors.New("reader failed")
	r := &fakeFrameReader{results: []localReadResult{
		{err: wantErr},
		{data: []byte{9}, wireLength: 1},
	}}
	s := newLocalSource(r)
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	frames := s.Receive(ctx)
	select {
	case f, ok := <-frames:
		if !ok || !errors.Is(f.Err, wantErr) {
			t.Fatalf("frame error = %v, open = %t, want %v", f.Err, ok, wantErr)
		}
	case <-ctx.Done():
		t.Fatal("terminal error timed out")
	}
	select {
	case _, ok := <-frames:
		if ok {
			t.Fatal("received a frame after the terminal error")
		}
	case <-ctx.Done():
		t.Fatal("channel did not close after the terminal error")
	}
}

func TestLocalSource_CancellationUnblocksWithinOnePoll(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := make(chan struct{})
		r := &fakeFrameReader{pollStarted: started}
		s := newLocalSource(r)
		defer func() { _ = s.Close() }()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		frames := s.Receive(ctx)
		<-started
		synctest.Wait()
		cancelledAt := time.Now()
		cancel()
		select {
		case f, ok := <-frames:
			if !ok || !errors.Is(f.Err, context.Canceled) {
				t.Fatalf("frame error = %v, open = %t, want context.Canceled", f.Err, ok)
			}
			if elapsed := time.Since(cancelledAt); elapsed > pollTimeout {
				t.Errorf("cancellation took %v, want at most %v", elapsed, pollTimeout)
			}
		case <-time.After(2 * pollTimeout):
			t.Fatal("Receive did not unblock within one poll of cancellation")
		}
		if _, ok := <-frames; ok {
			t.Fatal("channel remained open after cancellation")
		}
	})
}

func TestLocalSource_FilterRejects(t *testing.T) {
	vm, err := bpf.NewVM([]bpf.Instruction{
		bpf.LoadAbsolute{Off: 0, Size: 1},
		bpf.RetA{},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeFrameReader{results: []localReadResult{
		{data: []byte{0}, wireLength: 1},
		{data: []byte{1}, wireLength: 1},
	}}
	s := newLocalSource(r)
	s.vm = vm
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	select {
	case f, ok := <-s.Receive(ctx):
		if !ok || f.Err != nil || !bytes.Equal(f.Data, []byte{1}) {
			t.Fatalf("frame = %+v, open = %t, want accepted frame 01", f, ok)
		}
	case <-ctx.Done():
		t.Fatal("accepted frame timed out")
	}
}

func TestLocalSource_CloseIsIdempotent(t *testing.T) {
	wantErr := errors.New("close failed")
	r := &fakeFrameReader{closeErr: wantErr}
	s := newLocalSource(r)
	if err := s.Close(); !errors.Is(err, wantErr) {
		t.Errorf("first Close = %v, want %v", err, wantErr)
	}
	if err := s.Close(); err != nil {
		t.Errorf("second Close = %v, want nil", err)
	}
	if r.closeCalls != 1 {
		t.Errorf("reader close calls = %d, want 1", r.closeCalls)
	}
	select {
	case <-s.done:
	default:
		t.Error("Close did not signal the receiver")
	}
	if _, _, err := s.Stats(); err == nil {
		t.Error("Stats after Close succeeded")
	}
}
