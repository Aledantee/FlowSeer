// Package captureapi provides central persistence and RPC handlers for
// packet capture sessions and pcapng artifacts.
package captureapi

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	modelcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	netcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/capture/pcapng"
)

// Error codes returned by Store.
var (
	ErrCodeStore            = errs.NewCode("captureapi/store")
	ErrCodeConflict         = errs.NewCode("captureapi/conflict")
	ErrCodeDecode           = errs.NewCode("captureapi/decode")
	ErrCodeNotFound         = errs.NewCode("captureapi/not_found")
	ErrCodeArtifactNotFound = errs.NewCode("captureapi/artifact_not_found")
	ErrCodeBadSession       = errs.NewCode("captureapi/bad_session")
	ErrCodeArtifactExists   = errs.NewCode("captureapi/artifact_exists")
)

const (
	casRetries   = 8
	maxChunkSize = 1024 * 1024 // 1 MiB per download chunk
)

// activeWriter tracks an open pcapng file and renderer for an in-flight upload.
type activeWriter struct {
	file        *os.File
	writer      *pcapng.Writer
	packetCount uint64
	linkType    netcapturev1.LinkType
	snapLen     uint32
}

// Store manages capture session records in JetStream KeyValue storage and
// writes pcapng artifacts to disk under capturesDir. Safe for concurrent use.
type Store struct {
	kv          jetstream.KeyValue
	capturesDir string
	clock       func() time.Time

	mu      sync.Mutex
	writers map[string]*activeWriter
	deleted map[string]struct{}
}

// NewStore initializes a Store over the captures JetStream bucket and
// ensures capturesDir exists on disk.
func NewStore(kv jetstream.KeyValue, capturesDir string) (*Store, error) {
	if err := os.MkdirAll(capturesDir, 0o700); err != nil {
		return nil, errs.From(err).Code(ErrCodeStore).Attr("dir", capturesDir).Msg("create captures directory")
	}
	return &Store{
		kv:          kv,
		capturesDir: capturesDir,
		clock:       time.Now,
		writers:     make(map[string]*activeWriter),
		deleted:     make(map[string]struct{}),
	}, nil
}

// SetClock replaces the clock the retention sweep reads. Tests use it to
// place an artifact's expiry on either side of now without sleeping.
func (s *Store) SetClock(clock func() time.Time) {
	s.clock = clock
}

// artifactPath maps a session identifier onto its pcapng file. The identifier
// reaches a filesystem path here, so it is checked against the shape the
// schema gives it (CaptureSessionLocalRef.id is a uuid) rather than trusted:
// an id carrying a separator would otherwise place the file outside
// capturesDir, and the streaming RPCs are not covered by the validating
// interceptor.
func (s *Store) artifactPath(sessionID string) (string, error) {
	if _, err := uuid.Parse(sessionID); err != nil {
		return "", errs.From(err).Code(ErrCodeBadSession).Attr("session", sessionID).Msg("capture session identifier is not a uuid")
	}
	return filepath.Join(s.capturesDir, sessionID+".pcapng"), nil
}

// CreateSession persists a new capture session in the PENDING state.
func (s *Store) CreateSession(ctx context.Context, config *modelcapturev1.CaptureSessionConfig) (*modelcapturev1.CaptureSessionRecord, error) {
	sessionID := config.GetRef().GetCaptureSession().GetId()
	if sessionID == "" {
		return nil, errs.New().Code(ErrCodeStore).Msg("empty capture session identifier")
	}

	state := modelcapturev1.CaptureSessionState_builder{
		Ref:       config.GetRef(),
		Lifecycle: modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_PENDING.Enum(),
	}.Build()

	rec := modelcapturev1.CaptureSessionRecord_builder{
		Config: config,
		State:  state,
	}.Build()

	data, err := proto.Marshal(rec)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeDecode).Attr("session", sessionID).Msg("encode capture session record")
	}

	s.mu.Lock()
	delete(s.deleted, sessionID)
	s.mu.Unlock()

	if _, err := s.kv.Create(ctx, sessionID, data); err != nil {
		if errors.Is(err, jetstream.ErrKeyExists) {
			return nil, errs.New().Code(ErrCodeConflict).Attr("session", sessionID).Msg("capture session already exists")
		}
		return nil, errs.From(err).Code(ErrCodeStore).Attr("session", sessionID).Msg("create capture session record")
	}

	return rec, nil
}

// GetSession returns one capture session record and its revision, or (nil, 0, nil)
// if the session does not exist.
func (s *Store) GetSession(ctx context.Context, sessionID string) (*modelcapturev1.CaptureSessionRecord, uint64, error) {
	entry, err := s.kv.Get(ctx, sessionID)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, errs.From(err).Code(ErrCodeStore).Attr("session", sessionID).Msg("read capture session record")
	}

	rec := &modelcapturev1.CaptureSessionRecord{}
	if err := proto.Unmarshal(entry.Value(), rec); err != nil {
		return nil, 0, errs.From(err).Code(ErrCodeDecode).Attr("session", sessionID).Msg("decode capture session record")
	}
	return rec, entry.Revision(), nil
}

// ListSessions returns all capture session records ordered by session identifier.
func (s *Store) ListSessions(ctx context.Context) ([]*modelcapturev1.CaptureSessionRecord, error) {
	keys, err := s.kv.Keys(ctx)
	if errors.Is(err, jetstream.ErrNoKeysFound) {
		return nil, nil
	}
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeStore).Msg("list capture session records")
	}

	sort.Strings(keys)
	out := make([]*modelcapturev1.CaptureSessionRecord, 0, len(keys))
	for _, key := range keys {
		rec, _, err := s.GetSession(ctx, key)
		if err != nil {
			return nil, err
		}
		if rec != nil {
			out = append(out, rec)
		}
	}
	return out, nil
}

// MutateSession applies fn to the session record under compare-and-set revision checks.
func (s *Store) MutateSession(ctx context.Context, sessionID string, fn func(rec *modelcapturev1.CaptureSessionRecord) error) (*modelcapturev1.CaptureSessionRecord, error) {
	for attempt := 0; attempt < casRetries; attempt++ {
		rec, revision, err := s.GetSession(ctx, sessionID)
		if err != nil {
			return nil, err
		}
		if rec == nil {
			return nil, errs.New().Code(ErrCodeNotFound).Attr("session", sessionID).Msg("capture session not found")
		}

		if err := fn(rec); err != nil {
			return nil, err
		}

		data, err := proto.Marshal(rec)
		if err != nil {
			return nil, errs.From(err).Code(ErrCodeDecode).Attr("session", sessionID).Msg("encode capture session record")
		}

		_, err = s.kv.Update(ctx, sessionID, data, revision)
		if errors.Is(err, jetstream.ErrKeyRevisionMismatch) {
			continue
		}
		if err != nil {
			return nil, errs.From(err).Code(ErrCodeStore).Attr("session", sessionID).Msg("update capture session record")
		}
		return rec, nil
	}
	return nil, errs.New().Code(ErrCodeConflict).Attr("session", sessionID).Msg("session record update did not settle")
}

// DeleteSession removes both the session metadata record and any artifact file
// on disk. The writer is dropped under the same lock that admits a new one, so
// an upload stream in flight cannot recreate the file between the unlink and
// the record's removal: an artifact whose record is gone is one the retention
// sweep can no longer reach, and its payload would stay on disk forever.
func (s *Store) DeleteSession(ctx context.Context, sessionID string) error {
	path, err := s.artifactPath(sessionID)
	if err != nil {
		return err
	}

	s.mu.Lock()
	if w, ok := s.writers[sessionID]; ok {
		_ = w.file.Close()
		delete(s.writers, sessionID)
	}
	// An upload stream in flight does not know its session was deleted, and
	// its next chunk would open the path again. The file it wrote would then
	// have no record, and the sweep walks records, so its payload could never
	// expire. The tombstone is what refuses that chunk.
	s.deleted[sessionID] = struct{}{}
	rmErr := os.Remove(path)
	s.mu.Unlock()

	if rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
		return errs.From(rmErr).Code(ErrCodeStore).Attr("session", sessionID).Msg("remove artifact file")
	}

	if err := s.kv.Delete(ctx, sessionID); err != nil && !errors.Is(err, jetstream.ErrKeyNotFound) {
		return errs.From(err).Code(ErrCodeStore).Attr("session", sessionID).Msg("delete capture session record")
	}
	return nil
}

// AbandonWriter discards the partial pcapng of a session whose upload stream
// ended without a final chunk: it closes the writer, forgets it, and unlinks
// the file.
//
// Both halves are load-bearing. A writer nobody closes leaks a descriptor for
// the life of the process. A partial file is worse: it holds captured payload
// and has no artifact descriptor, so the sweep — which walks session records —
// can never reach it, and it would sit outside retention forever. Unlinking it
// also frees the path for a session that may still upload, since the artifact
// path refuses to open over an existing file.
//
// It unlinks only when it held the writer, which is what keeps it away from a
// finalized artifact: finalization drops the writer first.
func (s *Store) AbandonWriter(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	w, ok := s.writers[sessionID]
	if !ok {
		return
	}
	_ = w.file.Close()
	delete(s.writers, sessionID)

	if path, err := s.artifactPath(sessionID); err == nil {
		_ = os.Remove(path)
	}
}

// mayWriteArtifact refuses to open an artifact for a session whose record says
// the capture is over.
//
// The file and the record are two stores that must agree, and the record is
// the one of record: it carries the digest, the byte count and the purge
// stamp that everything else trusts. So a session that is gone, that already
// has an artifact, or whose payload retention has purged one, may not have a
// file written for it — otherwise the bytes on disk stop being the bytes the
// record describes, and nothing downstream can tell.
//
// A session that is merely holding an open writer is not consulted: that
// stream already has the permission, and asking again on every batch would
// put a network read in the packet path.
func (s *Store) mayWriteArtifact(ctx context.Context, sessionID string) error {
	s.mu.Lock()
	_, open := s.writers[sessionID]
	s.mu.Unlock()
	if open {
		return nil
	}

	rec, _, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if rec == nil {
		return errs.New().Code(ErrCodeNotFound).Attr("session", sessionID).Msg("capture session not found")
	}
	if artifact := rec.GetState().GetArtifact(); artifact != nil {
		return errs.New().Code(ErrCodeArtifactExists).Attr("session", sessionID).Msg("capture session has already produced its artifact")
	}
	return nil
}

// AppendPackets appends a slice of PacketRecords to the session's pcapng artifact.
func (s *Store) AppendPackets(ctx context.Context, sessionID string, linkType netcapturev1.LinkType, snapLen uint32, packets []*netcapturev1.PacketRecord) error {
	if len(packets) == 0 {
		return nil
	}

	// Opening a new file needs the record's permission, taken before the
	// lock because it is a network read. The writers map alone cannot give
	// it: the map is what this process is holding open, and the record is
	// what says whether the capture is still being written at all.
	if err := s.mayWriteArtifact(ctx, sessionID); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, gone := s.deleted[sessionID]; gone {
		return errs.New().Code(ErrCodeNotFound).Attr("session", sessionID).Msg("capture session was deleted")
	}

	w, ok := s.writers[sessionID]
	if !ok {
		path, err := s.artifactPath(sessionID)
		if err != nil {
			return err
		}
		// O_EXCL, not O_TRUNC: an artifact already on disk belongs to a
		// capture this Store is no longer writing, and truncating it would
		// destroy the bytes its recorded digest and packet count describe.
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, os.ErrExist) {
			return errs.From(err).Code(ErrCodeArtifactExists).Attr("session", sessionID).Msg("artifact file already exists for this session")
		}
		if err != nil {
			return errs.From(err).Code(ErrCodeStore).Attr("session", sessionID).Msg("create artifact file")
		}
		pw, err := pcapng.NewWriter(f, linkType, snapLen)
		if err != nil {
			_ = f.Close()
			return errs.From(err).Code(ErrCodeStore).Attr("session", sessionID).Msg("initialize pcapng renderer")
		}
		w = &activeWriter{
			file:     f,
			writer:   pw,
			linkType: linkType,
			snapLen:  snapLen,
		}
		s.writers[sessionID] = w
	}

	for _, pkt := range packets {
		if err := w.writer.WriteRecord(pkt); err != nil {
			return errs.From(err).Code(ErrCodeStore).Attr("session", sessionID).Msg("write packet record to pcapng")
		}
		w.packetCount++
	}
	return nil
}

// FinalizeArtifact closes the pcapng file, computes its SHA-256 digest, byte count,
// and packet total, and returns the constructed CaptureArtifact.
func (s *Store) FinalizeArtifact(ctx context.Context, sessionID string, linkType netcapturev1.LinkType, snapLen uint32, counters *netcapturev1.CaptureCounters, expiresAt time.Time) (*modelcapturev1.CaptureArtifact, error) {
	if err := s.mayWriteArtifact(ctx, sessionID); err != nil {
		return nil, err
	}

	s.mu.Lock()
	_, gone := s.deleted[sessionID]
	w, ok := s.writers[sessionID]
	if ok {
		delete(s.writers, sessionID)
	}
	s.mu.Unlock()

	if gone {
		if ok {
			_ = w.file.Close()
		}
		return nil, errs.New().Code(ErrCodeNotFound).Attr("session", sessionID).Msg("capture session was deleted")
	}

	path, err := s.artifactPath(sessionID)
	if err != nil {
		return nil, err
	}

	var packetCount uint64
	if ok {
		packetCount = w.packetCount
		if err := w.writer.Close(counters); err != nil {
			_ = w.file.Close()
			return nil, errs.From(err).Code(ErrCodeStore).Attr("session", sessionID).Msg("close pcapng writer")
		}
		// The digest below is published into a record JetStream fsyncs. Sync
		// the bytes first, so a crash between the two cannot leave a durable
		// digest over a file that was never written.
		if err := w.file.Sync(); err != nil {
			_ = w.file.Close()
			return nil, errs.From(err).Code(ErrCodeStore).Attr("session", sessionID).Msg("sync artifact file")
		}
		if err := w.file.Close(); err != nil {
			return nil, errs.From(err).Code(ErrCodeStore).Attr("session", sessionID).Msg("close artifact file")
		}
	} else {
		// No writer means no chunk carried a packet. O_EXCL for the reason
		// AppendPackets gives: a file that is already here belongs to a
		// capture whose writer this Store no longer holds.
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, os.ErrExist) {
			return nil, errs.From(err).Code(ErrCodeArtifactExists).Attr("session", sessionID).Msg("artifact file already exists for this session")
		}
		if err != nil {
			return nil, errs.From(err).Code(ErrCodeStore).Attr("session", sessionID).Msg("create empty artifact file")
		}
		pw, err := pcapng.NewWriter(f, linkType, snapLen)
		if err != nil {
			_ = f.Close()
			return nil, errs.From(err).Code(ErrCodeStore).Attr("session", sessionID).Msg("initialize empty pcapng writer")
		}
		if err := pw.Close(counters); err != nil {
			_ = f.Close()
			return nil, errs.From(err).Code(ErrCodeStore).Attr("session", sessionID).Msg("close empty pcapng writer")
		}
		if err := f.Sync(); err != nil {
			_ = f.Close()
			return nil, errs.From(err).Code(ErrCodeStore).Attr("session", sessionID).Msg("sync empty artifact file")
		}
		if err := f.Close(); err != nil {
			return nil, errs.From(err).Code(ErrCodeStore).Attr("session", sessionID).Msg("close empty artifact file")
		}
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeStore).Attr("session", sessionID).Msg("open finalized artifact file")
	}
	defer func() { _ = f.Close() }()

	hasher := sha256.New()
	size, err := io.Copy(hasher, f)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeStore).Attr("session", sessionID).Msg("compute artifact digest")
	}

	artifact := modelcapturev1.CaptureArtifact_builder{
		ByteSize:    proto.Uint64(uint64(size)),
		PacketCount: proto.Uint64(packetCount),
		Digest:      hasher.Sum(nil),
		LinkType:    linkType.Enum(),
		ExpiresAt:   timestamppb.New(expiresAt),
	}.Build()

	return artifact, nil
}

// ArtifactExists checks whether the on-disk pcapng artifact is present.
func (s *Store) ArtifactExists(sessionID string) bool {
	path, err := s.artifactPath(sessionID)
	if err != nil {
		return false
	}
	fi, statErr := os.Stat(path)
	return statErr == nil && !fi.IsDir()
}

// ReadArtifact reads the on-disk pcapng artifact in chunks of up to 1 MiB,
// passing each chunk to fn in order.
// Each chunk's Data aliases one reusable buffer and is valid only until fn
// returns.
func (s *Store) ReadArtifact(ctx context.Context, sessionID string, fn func(chunk *modelcapturev1.CaptureArtifactChunk) error) error {
	path, err := s.artifactPath(sessionID)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errs.New().Code(ErrCodeArtifactNotFound).Attr("session", sessionID).Msg("artifact not found")
		}
		return errs.From(err).Code(ErrCodeStore).Attr("session", sessionID).Msg("open artifact file")
	}
	defer func() { _ = f.Close() }()

	stat, err := f.Stat()
	if err != nil {
		return errs.From(err).Code(ErrCodeStore).Attr("session", sessionID).Msg("stat artifact file")
	}
	totalSize := stat.Size()

	if totalSize == 0 {
		// An empty artifact still owes the caller a final chunk: a stream
		// that simply ends carries no way to tell completion from a reset.
		return fn(modelcapturev1.CaptureArtifactChunk_builder{
			Offset: proto.Uint64(0),
			Final:  proto.Bool(true),
		}.Build())
	}

	buf := make([]byte, maxChunkSize)
	var offset uint64
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		n, readErr := io.ReadFull(f, buf)
		if n > 0 {
			isFinal := (offset+uint64(n) >= uint64(totalSize)) || errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF)
			chunk := modelcapturev1.CaptureArtifactChunk_builder{
				Offset: proto.Uint64(offset),
				Data:   buf[:n],
				Final:  proto.Bool(isFinal),
			}.Build()
			if err := fn(chunk); err != nil {
				return err
			}
			offset += uint64(n)
		}
		if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
			break
		}
		if readErr != nil {
			return errs.From(readErr).Code(ErrCodeStore).Attr("session", sessionID).Msg("read artifact file")
		}
	}
	return nil
}

// SweepExpired unlinks the on-disk artifact of every session past its
// expires_at and stamps purged_at on the descriptor, leaving the session
// record and its counters in the bucket. Purging the payload is the
// obligation the capture direction record takes on, so a sweep that could
// not unlink a file says so rather than returning a count that reads like
// nothing was due; the stamp is also what keeps a purged session from being
// re-swept on every tick for the life of the record.
func (s *Store) SweepExpired(ctx context.Context) (int, error) {
	keys, err := s.kv.Keys(ctx)
	if errors.Is(err, jetstream.ErrNoKeysFound) {
		return 0, nil
	}
	if err != nil {
		return 0, errs.From(err).Code(ErrCodeStore).Msg("list capture sessions for sweep")
	}

	now := s.clock()
	removed := 0
	var failures []error

	for _, key := range keys {
		rec, _, err := s.GetSession(ctx, key)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if rec == nil {
			continue
		}
		artifact := rec.GetState().GetArtifact()
		if artifact == nil || artifact.GetExpiresAt() == nil || artifact.HasPurgedAt() {
			continue
		}
		if !now.After(artifact.GetExpiresAt().AsTime()) {
			continue
		}

		path, err := s.artifactPath(key)
		if err != nil {
			failures = append(failures, err)
			continue
		}

		s.mu.Lock()
		if w, ok := s.writers[key]; ok {
			_ = w.file.Close()
			delete(s.writers, key)
		}
		rmErr := os.Remove(path)
		s.mu.Unlock()

		if rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			failures = append(failures, errs.From(rmErr).Code(ErrCodeStore).Attr("session", key).Msg("remove expired artifact file"))
			continue
		}
		if rmErr == nil {
			removed++
		}

		if _, err := s.MutateSession(ctx, key, func(r *modelcapturev1.CaptureSessionRecord) error {
			if a := r.GetState().GetArtifact(); a != nil {
				a.SetPurgedAt(timestamppb.New(now))
			}
			return nil
		}); err != nil {
			failures = append(failures, err)
		}
	}
	if len(failures) > 0 {
		return removed, errors.Join(failures...)
	}
	return removed, nil
}
