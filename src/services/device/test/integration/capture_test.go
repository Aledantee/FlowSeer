package integration_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	operatorcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1"
	capturev1connect "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1/capturev1connect"
	apiedgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	attachv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/attach/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/attach/v1/attachv1connect"
	captureedgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/capture/v1"
	captureedgev1connect "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/capture/v1/capturev1connect"
	operatorv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/operator/v1"
	modelcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	netcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
	"go.aledante.io/FlowSeer/src/common/spawn"
	"go.aledante.io/FlowSeer/src/modules/capture"
	"go.aledante.io/FlowSeer/src/modules/capture/rawsocket"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz"
	"go.aledante.io/FlowSeer/src/services/device/internal/captureapi"
)

func TestRemotePacketCapture_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	writeCredentials(t, filepath.Join(dir, "credentials"))

	registryPath := writeRegistry(t, filepath.Join(dir, "registry.textproto"), "0192e6a0-0000-7000-8000-00000000dead", 0)
	c := newCentral(t, dir, registryPath)
	c.start()
	defer c.shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Create and enroll an edge.
	created, err := c.admin().CreateEdge(ctx, connect.NewRequest(&apiedgev1.CreateEdgeRequest{}))
	if err != nil {
		t.Fatalf("CreateEdge: %v", err)
	}
	edgeID := created.Msg.GetEdge().GetConfig().GetRef().GetEdge().GetId()
	setupKey := created.Msg.GetProvisioning().GetSetupKey()

	c.shutdown()
	writeRegistry(t, registryPath, edgeID, 0)
	c.start()

	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	keyID := setupKey[len("fse1_") : len("fse1_")+26]
	keyProofPayload := edgev1.KeyProofPayload_builder{
		PublicKey:  pubKey,
		SetupKeyId: proto.String(keyID),
	}.Build()
	payloadWire, err := proto.MarshalOptions{Deterministic: true}.Marshal(keyProofPayload)
	if err != nil {
		t.Fatalf("marshal key proof payload: %v", err)
	}
	proof := edgev1.KeyProof_builder{
		Payload:   payloadWire,
		Signature: ed25519.Sign(privKey, payloadWire),
	}.Build()

	edgeAttachClient := attachv1connect.NewEdgeServiceClient(c.client, c.baseURL())
	if _, err := edgeAttachClient.Enroll(ctx, connect.NewRequest(attachv1.EnrollRequest_builder{
		SetupKey: proto.String(setupKey),
		Proof:    proof,
	}.Build())); err != nil {
		t.Fatalf("Enroll: %v", err)
	}

	// Header assertions for the edge RPCs that take one.
	signHeaderAssertion := func(procedure string, body []byte) string {
		nonce := make([]byte, 16)
		if _, err := rand.Read(nonce); err != nil {
			t.Fatalf("rand nonce: %v", err)
		}
		bodyHash := sha256.Sum256(body)
		now := time.Now()
		assertion := edgev1.EdgeAssertion_builder{
			Edge: edgev1.EdgeGlobalRef_builder{
				Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(edgeID)}.Build(),
			}.Build(),
			Audience:   proto.String("flowseer-e2e"),
			IssuedAt:   timestamppb.New(now),
			ExpiresAt:  timestamppb.New(now.Add(30 * time.Second)),
			Nonce:      nonce,
			Procedure:  proto.String(procedure),
			BodySha256: bodyHash[:],
		}.Build()
		payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(assertion)
		if err != nil {
			t.Fatalf("marshal assertion: %v", err)
		}
		signed := edgev1.SignedEdgeAssertion_builder{
			Payload:   payload,
			Signature: ed25519.Sign(privKey, payload),
		}.Build()
		signedBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(signed)
		if err != nil {
			t.Fatalf("marshal signed assertion: %v", err)
		}
		return "FlowSeer-Edge " + base64.RawStdEncoding.EncodeToString(signedBytes)
	}

	// Helper to sign in-stream assertions for UploadCapture
	signStreamAssertion := func() *edgev1.SignedEdgeAssertion {
		nonce := make([]byte, 16)
		if _, err := rand.Read(nonce); err != nil {
			t.Fatalf("rand nonce: %v", err)
		}
		bodyHash := sha256.Sum256(nil)
		now := time.Now()
		assertion := edgev1.EdgeAssertion_builder{
			Edge: edgev1.EdgeGlobalRef_builder{
				Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(edgeID)}.Build(),
			}.Build(),
			Audience:   proto.String("flowseer-e2e"),
			IssuedAt:   timestamppb.New(now),
			ExpiresAt:  timestamppb.New(now.Add(30 * time.Second)),
			Nonce:      nonce,
			Procedure:  proto.String(captureedgev1connect.CaptureEdgeServiceUploadCaptureProcedure),
			BodySha256: bodyHash[:],
		}.Build()
		payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(assertion)
		if err != nil {
			t.Fatalf("marshal stream assertion: %v", err)
		}
		return edgev1.SignedEdgeAssertion_builder{
			Payload:   payload,
			Signature: ed25519.Sign(privKey, payload),
		}.Build()
	}

	// The operator creates a capture session.
	createReq := operatorcapturev1.CreateCaptureSessionRequest_builder{
		Edge: edgev1.EdgeGlobalRef_builder{
			Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(edgeID)}.Build(),
		}.Build(),
		Name:        proto.String("e2e-capture"),
		Description: proto.String("end-to-end capture test"),
		Source: modelcapturev1.CaptureSource_builder{
			LocalInterface: modelcapturev1.LocalInterfaceSource_builder{
				InterfaceName: proto.String("eth0"),
				Promiscuous:   proto.Bool(true),
			}.Build(),
		}.Build(),
		Budget: modelcapturev1.CaptureBudget_builder{
			MaxPackets: proto.Uint64(10),
		}.Build(),
		Authorization: modelcapturev1.CaptureAuthorization_builder{
			RequestedBy: identityv1.OperatorRef_builder{
				Issuer:  proto.String("https://auth.example.com"),
				Subject: proto.String("zitadel|usr_123"),
			}.Build(),
			Reason:               proto.String("integration test"),
			FullPayloadRequested: proto.Bool(false),
		}.Build(),
	}.Build()

	createResp, err := c.captures().CreateCaptureSession(ctx, connect.NewRequest(createReq))
	if err != nil {
		t.Fatalf("CreateCaptureSession: %v", err)
	}
	sessionRef := createResp.Msg.GetSession().GetConfig().GetRef()
	sessionID := sessionRef.GetCaptureSession().GetId()
	if sessionID == "" {
		t.Fatal("expected assigned session UUID, got empty")
	}
	if createResp.Msg.GetSession().GetState().GetLifecycle() != modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_PENDING {
		t.Fatalf("got lifecycle %v, want PENDING", createResp.Msg.GetSession().GetState().GetLifecycle())
	}

	// The edge subscribes and receives the start assignment.
	// Connect client envelopes the request on the wire: 1 byte flags + 4 bytes length.
	// For an empty message with 0 bytes payload, the wire body is [0, 0, 0, 0, 0].
	connectEnvelopeEmpty := []byte{0, 0, 0, 0, 0}
	subReq := connect.NewRequest(&captureedgev1.SubscribeCaptureAssignmentsRequest{})
	subReq.Header().Set("Authorization", signHeaderAssertion(captureedgev1connect.CaptureEdgeServiceSubscribeCaptureAssignmentsProcedure, connectEnvelopeEmpty))

	subStream, err := c.edgeCaptures().SubscribeCaptureAssignments(ctx, subReq)
	if err != nil {
		t.Fatalf("SubscribeCaptureAssignments: %v", err)
	}
	defer func() { _ = subStream.Close() }()

	if !subStream.Receive() {
		t.Fatalf("expected start assignment on stream, ended: %v", subStream.Err())
	}
	startAssign := subStream.Msg().GetStart()
	if startAssign == nil {
		t.Fatalf("got assignment %+v, want start assignment", subStream.Msg())
	}
	if startAssign.GetRef().GetCaptureSession().GetId() != sessionID {
		t.Fatalf("assignment session ID = %q, want %q", startAssign.GetRef().GetCaptureSession().GetId(), sessionID)
	}

	// The operator opens a live tail.
	tailReceived := make(chan []*netcapturev1.PacketRecord, 10)
	tailDone := make(chan error, 1)

	tailReq := connect.NewRequest(operatorcapturev1.TailCaptureSessionRequest_builder{
		Session: sessionRef,
	}.Build())

	tailAttached := make(chan struct{})

	spawn.Go(ctx, "operator-tail", func() {
		tailStream, err := c.captures().TailCaptureSession(ctx, tailReq)
		if err != nil {
			tailDone <- err
			return
		}
		defer func() { _ = tailStream.Close() }()

		attached := false
		for tailStream.Receive() {
			if msg := tailStream.Msg(); msg.WhichBody() == operatorcapturev1.TailCaptureSessionResponse_Attached_case {
				attached = true
				close(tailAttached)
				continue
			}
			tailReceived <- tailStream.Msg().GetChunk().GetPackets()
		}
		if !attached {
			close(tailAttached)
		}
		tailDone <- tailStream.Err()
	})

	// Wait for the tail to say it is attached rather than guessing how long
	// the subscription takes: the handler reaches its subscription after a
	// round trip and a store read, and a chunk broadcast before then reaches
	// nobody and is not resent.
	select {
	case <-tailAttached:
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for the operator tail to attach")
	}

	// The edge uploads its packets.
	uploadStream := c.edgeCaptures().UploadCapture(ctx)

	// The stream must open with a SignedEdgeAssertion
	openReq := captureedgev1.UploadCaptureRequest_builder{
		Assertion: signStreamAssertion(),
	}.Build()
	if err := uploadStream.Send(openReq); err != nil {
		t.Fatalf("upload open assertion: %v", err)
	}

	chunk1 := captureedgev1.UploadCaptureRequest_builder{
		Chunk: modelcapturev1.CapturePacketChunk_builder{
			Session:       sessionRef,
			FirstSequence: proto.Uint64(1),
			Packets: []*netcapturev1.PacketRecord{
				netcapturev1.PacketRecord_builder{
					Sequence:       proto.Uint64(1),
					CapturedAt:     timestamppb.Now(),
					OriginalLength: proto.Uint32(64),
					Data:           []byte("packet-payload-1"),
				}.Build(),
			},
			Counters: netcapturev1.CaptureCounters_builder{
				ReceivedPackets: proto.Uint64(1),
				AcceptedPackets: proto.Uint64(1),
			}.Build(),
			Final: proto.Bool(false),
		}.Build(),
	}.Build()

	if err := uploadStream.Send(chunk1); err != nil {
		t.Fatalf("upload chunk 1: %v", err)
	}

	// Verify operator receives chunk 1 over live tail
	select {
	case packets := <-tailReceived:
		if len(packets) != 1 || string(packets[0].GetData()) != "packet-payload-1" {
			t.Fatalf("unexpected chunk 1 on tail: %+v", packets)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for chunk 1 on live tail")
	}

	// Upload final chunk
	chunk2 := captureedgev1.UploadCaptureRequest_builder{
		Chunk: modelcapturev1.CapturePacketChunk_builder{
			Session:       sessionRef,
			FirstSequence: proto.Uint64(2),
			Packets: []*netcapturev1.PacketRecord{
				netcapturev1.PacketRecord_builder{
					Sequence:       proto.Uint64(2),
					CapturedAt:     timestamppb.Now(),
					OriginalLength: proto.Uint32(64),
					Data:           []byte("packet-payload-2"),
				}.Build(),
			},
			Counters: netcapturev1.CaptureCounters_builder{
				ReceivedPackets: proto.Uint64(2),
				AcceptedPackets: proto.Uint64(2),
			}.Build(),
			Final:      proto.Bool(true),
			StopReason: modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_PACKET_COUNT.Enum(),
		}.Build(),
	}.Build()

	if err := uploadStream.Send(chunk2); err != nil {
		t.Fatalf("upload chunk 2: %v", err)
	}

	// Verify operator receives chunk 2 over live tail
	select {
	case packets := <-tailReceived:
		if len(packets) != 1 || string(packets[0].GetData()) != "packet-payload-2" {
			t.Fatalf("unexpected chunk 2 on tail: %+v", packets)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for chunk 2 on live tail")
	}

	// Live tail should complete cleanly
	select {
	case err := <-tailDone:
		if err != nil {
			t.Fatalf("tail stream completed with error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for tail stream completion")
	}

	// Edge closes upload stream and verifies response
	uploadResp, err := uploadStream.CloseAndReceive()
	if err != nil {
		t.Fatalf("close upload stream: %v", err)
	}
	if uploadResp.Msg.GetSession().GetCaptureSession().GetId() != sessionID {
		t.Fatalf("response session id = %q, want %q", uploadResp.Msg.GetSession().GetCaptureSession().GetId(), sessionID)
	}

	// The operator downloads the finalized pcapng artifact.
	downloadReq := connect.NewRequest(operatorcapturev1.DownloadCaptureSessionRequest_builder{
		Session: sessionRef,
	}.Build())

	downloadStream, err := c.captures().DownloadCaptureSession(ctx, downloadReq)
	if err != nil {
		t.Fatalf("DownloadCaptureSession: %v", err)
	}
	defer func() { _ = downloadStream.Close() }()

	var downloadedData []byte
	for downloadStream.Receive() {
		chunk := downloadStream.Msg().GetChunk()
		if chunk == nil {
			t.Fatal("expected non-nil download chunk")
		}
		downloadedData = append(downloadedData, chunk.GetData()...)
	}
	if downloadStream.Err() != nil {
		t.Fatalf("download stream error: %v", downloadStream.Err())
	}

	if len(downloadedData) == 0 {
		t.Fatal("expected downloaded pcapng data, got 0 bytes")
	}
	// Verify pcapng section header block magic: 0x0A0D0D0A
	pcapngMagic := []byte{0x0a, 0x0d, 0x0d, 0x0a}
	if !bytes.HasPrefix(downloadedData, pcapngMagic) {
		t.Fatalf("downloaded file does not have pcapng magic header")
	}
	// The digest below is over what this store itself wrote, so it proves the
	// download is faithful and nothing about the body. Both payloads have to
	// be in it, or an empty pcapng would satisfy every check here.
	for _, payload := range [][]byte{[]byte("packet-payload-1"), []byte("packet-payload-2")} {
		if !bytes.Contains(downloadedData, payload) {
			t.Fatalf("the downloaded artifact does not carry %q", payload)
		}
	}

	// Check session state via GetCaptureSession
	getResp, err := c.captures().GetCaptureSession(ctx, connect.NewRequest(operatorcapturev1.GetCaptureSessionRequest_builder{
		Session: sessionRef,
	}.Build()))
	if err != nil {
		t.Fatalf("GetCaptureSession: %v", err)
	}
	sessionState := getResp.Msg.GetSession().GetState()
	if sessionState.GetLifecycle() != modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_COMPLETED {
		t.Fatalf("got lifecycle %v, want COMPLETED", sessionState.GetLifecycle())
	}
	artifact := sessionState.GetArtifact()
	if artifact == nil {
		t.Fatal("expected non-nil artifact in session state")
	}
	if artifact.GetPacketCount() != 2 {
		t.Fatalf("artifact packet count = %d, want 2", artifact.GetPacketCount())
	}
	if artifact.GetByteSize() != uint64(len(downloadedData)) {
		t.Fatalf("artifact byte size = %d, downloaded %d bytes", artifact.GetByteSize(), len(downloadedData))
	}
	if sessionState.GetCounters().GetAcceptedPackets() != 2 {
		t.Fatalf("session counters accepted = %d, want 2", sessionState.GetCounters().GetAcceptedPackets())
	}
	hasher := sha256.New()
	hasher.Write(downloadedData)
	if !bytes.Equal(hasher.Sum(nil), artifact.GetDigest()) {
		t.Fatalf("downloaded data digest mismatch")
	}

	// Retention purges the payload and keeps the record.
	artifactPath := filepath.Join(dir, "central-state", "captures", edgebus.DefaultTenant, sessionID+".pcapng")
	if _, err := os.Stat(artifactPath); err != nil {
		t.Fatalf("expected artifact file on disk at %s: %v", artifactPath, err)
	}

	// Open Store over the hub's JetStream to simulate artifact expiry and run sweep
	c.mu.Lock()
	hub := c.hub
	c.mu.Unlock()
	if hub == nil {
		t.Fatal("central reported nil hub")
	}
	kv, err := hub.JetStream().KeyValue(ctx, edgebus.CapturesBucket)
	if err != nil {
		t.Fatalf("open captures KV: %v", err)
	}
	capturesStore, err := captureapi.NewStore(kv, filepath.Join(dir, "central-state", "captures"), time.Now)
	if err != nil {
		t.Fatalf("open captures store: %v", err)
	}

	// Mutate session record so expires_at is in the past
	if _, err := capturesStore.MutateSession(ctx, edgebus.DefaultTenant, sessionID, func(rec *modelcapturev1.CaptureSessionRecord) error {
		rec.GetState().GetArtifact().SetExpiresAt(timestamppb.New(time.Now().Add(-time.Hour)))
		return nil
	}); err != nil {
		t.Fatalf("mutate session expires_at: %v", err)
	}

	// Sweep expired artifacts
	swept, err := capturesStore.SweepExpired(ctx)
	if err != nil {
		t.Fatalf("SweepExpired: %v", err)
	}
	if swept != 1 {
		t.Fatalf("got %d swept artifacts, want 1", swept)
	}

	// Verify artifact file is purged from disk
	if _, err := os.Stat(artifactPath); !os.IsNotExist(err) {
		t.Fatalf("got artifact stat error %v, want file to be unlinked", err)
	}

	// The payload is gone, so the download is CodeNotFound rather than the
	// FailedPrecondition an unfinished capture answers with.
	expiredDownloadStream, err := c.captures().DownloadCaptureSession(ctx, downloadReq)
	if err != nil {
		t.Fatalf("download invocation after sweep: %v", err)
	}
	if expiredDownloadStream.Receive() {
		t.Fatal("expected download stream to fail after artifact swept")
	}
	if connect.CodeOf(expiredDownloadStream.Err()) != connect.CodeNotFound {
		t.Fatalf("got expired download error %v, want CodeNotFound", expiredDownloadStream.Err())
	}
	_ = expiredDownloadStream.Close()

	// Session record remains readable and intact
	afterSweepResp, err := c.captures().GetCaptureSession(ctx, connect.NewRequest(operatorcapturev1.GetCaptureSessionRequest_builder{
		Session: sessionRef,
	}.Build()))
	if err != nil {
		t.Fatalf("GetCaptureSession after sweep: %v", err)
	}
	afterSweep := afterSweepResp.Msg.GetSession().GetState()
	if afterSweep.GetLifecycle() != modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_COMPLETED {
		t.Fatalf("got session lifecycle %v after sweep, want COMPLETED", afterSweep.GetLifecycle())
	}
	// The retention departure: the payload goes, the record and its counters
	// stay, and the descriptor says when the bytes were purged.
	if afterSweep.GetCounters().GetAcceptedPackets() != 2 {
		t.Fatalf("counters did not survive the sweep: accepted = %d, want 2", afterSweep.GetCounters().GetAcceptedPackets())
	}
	sweptArtifact := afterSweep.GetArtifact()
	if sweptArtifact.GetPacketCount() != artifact.GetPacketCount() ||
		sweptArtifact.GetByteSize() != artifact.GetByteSize() ||
		!bytes.Equal(sweptArtifact.GetDigest(), artifact.GetDigest()) {
		t.Fatal("the artifact descriptor did not survive the sweep")
	}
	if !sweptArtifact.HasPurgedAt() {
		t.Fatal("expected the swept artifact to be stamped purged_at")
	}
}

// TestRemotePacketCapture_RetentionSweepRunsOnConfiguredInterval proves the
// host's retention sweeper honors the configured capture_sweep cadence. A
// central started with a one-second sweep (the schema minimum) purges an
// artifact whose deadline has passed within a few ticks, where the built-in
// one-minute default would leave it on disk for the length of the test.
func TestRemotePacketCapture_RetentionSweepRunsOnConfiguredInterval(t *testing.T) {
	dir := t.TempDir()
	writeCredentials(t, filepath.Join(dir, "credentials"))
	registryPath := writeRegistry(t, filepath.Join(dir, "registry.textproto"), "0192e6a0-0000-7000-8000-00000000dead", 0)

	c := newCentral(t, dir, registryPath)
	c.captureSweep = time.Second
	c.start()
	defer c.shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Fabricate a completed session with an already-expired artifact directly
	// in the shared captures bucket and directory, so the running service's
	// sweeper is the only thing that can purge it. Going through the store
	// writes the record the sweeper lists and the pcapng file it unlinks
	// without needing an enrolled edge to upload one.
	c.mu.Lock()
	hub := c.hub
	c.mu.Unlock()
	if hub == nil {
		t.Fatal("central reported nil hub")
	}
	capturesDir := filepath.Join(dir, "central-state", "captures")
	kv, err := hub.JetStream().KeyValue(ctx, edgebus.CapturesBucket)
	if err != nil {
		t.Fatalf("open captures KV: %v", err)
	}
	store, err := captureapi.NewStore(kv, capturesDir, time.Now)
	if err != nil {
		t.Fatalf("open captures store: %v", err)
	}

	const sessionID = "0192e700-0000-7000-8000-0000000000fe"
	config := modelcapturev1.CaptureSessionConfig_builder{
		Ref: modelcapturev1.CaptureSessionGlobalRef_builder{
			Edge: edgev1.EdgeGlobalRef_builder{
				Edge: edgev1.EdgeLocalRef_builder{Id: proto.String("0192e6a0-0000-7000-8000-00000000dead")}.Build(),
			}.Build(),
			CaptureSession: modelcapturev1.CaptureSessionLocalRef_builder{Id: proto.String(sessionID)}.Build(),
		}.Build(),
		Name: proto.String("sweep-cadence"),
		Source: modelcapturev1.CaptureSource_builder{
			LocalInterface: modelcapturev1.LocalInterfaceSource_builder{InterfaceName: proto.String("eth0")}.Build(),
		}.Build(),
	}.Build()
	if _, err := store.CreateSession(ctx, edgebus.DefaultTenant, config); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	const linkType = netcapturev1.LinkType_LINK_TYPE_ETHERNET
	packet := netcapturev1.PacketRecord_builder{
		CapturedAt:     timestamppb.Now(),
		OriginalLength: proto.Uint32(uint32(len("sweep"))),
		Data:           []byte("sweep"),
	}.Build()
	if err := store.AppendPackets(ctx, edgebus.DefaultTenant, sessionID, linkType, 128, []*netcapturev1.PacketRecord{packet}); err != nil {
		t.Fatalf("AppendPackets: %v", err)
	}
	counters := netcapturev1.CaptureCounters_builder{ReceivedPackets: proto.Uint64(1), AcceptedPackets: proto.Uint64(1)}.Build()
	artifact, err := store.FinalizeArtifact(ctx, edgebus.DefaultTenant, sessionID, linkType, 128, counters, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("FinalizeArtifact: %v", err)
	}
	if _, err := store.MutateSession(ctx, edgebus.DefaultTenant, sessionID, func(rec *modelcapturev1.CaptureSessionRecord) error {
		rec.GetState().SetLifecycle(modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_COMPLETED)
		rec.GetState().SetArtifact(artifact)
		return nil
	}); err != nil {
		t.Fatalf("MutateSession: %v", err)
	}

	artifactPath := filepath.Join(capturesDir, edgebus.DefaultTenant, sessionID+".pcapng")
	if _, err := os.Stat(artifactPath); err != nil {
		t.Fatalf("expected artifact file on disk at %s: %v", artifactPath, err)
	}

	// The one-second sweeper should unlink the expired payload well within
	// this window; the one-minute default would not.
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(artifactPath); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("artifact %s was not swept within the window; the sweeper ignored the configured interval", artifactPath)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

type integrationCaptureSource struct {
	frames chan rawsocket.Frame
	closed chan struct{}
}

func newIntegrationCaptureSource(buffer int) *integrationCaptureSource {
	return &integrationCaptureSource{
		frames: make(chan rawsocket.Frame, buffer),
		closed: make(chan struct{}),
	}
}

func (s *integrationCaptureSource) pushFrame(data []byte) {
	s.frames <- rawsocket.Frame{
		Data:           data,
		OriginalLength: uint32(len(data)),
		CapturedAt:     time.Now(),
	}
}

func (s *integrationCaptureSource) Receive(_ context.Context) <-chan rawsocket.Frame {
	return s.frames
}

// waitDrained blocks until the engine has taken every pushed frame off the
// channel, so a test that acts on what the engine holds acts after it holds
// it.
func (s *integrationCaptureSource) waitDrained(ctx context.Context, t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if len(s.frames) == 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context ended while waiting for the source to drain: %v", ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatalf("timed out waiting for the engine to drain the source; %d frames left", len(s.frames))
}

func (s *integrationCaptureSource) Stats() (uint64, uint64, error) {
	return 0, 0, nil
}

func (s *integrationCaptureSource) Close() error {
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
	return nil
}

func pollSessionCondition(
	ctx context.Context,
	t *testing.T,
	c *central,
	sessionRef *modelcapturev1.CaptureSessionGlobalRef,
	cond func(*modelcapturev1.CaptureSessionState) bool,
	description string,
) *modelcapturev1.CaptureSessionState {
	t.Helper()

	const timeout = 30 * time.Second
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := c.captures().GetCaptureSession(ctx, connect.NewRequest(operatorcapturev1.GetCaptureSessionRequest_builder{
			Session: sessionRef,
		}.Build()))
		if err == nil {
			state := resp.Msg.GetSession().GetState()
			if cond(state) {
				return state
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context canceled while waiting for %s: %v", description, ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}

	resp, err := c.captures().GetCaptureSession(ctx, connect.NewRequest(operatorcapturev1.GetCaptureSessionRequest_builder{
		Session: sessionRef,
	}.Build()))
	if err != nil {
		t.Fatalf("timed out waiting for %s; GetCaptureSession error: %v", description, err)
	}
	t.Fatalf("timed out waiting for %s; current lifecycle is %v (stop reason: %v, artifact: %v)",
		description, resp.Msg.GetSession().GetState().GetLifecycle(), resp.Msg.GetSession().GetState().GetStopReason(), resp.Msg.GetSession().GetState().GetArtifact() != nil)
	return nil
}

type testLogRecorder struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *testLogRecorder) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *testLogRecorder) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func TestRemotePacketCapture_EndToEndWithAgent(t *testing.T) {
	dir := t.TempDir()
	writeCredentials(t, filepath.Join(dir, "credentials"))

	registryPath := writeRegistry(t, filepath.Join(dir, "registry.textproto"), "0192e6a0-0000-7000-8000-00000000dead", 0)
	c := newCentral(t, dir, registryPath)
	c.start()
	defer c.shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	created, err := c.admin().CreateEdge(ctx, connect.NewRequest(&apiedgev1.CreateEdgeRequest{}))
	if err != nil {
		t.Fatalf("CreateEdge: %v", err)
	}
	edgeID := created.Msg.GetEdge().GetConfig().GetRef().GetEdge().GetId()
	provisioning := created.Msg.GetProvisioning()

	c.shutdown()
	writeRegistry(t, registryPath, edgeID, 0)
	c.start()

	source := newIntegrationCaptureSource(20)
	for i := 1; i <= 10; i++ {
		source.pushFrame([]byte(fmt.Sprintf("synthetic-agent-frame-%d", i)))
	}

	a := startAgentWithOptions(t, dir, provisioning, c.baseURL(), agentOptions{
		openCaptureSource: func(_ context.Context, _ capture.Config) (capture.Source, bool, error) {
			return source, false, nil
		},
	})
	defer a.shutdown()

	createReq := operatorcapturev1.CreateCaptureSessionRequest_builder{
		Edge: edgev1.EdgeGlobalRef_builder{
			Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(edgeID)}.Build(),
		}.Build(),
		Name:        proto.String("agent-e2e-capture"),
		Description: proto.String("live agent packet capture test"),
		Source: modelcapturev1.CaptureSource_builder{
			LocalInterface: modelcapturev1.LocalInterfaceSource_builder{
				InterfaceName: proto.String("eth0"),
				Promiscuous:   proto.Bool(true),
			}.Build(),
		}.Build(),
		Budget: modelcapturev1.CaptureBudget_builder{
			MaxPackets: proto.Uint64(10),
		}.Build(),
		Authorization: modelcapturev1.CaptureAuthorization_builder{
			RequestedBy: identityv1.OperatorRef_builder{
				Issuer:  proto.String("https://auth.example.com"),
				Subject: proto.String("zitadel|usr_123"),
			}.Build(),
			Reason:               proto.String("e2e agent test"),
			FullPayloadRequested: proto.Bool(false),
		}.Build(),
	}.Build()

	createResp, err := c.captures().CreateCaptureSession(ctx, connect.NewRequest(createReq))
	if err != nil {
		t.Fatalf("CreateCaptureSession: %v", err)
	}
	sessionRef := createResp.Msg.GetSession().GetConfig().GetRef()

	state := pollSessionCondition(ctx, t, c, sessionRef, func(s *modelcapturev1.CaptureSessionState) bool {
		return s.GetLifecycle() == modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_COMPLETED && s.GetArtifact() != nil
	}, "COMPLETED with artifact")

	if state.GetStopReason() != modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_PACKET_COUNT {
		t.Errorf("stop reason = %v, want PACKET_COUNT", state.GetStopReason())
	}
	if state.GetCounters().GetAcceptedPackets() != 10 {
		t.Errorf("accepted packets = %d, want 10", state.GetCounters().GetAcceptedPackets())
	}

	downloadReq := connect.NewRequest(operatorcapturev1.DownloadCaptureSessionRequest_builder{
		Session: sessionRef,
	}.Build())
	downloadStream, err := c.captures().DownloadCaptureSession(ctx, downloadReq)
	if err != nil {
		t.Fatalf("DownloadCaptureSession: %v", err)
	}
	defer func() { _ = downloadStream.Close() }()

	var downloaded []byte
	for downloadStream.Receive() {
		chunk := downloadStream.Msg().GetChunk()
		if chunk != nil {
			downloaded = append(downloaded, chunk.GetData()...)
		}
	}
	if err := downloadStream.Err(); err != nil {
		t.Fatalf("download stream: %v", err)
	}

	pcapngMagic := []byte{0x0a, 0x0d, 0x0d, 0x0a}
	if !bytes.HasPrefix(downloaded, pcapngMagic) {
		t.Fatal("downloaded artifact missing pcapng magic header")
	}
	for i := 1; i <= 10; i++ {
		payload := []byte(fmt.Sprintf("synthetic-agent-frame-%d", i))
		if !bytes.Contains(downloaded, payload) {
			t.Errorf("downloaded artifact missing frame payload %q", payload)
		}
	}
}

func TestRemotePacketCapture_OperatorCancellation(t *testing.T) {
	dir := t.TempDir()
	writeCredentials(t, filepath.Join(dir, "credentials"))

	registryPath := writeRegistry(t, filepath.Join(dir, "registry.textproto"), "0192e6a0-0000-7000-8000-00000000dead", 0)
	c := newCentral(t, dir, registryPath)
	c.start()
	defer c.shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	created, err := c.admin().CreateEdge(ctx, connect.NewRequest(&apiedgev1.CreateEdgeRequest{}))
	if err != nil {
		t.Fatalf("CreateEdge: %v", err)
	}
	edgeID := created.Msg.GetEdge().GetConfig().GetRef().GetEdge().GetId()
	provisioning := created.Msg.GetProvisioning()

	c.shutdown()
	writeRegistry(t, registryPath, edgeID, 0)
	c.start()

	source := newIntegrationCaptureSource(50)
	for i := 1; i <= 20; i++ {
		source.pushFrame([]byte(fmt.Sprintf("cancel-frame-%d", i)))
	}

	a := startAgentWithOptions(t, dir, provisioning, c.baseURL(), agentOptions{
		openCaptureSource: func(_ context.Context, _ capture.Config) (capture.Source, bool, error) {
			return source, false, nil
		},
	})
	defer a.shutdown()

	createReq := operatorcapturev1.CreateCaptureSessionRequest_builder{
		Edge: edgev1.EdgeGlobalRef_builder{
			Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(edgeID)}.Build(),
		}.Build(),
		Name:        proto.String("cancel-capture"),
		Description: proto.String("operator cancellation test"),
		Source: modelcapturev1.CaptureSource_builder{
			LocalInterface: modelcapturev1.LocalInterfaceSource_builder{
				InterfaceName: proto.String("eth0"),
				Promiscuous:   proto.Bool(true),
			}.Build(),
		}.Build(),
		Budget: modelcapturev1.CaptureBudget_builder{
			MaxPackets: proto.Uint64(1000),
		}.Build(),
		Authorization: modelcapturev1.CaptureAuthorization_builder{
			RequestedBy: identityv1.OperatorRef_builder{
				Issuer:  proto.String("https://auth.example.com"),
				Subject: proto.String("zitadel|usr_123"),
			}.Build(),
			Reason:               proto.String("cancellation test"),
			FullPayloadRequested: proto.Bool(false),
		}.Build(),
	}.Build()

	createResp, err := c.captures().CreateCaptureSession(ctx, connect.NewRequest(createReq))
	if err != nil {
		t.Fatalf("CreateCaptureSession: %v", err)
	}
	sessionRef := createResp.Msg.GetSession().GetConfig().GetRef()

	// Wait until running (agent opened UploadCapture and sent first chunk)
	pollSessionCondition(ctx, t, c, sessionRef, func(s *modelcapturev1.CaptureSessionState) bool {
		return s.GetLifecycle() == modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_RUNNING
	}, "RUNNING")

	// RUNNING is set by the agent's zero-packet opening chunk, which precedes
	// the engine start, so stopping on it alone races the first frame and the
	// artifact assertion below would be a coin toss. Wait for the engine to
	// take every frame off the source instead: what this test is about is
	// that the stop flushes what the engine had buffered, which needs the
	// engine to have buffered something.
	source.waitDrained(ctx, t)

	// Operator stops session
	stopReq := operatorcapturev1.StopCaptureSessionRequest_builder{
		Session: sessionRef,
	}.Build()
	if _, err := c.captures().StopCaptureSession(ctx, connect.NewRequest(stopReq)); err != nil {
		t.Fatalf("StopCaptureSession: %v", err)
	}

	state := pollSessionCondition(ctx, t, c, sessionRef, func(s *modelcapturev1.CaptureSessionState) bool {
		return s.GetLifecycle() == modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_CANCELED && s.GetArtifact() != nil
	}, "CANCELED with artifact")

	if state.GetStopReason() != modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_OPERATOR {
		t.Errorf("stop reason = %v, want OPERATOR", state.GetStopReason())
	}
	if state.GetArtifact() == nil {
		t.Fatal("expected non-nil artifact attached to canceled session")
	}

	downloadReq := connect.NewRequest(operatorcapturev1.DownloadCaptureSessionRequest_builder{
		Session: sessionRef,
	}.Build())
	downloadStream, err := c.captures().DownloadCaptureSession(ctx, downloadReq)
	if err != nil {
		t.Fatalf("DownloadCaptureSession: %v", err)
	}
	defer func() { _ = downloadStream.Close() }()

	var downloaded []byte
	for downloadStream.Receive() {
		chunk := downloadStream.Msg().GetChunk()
		if chunk != nil {
			downloaded = append(downloaded, chunk.GetData()...)
		}
	}
	if err := downloadStream.Err(); err != nil {
		t.Fatalf("download stream: %v", err)
	}

	pcapngMagic := []byte{0x0a, 0x0d, 0x0d, 0x0a}
	if !bytes.HasPrefix(downloaded, pcapngMagic) {
		t.Fatal("downloaded artifact missing pcapng magic header")
	}
	// A header-only pcapng satisfies every check above, and is what a stop
	// that canceled the engine without flushing its buffer would produce.
	if !bytes.Contains(downloaded, []byte("cancel-frame-1")) {
		t.Error("the canceled session's artifact carries no captured frame; the final flush lost what the engine held")
	}
	if n := state.GetArtifact().GetPacketCount(); n == 0 {
		t.Error("artifact packet count = 0, want the frames buffered before the stop")
	}
}

func TestRemotePacketCapture_InactivityTimeout(t *testing.T) {
	dir := t.TempDir()
	writeCredentials(t, filepath.Join(dir, "credentials"))

	registryPath := writeRegistry(t, filepath.Join(dir, "registry.textproto"), "0192e6a0-0000-7000-8000-00000000dead", 0)
	c := newCentral(t, dir, registryPath)
	c.start()
	defer c.shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	created, err := c.admin().CreateEdge(ctx, connect.NewRequest(&apiedgev1.CreateEdgeRequest{}))
	if err != nil {
		t.Fatalf("CreateEdge: %v", err)
	}
	edgeID := created.Msg.GetEdge().GetConfig().GetRef().GetEdge().GetId()
	provisioning := created.Msg.GetProvisioning()

	c.shutdown()
	writeRegistry(t, registryPath, edgeID, 0)
	c.start()

	// Empty source: no frames ever pushed
	source := newIntegrationCaptureSource(10)

	a := startAgentWithOptions(t, dir, provisioning, c.baseURL(), agentOptions{
		openCaptureSource: func(_ context.Context, _ capture.Config) (capture.Source, bool, error) {
			return source, false, nil
		},
		captureInactivityTimeout: 1 * time.Second,
	})
	defer a.shutdown()

	createReq := operatorcapturev1.CreateCaptureSessionRequest_builder{
		Edge: edgev1.EdgeGlobalRef_builder{
			Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(edgeID)}.Build(),
		}.Build(),
		Name:        proto.String("idle-capture"),
		Description: proto.String("inactivity timeout test"),
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
			RequestedBy: identityv1.OperatorRef_builder{
				Issuer:  proto.String("https://auth.example.com"),
				Subject: proto.String("zitadel|usr_123"),
			}.Build(),
			Reason:               proto.String("inactivity test"),
			FullPayloadRequested: proto.Bool(false),
		}.Build(),
	}.Build()

	createResp, err := c.captures().CreateCaptureSession(ctx, connect.NewRequest(createReq))
	if err != nil {
		t.Fatalf("CreateCaptureSession: %v", err)
	}
	sessionRef := createResp.Msg.GetSession().GetConfig().GetRef()

	// No poll for RUNNING on the way: the agent holds it for one inactivity
	// timeout, and a poll that lost that second to a scheduling stall would
	// fail on FAILED, which is the state this test is waiting for.
	//
	// After 1s of inactivity, agent aborts upload without final chunk,
	// and central transitions session to FAILED with stop_reason: ERROR.
	state := pollSessionCondition(ctx, t, c, sessionRef, func(s *modelcapturev1.CaptureSessionState) bool {
		return s.GetLifecycle() == modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_FAILED
	}, "FAILED")

	if state.GetStopReason() != modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_ERROR {
		t.Errorf("stop reason = %v, want ERROR", state.GetStopReason())
	}
	if state.GetArtifact() != nil {
		t.Error("expected nil artifact on failed session")
	}
}

func TestRemotePacketCapture_TelemetryPrivacy(t *testing.T) {
	dir := t.TempDir()
	writeCredentials(t, filepath.Join(dir, "credentials"))

	registryPath := writeRegistry(t, filepath.Join(dir, "registry.textproto"), "0192e6a0-0000-7000-8000-00000000dead", 0)
	c := newCentral(t, dir, registryPath)
	c.start()
	defer c.shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	created, err := c.admin().CreateEdge(ctx, connect.NewRequest(&apiedgev1.CreateEdgeRequest{}))
	if err != nil {
		t.Fatalf("CreateEdge: %v", err)
	}
	edgeID := created.Msg.GetEdge().GetConfig().GetRef().GetEdge().GetId()
	provisioning := created.Msg.GetProvisioning()

	c.shutdown()
	writeRegistry(t, registryPath, edgeID, 0)
	c.start()

	secretMarker := "TOPSECRET_PAYLOAD_MARKER_998877"
	source := newIntegrationCaptureSource(120)
	for i := 1; i <= 100; i++ {
		source.pushFrame([]byte(fmt.Sprintf("%s_packet_%03d", secretMarker, i)))
	}

	logSink := &testLogRecorder{}
	logger := slog.New(slog.NewJSONHandler(logSink, &slog.HandlerOptions{Level: slog.LevelDebug}))

	a := startAgentWithOptions(t, dir, provisioning, c.baseURL(), agentOptions{
		openCaptureSource: func(_ context.Context, _ capture.Config) (capture.Source, bool, error) {
			return source, false, nil
		},
		logger: logger,
	})
	defer a.shutdown()

	createReq := operatorcapturev1.CreateCaptureSessionRequest_builder{
		Edge: edgev1.EdgeGlobalRef_builder{
			Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(edgeID)}.Build(),
		}.Build(),
		Name:        proto.String("privacy-capture"),
		Description: proto.String("telemetry privacy test"),
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
			RequestedBy: identityv1.OperatorRef_builder{
				Issuer:  proto.String("https://auth.example.com"),
				Subject: proto.String("zitadel|usr_123"),
			}.Build(),
			Reason:               proto.String("privacy test"),
			FullPayloadRequested: proto.Bool(false),
		}.Build(),
	}.Build()

	createResp, err := c.captures().CreateCaptureSession(ctx, connect.NewRequest(createReq))
	if err != nil {
		t.Fatalf("CreateCaptureSession: %v", err)
	}
	sessionRef := createResp.Msg.GetSession().GetConfig().GetRef()
	sessionID := sessionRef.GetCaptureSession().GetId()

	pollSessionCondition(ctx, t, c, sessionRef, func(s *modelcapturev1.CaptureSessionState) bool {
		return s.GetLifecycle() == modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_COMPLETED && s.GetArtifact() != nil
	}, "COMPLETED with artifact")

	// Verify artifact contains payload
	downloadReq := connect.NewRequest(operatorcapturev1.DownloadCaptureSessionRequest_builder{
		Session: sessionRef,
	}.Build())
	downloadStream, err := c.captures().DownloadCaptureSession(ctx, downloadReq)
	if err != nil {
		t.Fatalf("DownloadCaptureSession: %v", err)
	}
	defer func() { _ = downloadStream.Close() }()

	var downloaded []byte
	for downloadStream.Receive() {
		chunk := downloadStream.Msg().GetChunk()
		if chunk != nil {
			downloaded = append(downloaded, chunk.GetData()...)
		}
	}
	if !bytes.Contains(downloaded, []byte(secretMarker)) {
		t.Fatal("downloaded artifact does not contain the packet payload")
	}

	// The screen has to see a leak in the shape a leak would take. A
	// slog.JSONHandler writes a []byte attribute base64-encoded, and a
	// hex-rendered one is what a handler dumping a frame tends to produce, so
	// searching for the plain marker alone would pass over both.
	logs := logSink.String()
	for name, encoded := range map[string]string{
		"plain":  secretMarker,
		"base64": base64.StdEncoding.EncodeToString([]byte(secretMarker)),
		"hex":    fmt.Sprintf("%x", secretMarker),
	} {
		if strings.Contains(logs, encoded) {
			t.Errorf("agent logs leaked packet payload bytes (%s encoding)", name)
		}
	}

	// What the logs must carry instead, which is also what proves the screen
	// above ran against records that describe this capture rather than an
	// empty buffer.
	if !strings.Contains(logs, sessionID) {
		t.Errorf("agent logs missing session ID %q", sessionID)
	}
	if !strings.Contains(logs, "flowseer.capture.chunk.first_sequence") {
		t.Error("agent logs carry no chunk records; the payload screen proved nothing")
	}
}

func TestCapture_PerTenantArtifactDirectoryAndCrossTenantIsolation(t *testing.T) {
	dir := t.TempDir()
	writeCredentials(t, filepath.Join(dir, "credentials"))

	registryPath := writeRegistry(t, filepath.Join(dir, "registry.textproto"), "0192e6a0-0000-7000-8000-00000000dead", 0)
	c := newCentral(t, dir, registryPath)

	tenantA := "0192e6a0-aaaa-7000-8000-0000000000aa"
	tenantB := "0192e6a0-bbbb-7000-8000-0000000000bb"
	var sessionID string

	c.startWithTenant(tenantA)
	defer c.shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c.mu.Lock()
	hub := c.hub
	c.mu.Unlock()
	if hub == nil {
		t.Fatal("central reported nil hub")
	}

	kv, err := hub.JetStream().KeyValue(ctx, edgebus.CapturesBucket)
	if err != nil {
		t.Fatalf("open captures KV: %v", err)
	}

	capturesDir := filepath.Join(dir, "central-state", "captures")
	store, err := captureapi.NewStore(kv, capturesDir, time.Now)
	if err != nil {
		t.Fatalf("open captures store: %v", err)
	}

	created, err := c.admin().CreateEdge(ctx, connect.NewRequest(apiedgev1.CreateEdgeRequest_builder{
		Name: proto.String("cross-tenant-edge"),
	}.Build()))
	if err != nil {
		t.Fatalf("CreateEdge: %v", err)
	}
	edgeRef := created.Msg.GetEdge().GetConfig().GetRef()

	sessionResp, err := c.captures().CreateCaptureSession(ctx, connect.NewRequest(operatorcapturev1.CreateCaptureSessionRequest_builder{
		Edge: edgeRef,
		Name: proto.String("cross-tenant-e2e"),
		Source: modelcapturev1.CaptureSource_builder{
			LocalInterface: modelcapturev1.LocalInterfaceSource_builder{InterfaceName: proto.String("eth0")}.Build(),
		}.Build(),
		Budget: modelcapturev1.CaptureBudget_builder{
			MaxPackets: proto.Uint64(10),
		}.Build(),
		Authorization: modelcapturev1.CaptureAuthorization_builder{
			Reason:               proto.String("e2e-cross-tenant"),
			FullPayloadRequested: proto.Bool(false),
		}.Build(),
	}.Build()))
	if err != nil {
		t.Fatalf("CreateCaptureSession: %v", err)
	}
	sessionRef := sessionResp.Msg.GetSession().GetConfig().GetRef()
	sessionID = sessionRef.GetCaptureSession().GetId()

	linkType := netcapturev1.LinkType_LINK_TYPE_ETHERNET
	packet := netcapturev1.PacketRecord_builder{
		CapturedAt:     timestamppb.Now(),
		OriginalLength: proto.Uint32(uint32(len("payload-tenant-a"))),
		Data:           []byte("payload-tenant-a"),
	}.Build()
	if err := store.AppendPackets(ctx, tenantA, sessionID, linkType, 128, []*netcapturev1.PacketRecord{packet}); err != nil {
		t.Fatalf("AppendPackets: %v", err)
	}
	counters := netcapturev1.CaptureCounters_builder{ReceivedPackets: proto.Uint64(1), AcceptedPackets: proto.Uint64(1)}.Build()
	artifact, err := store.FinalizeArtifact(ctx, tenantA, sessionID, linkType, 128, counters, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("FinalizeArtifact: %v", err)
	}
	if _, err := store.MutateSession(ctx, tenantA, sessionID, func(rec *modelcapturev1.CaptureSessionRecord) error {
		rec.GetState().SetLifecycle(modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_COMPLETED)
		rec.GetState().SetArtifact(artifact)
		return nil
	}); err != nil {
		t.Fatalf("MutateSession: %v", err)
	}

	// Verify artifact file landed under <capturesDir>/<tenantA>/<sessionID>.pcapng
	expectedPathA := filepath.Join(capturesDir, tenantA, sessionID+".pcapng")
	if _, err := os.Stat(expectedPathA); err != nil {
		t.Fatalf("expected artifact on disk at %s: %v", expectedPathA, err)
	}

	// Verify tenant A can download artifact over Connect RPC and trail is recorded
	recordsBefore := len(c.operatorActionRecords(t))
	streamA, err := c.captures().DownloadCaptureSession(ctx, connect.NewRequest(operatorcapturev1.DownloadCaptureSessionRequest_builder{
		Session: sessionRef,
	}.Build()))
	if err != nil {
		t.Fatalf("DownloadCaptureSession tenant A: %v", err)
	}
	if !streamA.Receive() {
		t.Fatalf("tenant A download stream had no chunks: %v", streamA.Err())
	}
	for streamA.Receive() {
	}
	if streamA.Err() != nil {
		t.Fatalf("download stream error: %v", streamA.Err())
	}

	downloadRecords := c.operatorActionRecords(t)[recordsBefore:]
	if len(downloadRecords) != 2 {
		t.Fatalf("got %d operator action records for download, want 2", len(downloadRecords))
	}
	if downloadRecords[0].Event.GetAction() != operatorv1.OperatorAction_OPERATOR_ACTION_CAPTURE_DOWNLOAD {
		t.Errorf("attempt action = %v, want CAPTURE_DOWNLOAD", downloadRecords[0].Event.GetAction())
	}
	if downloadRecords[1].Event.GetCompleted().GetOutcome() != operatorv1.OperatorActionOutcome_OPERATOR_ACTION_OUTCOME_SUCCEEDED {
		t.Errorf("completion outcome = %v, want SUCCEEDED", downloadRecords[1].Event.GetCompleted().GetOutcome())
	}
	if downloadRecords[0].Event.GetCallId() == "" || downloadRecords[0].Event.GetCallId() != downloadRecords[1].Event.GetCallId() {
		t.Fatalf("call_id mismatch: attempt=%q, completion=%q", downloadRecords[0].Event.GetCallId(), downloadRecords[1].Event.GetCallId())
	}
	wantOp := identityv1.OperatorRef_builder{
		Issuer:  proto.String(c.issuer.URL()),
		Subject: proto.String("e2e-operator"),
	}.Build()
	if !proto.Equal(downloadRecords[0].Event.GetOperator(), wantOp) {
		t.Errorf("attempt operator = %v, want %v", downloadRecords[0].Event.GetOperator(), wantOp)
	}
	if !proto.Equal(downloadRecords[1].Event.GetOperator(), wantOp) {
		t.Errorf("completion operator = %v, want %v", downloadRecords[1].Event.GetOperator(), wantOp)
	}
	if downloadRecords[0].Event.GetCaptureSession().GetCaptureSession().GetId() != sessionID {
		t.Errorf("attempt session id = %q, want %q", downloadRecords[0].Event.GetCaptureSession().GetCaptureSession().GetId(), sessionID)
	}
	if downloadRecords[1].Event.GetCaptureSession().GetCaptureSession().GetId() != sessionID {
		t.Errorf("completion session id = %q, want %q", downloadRecords[1].Event.GetCaptureSession().GetCaptureSession().GetId(), sessionID)
	}
	wantSubject := "flowseer." + tenantA + ".operator.action.capture_download"
	if downloadRecords[0].Subject != wantSubject {
		t.Errorf("attempt subject = %q, want %q", downloadRecords[0].Subject, wantSubject)
	}
	if downloadRecords[1].Subject != wantSubject {
		t.Errorf("completion subject = %q, want %q", downloadRecords[1].Subject, wantSubject)
	}

	// Verify tenant B cannot read artifact directly from store
	err = store.ReadArtifact(ctx, tenantB, sessionID, func(_ *modelcapturev1.CaptureArtifactChunk) error {
		return nil
	})
	if err == nil {
		t.Fatal("tenant B was able to read tenant A artifact directly from store")
	}

	// Restart central under tenant B over the same state dir
	c.shutdown()
	c.startWithTenant(tenantB)

	// Verify cross-tenant download isolation over Connect RPC
	reqB := connect.NewRequest(operatorcapturev1.DownloadCaptureSessionRequest_builder{
		Session: sessionRef,
	}.Build())
	streamB, err := c.captures().DownloadCaptureSession(ctx, reqB)
	if err == nil {
		if streamB.Receive() {
			t.Fatal("tenant B received chunk for tenant A capture session")
		}
		if code := connect.CodeOf(streamB.Err()); code != connect.CodePermissionDenied {
			t.Fatalf("tenant B download stream code = %v, want CodePermissionDenied", code)
		}
	} else if code := connect.CodeOf(err); code != connect.CodePermissionDenied {
		t.Fatalf("tenant B download got code = %v, want CodePermissionDenied", code)
	}

	// Verify GetCaptureSession under tenant B returns CodePermissionDenied
	_, err = c.captures().GetCaptureSession(ctx, connect.NewRequest(operatorcapturev1.GetCaptureSessionRequest_builder{
		Session: sessionRef,
	}.Build()))
	if code := connect.CodeOf(err); code != connect.CodePermissionDenied {
		t.Fatalf("GetCaptureSession under tenant B got code = %v, want CodePermissionDenied", code)
	}
}

func TestDownloadAndTailWithoutDownloadPermissionDenied(t *testing.T) {
	dir := t.TempDir()
	writeCredentials(t, filepath.Join(dir, "credentials"))

	registryPath := writeRegistry(t, filepath.Join(dir, "registry.textproto"), "0192e6a0-0000-7000-8000-00000000dead", 0)
	c := newCentral(t, dir, registryPath)
	c.start()
	defer c.shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	created, err := c.admin().CreateEdge(ctx, connect.NewRequest(apiedgev1.CreateEdgeRequest_builder{
		Name: proto.String("edge-cap"),
	}.Build()))
	if err != nil {
		t.Fatalf("CreateEdge: %v", err)
	}
	edgeRef := created.Msg.GetEdge().GetConfig().GetRef()

	sessionResp, err := c.captures().CreateCaptureSession(ctx, connect.NewRequest(operatorcapturev1.CreateCaptureSessionRequest_builder{
		Edge: edgeRef,
		Name: proto.String("session-test"),
		Source: modelcapturev1.CaptureSource_builder{
			LocalInterface: modelcapturev1.LocalInterfaceSource_builder{InterfaceName: proto.String("eth0")}.Build(),
		}.Build(),
		Budget: modelcapturev1.CaptureBudget_builder{
			MaxPackets: proto.Uint64(10),
		}.Build(),
		Authorization: modelcapturev1.CaptureAuthorization_builder{
			Reason:               proto.String("test"),
			FullPayloadRequested: proto.Bool(false),
		}.Build(),
	}.Build()))
	if err != nil {
		t.Fatalf("CreateCaptureSession: %v", err)
	}
	sessionRef := sessionResp.Msg.GetSession().GetConfig().GetRef()

	// Create a token for a user with NO download grant
	noDownloadToken := c.issuer.Sign(map[string]any{
		"iss": c.issuer.URL(),
		"sub": "operator-no-download",
		"aud": "flowseer-e2e",
		"exp": time.Now().Add(24 * time.Hour).Unix(),
	})
	noDownloadPrincipalID := authn.ComputePrincipalID(c.issuer.URL(), "operator-no-download")
	if err := c.engine.Write(ctx, []authz.Tuple{
		{Object: "tenant:" + edgebus.DefaultTenant, Relation: "member", User: "user:" + noDownloadPrincipalID},
	}, nil); err != nil {
		t.Fatal(err)
	}
	c.engine.Grant("user:"+noDownloadPrincipalID, "view", "capture_session")
	c.engine.Grant("user:"+noDownloadPrincipalID, "manage", "capture_session")

	noDownloadClient := authClient(filepath.Join(c.dir, "central-state", "tls.crt"), noDownloadToken, edgebus.DefaultTenant)
	capturesClient := capturev1connect.NewCaptureServiceClient(noDownloadClient, c.baseURL())

	// Download should be refused with CodePermissionDenied
	downloadStream, err := capturesClient.DownloadCaptureSession(ctx, connect.NewRequest(operatorcapturev1.DownloadCaptureSessionRequest_builder{
		Session: sessionRef,
	}.Build()))
	if err == nil {
		if downloadStream.Receive() {
			t.Fatal("expected download to be refused, but received data")
		}
		if connect.CodeOf(downloadStream.Err()) != connect.CodePermissionDenied {
			t.Fatalf("download stream error code = %v, want CodePermissionDenied", connect.CodeOf(downloadStream.Err()))
		}
	} else if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("download error code = %v, want CodePermissionDenied", connect.CodeOf(err))
	}

	// Tail should be refused with CodePermissionDenied
	tailStream, err := capturesClient.TailCaptureSession(ctx, connect.NewRequest(operatorcapturev1.TailCaptureSessionRequest_builder{
		Session: sessionRef,
	}.Build()))
	if err == nil {
		if tailStream.Receive() {
			t.Fatal("expected tail to be refused, but received data")
		}
		if connect.CodeOf(tailStream.Err()) != connect.CodePermissionDenied {
			t.Fatalf("tail stream error code = %v, want CodePermissionDenied", connect.CodeOf(tailStream.Err()))
		}
	} else if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("tail error code = %v, want CodePermissionDenied", connect.CodeOf(err))
	}

	// Positive control: GetCaptureSession succeeds with view grant
	getResp, err := capturesClient.GetCaptureSession(ctx, connect.NewRequest(operatorcapturev1.GetCaptureSessionRequest_builder{
		Session: sessionRef,
	}.Build()))
	if err != nil {
		t.Fatalf("GetCaptureSession with view grant: %v", err)
	}
	if getResp.Msg.GetSession().GetConfig().GetRef().GetCaptureSession().GetId() != sessionRef.GetCaptureSession().GetId() {
		t.Fatalf("GetCaptureSession ID = %q, want %q", getResp.Msg.GetSession().GetConfig().GetRef().GetCaptureSession().GetId(), sessionRef.GetCaptureSession().GetId())
	}
}
