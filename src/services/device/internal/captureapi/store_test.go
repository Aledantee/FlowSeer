package captureapi_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	modelcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	netcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/captureapi"
)

const (
	testEdgeID    = "0192e6a0-0000-7000-8000-0000000000ed"
	testSessionID = "0192e6a0-1111-7000-8000-000000000001"
)

func newTestStore(t *testing.T) *captureapi.Store {
	t.Helper()
	store, _ := newTestStoreDir(t)
	return store
}

// newTestStoreDir also hands back the directory the artifacts land in, for the
// tests that have to look at the files themselves.
func newTestStoreDir(t *testing.T) (*captureapi.Store, string) {
	t.Helper()
	hub, err := edgebus.StartHub(context.Background(), edgebus.HubConfig{
		StateDir:    t.TempDir(),
		FsyncPolicy: service.BusFsyncPeriodic,
		ListenPort:  0,
	})
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	t.Cleanup(hub.Close)

	kv, err := hub.JetStream().KeyValue(context.Background(), edgebus.CapturesBucket)
	if err != nil {
		t.Fatalf("captures bucket: %v", err)
	}

	capturesDir := filepath.Join(t.TempDir(), "captures")
	store, err := captureapi.NewStore(kv, capturesDir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return store, capturesDir
}

func newSessionConfig(t *testing.T, sessionID string) *modelcapturev1.CaptureSessionConfig {
	t.Helper()
	cfg := modelcapturev1.CaptureSessionConfig_builder{
		Ref: modelcapturev1.CaptureSessionGlobalRef_builder{
			Edge: edgev1.EdgeGlobalRef_builder{
				Edge: edgev1.EdgeLocalRef_builder{
					Id: proto.String(testEdgeID),
				}.Build(),
			}.Build(),
			CaptureSession: modelcapturev1.CaptureSessionLocalRef_builder{
				Id: proto.String(sessionID),
			}.Build(),
		}.Build(),
		Name:        proto.String("test-capture"),
		Description: proto.String("test capture session"),
		Source: modelcapturev1.CaptureSource_builder{
			LocalInterface: modelcapturev1.LocalInterfaceSource_builder{
				InterfaceName: proto.String("eth0"),
				Promiscuous:   proto.Bool(true),
			}.Build(),
		}.Build(),
		Budget: modelcapturev1.CaptureBudget_builder{
			MaxPackets: proto.Uint64(100),
		}.Build(),
		Authorization: modelcapturev1.CaptureAuthorization_builder{
			Operator:             proto.String("alice"),
			Reason:               proto.String("investigating drop"),
			FullPayloadRequested: proto.Bool(false),
		}.Build(),
	}.Build()

	if err := protovalidate.Validate(cfg); err != nil {
		t.Fatalf("invalid test session config: %v", err)
	}
	return cfg
}

func newPacket(data []byte) *netcapturev1.PacketRecord {
	origLen := uint32(len(data))
	return netcapturev1.PacketRecord_builder{
		CapturedAt:     timestamppb.Now(),
		OriginalLength: proto.Uint32(origLen),
		Data:           data,
	}.Build()
}

// block is one parsed pcapng block.
type block struct {
	typ  uint32
	body []byte
}

func splitBlocks(t *testing.T, buf []byte) []block {
	t.Helper()
	var blocks []block
	for len(buf) > 0 {
		if len(buf) < 12 {
			t.Fatalf("trailing %d bytes are too short for a block header/trailer", len(buf))
		}
		typ := binary.LittleEndian.Uint32(buf[0:4])
		total := binary.LittleEndian.Uint32(buf[4:8])
		if total%4 != 0 {
			t.Fatalf("block total length %d is not a multiple of 4", total)
		}
		if uint32(len(buf)) < total {
			t.Fatalf("block claims total length %d, only %d bytes remain", total, len(buf))
		}
		trailer := binary.LittleEndian.Uint32(buf[total-4 : total])
		if trailer != total {
			t.Fatalf("block trailer length %d does not match leading length %d", trailer, total)
		}
		blocks = append(blocks, block{typ: typ, body: buf[8 : total-4]})
		buf = buf[total:]
	}
	return blocks
}

const (
	blockTypeSHB = 0x0A0D0D0A
	blockTypeIDB = 0x00000001
	blockTypeISB = 0x00000005
	blockTypeEPB = 0x00000006
)

func TestCreateAndGetSession(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	cfg := newSessionConfig(t, testSessionID)
	rec, err := s.CreateSession(ctx, cfg)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if rec.GetState().GetLifecycle() != modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_PENDING {
		t.Errorf("got lifecycle %v, want %v", rec.GetState().GetLifecycle(), modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_PENDING)
	}

	// Duplicate create must fail with ErrCodeConflict.
	_, err = s.CreateSession(ctx, cfg)
	if code, ok := errs.CodeOf(err); !ok || code != captureapi.ErrCodeConflict {
		t.Fatalf("got err %v, want ErrCodeConflict", err)
	}

	// Get session.
	got, rev, err := s.GetSession(ctx, testSessionID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got == nil {
		t.Fatal("GetSession returned nil record")
	}
	if rev == 0 {
		t.Error("GetSession returned revision 0 for existing session")
	}
	if got.GetConfig().GetName() != "test-capture" {
		t.Errorf("got name %q, want %q", got.GetConfig().GetName(), "test-capture")
	}

	// Unknown session returns nil, 0, nil.
	unknown, urev, err := s.GetSession(ctx, "0192e6a0-9999-7000-8000-000000000099")
	if err != nil {
		t.Fatalf("GetSession unknown: %v", err)
	}
	if unknown != nil || urev != 0 {
		t.Errorf("got unknown session %v, rev %d; want nil, 0", unknown, urev)
	}
}

func TestMutateSessionCAS(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	cfg := newSessionConfig(t, testSessionID)
	if _, err := s.CreateSession(ctx, cfg); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Mutate to RUNNING.
	updated, err := s.MutateSession(ctx, testSessionID, func(rec *modelcapturev1.CaptureSessionRecord) error {
		rec.GetState().SetLifecycle(modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_RUNNING)
		rec.GetState().SetStartedAt(timestamppb.Now())
		return nil
	})
	if err != nil {
		t.Fatalf("MutateSession: %v", err)
	}
	if updated.GetState().GetLifecycle() != modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_RUNNING {
		t.Errorf("got lifecycle %v, want %v", updated.GetState().GetLifecycle(), modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_RUNNING)
	}

	// Mutate on unknown session fails with ErrCodeNotFound.
	_, err = s.MutateSession(ctx, "0192e6a0-9999-7000-8000-000000000099", func(_ *modelcapturev1.CaptureSessionRecord) error {
		return nil
	})
	if code, ok := errs.CodeOf(err); !ok || code != captureapi.ErrCodeNotFound {
		t.Fatalf("got err %v, want ErrCodeNotFound", err)
	}
}

func TestAppendPacketsAndFinalizeArtifact(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	cfg := newSessionConfig(t, testSessionID)
	if _, err := s.CreateSession(ctx, cfg); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Create 100 Ethernet frames: 14-byte Ethernet header + payload.
	packets := make([]*netcapturev1.PacketRecord, 100)
	for i := range packets {
		frame := make([]byte, 64)
		frame[0] = 0x00 // dest mac
		frame[5] = 0x01
		frame[6] = 0x00 // src mac
		frame[11] = 0x02
		frame[12] = 0x08 // EtherType IPv4
		frame[13] = 0x00
		frame[14] = byte(i)
		packets[i] = newPacket(frame)
	}

	// Append in two batches of 50 packets.
	linkType := netcapturev1.LinkType_LINK_TYPE_ETHERNET
	snapLen := uint32(128)
	if err := s.AppendPackets(ctx, testSessionID, linkType, snapLen, packets[:50]); err != nil {
		t.Fatalf("AppendPackets batch 1: %v", err)
	}
	if err := s.AppendPackets(ctx, testSessionID, linkType, snapLen, packets[50:]); err != nil {
		t.Fatalf("AppendPackets batch 2: %v", err)
	}

	counters := netcapturev1.CaptureCounters_builder{
		Received: proto.Uint64(100),
		Accepted: proto.Uint64(100),
	}.Build()

	expiresAt := time.Now().Add(24 * time.Hour)
	artifact, err := s.FinalizeArtifact(ctx, testSessionID, linkType, snapLen, counters, expiresAt)
	if err != nil {
		t.Fatalf("FinalizeArtifact: %v", err)
	}

	if artifact.GetPacketCount() != 100 {
		t.Errorf("got packet count %d, want 100", artifact.GetPacketCount())
	}
	if artifact.GetLinkType() != linkType {
		t.Errorf("got link type %v, want %v", artifact.GetLinkType(), linkType)
	}

	// Verify file on disk.
	if !s.ArtifactExists(testSessionID) {
		t.Fatal("ArtifactExists returned false after finalization")
	}

	var data []byte
	err = s.ReadArtifact(ctx, testSessionID, func(chunk *modelcapturev1.CaptureArtifactChunk) error {
		data = append(data, chunk.GetData()...)
		return nil
	})
	if err != nil {
		t.Fatalf("ReadArtifact: %v", err)
	}

	if uint64(len(data)) != artifact.GetByteSize() {
		t.Errorf("got byte size %d, want %d", len(data), artifact.GetByteSize())
	}

	hasher := sha256.New()
	hasher.Write(data)
	if !bytes.Equal(hasher.Sum(nil), artifact.GetDigest()) {
		t.Error("artifact digest does not match SHA-256 of file contents")
	}

	// Structural block check: 1 SHB, 1 IDB, 100 EPB, 1 ISB.
	blocks := splitBlocks(t, data)
	if len(blocks) != 103 {
		t.Fatalf("got %d blocks, want 103 (SHB + IDB + 100 EPB + ISB)", len(blocks))
	}
	if blocks[0].typ != blockTypeSHB {
		t.Errorf("block 0 typ = 0x%08x, want SHB (0x%08x)", blocks[0].typ, blockTypeSHB)
	}
	if blocks[1].typ != blockTypeIDB {
		t.Errorf("block 1 typ = 0x%08x, want IDB (0x%08x)", blocks[1].typ, blockTypeIDB)
	}
	for i := 2; i < 102; i++ {
		if blocks[i].typ != blockTypeEPB {
			t.Fatalf("block %d typ = 0x%08x, want EPB", i, blocks[i].typ)
		}
	}
	if blocks[102].typ != blockTypeISB {
		t.Errorf("block 102 typ = 0x%08x, want ISB (0x%08x)", blocks[102].typ, blockTypeISB)
	}

	// If capinfos is installed, verify against Wireshark's capinfos.
	if capinfosPath, err := exec.LookPath("capinfos"); err == nil {
		tmpFile := filepath.Join(t.TempDir(), "capture.pcapng")
		if err := os.WriteFile(tmpFile, data, 0o600); err != nil {
			t.Fatalf("write tmp capture: %v", err)
		}
		cmd := exec.Command(capinfosPath, "-c", "-e", tmpFile)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("capinfos failed: %v\nOutput: %s", err, string(out))
		}
	}
}

func TestReadArtifactChunked(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	cfg := newSessionConfig(t, testSessionID)
	if _, err := s.CreateSession(ctx, cfg); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Generate ~1.5 MB of packet data (1500 packets of 1024 bytes).
	packets := make([]*netcapturev1.PacketRecord, 1500)
	packetPayload := make([]byte, 1024)
	for i := range packets {
		packets[i] = newPacket(packetPayload)
	}

	linkType := netcapturev1.LinkType_LINK_TYPE_ETHERNET
	snapLen := uint32(2048)
	if err := s.AppendPackets(ctx, testSessionID, linkType, snapLen, packets); err != nil {
		t.Fatalf("AppendPackets: %v", err)
	}

	counters := netcapturev1.CaptureCounters_builder{
		Received: proto.Uint64(1500),
		Accepted: proto.Uint64(1500),
	}.Build()

	expiresAt := time.Now().Add(24 * time.Hour)
	artifact, err := s.FinalizeArtifact(ctx, testSessionID, linkType, snapLen, counters, expiresAt)
	if err != nil {
		t.Fatalf("FinalizeArtifact: %v", err)
	}

	if artifact.GetByteSize() <= 1024*1024 {
		t.Fatalf("expected artifact byte size > 1MB, got %d", artifact.GetByteSize())
	}

	var chunks []*modelcapturev1.CaptureArtifactChunk
	err = s.ReadArtifact(ctx, testSessionID, func(chunk *modelcapturev1.CaptureArtifactChunk) error {
		chunks = append(chunks, chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("ReadArtifact: %v", err)
	}

	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks for >1MB file, got %d", len(chunks))
	}

	var totalBytes []byte
	for i, chunk := range chunks {
		if uint64(len(chunk.GetData())) > 1024*1024 {
			t.Errorf("chunk %d data length %d exceeds 1MB", i, len(chunk.GetData()))
		}
		if chunk.GetOffset() != uint64(len(totalBytes)) {
			t.Errorf("chunk %d offset %d, want %d", i, chunk.GetOffset(), len(totalBytes))
		}
		isLast := (i == len(chunks)-1)
		if chunk.GetFinal() != isLast {
			t.Errorf("chunk %d final = %v, want %v", i, chunk.GetFinal(), isLast)
		}
		totalBytes = append(totalBytes, chunk.GetData()...)
	}

	if uint64(len(totalBytes)) != artifact.GetByteSize() {
		t.Errorf("concatenated bytes %d != artifact byte size %d", len(totalBytes), artifact.GetByteSize())
	}
}

func TestSweepExpired(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	cfg := newSessionConfig(t, testSessionID)
	if _, err := s.CreateSession(ctx, cfg); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	linkType := netcapturev1.LinkType_LINK_TYPE_ETHERNET
	snapLen := uint32(128)
	packets := []*netcapturev1.PacketRecord{newPacket([]byte("test packet"))}
	if err := s.AppendPackets(ctx, testSessionID, linkType, snapLen, packets); err != nil {
		t.Fatalf("AppendPackets: %v", err)
	}

	counters := netcapturev1.CaptureCounters_builder{
		Received: proto.Uint64(1),
		Accepted: proto.Uint64(1),
	}.Build()

	// Expired 1 hour ago.
	expiresAt := time.Now().Add(-1 * time.Hour)
	artifact, err := s.FinalizeArtifact(ctx, testSessionID, linkType, snapLen, counters, expiresAt)
	if err != nil {
		t.Fatalf("FinalizeArtifact: %v", err)
	}

	// Update session with finalized artifact in state.
	_, err = s.MutateSession(ctx, testSessionID, func(rec *modelcapturev1.CaptureSessionRecord) error {
		rec.GetState().SetLifecycle(modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_COMPLETED)
		rec.GetState().SetStopReason(modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_PACKET_COUNT)
		rec.GetState().SetArtifact(artifact)
		rec.GetState().SetCounters(counters)
		return nil
	})
	if err != nil {
		t.Fatalf("MutateSession: %v", err)
	}

	if !s.ArtifactExists(testSessionID) {
		t.Fatal("artifact file should exist before sweep")
	}

	// Run sweep.
	removed, err := s.SweepExpired(ctx)
	if err != nil {
		t.Fatalf("SweepExpired: %v", err)
	}
	if removed != 1 {
		t.Errorf("got %d removed files, want 1", removed)
	}

	// Artifact file must be gone.
	if s.ArtifactExists(testSessionID) {
		t.Error("artifact file still exists after sweep")
	}

	// ReadArtifact should return ErrCodeArtifactNotFound.
	err = s.ReadArtifact(ctx, testSessionID, func(_ *modelcapturev1.CaptureArtifactChunk) error {
		return nil
	})
	if code, ok := errs.CodeOf(err); !ok || code != captureapi.ErrCodeArtifactNotFound {
		t.Fatalf("ReadArtifact got %v, want ErrCodeArtifactNotFound", err)
	}

	// Session record in KV must remain intact with metadata and counters.
	rec, _, err := s.GetSession(ctx, testSessionID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if rec == nil {
		t.Fatal("session record was deleted from KV")
	}
	if rec.GetState().GetCounters().GetReceived() != 1 {
		t.Errorf("got counters received %d, want 1", rec.GetState().GetCounters().GetReceived())
	}
	if rec.GetState().GetArtifact().GetByteSize() != artifact.GetByteSize() {
		t.Errorf("got artifact byte size %d, want %d", rec.GetState().GetArtifact().GetByteSize(), artifact.GetByteSize())
	}
}

func TestDeleteSession(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	cfg := newSessionConfig(t, testSessionID)
	if _, err := s.CreateSession(ctx, cfg); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	linkType := netcapturev1.LinkType_LINK_TYPE_ETHERNET
	snapLen := uint32(128)
	packets := []*netcapturev1.PacketRecord{newPacket([]byte("test packet"))}
	if err := s.AppendPackets(ctx, testSessionID, linkType, snapLen, packets); err != nil {
		t.Fatalf("AppendPackets: %v", err)
	}

	counters := netcapturev1.CaptureCounters_builder{
		Received: proto.Uint64(1),
	}.Build()

	if _, err := s.FinalizeArtifact(ctx, testSessionID, linkType, snapLen, counters, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("FinalizeArtifact: %v", err)
	}

	if !s.ArtifactExists(testSessionID) {
		t.Fatal("artifact file should exist before delete")
	}

	if err := s.DeleteSession(ctx, testSessionID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	if s.ArtifactExists(testSessionID) {
		t.Error("artifact file still exists after delete")
	}

	rec, _, err := s.GetSession(ctx, testSessionID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if rec != nil {
		t.Error("session record still exists in KV after delete")
	}
}

func TestListSessions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id1 := "0192e6a0-0000-7000-8000-000000000001"
	id2 := "0192e6a0-0000-7000-8000-000000000002"

	if _, err := s.CreateSession(ctx, newSessionConfig(t, id2)); err != nil {
		t.Fatalf("create id2: %v", err)
	}
	if _, err := s.CreateSession(ctx, newSessionConfig(t, id1)); err != nil {
		t.Fatalf("create id1: %v", err)
	}

	list, err := s.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}

	if len(list) != 2 {
		t.Fatalf("got %d sessions, want 2", len(list))
	}
	if list[0].GetConfig().GetRef().GetCaptureSession().GetId() != id1 {
		t.Errorf("list[0] id = %s, want %s", list[0].GetConfig().GetRef().GetCaptureSession().GetId(), id1)
	}
	if list[1].GetConfig().GetRef().GetCaptureSession().GetId() != id2 {
		t.Errorf("list[1] id = %s, want %s", list[1].GetConfig().GetRef().GetCaptureSession().GetId(), id2)
	}
}

// A sweep that unlinks every artifact it walks, regardless of expiry, passes a
// test holding only an expired session. The unexpired session is the input only
// the expiry check rejects.
func TestSweepExpiredLeavesUnexpiredArtifacts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Date(2026, 9, 18, 14, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return now })

	const (
		expiredID   = "0192e6a0-0000-7000-8000-0000000000e1"
		retainedID  = "0192e6a0-0000-7000-8000-0000000000e2"
		unfinishedI = "0192e6a0-0000-7000-8000-0000000000e3"
	)

	finalize := func(sessionID string, expiresAt time.Time) {
		t.Helper()
		if _, err := s.CreateSession(ctx, newSessionConfig(t, sessionID)); err != nil {
			t.Fatalf("CreateSession %s: %v", sessionID, err)
		}
		packets := []*netcapturev1.PacketRecord{newPacket([]byte("test packet"))}
		if err := s.AppendPackets(ctx, sessionID, netcapturev1.LinkType_LINK_TYPE_ETHERNET, 128, packets); err != nil {
			t.Fatalf("AppendPackets %s: %v", sessionID, err)
		}
		counters := netcapturev1.CaptureCounters_builder{
			Received: proto.Uint64(1),
			Accepted: proto.Uint64(1),
		}.Build()
		artifact, err := s.FinalizeArtifact(ctx, sessionID, netcapturev1.LinkType_LINK_TYPE_ETHERNET, 128, counters, expiresAt)
		if err != nil {
			t.Fatalf("FinalizeArtifact %s: %v", sessionID, err)
		}
		if _, err := s.MutateSession(ctx, sessionID, func(rec *modelcapturev1.CaptureSessionRecord) error {
			rec.GetState().SetLifecycle(modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_COMPLETED)
			rec.GetState().SetStopReason(modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_PACKET_COUNT)
			rec.GetState().SetArtifact(artifact)
			rec.GetState().SetCounters(counters)
			return nil
		}); err != nil {
			t.Fatalf("MutateSession %s: %v", sessionID, err)
		}
	}

	finalize(expiredID, now.Add(-time.Hour))
	finalize(retainedID, now.Add(time.Hour))

	// A session still capturing has a file on disk and no artifact
	// descriptor; nothing about it is expired yet.
	if _, err := s.CreateSession(ctx, newSessionConfig(t, unfinishedI)); err != nil {
		t.Fatalf("CreateSession %s: %v", unfinishedI, err)
	}
	if err := s.AppendPackets(ctx, unfinishedI, netcapturev1.LinkType_LINK_TYPE_ETHERNET, 128,
		[]*netcapturev1.PacketRecord{newPacket([]byte("in flight"))}); err != nil {
		t.Fatalf("AppendPackets %s: %v", unfinishedI, err)
	}

	removed, err := s.SweepExpired(ctx)
	if err != nil {
		t.Fatalf("SweepExpired: %v", err)
	}
	if removed != 1 {
		t.Fatalf("got %d removed files, want only the expired one", removed)
	}
	if s.ArtifactExists(expiredID) {
		t.Error("the expired artifact is still on disk")
	}
	if !s.ArtifactExists(retainedID) {
		t.Error("an artifact inside its retention window was purged")
	}
	if !s.ArtifactExists(unfinishedI) {
		t.Error("an in-flight capture's file was purged")
	}
}

// The artifact's recorded digest, byte size and packet count describe bytes
// that must still be there when someone downloads them.
func TestAppendPacketsRefusesToOverwriteAFinalizedArtifact(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.CreateSession(ctx, newSessionConfig(t, testSessionID)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	linkType := netcapturev1.LinkType_LINK_TYPE_ETHERNET
	packets := []*netcapturev1.PacketRecord{newPacket([]byte("first capture"))}
	if err := s.AppendPackets(ctx, testSessionID, linkType, 128, packets); err != nil {
		t.Fatalf("AppendPackets: %v", err)
	}
	counters := netcapturev1.CaptureCounters_builder{
		Received: proto.Uint64(1),
		Accepted: proto.Uint64(1),
	}.Build()
	artifact, err := s.FinalizeArtifact(ctx, testSessionID, linkType, 128, counters, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("FinalizeArtifact: %v", err)
	}

	err = s.AppendPackets(ctx, testSessionID, linkType, 128,
		[]*netcapturev1.PacketRecord{newPacket([]byte("second capture"))})
	if code, ok := errs.CodeOf(err); !ok || code != captureapi.ErrCodeArtifactExists {
		t.Fatalf("AppendPackets after finalization got %v, want ErrCodeArtifactExists", err)
	}

	_, err = s.FinalizeArtifact(ctx, testSessionID, linkType, 128, counters, time.Now().Add(time.Hour))
	if code, ok := errs.CodeOf(err); !ok || code != captureapi.ErrCodeArtifactExists {
		t.Fatalf("FinalizeArtifact a second time got %v, want ErrCodeArtifactExists", err)
	}

	var size int64
	hasher := sha256.New()
	if err := s.ReadArtifact(ctx, testSessionID, func(chunk *modelcapturev1.CaptureArtifactChunk) error {
		size += int64(len(chunk.GetData()))
		hasher.Write(chunk.GetData())
		return nil
	}); err != nil {
		t.Fatalf("ReadArtifact: %v", err)
	}
	if uint64(size) != artifact.GetByteSize() || !bytes.Equal(hasher.Sum(nil), artifact.GetDigest()) {
		t.Fatal("the stored artifact no longer matches the digest recorded for it")
	}
}

// A session identifier reaches a filesystem path, so a value that is not the
// uuid the schema promises is refused before one is built.
func TestArtifactPathsRefuseANonUUIDSession(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	escape := "../../escaped"

	if err := s.DeleteSession(ctx, escape); err == nil {
		t.Error("DeleteSession accepted a session id that is not a uuid")
	}
	if err := s.ReadArtifact(ctx, escape, func(_ *modelcapturev1.CaptureArtifactChunk) error {
		return nil
	}); err == nil {
		t.Error("ReadArtifact accepted a session id that is not a uuid")
	}
	if err := s.AppendPackets(ctx, escape, netcapturev1.LinkType_LINK_TYPE_ETHERNET, 128,
		[]*netcapturev1.PacketRecord{newPacket([]byte("x"))}); err == nil {
		t.Error("AppendPackets accepted a session id that is not a uuid")
	}
	if s.ArtifactExists(escape) {
		t.Error("ArtifactExists answered for a session id that is not a uuid")
	}
}

// An empty artifact still ends its download with a final chunk; a stream that
// simply stops carries no way to tell completion from a reset.
func TestReadArtifactEmitsAFinalChunkForAnEmptyArtifact(t *testing.T) {
	s, dir := newTestStoreDir(t)
	ctx := context.Background()

	if _, err := s.CreateSession(ctx, newSessionConfig(t, testSessionID)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, testSessionID+".pcapng"), nil, 0o600); err != nil {
		t.Fatalf("write empty artifact: %v", err)
	}

	var chunks []*modelcapturev1.CaptureArtifactChunk
	if err := s.ReadArtifact(ctx, testSessionID, func(chunk *modelcapturev1.CaptureArtifactChunk) error {
		chunks = append(chunks, chunk)
		return nil
	}); err != nil {
		t.Fatalf("ReadArtifact: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks for an empty artifact, want 1", len(chunks))
	}
	if !chunks[0].GetFinal() || len(chunks[0].GetData()) != 0 {
		t.Fatalf("got chunk %+v, want one empty final chunk", chunks[0])
	}
}
