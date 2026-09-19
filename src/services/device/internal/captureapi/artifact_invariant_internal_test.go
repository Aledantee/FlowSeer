package captureapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	modelcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	netcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
)

// Three rounds of review each found a defect in the previous round's fix for
// the same thing: which operation may create, truncate, or unlink the pcapng
// file a session's record describes. Each fix was right about the case in
// front of it — O_EXCL for a finalized artifact, an unlink for an abandoned
// writer, a tombstone for a deleted session — and each left a sequence the
// next round found. Patching a fourth case would be the same bet.
//
// So this states the property instead, and enumerates the sequences rather
// than choosing them:
//
//	A pcapng file exists exactly while a session record claims it.
//
// Which unfolds into three checks, run after every step of every sequence:
//
//   - no file without a record: the sweep walks records, so a file no record
//     names holds captured payload that can never expire;
//   - no record claiming a live artifact without its file: a download would
//     serve, or an operator would trust, bytes that are not there;
//   - an artifact's recorded digest matches the bytes on disk: a finalized
//     capture is an audit record and nothing may rewrite it in place.
//
// A sequence that violates one is printed in full, because the sequence is
// the finding.
func TestArtifactFileExistsExactlyWhileARecordClaimsIt(t *testing.T) {
	store, dir := newInvariantStore(t)
	ctx := context.Background()

	now := time.Date(2026, 9, 18, 14, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return now })

	// Every operation that can touch a session's file or its record. Each
	// returns without failing the test: what a step returns is not the
	// property, the state it leaves behind is.
	type step struct {
		name string
		run  func(sessionID string)
	}

	packets := []*netcapturev1.PacketRecord{invariantPacket()}
	counters := netcapturev1.CaptureCounters_builder{
		Received: proto.Uint64(1),
		Accepted: proto.Uint64(1),
	}.Build()
	linkType := netcapturev1.LinkType_LINK_TYPE_ETHERNET

	steps := []step{
		{"create", func(id string) {
			_, _ = store.CreateSession(ctx, invariantConfig(id))
		}},
		{"append", func(id string) {
			_ = store.AppendPackets(ctx, id, linkType, 128, packets)
		}},
		{"finalize", func(id string) {
			artifact, err := store.FinalizeArtifact(ctx, id, linkType, 128, counters, now.Add(time.Hour))
			if err != nil {
				return
			}
			_, _ = store.MutateSession(ctx, id, func(rec *modelcapturev1.CaptureSessionRecord) error {
				rec.GetState().SetLifecycle(modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_COMPLETED)
				rec.GetState().SetStopReason(modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_PACKET_COUNT)
				rec.GetState().SetCounters(counters)
				rec.GetState().SetArtifact(artifact)
				return nil
			})
		}},
		{"finalize_expired", func(id string) {
			artifact, err := store.FinalizeArtifact(ctx, id, linkType, 128, counters, now.Add(-time.Hour))
			if err != nil {
				return
			}
			_, _ = store.MutateSession(ctx, id, func(rec *modelcapturev1.CaptureSessionRecord) error {
				rec.GetState().SetLifecycle(modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_COMPLETED)
				rec.GetState().SetStopReason(modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_PACKET_COUNT)
				rec.GetState().SetCounters(counters)
				rec.GetState().SetArtifact(artifact)
				return nil
			})
		}},
		{"cancel", func(id string) {
			// The record half moves without the file half: an operator's stop.
			_, _ = store.MutateSession(ctx, id, func(rec *modelcapturev1.CaptureSessionRecord) error {
				rec.GetState().SetLifecycle(modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_CANCELED)
				rec.GetState().SetStopReason(modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_OPERATOR)
				return nil
			})
		}},
		{"finalize_record_lost", func(id string) {
			// The artifact is written and its descriptor never reaches the
			// record — the shape every FinalizeArtifact failure path and a
			// failed post-finalize record write leave behind. Whoever wrote
			// the bytes owns them until a record names them.
			if _, err := store.FinalizeArtifact(ctx, id, linkType, 128, counters, now.Add(time.Hour)); err != nil {
				return
			}
			store.DiscardArtifact(id)
		}},
		{"abandon", func(id string) { store.AbandonWriter(id) }},
		{"delete", func(id string) { _ = store.DeleteSession(ctx, id) }},
		{"sweep", func(string) { _, _ = store.SweepExpired(ctx) }},
	}

	// Every sequence opens with a create, because a step against a session
	// that was never created leaves nothing to check, and spending the
	// enumeration on those would buy a third of the depth. Create stays in
	// the alphabet as well, so a re-create after a delete is still walked.
	const depth = 3
	indices := make([]int, depth)
	sequences := 0

	for {
		sessionID := uuid.NewString()
		taken := []string{"create"}
		if _, err := store.CreateSession(ctx, invariantConfig(sessionID)); err != nil {
			t.Fatalf("create: %v", err)
		}
		if failure := checkArtifactInvariant(ctx, store, dir); failure != "" {
			t.Fatalf("after create: %s", failure)
		}
		for _, i := range indices {
			steps[i].run(sessionID)
			taken = append(taken, steps[i].name)
			if failure := checkArtifactInvariant(ctx, store, dir); failure != "" {
				t.Fatalf("after %s: %s", strings.Join(taken, " -> "), failure)
			}
		}
		sequences++

		// Each sequence runs against an empty store, so one sequence's
		// leftovers cannot mask or explain the next one's violation — and so
		// the check above stays a scan of one session rather than of every
		// session walked so far. A session that cannot be cleaned up is
		// itself a violation, since delete is the operator's only way out.
		store.AbandonWriter(sessionID)
		if err := store.DeleteSession(ctx, sessionID); err != nil {
			t.Fatalf("after %s: the sequence left a session delete could not clean up: %v", strings.Join(taken, " -> "), err)
		}
		if failure := checkArtifactInvariant(ctx, store, dir); failure != "" {
			t.Fatalf("after %s and a delete: %s", strings.Join(taken, " -> "), failure)
		}
		if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
			t.Fatalf("after %s and a delete: %d file(s) remain in the captures directory (err %v)",
				strings.Join(taken, " -> "), len(entries), err)
		}

		// Odometer over the step alphabet.
		pos := depth - 1
		for pos >= 0 {
			indices[pos]++
			if indices[pos] < len(steps) {
				break
			}
			indices[pos] = 0
			pos--
		}
		if pos < 0 {
			break
		}
	}

	if want := pow(len(steps), depth); sequences != want {
		t.Fatalf("walked %d sequences, want %d", sequences, want)
	}
	t.Logf("held across %d sequences of a create and %d further operations", sequences, depth)
}

// checkArtifactInvariant returns the first violation it finds, or "".
func checkArtifactInvariant(ctx context.Context, store *Store, dir string) string {
	records, err := store.ListSessions(ctx)
	if err != nil {
		return fmt.Sprintf("ListSessions: %v", err)
	}

	claimed := make(map[string]*modelcapturev1.CaptureSessionRecord, len(records))
	for _, rec := range records {
		claimed[rec.GetConfig().GetRef().GetCaptureSession().GetId()] = rec
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Sprintf("read captures dir: %v", err)
	}
	onDisk := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.Name(), ".pcapng")
		onDisk[id] = struct{}{}

		rec, ok := claimed[id]
		if !ok {
			return fmt.Sprintf("%s is on disk with no session record; the sweep walks records, so its payload can never expire", entry.Name())
		}
		artifact := rec.GetState().GetArtifact()
		switch {
		case artifact == nil && !store.hasOpenWriter(id):
			// A file whose record claims no artifact is legitimate only while
			// a writer owns it. Exempting the state outright would bless the
			// orphan class this property exists to catch: bytes with no
			// writer and no descriptor, which the record-walking sweep can
			// never reach.
			return fmt.Sprintf("%s is on disk with no writer and no artifact descriptor; nothing owns those bytes and no sweep can reach them", entry.Name())
		case artifact != nil && artifact.HasPurgedAt():
			return fmt.Sprintf("%s is on disk although its record says the payload was purged", entry.Name())
		}
	}

	for id, rec := range claimed {
		artifact := rec.GetState().GetArtifact()
		if artifact == nil || artifact.HasPurgedAt() {
			continue
		}
		if _, ok := onDisk[id]; !ok {
			return fmt.Sprintf("session %s claims a retained artifact of %d bytes that is not on disk", id, artifact.GetByteSize())
		}
		if got := fileDigest(filepath.Join(dir, id+".pcapng")); got != "" && got != digestHex(artifact.GetDigest()) {
			return fmt.Sprintf("session %s has an artifact on disk that does not match its recorded digest", id)
		}
	}
	return ""
}

func pow(base, exp int) int {
	out := 1
	for range exp {
		out *= base
	}
	return out
}

func fileDigest(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func digestHex(digest []byte) string {
	return hex.EncodeToString(digest)
}

func newInvariantStore(t *testing.T) (*Store, string) {
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

	dir := filepath.Join(t.TempDir(), "captures")
	store, err := NewStore(kv, dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return store, dir
}

func invariantConfig(sessionID string) *modelcapturev1.CaptureSessionConfig {
	return modelcapturev1.CaptureSessionConfig_builder{
		Ref: modelcapturev1.CaptureSessionGlobalRef_builder{
			Edge: edgev1.EdgeGlobalRef_builder{
				Edge: edgev1.EdgeLocalRef_builder{
					Id: proto.String("0192e6a0-0000-7000-8000-0000000000ed"),
				}.Build(),
			}.Build(),
			CaptureSession: modelcapturev1.CaptureSessionLocalRef_builder{
				Id: proto.String(sessionID),
			}.Build(),
		}.Build(),
		Source: modelcapturev1.CaptureSource_builder{
			LocalInterface: modelcapturev1.LocalInterfaceSource_builder{
				InterfaceName: proto.String("eth0"),
			}.Build(),
		}.Build(),
		Budget: modelcapturev1.CaptureBudget_builder{
			MaxPackets: proto.Uint64(10),
		}.Build(),
		Authorization: modelcapturev1.CaptureAuthorization_builder{
			Operator:             proto.String("alice"),
			Reason:               proto.String("investigation"),
			FullPayloadRequested: proto.Bool(false),
		}.Build(),
	}.Build()
}

func invariantPacket() *netcapturev1.PacketRecord {
	data := []byte("captured payload")
	return netcapturev1.PacketRecord_builder{
		Sequence:       proto.Uint64(1),
		CapturedAt:     timestamppb.Now(),
		OriginalLength: proto.Uint32(uint32(len(data))),
		Data:           data,
	}.Build()
}
