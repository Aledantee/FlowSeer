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

	mu      sync.Mutex
	writers map[string]*activeWriter
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
		writers:     make(map[string]*activeWriter),
	}, nil
}

func (s *Store) artifactPath(sessionID string) string {
	return filepath.Join(s.capturesDir, sessionID+".pcapng")
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

// DeleteSession removes both the session metadata record and any artifact file on disk.
func (s *Store) DeleteSession(ctx context.Context, sessionID string) error {
	path := s.artifactPath(sessionID)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errs.From(err).Code(ErrCodeStore).Attr("session", sessionID).Msg("remove artifact file")
	}

	s.mu.Lock()
	if w, ok := s.writers[sessionID]; ok {
		_ = w.file.Close()
		delete(s.writers, sessionID)
	}
	s.mu.Unlock()

	if err := s.kv.Delete(ctx, sessionID); err != nil && !errors.Is(err, jetstream.ErrKeyNotFound) {
		return errs.From(err).Code(ErrCodeStore).Attr("session", sessionID).Msg("delete capture session record")
	}
	return nil
}

// AppendPackets appends a slice of PacketRecords to the session's pcapng artifact.
func (s *Store) AppendPackets(_ context.Context, sessionID string, linkType netcapturev1.LinkType, snapLen uint32, packets []*netcapturev1.PacketRecord) error {
	if len(packets) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	w, ok := s.writers[sessionID]
	if !ok {
		path := s.artifactPath(sessionID)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
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
func (s *Store) FinalizeArtifact(_ context.Context, sessionID string, linkType netcapturev1.LinkType, snapLen uint32, counters *netcapturev1.CaptureCounters, expiresAt time.Time) (*modelcapturev1.CaptureArtifact, error) {
	s.mu.Lock()
	w, ok := s.writers[sessionID]
	if ok {
		delete(s.writers, sessionID)
	}
	s.mu.Unlock()

	var packetCount uint64
	if ok {
		packetCount = w.packetCount
		if err := w.writer.Close(counters); err != nil {
			_ = w.file.Close()
			return nil, errs.From(err).Code(ErrCodeStore).Attr("session", sessionID).Msg("close pcapng writer")
		}
		if err := w.file.Close(); err != nil {
			return nil, errs.From(err).Code(ErrCodeStore).Attr("session", sessionID).Msg("close artifact file")
		}
	} else {
		path := s.artifactPath(sessionID)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
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
		if err := f.Close(); err != nil {
			return nil, errs.From(err).Code(ErrCodeStore).Attr("session", sessionID).Msg("close empty artifact file")
		}
	}

	path := s.artifactPath(sessionID)
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
	fi, err := os.Stat(s.artifactPath(sessionID))
	return err == nil && !fi.IsDir()
}

// ReadArtifact reads the on-disk pcapng artifact in chunks of up to 1 MiB,
// passing each chunk to fn in order.
func (s *Store) ReadArtifact(ctx context.Context, sessionID string, fn func(chunk *modelcapturev1.CaptureArtifactChunk) error) error {
	path := s.artifactPath(sessionID)
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

// SweepExpired unlinks any on-disk artifact files whose expiration timestamp has passed,
// preserving session metadata and counters in the JetStream bucket.
func (s *Store) SweepExpired(ctx context.Context) (int, error) {
	keys, err := s.kv.Keys(ctx)
	if errors.Is(err, jetstream.ErrNoKeysFound) {
		return 0, nil
	}
	if err != nil {
		return 0, errs.From(err).Code(ErrCodeStore).Msg("list capture sessions for sweep")
	}

	now := time.Now()
	removed := 0

	for _, key := range keys {
		rec, _, err := s.GetSession(ctx, key)
		if err != nil || rec == nil {
			continue
		}
		artifact := rec.GetState().GetArtifact()
		if artifact == nil || artifact.GetExpiresAt() == nil {
			continue
		}
		if now.After(artifact.GetExpiresAt().AsTime()) {
			path := s.artifactPath(key)
			if err := os.Remove(path); err == nil {
				removed++
			}
			s.mu.Lock()
			if w, ok := s.writers[key]; ok {
				_ = w.file.Close()
				delete(s.writers, key)
			}
			s.mu.Unlock()
		}
	}
	return removed, nil
}
