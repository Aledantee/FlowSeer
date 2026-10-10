package seglog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{Dir: t.TempDir(), SegmentBytes: 64, MaxRecordBytes: 36, MaxBytes: 1024, MaxAge: time.Hour, RollAge: 10 * time.Minute, Linger: 200 * time.Millisecond}
}

func openTestLog(t *testing.T, cfg Config) *Log {
	t.Helper()
	l, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := l.Close(); err != nil {
			requireCode(t, err, ErrCodeClosed)
		}
	})
	return l
}

func requireCode(t *testing.T, err error, want errs.Code) {
	t.Helper()
	if got, ok := errs.CodeOf(err); !ok || got != want {
		t.Fatalf("got %v, want code %v", err, want)
	}
}

func appendTest(t *testing.T, l *Log, record string) {
	t.Helper()
	if err := l.Append([]byte(record)); err != nil {
		t.Fatal(err)
	}
}

func nextTest(t *testing.T, l *Log) Segment {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s, err := l.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func statsTest(t *testing.T, l *Log) Stats {
	t.Helper()
	s, err := l.Stats()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLogOrderAcrossReopen(t *testing.T) {
	cfg := testConfig(t)
	l := openTestLog(t, cfg)
	want := []string{"c", "a", "b", "d", "e", "f", "g", "h", "i", "j", "k"}
	for _, r := range want {
		appendTest(t, l, r)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	l = openTestLog(t, cfg)
	var got []string
	var last uint64
	for statsTest(t, l).Records > 0 {
		s := nextTest(t, l)
		if len(got) > 0 && s.Sequence <= last {
			t.Fatalf("got sequence %d after %d", s.Sequence, last)
		}
		last = s.Sequence
		for _, r := range s.Records {
			got = append(got, string(r))
		}
		if err := l.Ack(s.Sequence); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSequenceAfterHighestRemoved(t *testing.T) {
	for _, discard := range []bool{false, true} {
		t.Run(fmt.Sprint(discard), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cfg := testConfig(t)
				l := openTestLog(t, cfg)
				appendTest(t, l, "older")
				older := nextTest(t, l)
				appendTest(t, l, "highest")
				if err := l.Close(); err != nil {
					t.Fatal(err)
				}
				l = openTestLog(t, cfg)
				if discard {
					time.Sleep(2 * cfg.MaxAge)
					appendTest(t, l, "retained")
				} else if err := l.Ack(older.Sequence + 1); err != nil {
					t.Fatal(err)
				}
				if err := l.Close(); err != nil {
					t.Fatal(err)
				}
				l = openTestLog(t, cfg)
				appendTest(t, l, "new")
				if err := l.Close(); err != nil {
					t.Fatal(err)
				}
				l = openTestLog(t, cfg)
				var got []string
				for statsTest(t, l).Records > 0 {
					s := nextTest(t, l)
					for _, r := range s.Records {
						got = append(got, string(r))
					}
					if err := l.Ack(s.Sequence); err != nil {
						t.Fatal(err)
					}
				}
				want := []string{"older", "new"}
				if discard {
					want = []string{"retained", "new"}
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("got %v, want %v", got, want)
				}
			})
		})
	}
}

func writeFixture(t *testing.T, dir, name string, records ...string) []byte {
	t.Helper()
	b := appendSegmentHeader(nil, time.Now())
	for _, r := range records {
		var err error
		b, err = appendRecord(b, []byte(r))
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRecoveryTornOpen(t *testing.T) {
	cfg := testConfig(t)
	name := "0000000000000003.open"
	b := writeFixture(t, cfg.Dir, name, "a", "b", "c")
	if err := os.WriteFile(filepath.Join(cfg.Dir, name), b[:len(b)-1], 0o600); err != nil {
		t.Fatal(err)
	}
	l := openTestLog(t, cfg)
	s := nextTest(t, l)
	if s.Sequence != 3 || !reflect.DeepEqual(s.Records, [][]byte{[]byte("a"), []byte("b")}) {
		t.Fatalf("got %+v, want sequence 3 records a,b", s)
	}
	st := statsTest(t, l)
	if st.TruncatedBytes != 8 || st.Bytes != 38 || st.Records != 2 || st.Segments != 1 {
		t.Fatalf("got %+v, want 8 truncated, 38 bytes, 2 records, 1 segment", st)
	}
	info, err := os.Stat(filepath.Join(cfg.Dir, "0000000000000003.seg"))
	if err != nil || info.Size() != 38 {
		t.Fatalf("got %v %v, want sealed size 38", info, err)
	}
}

func TestRecoveryDamagedSealed(t *testing.T) {
	cfg := testConfig(t)
	name := "0000000000000001.seg"
	b := writeFixture(t, cfg.Dir, name, "a", "b", "c")
	b[37] ^= 1
	if err := os.WriteFile(filepath.Join(cfg.Dir, name), b, 0o600); err != nil {
		t.Fatal(err)
	}
	l := openTestLog(t, cfg)
	if got := nextTest(t, l).Records; !reflect.DeepEqual(got, [][]byte{[]byte("a")}) {
		t.Fatalf("got %q, want [a]", got)
	}
	st := statsTest(t, l)
	if st.CorruptSegments != 1 || st.CorruptBytes != 18 || st.Bytes != 47 {
		t.Fatalf("got %+v, want 1 corrupt segment, 18 corrupt bytes, 47 disk bytes", st)
	}
}

func TestRecoveryBadHeaderAndForeign(t *testing.T) {
	cfg := testConfig(t)
	if err := os.WriteFile(filepath.Join(cfg.Dir, "0000000000000009.seg"), []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Dir, "hub.creds"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	l := openTestLog(t, cfg)
	st := statsTest(t, l)
	if st.CorruptSegments != 1 || st.CorruptBytes != 3 || st.Segments != 0 {
		t.Fatalf("got %+v, want bad header counted and removed", st)
	}
	if _, err := os.Stat(filepath.Join(cfg.Dir, "0000000000000009.seg")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("got %v, want removed file", err)
	}
	if got, err := os.ReadFile(filepath.Join(cfg.Dir, "hub.creds")); err != nil || string(got) != "keep" {
		t.Fatalf("got %q %v, want keep", got, err)
	}
}

func TestByteBounds(t *testing.T) {
	cfg := testConfig(t)
	cfg.SegmentBytes, cfg.MaxRecordBytes, cfg.MaxBytes = 64<<10, 4<<10, 128<<10
	l := openTestLog(t, cfg)
	for i := range 256 {
		b := bytes.Repeat([]byte{byte(i)}, 1024)
		if err := l.Append(b); err != nil {
			t.Fatal(err)
		}
	}
	st := statsTest(t, l)
	if st.Bytes > cfg.MaxBytes || st.DiscardedRecords == 0 || st.DiscardedBytes == 0 {
		t.Fatalf("got %+v, want bounded disk with discards", st)
	}
	var disk int64
	entries, err := os.ReadDir(cfg.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		disk += info.Size()
	}
	if disk != st.Bytes || disk > cfg.MaxBytes {
		t.Fatalf("got disk %d stats %d, want at most %d", disk, st.Bytes, cfg.MaxBytes)
	}
	if got := nextTest(t, l).Records[0][0]; got != 189 {
		t.Fatalf("got first record %d, want 189", got)
	}
}

func TestAgeBoundsAtAppendAndNext(t *testing.T) {
	for _, priorNext := range []bool{false, true} {
		for _, atAppend := range []bool{false, true} {
			t.Run(fmt.Sprintf("prior=%t/append=%t", priorNext, atAppend), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					cfg := testConfig(t)
					l := openTestLog(t, cfg)
					appendTest(t, l, "old")
					if priorNext {
						if got := nextTest(t, l).Records; string(got[0]) != "old" {
							t.Fatal(got)
						}
					}
					time.Sleep(55 * time.Minute)
					appendTest(t, l, "young")
					time.Sleep(6 * time.Minute)
					if atAppend {
						appendTest(t, l, "new")
						if st := statsTest(t, l); st.DiscardedRecords != 1 {
							t.Fatalf("got %+v before Next, want one discard on Append", st)
						}
					}
					s := nextTest(t, l)
					if string(s.Records[0]) != "young" {
						t.Fatalf("got %q, want young", s.Records)
					}
					st := statsTest(t, l)
					if st.DiscardedRecords != 1 || st.DiscardedBytes != 31 {
						t.Fatalf("got %+v, want 1 discarded record and 31 bytes", st)
					}
				})
			})
		}
	}
}

func TestNextLingerAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := testConfig(t)
		l := openTestLog(t, cfg)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		start := time.Now()
		if _, err := l.Next(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("got %v, want deadline", err)
		}
		if got := time.Since(start); got != time.Second {
			t.Fatalf("got %v, want 1s", got)
		}
		appendTest(t, l, "a")
		start = time.Now()
		if got := nextTest(t, l).Records; string(got[0]) != "a" {
			t.Fatal(got)
		}
		if got := time.Since(start); got != 200*time.Millisecond {
			t.Fatalf("got %v, want 200ms", got)
		}
	})
}

func TestNextRepeatAndAck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := openTestLog(t, testConfig(t))
		appendTest(t, l, "abc")
		first := nextTest(t, l)
		first.Records[0][0] = 'X'
		first.Records[0] = []byte("changed")
		second := nextTest(t, l)
		if second.Sequence != first.Sequence || string(second.Records[0]) != "abc" {
			t.Fatalf("got %+v, want original records and sequence %d", second, first.Sequence)
		}
		if err := l.Ack(first.Sequence); err != nil {
			t.Fatal(err)
		}
		if st := statsTest(t, l); st.Segments != 0 || st.Records != 0 || st.Bytes != 0 {
			t.Fatalf("got %+v, want empty", st)
		}
		if _, err := os.Stat(l.path(first.Sequence, sealedSuffix)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("got %v, want removed", err)
		}
	})
}

func TestAckDiscarded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := testConfig(t)
		l := openTestLog(t, cfg)
		appendTest(t, l, "a")
		s := nextTest(t, l)
		time.Sleep(2 * cfg.MaxAge)
		appendTest(t, l, "b")
		if st := statsTest(t, l); st.DiscardedRecords != 1 {
			t.Fatalf("got %+v, want one discard", st)
		}
		if err := l.Ack(s.Sequence); err != nil {
			t.Fatal(err)
		}
		if got := nextTest(t, l).Records; string(got[0]) != "b" {
			t.Fatal(got)
		}
	})
}

func TestConcurrentAppend(t *testing.T) {
	cfg := testConfig(t)
	cfg.MaxBytes = 1 << 20
	l := openTestLog(t, cfg)
	var wg sync.WaitGroup
	for worker := range 8 {
		wg.Go(func() {
			for i := range 1000 {
				if err := l.Append(fmt.Appendf(nil, "%d:%d", worker, i)); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	if st := statsTest(t, l); st.Records != 8000 {
		t.Fatalf("got %d, want 8000", st.Records)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	l = openTestLog(t, cfg)
	seen := make(map[string]bool)
	for statsTest(t, l).Records > 0 {
		s := nextTest(t, l)
		for _, b := range s.Records {
			key := string(b)
			if seen[key] {
				t.Fatalf("duplicate %q", key)
			}
			seen[key] = true
		}
		if err := l.Ack(s.Sequence); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 8000 {
		t.Fatalf("got %d unique records, want 8000", len(seen))
	}
}

func TestRecordBounds(t *testing.T) {
	l := openTestLog(t, testConfig(t))
	for _, n := range []int{0, 37} {
		requireCode(t, l.Append(make([]byte, n)), ErrCodeRecord)
	}
	if err := l.Append(make([]byte, 36)); err != nil {
		t.Fatal(err)
	}
	appendTest(t, l, "a")
	if st := statsTest(t, l); st.Records != 2 {
		t.Fatalf("got %+v, want two records", st)
	}
}

func TestConfigBoundsAndDefaults(t *testing.T) {
	cfg := testConfig(t)
	cases := []struct {
		name   string
		change func(*Config)
	}{
		{"dir", func(c *Config) { c.Dir = "" }},
		{"bytes negative", func(c *Config) { c.MaxBytes = -1 }},
		{"bytes small", func(c *Config) { c.MaxBytes = 127 }},
		{"segment negative", func(c *Config) { c.SegmentBytes = -1 }},
		{"segment small", func(c *Config) { c.SegmentBytes = 28 }},
		{"record negative", func(c *Config) { c.MaxRecordBytes = -1 }},
		{"record large", func(c *Config) { c.MaxRecordBytes = 37 }},
		{"age", func(c *Config) { c.MaxAge = -1 }},
		{"roll negative", func(c *Config) { c.RollAge = -1 }},
		{"roll large", func(c *Config) { c.RollAge = 31 * time.Minute }},
		{"linger", func(c *Config) { c.Linger = -1 }},
		{"sync", func(c *Config) { c.SyncInterval = -1 }},
		{"overflow", func(c *Config) { c.SegmentBytes = 1<<62 + 1 }},
		{"format length", func(c *Config) { c.MaxBytes = 1 << 35; c.SegmentBytes = 1 << 33; c.MaxRecordBytes = 1 << 32 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { c := cfg; tc.change(&c); _, err := Open(c); requireCode(t, err, ErrCodeConfig) })
	}
	l := openTestLog(t, Config{Dir: t.TempDir()})
	want := Config{Dir: l.cfg.Dir, MaxBytes: 256 << 20, MaxAge: 7 * 24 * time.Hour, SegmentBytes: 512 << 10, MaxRecordBytes: 256 << 10, RollAge: 10 * time.Minute, Linger: 200 * time.Millisecond, SyncInterval: 5 * time.Second}
	if l.cfg != want {
		t.Fatalf("got %+v, want %+v", l.cfg, want)
	}
}

func TestCloseWakesNextAndRejectsMethods(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := openTestLog(t, testConfig(t))
		result := make(chan error, 1)
		var wg sync.WaitGroup
		wg.Go(func() { _, err := l.Next(context.Background()); result <- err })
		synctest.Wait()
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
		requireCode(t, <-result, ErrCodeClosed)
		wg.Wait()
		requireCode(t, l.Append([]byte("a")), ErrCodeClosed)
		_, err := l.Next(context.Background())
		requireCode(t, err, ErrCodeClosed)
		requireCode(t, l.Ack(1), ErrCodeClosed)
		_, err = l.Stats()
		requireCode(t, err, ErrCodeClosed)
		requireCode(t, l.Close(), ErrCodeClosed)
	})
}

func TestModesAndLazyCreation(t *testing.T) {
	cfg := testConfig(t)
	cfg.Dir = filepath.Join(cfg.Dir, "log")
	l := openTestLog(t, cfg)
	info, err := os.Stat(cfg.Dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("got %v %v, want 0700", info, err)
	}
	if st := statsTest(t, l); st.Segments != 0 {
		t.Fatalf("got %+v, want no empty active file", st)
	}
	appendTest(t, l, "a")
	info, err = os.Stat(l.path(l.active.sequence, openSuffix))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("got %v %v, want 0600", info, err)
	}
}

func TestRollAgeAndSyncInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := testConfig(t)
		l := openTestLog(t, cfg)
		var calls []string
		l.ops.sync = func(f *os.File) error { calls = append(calls, "sync"); return f.Sync() }
		l.ops.rename = func(a, b string) error { calls = append(calls, "rename"); return os.Rename(a, b) }
		appendTest(t, l, "a")
		time.Sleep(4 * time.Second)
		appendTest(t, l, "b")
		if len(calls) != 0 {
			t.Fatalf("got %v, want no sync", calls)
		}
		time.Sleep(time.Second)
		appendTest(t, l, "c")
		if !reflect.DeepEqual(calls, []string{"sync"}) {
			t.Fatalf("got %v, want [sync]", calls)
		}
		time.Sleep(cfg.RollAge)
		appendTest(t, l, "d")
		if got := nextTest(t, l).Records; !reflect.DeepEqual(got, [][]byte{[]byte("a"), []byte("b"), []byte("c")}) {
			t.Fatalf("got %q, want a,b,c", got)
		}
		if !reflect.DeepEqual(calls, []string{"sync", "rename"}) {
			t.Fatalf("got %v, want sync then rename without seal sync", calls)
		}
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(calls, []string{"sync", "rename", "sync", "rename"}) {
			t.Fatal(calls)
		}
	})
}

func TestSealRenameFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := openTestLog(t, testConfig(t))
		appendTest(t, l, "a")
		l.ops.rename = func(_, _ string) error { return errors.New("rename denied") }
		_, err := l.Next(context.Background())
		requireCode(t, err, ErrCodeIO)
		entries, err := os.ReadDir(l.cfg.Dir)
		if err != nil || len(entries) != 1 || filepath.Ext(entries[0].Name()) != openSuffix {
			t.Fatalf("got %v %v, want only open file", entries, err)
		}
		if len(l.sealed) != 0 {
			t.Fatalf("got %d reader-visible segments, want 0", len(l.sealed))
		}
		l.ops.rename = os.Rename
		if got := nextTest(t, l).Records; string(got[0]) != "a" {
			t.Fatal(got)
		}
	})
}

func TestNextByteBound(t *testing.T) {
	cfg := testConfig(t)
	cfg.MaxBytes = 128
	for i := range 3 {
		writeFixture(t, cfg.Dir, fmt.Sprintf("%016x.seg", i), "abcdefghijklmnopqrstuvwxyz0123456789")
	}
	l := openTestLog(t, cfg)
	if st := statsTest(t, l); st.Bytes != 192 {
		t.Fatalf("got %+v, want 192 bytes before Next", st)
	}
	s := nextTest(t, l)
	if s.Sequence != 1 {
		t.Fatalf("got sequence %d, want 1", s.Sequence)
	}
	if st := statsTest(t, l); st.Bytes != 128 || st.DiscardedRecords != 1 || st.DiscardedBytes != 64 {
		t.Fatalf("got %+v, want 128 bytes, 1 record and 64 bytes discarded", st)
	}
}

func TestRollSingleRecordAndOldestAge(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := testConfig(t)
		l := openTestLog(t, cfg)
		appendTest(t, l, "a")
		time.Sleep(cfg.RollAge)
		appendTest(t, l, "b")
		st := statsTest(t, l)
		if st.Segments != 2 || st.OldestAge != 10*time.Minute {
			t.Fatalf("got %+v, want 2 segments with oldest age 10m", st)
		}
		s := nextTest(t, l)
		if !reflect.DeepEqual(s.Records, [][]byte{[]byte("a")}) {
			t.Fatalf("got %q, want [a]", s.Records)
		}
		if err := l.Ack(s.Sequence); err != nil {
			t.Fatal(err)
		}
		if st := statsTest(t, l); st.OldestAge != 0 || st.Records != 1 {
			t.Fatalf("got %+v, want newborn segment with one record", st)
		}
	})
}

func TestNextWakesOnAppend(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := openTestLog(t, testConfig(t))
		result := make(chan Segment, 1)
		var wg sync.WaitGroup
		wg.Go(func() { result <- nextTest(t, l) })
		synctest.Wait()
		select {
		case s := <-result:
			t.Fatalf("got %+v before append", s)
		default:
		}
		start := time.Now()
		appendTest(t, l, "a")
		s := <-result
		wg.Wait()
		if len(s.Records) != 1 || string(s.Records[0]) != "a" || time.Since(start) != 200*time.Millisecond {
			t.Fatalf("got %+v after %v, want a after 200ms", s, time.Since(start))
		}
	})
}

func TestSyncFailureStillCloses(t *testing.T) {
	l := openTestLog(t, testConfig(t))
	appendTest(t, l, "a")
	file := l.file
	l.ops.sync = func(*os.File) error { return errors.New("sync denied") }
	requireCode(t, l.Close(), ErrCodeIO)
	if _, err := file.Write([]byte("x")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("got %v, want closed file", err)
	}
	_, err := l.Stats()
	requireCode(t, err, ErrCodeClosed)
	l = openTestLog(t, l.cfg)
	if got := nextTest(t, l).Records; string(got[0]) != "a" {
		t.Fatal(got)
	}
}

func TestNextExpiresActiveWithoutPriorAging(t *testing.T) {
	for _, priorNext := range []bool{false, true} {
		t.Run(fmt.Sprint(priorNext), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cfg := testConfig(t)
				l := openTestLog(t, cfg)
				appendTest(t, l, "old")
				if priorNext {
					if got := nextTest(t, l).Records; string(got[0]) != "old" {
						t.Fatal(got)
					}
				}
				time.Sleep(cfg.MaxAge + time.Second)
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if s, err := l.Next(ctx); !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("got %+v %v, want expired log and deadline", s, err)
				}
				if st := statsTest(t, l); st.Segments != 0 || st.DiscardedRecords != 1 || st.DiscardedBytes != 31 {
					t.Fatalf("got %+v, want old segment discarded", st)
				}
				appendTest(t, l, "young")
				if got := nextTest(t, l).Records; string(got[0]) != "young" {
					t.Fatal(got)
				}
			})
		})
	}
}

func TestAppendWriteFailureKeepsLaterRecords(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := openTestLog(t, testConfig(t))
		appendTest(t, l, "a")
		calls := 0
		l.ops.write = func(f *os.File, b []byte) (int, error) {
			calls++
			if len(b) != 11 {
				t.Fatalf("got write length %d, want 11", len(b))
			}
			n, err := f.Write(b[:9])
			if err != nil {
				return n, err
			}
			return n, errors.New("partial write")
		}
		requireCode(t, l.Append([]byte("bad")), ErrCodeIO)
		if calls != 1 {
			t.Fatalf("got %d writes, want one record write", calls)
		}
		l.ops.write = (*os.File).Write
		appendTest(t, l, "b")
		if st := statsTest(t, l); st.Bytes != 67 || st.Records != 2 || st.Segments != 2 {
			t.Fatalf("got %+v, want partial bytes counted and later record in new segment", st)
		}
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
		l = openTestLog(t, l.cfg)
		var got []string
		for statsTest(t, l).Records > 0 {
			s := nextTest(t, l)
			for _, r := range s.Records {
				got = append(got, string(r))
			}
			if err := l.Ack(s.Sequence); err != nil {
				t.Fatal(err)
			}
		}
		if !reflect.DeepEqual(got, []string{"a", "b"}) {
			t.Fatalf("got %v, want [a b]", got)
		}
	})
}

func TestAgeBoundWithNonmonotonicHeaders(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := testConfig(t)
		for i, age := range []time.Duration{0, 2 * time.Hour, 0} {
			b := appendSegmentHeader(nil, time.Now().Add(-age))
			b, err := appendRecord(b, []byte{byte(i)})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(cfg.Dir, fmt.Sprintf("%016x.seg", i)), b, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		l := openTestLog(t, cfg)
		s := nextTest(t, l)
		if s.Sequence != 0 {
			t.Fatalf("got %d, want oldest retained sequence 0", s.Sequence)
		}
		if st := statsTest(t, l); st.DiscardedRecords != 1 || st.Bytes != 58 {
			t.Fatalf("got %+v, want expired middle segment removed", st)
		}
		if err := l.Ack(s.Sequence); err != nil {
			t.Fatal(err)
		}
		if s := nextTest(t, l); s.Sequence != 2 {
			t.Fatalf("got %d, want younger sequence 2", s.Sequence)
		}
	})
}
