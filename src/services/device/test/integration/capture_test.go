package integration_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	operatorcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1"
	apiedgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	attachv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/attach/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/attach/v1/attachv1connect"
	captureedgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/capture/v1"
	captureedgev1connect "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/capture/v1/capturev1connect"
	modelcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	netcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
	"go.aledante.io/FlowSeer/src/common/spawn"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
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
			Operator:             proto.String("alice"),
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
		t.Fatalf("expected PENDING lifecycle, got: %v", createResp.Msg.GetSession().GetState().GetLifecycle())
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
		t.Fatalf("expected start assignment, got: %+v", subStream.Msg())
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
				Received: proto.Uint64(1),
				Accepted: proto.Uint64(1),
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
				Received: proto.Uint64(2),
				Accepted: proto.Uint64(2),
			}.Build(),
			Final: proto.Bool(true),
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
		t.Fatalf("expected COMPLETED lifecycle, got: %v", sessionState.GetLifecycle())
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
	if sessionState.GetCounters().GetAccepted() != 2 {
		t.Fatalf("session counters accepted = %d, want 2", sessionState.GetCounters().GetAccepted())
	}
	hasher := sha256.New()
	hasher.Write(downloadedData)
	if !bytes.Equal(hasher.Sum(nil), artifact.GetDigest()) {
		t.Fatalf("downloaded data digest mismatch")
	}

	// Retention purges the payload and keeps the record.
	artifactPath := filepath.Join(dir, "central-state", "captures", sessionID+".pcapng")
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
	capturesStore, err := captureapi.NewStore(kv, filepath.Join(dir, "central-state", "captures"))
	if err != nil {
		t.Fatalf("open captures store: %v", err)
	}

	// Mutate session record so expires_at is in the past
	if _, err := capturesStore.MutateSession(ctx, sessionID, func(rec *modelcapturev1.CaptureSessionRecord) error {
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
		t.Fatalf("expected 1 swept artifact, got: %d", swept)
	}

	// Verify artifact file is purged from disk
	if _, err := os.Stat(artifactPath); !os.IsNotExist(err) {
		t.Fatalf("expected artifact file to be unlinked, got err: %v", err)
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
		t.Fatalf("expected CodeNotFound for expired download, got: %v", expiredDownloadStream.Err())
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
		t.Fatalf("expected session to remain COMPLETED, got: %v", afterSweep.GetLifecycle())
	}
	// The retention departure: the payload goes, the record and its counters
	// stay, and the descriptor says when the bytes were purged.
	if afterSweep.GetCounters().GetAccepted() != 2 {
		t.Fatalf("counters did not survive the sweep: accepted = %d, want 2", afterSweep.GetCounters().GetAccepted())
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
