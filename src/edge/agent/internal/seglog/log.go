package seglog

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// Config sets disk and age bounds. Zero optional fields select their defaults.
// Negative fields and inconsistent bounds return [ErrCodeConfig] from [Open].
type Config struct {
	// Dir is required and must have one owning process.
	Dir string
	// MaxBytes includes file and record headers. Default: 256 MiB.
	// It must be at least twice SegmentBytes.
	MaxBytes int64
	// MaxAge bounds header age, not individual record age. Default: seven days.
	MaxAge time.Duration
	// SegmentBytes includes all headers. Default: 512 KiB.
	SegmentBytes int64
	// MaxRecordBytes bounds payload length. Default: 256 KiB.
	// It must fit SegmentBytes minus 28 bytes of headers.
	MaxRecordBytes int64
	// RollAge caps the age spread within a segment. Default: ten minutes.
	// It must not exceed half MaxAge.
	RollAge time.Duration
	// Linger is the header age at which a waiting Next seals. Default: 200 ms.
	Linger time.Duration
	// SyncInterval is checked on Append. Default: five seconds.
	SyncInterval time.Duration
}

// Segment is a sealed file's valid record prefix in append order.
// Records belong to the caller and may be changed without affecting retries.
type Segment struct {
	// Sequence identifies the file to pass to Ack, within this open Log.
	Sequence uint64
	Records  [][]byte
}

// Stats describes current disk contents and counters since Open.
// Acknowledged files do not contribute to the discard counters.
type Stats struct {
	// Segments includes the active file, when present.
	Segments int
	Records  int64
	Bytes    int64
	// OldestAge is zero for an empty log or a future-dated header.
	OldestAge        time.Duration
	DiscardedRecords int64
	DiscardedBytes   int64
	CorruptSegments  int64
	// CorruptBytes counts rejected suffixes, including removed bad headers.
	CorruptBytes   int64
	TruncatedBytes int64
}

type segmentFile struct {
	sequence   uint64
	created    time.Time
	bytes      int64
	validBytes int64
	records    int64
}

type fileOps struct {
	sync   func(*os.File) error
	rename func(string, string) error
	write  func(*os.File, []byte) (int, error)
}

// Log serializes concurrent methods and owns its files until Close.
// Its zero value is unusable. Use one reader and acknowledge its returned
// sequence only after every record is settled. Bounds may discard that file
// while the reader holds its copy. The package starts no goroutines.
type Log struct {
	mu           sync.Mutex
	cfg          Config
	ops          fileOps
	sealed       []segmentFile
	active       *segmentFile
	file         *os.File
	lastSync     time.Time
	nextSequence uint64
	exhausted    bool
	closed       bool
	changed      chan struct{}
	counters     Stats
}

// Open creates Dir with mode 0700 if absent and recovers recognized files.
// Existing permissions are preserved. Bad headers are removed, damaged sealed
// files retain their valid prefix, and torn open tails are truncated and sealed.
// Foreign names are untouched. Errors carry [ErrCodeConfig] or [ErrCodeIO].
func Open(cfg Config) (*Log, error) {
	cfg, err := resolveConfig(cfg)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return nil, ioError(err, "create log directory")
	}
	l := &Log{cfg: cfg, changed: make(chan struct{}), ops: fileOps{sync: (*os.File).Sync, rename: os.Rename, write: (*os.File).Write}}
	entries, err := os.ReadDir(cfg.Dir)
	if err != nil {
		return nil, ioError(err, "list log directory")
	}
	for _, entry := range entries {
		seq, sealed, ok := parseSegmentName(entry.Name())
		if !ok {
			continue
		}
		if seq >= l.nextSequence {
			if seq == math.MaxUint64 {
				l.exhausted = true
			} else {
				l.nextSequence = seq + 1
			}
		}
		if err := l.recoverFile(entry.Name(), seq, sealed); err != nil {
			return nil, err
		}
	}
	slices.SortFunc(l.sealed, func(a, b segmentFile) int {
		if a.sequence < b.sequence {
			return -1
		}
		if a.sequence > b.sequence {
			return 1
		}
		return 0
	})
	return l, nil
}

func resolveConfig(c Config) (Config, error) {
	if c.MaxBytes == 0 {
		c.MaxBytes = 256 << 20
	}
	if c.MaxAge == 0 {
		c.MaxAge = 7 * 24 * time.Hour
	}
	if c.SegmentBytes == 0 {
		c.SegmentBytes = 512 << 10
	}
	if c.MaxRecordBytes == 0 {
		c.MaxRecordBytes = 256 << 10
	}
	if c.RollAge == 0 {
		c.RollAge = 10 * time.Minute
	}
	if c.Linger == 0 {
		c.Linger = 200 * time.Millisecond
	}
	if c.SyncInterval == 0 {
		c.SyncInterval = 5 * time.Second
	}
	if c.Dir == "" || c.MaxBytes < 0 || c.SegmentBytes <= segmentHeaderBytes+recordHeaderBytes || c.SegmentBytes > c.MaxBytes/2 || c.MaxRecordBytes < 1 || c.MaxRecordBytes > c.SegmentBytes-segmentHeaderBytes-recordHeaderBytes || c.MaxRecordBytes > math.MaxUint32 || c.MaxAge <= 0 || c.RollAge <= 0 || c.RollAge > c.MaxAge/2 || c.Linger <= 0 || c.SyncInterval <= 0 {
		return Config{}, errs.New().Code(ErrCodeConfig).Msg("invalid log bounds or timing")
	}
	return c, nil
}

func ioError(err error, message string) error { return errs.From(err).Code(ErrCodeIO).Msg(message) }

func closedError() error { return errs.New().Code(ErrCodeClosed).Msg("log is closed") }

func (l *Log) path(seq uint64, suffix string) string {
	return filepath.Join(l.cfg.Dir, fmt.Sprintf("%016x%s", seq, suffix))
}

func (l *Log) recoverFile(name string, seq uint64, sealed bool) error {
	path := filepath.Join(l.cfg.Dir, name)
	b, err := os.ReadFile(path)
	if err != nil {
		return ioError(err, "read segment")
	}
	scan := scanSegment(b)
	if scan.offset == 0 {
		if err := os.Remove(path); err != nil {
			return ioError(err, "remove bad segment header")
		}
		l.counters.CorruptSegments++
		l.counters.CorruptBytes += int64(len(b))
		return nil
	}
	s := segmentFile{sequence: seq, created: scan.created, bytes: int64(len(b)), validBytes: int64(scan.offset), records: int64(len(scan.records))}
	if scan.reason != "" {
		if sealed {
			l.counters.CorruptSegments++
			l.counters.CorruptBytes += int64(len(b) - scan.offset)
		} else {
			if err := os.Truncate(path, int64(scan.offset)); err != nil {
				return ioError(err, "truncate open tail")
			}
			l.counters.TruncatedBytes += int64(len(b) - scan.offset)
			s.bytes = int64(scan.offset)
		}
	}
	if !sealed {
		if _, err := os.Lstat(l.path(seq, sealedSuffix)); err == nil {
			return errs.New().Code(ErrCodeIO).Msg("open and sealed names share a sequence")
		} else if !os.IsNotExist(err) {
			return ioError(err, "check sealed destination")
		}
		if err := l.ops.rename(path, l.path(seq, sealedSuffix)); err != nil {
			return ioError(err, "seal recovered segment")
		}
	}
	l.sealed = append(l.sealed, s)
	return nil
}

// Append copies a nonempty payload into one write of its record header and body.
// It rolls before a record exceeds SegmentBytes or RollAge and discards oldest
// sealed files to enforce both bounds. Empty or oversized records return
// [ErrCodeRecord]. I/O errors return [ErrCodeIO] and may have written a prefix.
// After Close it returns [ErrCodeClosed].
func (l *Log) Append(payload []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return closedError()
	}
	if len(payload) == 0 || int64(len(payload)) > l.cfg.MaxRecordBytes {
		return errs.New().Code(ErrCodeRecord).Attr("bytes", len(payload)).Msg("record exceeds payload bound or is empty")
	}
	now := time.Now()
	if l.active != nil && (l.file == nil || l.active.validBytes != l.active.bytes || l.active.bytes+recordHeaderBytes+int64(len(payload)) > l.cfg.SegmentBytes || now.Sub(l.active.created) >= l.cfg.RollAge) {
		if err := l.seal(); err != nil {
			return err
		}
	}
	if err := l.enforceBounds(now); err != nil {
		return err
	}
	if l.active == nil {
		if err := l.createActive(now); err != nil {
			return err
		}
	}
	b, err := appendRecord(nil, payload)
	if err != nil {
		return err
	}
	n, err := l.ops.write(l.file, b)
	l.active.bytes += int64(n)
	l.notify()
	if err != nil {
		return ioError(err, "append record")
	}
	l.active.validBytes = l.active.bytes
	l.active.records++
	if now.Sub(l.lastSync) >= l.cfg.SyncInterval {
		if err := l.ops.sync(l.file); err != nil {
			return ioError(err, "sync active segment")
		}
		l.lastSync = now
	}
	return l.enforceBounds(now)
}

func (l *Log) createActive(now time.Time) error {
	if l.exhausted {
		return errs.New().Code(ErrCodeIO).Msg("segment sequence exhausted")
	}
	seq := l.nextSequence
	f, err := os.OpenFile(l.path(seq, openSuffix), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return ioError(err, "create active segment")
	}
	if _, err := f.Write(appendSegmentHeader(nil, now)); err != nil {
		_ = f.Close() // Preserve the write error. Open will remove the bad header.
		return ioError(err, "write segment header")
	}
	l.file = f
	l.active = &segmentFile{sequence: seq, created: now, bytes: segmentHeaderBytes, validBytes: segmentHeaderBytes}
	l.lastSync = now
	if seq == math.MaxUint64 {
		l.exhausted = true
	} else {
		l.nextSequence++
	}
	return nil
}

func (l *Log) seal() error {
	if l.active == nil {
		return nil
	}
	if l.file != nil {
		err := l.file.Close()
		l.file = nil
		if err != nil {
			return ioError(err, "close active segment")
		}
	}
	if err := l.ops.rename(l.path(l.active.sequence, openSuffix), l.path(l.active.sequence, sealedSuffix)); err != nil {
		return ioError(err, "seal active segment")
	}
	l.sealed = append(l.sealed, *l.active)
	l.active = nil
	l.notify()
	return nil
}

func (l *Log) notify() { close(l.changed); l.changed = make(chan struct{}) }

func (l *Log) diskBytes() int64 {
	var n int64
	for _, s := range l.sealed {
		n += s.bytes
	}
	if l.active != nil {
		n += l.active.bytes
	}
	return n
}

func (l *Log) enforceBounds(now time.Time) error {
	if l.active != nil && now.Sub(l.active.created) > l.cfg.MaxAge {
		if err := l.seal(); err != nil {
			return err
		}
	}
	bytes := l.diskBytes()
	for i := 0; i < len(l.sealed); {
		s := l.sealed[i]
		if bytes <= l.cfg.MaxBytes && now.Sub(s.created) <= l.cfg.MaxAge {
			i++
			continue
		}
		if err := os.Remove(l.path(s.sequence, sealedSuffix)); err != nil {
			return ioError(err, "discard bounded segment")
		}
		l.sealed = slices.Delete(l.sealed, i, i+1)
		bytes -= s.bytes
		l.counters.DiscardedRecords += s.records
		l.counters.DiscardedBytes += s.bytes
		l.notify()
	}
	return nil
}

// Next waits for the oldest sealed segment, sealing an active segment at Linger
// when none is sealed. It repeats that segment until Ack or bound eviction.
// Records are independent copies. Bounds are checked before delivery.
// Cancellation returns ctx.Err(). Close wakes waiters with [ErrCodeClosed].
// File failures return [ErrCodeIO]. Only one reader should call Next.
func (l *Log) Next(ctx context.Context) (Segment, error) {
	for {
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			return Segment{}, closedError()
		}
		if err := ctx.Err(); err != nil {
			l.mu.Unlock()
			return Segment{}, err
		}
		now := time.Now()
		if err := l.enforceBounds(now); err != nil {
			l.mu.Unlock()
			return Segment{}, err
		}
		if len(l.sealed) == 0 && l.active != nil && now.Sub(l.active.created) >= l.cfg.Linger {
			if err := l.seal(); err != nil {
				l.mu.Unlock()
				return Segment{}, err
			}
		}
		if len(l.sealed) > 0 {
			s, err := l.readSegment(l.sealed[0])
			l.mu.Unlock()
			return s, err
		}
		changed := l.changed
		var timer *time.Timer
		var tick <-chan time.Time
		if l.active != nil {
			timer = time.NewTimer(l.cfg.Linger - now.Sub(l.active.created))
			tick = timer.C
		}
		l.mu.Unlock()
		select {
		case <-changed:
		case <-tick:
		case <-ctx.Done():
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

func (l *Log) readSegment(s segmentFile) (Segment, error) {
	b, err := os.ReadFile(l.path(s.sequence, sealedSuffix))
	if err != nil {
		return Segment{}, ioError(err, "read sealed segment")
	}
	if int64(len(b)) < s.validBytes {
		return Segment{}, errs.New().Code(ErrCodeIO).Msg("sealed segment shortened after recovery")
	}
	scan := scanSegment(b[:s.validBytes])
	if scan.reason != "" {
		return Segment{}, errs.New().Code(ErrCodeIO).Msg("sealed segment changed after recovery")
	}
	return Segment{Sequence: s.sequence, Records: scan.records}, nil
}

// Ack removes the named sealed file. An absent sequence, including one discarded
// by bounds, succeeds. It leaves an active file alone. Removal is not synced,
// so a power cut can cause redelivery. Errors carry [ErrCodeIO] or [ErrCodeClosed].
func (l *Log) Ack(sequence uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return closedError()
	}
	for i, s := range l.sealed {
		if s.sequence != sequence {
			continue
		}
		if err := os.Remove(l.path(sequence, sealedSuffix)); err != nil {
			return ioError(err, "acknowledge segment")
		}
		l.sealed = slices.Delete(l.sealed, i, i+1)
		l.notify()
		return nil
	}
	return nil
}

// Stats returns a snapshot without enforcing bounds. Counters reset at Open.
// After Close it returns [ErrCodeClosed].
func (l *Log) Stats() (Stats, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return Stats{}, closedError()
	}
	s := l.counters
	s.Segments = len(l.sealed)
	var oldest time.Time
	for _, f := range l.sealed {
		s.Records += f.records
		s.Bytes += f.bytes
		if oldest.IsZero() || f.created.Before(oldest) {
			oldest = f.created
		}
	}
	if l.active != nil {
		s.Segments++
		s.Records += l.active.records
		s.Bytes += l.active.bytes
		if oldest.IsZero() || l.active.created.Before(oldest) {
			oldest = l.active.created
		}
	}
	if !oldest.IsZero() {
		s.OldestAge = max(time.Since(oldest), 0)
	}
	return s, nil
}

// Close syncs and seals the active file and wakes Next. It releases the file
// even if syncing fails. The log stays closed after an I/O error, and a new
// Open recovers any remaining open file. Repeated Close returns [ErrCodeClosed].
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return closedError()
	}
	l.closed = true
	l.notify()
	var syncErr error
	if l.file != nil {
		syncErr = l.ops.sync(l.file)
	}
	err := l.seal()
	if syncErr != nil {
		return ioError(syncErr, "sync closing segment")
	}
	return err
}
