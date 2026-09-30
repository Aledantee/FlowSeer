package tenantstore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/synadia-io/orbit.go/jetstreamext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
)

const (
	claimIssuer = "https://idp.example.test"
	claimBucket = edgebus.TenantBucket
)

type claimFixture struct {
	store  *Store
	kv     jetstream.KeyValue
	stream jetstream.Stream
	js     jetstream.JetStream
}

func newClaimFixture(t *testing.T) *claimFixture {
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
	js := hub.JetStream()
	store, err := New(context.Background(), js, claimBucket)
	if err != nil {
		t.Fatalf("new tenant store: %v", err)
	}
	kv, err := js.KeyValue(context.Background(), claimBucket)
	if err != nil {
		t.Fatalf("open tenant bucket: %v", err)
	}
	stream, err := js.Stream(context.Background(), "KV_"+claimBucket)
	if err != nil {
		t.Fatalf("open tenant stream: %v", err)
	}
	return &claimFixture{store: store, kv: kv, stream: stream, js: js}
}

func claimConfig(id, org string) *identityv1.TenantConfig {
	return claimConfigWithName(id, org, "Claim test tenant")
}

func claimConfigWithName(id, org, name string) *identityv1.TenantConfig {
	return identityv1.TenantConfig_builder{
		Ref: identityv1.TenantGlobalRef_builder{
			Tenant: identityv1.TenantLocalRef_builder{Id: proto.String(id)}.Build(),
		}.Build(),
		Issuer:                 proto.String(claimIssuer),
		OrganizationClaimName:  proto.String("org_id"),
		OrganizationClaimValue: proto.String(org),
		Name:                   proto.String(name),
		Description:            proto.String("Tenant used by claim property tests"),
	}.Build()
}

func claimRecord(config *identityv1.TenantConfig) *identityv1.TenantRecord {
	lifecycle := identityv1.TenantLifecycle_TENANT_LIFECYCLE_ACTIVE
	return identityv1.TenantRecord_builder{
		Config: config,
		State: identityv1.TenantState_builder{
			Ref:       config.GetRef(),
			Lifecycle: &lifecycle,
			CreatedAt: timestamppb.New(time.Unix(1, 0)),
		}.Build(),
	}.Build()
}

func claimSubject(key string) string {
	return "$KV." + claimBucket + "." + key
}

func rawLast(t *testing.T, stream jetstream.Stream, key string) *jetstream.RawStreamMsg {
	t.Helper()
	msg, err := stream.GetLastMsgForSubject(context.Background(), claimSubject(key))
	if errors.Is(err, jetstream.ErrMsgNotFound) {
		return nil
	}
	if err != nil {
		t.Fatalf("last message for %s: %v", key, err)
	}
	return msg
}

type claimMessageSnapshot struct {
	sequence uint64
	data     []byte
	marker   bool
}

type claimInvariantOptions struct {
	target          *identityv1.TenantConfig
	targetCommitted bool
	allowedOrphans  map[string]claimMessageSnapshot
}

func claimSnapshot(t *testing.T, fixture *claimFixture) map[string]claimMessageSnapshot {
	t.Helper()
	info, err := fixture.stream.Info(context.Background(), jetstream.WithSubjectFilter(claimSubject(">")))
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	snapshot := make(map[string]claimMessageSnapshot, len(info.State.Subjects))
	for subject := range info.State.Subjects {
		key := strings.TrimPrefix(subject, claimSubject(""))
		msg := rawLast(t, fixture.stream, key)
		if msg == nil {
			continue
		}
		snapshot[key] = claimMessageSnapshot{
			sequence: msg.Sequence,
			data:     append([]byte(nil), msg.Data...),
			marker:   isMarker(msg),
		}
	}
	return snapshot
}

func claimStreamLastSequence(t *testing.T, fixture *claimFixture) uint64 {
	t.Helper()
	info, err := fixture.stream.Info(context.Background())
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	return info.State.LastSeq
}

func assertNoNewOperations(t *testing.T, fixture *claimFixture, beforeSequence uint64) {
	t.Helper()
	afterSequence := claimStreamLastSequence(t, fixture)
	for sequence := beforeSequence + 1; sequence <= afterSequence; sequence++ {
		msg, err := fixture.stream.GetMsg(context.Background(), sequence)
		if err != nil {
			t.Fatalf("read stream message %d: %v", sequence, err)
		}
		if operation := msg.Header.Get("KV-Operation"); operation != "" {
			t.Fatalf("Create added %s operation at sequence %d", operation, sequence)
		}
	}
}

func assertCommittedSnapshotUnchanged(t *testing.T, fixture *claimFixture, before map[string]claimMessageSnapshot) {
	t.Helper()
	protected := make(map[string]struct{})
	for key, current := range before {
		if current.marker || !strings.HasPrefix(key, orgIndexPrefix) {
			continue
		}
		id := string(current.data)
		record, ok := before[id]
		if !ok || record.marker {
			continue
		}
		protected[key] = struct{}{}
		protected[id] = struct{}{}
	}
	for key := range protected {
		beforeMsg := before[key]
		afterMsg := rawLast(t, fixture.stream, key)
		if afterMsg == nil || afterMsg.Sequence != beforeMsg.sequence || !slices.Equal(afterMsg.Data, beforeMsg.data) {
			t.Fatalf("committed subject %s changed from sequence %d", key, beforeMsg.sequence)
		}
	}
}

func assertClaimCallPreservesState(t *testing.T, fixture *claimFixture, before map[string]claimMessageSnapshot, beforeSequence uint64) {
	t.Helper()
	assertNoNewOperations(t, fixture, beforeSequence)
	assertCommittedSnapshotUnchanged(t, fixture, before)
}

func claimSeededOrphans(t *testing.T, before map[string]claimMessageSnapshot, keys []string) map[string]claimMessageSnapshot {
	t.Helper()
	allowed := make(map[string]claimMessageSnapshot, len(keys))
	for _, key := range keys {
		message, ok := before[key]
		if !ok || message.marker {
			t.Fatalf("seeded orphan %s is not a live message", key)
		}
		allowed[key] = message
	}
	return allowed
}

func seedClaimRecord(t *testing.T, fixture *claimFixture, config *identityv1.TenantConfig) {
	t.Helper()
	record := claimRecord(config)
	if err := protovalidate.Validate(record); err != nil {
		t.Fatalf("validate seeded record: %v", err)
	}
	data, err := proto.Marshal(record)
	if err != nil {
		t.Fatalf("marshal seeded record: %v", err)
	}
	if _, err := fixture.kv.Create(context.Background(), config.GetRef().GetTenant().GetId(), data); err != nil {
		t.Fatalf("seed record: %v", err)
	}
}

type claimCallKind string

const (
	claimReadCall    claimCallKind = "R"
	claimPublishCall claimCallKind = "P"
)

type claimSeedPoint int

const (
	claimAfterFirstRead claimSeedPoint = iota
	claimAtPublish
)

type claimFaultMode string

const (
	claimReadFault    claimFaultMode = "read"
	claimBeforeSend   claimFaultMode = "before-send"
	claimAbandonBatch claimFaultMode = "staged-and-abandoned"
	claimLoseReply    claimFaultMode = "lost-reply"
)

type claimFault struct {
	position int
	mode     claimFaultMode
}

type claimCase struct {
	name            string
	config          *identityv1.TenantConfig
	expectedOK      bool
	path            []claimCallKind
	expectedCalls   int
	setup           func(*testing.T, *claimFixture)
	orphanKeys      []string
	preserveRecord  bool
	publishFaults   bool
	competitor      *identityv1.TenantConfig
	competitorPoint claimSeedPoint
}

func claimCases() []claimCase {
	target := claimConfig("0192e6a0-0000-7000-8000-000000001001", "org-claim-target")
	deleted := func(t *testing.T, f *claimFixture) {
		if _, err := f.store.Create(context.Background(), target); err != nil {
			t.Fatalf("seed tenant: %v", err)
		}
		for _, key := range []string{target.GetRef().GetTenant().GetId(), OrgIndexKey(claimIssuer, target.GetOrganizationClaimValue())} {
			if err := f.kv.Delete(context.Background(), key); err != nil {
				t.Fatalf("delete %s: %v", key, err)
			}
		}
	}
	return []claimCase{
		{
			name:          "empty",
			config:        target,
			expectedOK:    true,
			path:          []claimCallKind{claimReadCall, claimReadCall, claimPublishCall},
			expectedCalls: 3,
			publishFaults: true,
		},
		{
			name:          "same tenant committed",
			config:        target,
			expectedOK:    true,
			path:          []claimCallKind{claimReadCall, claimReadCall},
			expectedCalls: 2,
			setup: func(t *testing.T, f *claimFixture) {
				if _, err := f.store.Create(context.Background(), target); err != nil {
					t.Fatalf("seed committed tenant: %v", err)
				}
			},
		},
		{
			name:          "id committed for another organization",
			config:        target,
			path:          []claimCallKind{claimReadCall},
			expectedCalls: 1,
			setup: func(t *testing.T, f *claimFixture) {
				if _, err := f.store.Create(context.Background(), claimConfig(target.GetRef().GetTenant().GetId(), "org-other")); err != nil {
					t.Fatalf("seed tenant with other organization: %v", err)
				}
			},
		},
		{
			name:          "organization committed by another id",
			config:        target,
			path:          []claimCallKind{claimReadCall, claimReadCall},
			expectedCalls: 2,
			setup: func(t *testing.T, f *claimFixture) {
				if _, err := f.store.Create(context.Background(), claimConfig("0192e6a0-0000-7000-8000-000000001002", target.GetOrganizationClaimValue())); err != nil {
					t.Fatalf("seed organization owner: %v", err)
				}
			},
		},
		{
			name:          "both keys deleted",
			config:        target,
			expectedOK:    true,
			path:          []claimCallKind{claimReadCall, claimReadCall, claimPublishCall},
			expectedCalls: 3,
			setup:         deleted,
			publishFaults: true,
		},
		{
			name:           "record without index",
			config:         target,
			expectedOK:     true,
			path:           []claimCallKind{claimReadCall, claimReadCall, claimPublishCall},
			expectedCalls:  3,
			publishFaults:  true,
			orphanKeys:     []string{target.GetRef().GetTenant().GetId()},
			preserveRecord: true,
			setup: func(t *testing.T, f *claimFixture) {
				seedClaimRecord(t, f, target)
			},
		},
		{
			name:          "record without index, organization claimed",
			config:        claimConfig("0192e6a0-0000-7000-8000-000000001003", "org-record-claimed"),
			path:          []claimCallKind{claimReadCall, claimReadCall},
			expectedCalls: 2,
			orphanKeys:    []string{"0192e6a0-0000-7000-8000-000000001003"},
			setup: func(t *testing.T, f *claimFixture) {
				if _, err := f.store.Create(context.Background(), claimConfig("0192e6a0-0000-7000-8000-000000001004", "org-record-claimed")); err != nil {
					t.Fatalf("seed organization owner: %v", err)
				}
				seedClaimRecord(t, f, claimConfig("0192e6a0-0000-7000-8000-000000001003", "org-record-claimed"))
			},
		},
		{
			name:          "organization index names target without record",
			config:        target,
			path:          []claimCallKind{claimReadCall, claimReadCall, claimReadCall},
			expectedCalls: 3,
			orphanKeys:    []string{OrgIndexKey(claimIssuer, target.GetOrganizationClaimValue())},
			setup: func(t *testing.T, f *claimFixture) {
				orgKey := OrgIndexKey(claimIssuer, target.GetOrganizationClaimValue())
				if _, err := f.kv.Create(context.Background(), orgKey, []byte(target.GetRef().GetTenant().GetId())); err != nil {
					t.Fatalf("seed target organization index: %v", err)
				}
			},
		},
		{
			name:            "same config competitor after first read",
			config:          claimConfig("0192e6a0-0000-7000-8000-000000001010", "org-reread-same"),
			expectedOK:      true,
			path:            []claimCallKind{claimReadCall, claimReadCall, claimReadCall},
			expectedCalls:   3,
			competitor:      claimConfig("0192e6a0-0000-7000-8000-000000001010", "org-reread-same"),
			competitorPoint: claimAfterFirstRead,
		},
		{
			name:            "same config competitor at publish",
			config:          claimConfig("0192e6a0-0000-7000-8000-000000001011", "org-reread-publish"),
			expectedOK:      true,
			path:            []claimCallKind{claimReadCall, claimReadCall, claimPublishCall, claimReadCall, claimReadCall},
			expectedCalls:   5,
			competitor:      claimConfig("0192e6a0-0000-7000-8000-000000001011", "org-reread-publish"),
			competitorPoint: claimAtPublish,
		},
		{
			name:            "same id and organization with different name",
			config:          claimConfigWithName("0192e6a0-0000-7000-8000-000000001012", "org-reread-different", "requested"),
			path:            []claimCallKind{claimReadCall, claimReadCall, claimReadCall},
			expectedCalls:   3,
			competitor:      claimConfigWithName("0192e6a0-0000-7000-8000-000000001012", "org-reread-different", "committed"),
			competitorPoint: claimAfterFirstRead,
		},
	}
}

func assertCreateOutcome(t *testing.T, fixture *claimFixture, config *identityv1.TenantConfig, record *identityv1.TenantRecord, err error, wantSuccess bool) {
	t.Helper()
	if !wantSuccess {
		if err == nil {
			t.Fatalf("Create succeeded, want %v", ErrCodeAlreadyExists)
		}
		if code, _ := errs.CodeOf(err); code != ErrCodeAlreadyExists {
			t.Fatalf("Create error code = %v, want %v: %v", code, ErrCodeAlreadyExists, err)
		}
		if record != nil {
			t.Fatalf("Create returned %v with an AlreadyExists error", record)
		}
		return
	}
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if record == nil {
		t.Fatal("Create returned nil record")
	}
	message := rawLast(t, fixture.stream, config.GetRef().GetTenant().GetId())
	if message == nil || isMarker(message) {
		t.Fatal("successful Create has no live record message")
	}
	stored := &identityv1.TenantRecord{}
	if err := proto.Unmarshal(message.Data, stored); err != nil {
		t.Fatalf("decode successful record: %v", err)
	}
	if !proto.Equal(record, stored) {
		t.Fatalf("returned record differs from the committed record")
	}
}

func assertSeededRecordPreserved(t *testing.T, fixture *claimFixture, before map[string]claimMessageSnapshot, config *identityv1.TenantConfig, returned *identityv1.TenantRecord) {
	t.Helper()
	id := config.GetRef().GetTenant().GetId()
	seeded := before[id]
	current := rawLast(t, fixture.stream, id)
	if current == nil || isMarker(current) {
		t.Fatalf("seeded record %s is missing after completion", id)
	}
	if !slices.Equal(current.Data, seeded.data) {
		t.Fatalf("completed record %s bytes changed", id)
	}
	seededRecord := &identityv1.TenantRecord{}
	if err := proto.Unmarshal(seeded.data, seededRecord); err != nil {
		t.Fatalf("decode seeded record %s: %v", id, err)
	}
	if !proto.Equal(returned, seededRecord) {
		t.Fatalf("completed Create returned a record different from the seeded record")
	}
}

func claimOrphanMatches(message *jetstream.RawStreamMsg, seeded map[string]claimMessageSnapshot, key string) bool {
	if message == nil {
		return false
	}
	seed, ok := seeded[key]
	return ok && !isMarker(message) && message.Sequence == seed.sequence && slices.Equal(message.Data, seed.data)
}

func assertClaimInvariant(t *testing.T, fixture *claimFixture, options claimInvariantOptions) {
	t.Helper()
	ctx := context.Background()
	live := make(map[string]*jetstream.RawStreamMsg)
	info, err := fixture.stream.Info(ctx, jetstream.WithSubjectFilter(claimSubject(">")))
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	for subject := range info.State.Subjects {
		key := strings.TrimPrefix(subject, claimSubject(""))
		msg := rawLast(t, fixture.stream, key)
		if msg == nil || isMarker(msg) {
			continue
		}
		live[key] = msg
	}

	records := make(map[string]*identityv1.TenantRecord)
	for key, msg := range live {
		if strings.HasPrefix(key, orgIndexPrefix) {
			continue
		}
		record := &identityv1.TenantRecord{}
		if err := proto.Unmarshal(msg.Data, record); err != nil {
			t.Fatalf("decode record %s: %v", key, err)
		}
		config := record.GetConfig()
		if config == nil || config.GetRef().GetTenant().GetId() != key {
			t.Fatalf("record %s contains an invalid tenant reference", key)
		}
		records[key] = record
	}

	committed := make(map[string]*identityv1.TenantConfig)
	for id, record := range records {
		config := record.GetConfig()
		orgKey := OrgIndexKey(config.GetIssuer(), config.GetOrganizationClaimValue())
		indexMsg, ok := live[orgKey]
		if !ok || string(indexMsg.Data) != id {
			if !claimOrphanMatches(live[id], options.allowedOrphans, id) {
				t.Fatalf("live record %s is not committed", id)
			}
			continue
		}
		committed[id] = config
	}
	for key, msg := range live {
		if !strings.HasPrefix(key, orgIndexPrefix) {
			continue
		}
		id := string(msg.Data)
		record, ok := records[id]
		if !ok {
			if !claimOrphanMatches(msg, options.allowedOrphans, key) {
				t.Fatalf("live organization index %s names missing record %s", key, id)
			}
			continue
		}
		config := record.GetConfig()
		if OrgIndexKey(config.GetIssuer(), config.GetOrganizationClaimValue()) != key {
			t.Fatalf("index %s does not match record %s", key, id)
		}
	}

	list, err := fixture.store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	listIDs := make([]string, 0, len(list))
	for _, record := range list {
		listIDs = append(listIDs, record.GetConfig().GetRef().GetTenant().GetId())
	}
	expectedIDs := make([]string, 0, len(committed))
	for id, config := range committed {
		expectedIDs = append(expectedIDs, id)
		got, err := fixture.store.Get(ctx, id)
		if err != nil {
			t.Fatalf("Get %s: %v", id, err)
		}
		if got == nil || !proto.Equal(got.GetConfig(), config) {
			t.Fatalf("Get %s disagrees with the committed pair", id)
		}
		lookup, err := fixture.store.LookupByOrg(ctx, config.GetIssuer(), config.GetOrganizationClaimValue())
		if err != nil {
			t.Fatalf("LookupByOrg %s: %v", id, err)
		}
		if lookup == nil || !proto.Equal(lookup.GetConfig(), config) {
			t.Fatalf("LookupByOrg %s disagrees with the committed pair", id)
		}
	}
	slices.Sort(listIDs)
	slices.Sort(expectedIDs)
	if !slices.Equal(listIDs, expectedIDs) {
		t.Fatalf("List IDs = %v, want %v", listIDs, expectedIDs)
	}
	if options.targetCommitted {
		targetID := options.target.GetRef().GetTenant().GetId()
		config, ok := committed[targetID]
		if !ok || !proto.Equal(config, options.target) {
			t.Fatalf("target %s is not committed with the requested configuration", targetID)
		}
	}
}

func seedClaimConflict(t *testing.T, fixture *claimFixture, config *identityv1.TenantConfig) {
	t.Helper()
	competitorStore, err := New(context.Background(), fixture.js, claimBucket)
	if err != nil {
		t.Fatalf("new competitor store: %v", err)
	}
	if _, err := competitorStore.Create(context.Background(), config); err != nil {
		t.Fatalf("seed conflict: %v", err)
	}
}

func assertStoreFault(t *testing.T, record *identityv1.TenantRecord, err error) {
	t.Helper()
	if code, _ := errs.CodeOf(err); code != ErrCodeStore {
		t.Fatalf("faulted Create error code = %v, want %v: %v", code, ErrCodeStore, err)
	}
	if record != nil {
		t.Fatalf("faulted Create returned %v", record)
	}
}

func runClaimCase(t *testing.T, c claimCase, fault *claimFault) {
	t.Helper()
	fixture := newClaimFixture(t)
	if c.setup != nil {
		c.setup(t, fixture)
	}
	before := claimSnapshot(t, fixture)
	beforeSequence := claimStreamLastSequence(t, fixture)
	calls := 0
	seeded := false
	seed := func() {
		if c.competitor != nil && !seeded {
			seeded = true
			seedClaimConflict(t, fixture, c.competitor)
		}
	}
	failAt := 0
	mode := claimFaultMode("")
	if fault != nil {
		failAt = fault.position
		mode = fault.mode
	}
	realLastMsg := fixture.store.lastMsg
	realPublish := fixture.store.publish
	var publisher jetstreamext.BatchPublisher
	if mode == claimAbandonBatch {
		var err error
		publisher, err = jetstreamext.NewBatchPublisher(fixture.js, jetstreamext.BatchFlowControl{AckFirst: false})
		if err != nil {
			t.Fatalf("new batch publisher: %v", err)
		}
	}
	fixture.store.lastMsg = func(ctx context.Context, subject string) (*jetstream.RawStreamMsg, error) {
		calls++
		if calls == failAt {
			seed()
			return nil, errors.New("injected claim read failure")
		}
		msg, err := realLastMsg(ctx, subject)
		if (err == nil || errors.Is(err, jetstream.ErrMsgNotFound)) && c.competitorPoint == claimAfterFirstRead && calls == 1 {
			seed()
		}
		return msg, err
	}
	fixture.store.publish = func(ctx context.Context, messages []*nats.Msg) (*jetstreamext.BatchAck, error) {
		calls++
		if c.competitorPoint == claimAtPublish {
			seed()
		}
		if calls == failAt {
			switch mode {
			case claimBeforeSend:
				return nil, errors.New("injected publish failure")
			case claimAbandonBatch:
				if err := publisher.AddMsg(messages[0]); err != nil {
					return nil, err
				}
				return nil, errors.New("abandoned batch")
			case claimLoseReply:
				ack, err := realPublish(ctx, messages)
				if err != nil {
					return ack, err
				}
				return nil, context.DeadlineExceeded
			}
		}
		return realPublish(ctx, messages)
	}
	record, err := fixture.store.Create(context.Background(), c.config)
	if fault == nil {
		if calls != c.expectedCalls {
			t.Fatalf("unfaulted calls = %d, want %d (%v)", calls, c.expectedCalls, c.path)
		}
		assertCreateOutcome(t, fixture, c.config, record, err, c.expectedOK)
		assertClaimCallPreservesState(t, fixture, before, beforeSequence)
		assertClaimInvariant(t, fixture, claimInvariantOptions{
			target:          c.config,
			targetCommitted: c.expectedOK,
			allowedOrphans:  claimSeededOrphans(t, before, c.orphanKeys),
		})
		if c.preserveRecord && c.expectedOK {
			assertSeededRecordPreserved(t, fixture, before, c.config, record)
		}
		return
	}
	if calls != failAt {
		t.Fatalf("fault at call %d reached call %d", failAt, calls)
	}
	assertStoreFault(t, record, err)
	fixture.store.lastMsg = realLastMsg
	fixture.store.publish = realPublish
	assertClaimCallPreservesState(t, fixture, before, beforeSequence)
	assertClaimInvariant(t, fixture, claimInvariantOptions{
		target:         c.config,
		allowedOrphans: claimSeededOrphans(t, before, c.orphanKeys),
	})
	afterFault := claimSnapshot(t, fixture)
	afterFaultSequence := claimStreamLastSequence(t, fixture)
	retry, retryErr := fixture.store.Create(context.Background(), c.config)
	assertCreateOutcome(t, fixture, c.config, retry, retryErr, c.expectedOK)
	assertClaimCallPreservesState(t, fixture, afterFault, afterFaultSequence)
	assertClaimInvariant(t, fixture, claimInvariantOptions{
		target:          c.config,
		targetCommitted: c.expectedOK,
		allowedOrphans:  claimSeededOrphans(t, before, c.orphanKeys),
	})
	if c.preserveRecord && c.expectedOK {
		assertSeededRecordPreserved(t, fixture, before, c.config, retry)
	}
}

func validateClaimCase(t *testing.T, c claimCase) (publishAt int) {
	t.Helper()
	if len(c.path) != c.expectedCalls {
		t.Fatalf("case %s pins %d calls but names %d", c.name, c.expectedCalls, len(c.path))
	}
	for position, kind := range c.path {
		if kind == claimPublishCall {
			if publishAt != 0 {
				t.Fatalf("case %s names multiple publish calls", c.name)
			}
			publishAt = position + 1
		}
	}
	if c.publishFaults && publishAt == 0 {
		t.Fatalf("case %s enables publish faults without a publish call", c.name)
	}
	return publishAt
}

func TestClaimMatrix(t *testing.T) {
	for _, c := range claimCases() {
		c := c
		t.Run(c.name, func(t *testing.T) {
			publishAt := validateClaimCase(t, c)
			runClaimCase(t, c, nil)
			for position, kind := range c.path {
				if kind != claimReadCall {
					continue
				}
				position++
				t.Run(fmt.Sprintf("fault %s%d", kind, position), func(t *testing.T) {
					runClaimCase(t, c, &claimFault{position: position, mode: claimReadFault})
				})
			}
			if !c.publishFaults {
				return
			}
			for _, mode := range []claimFaultMode{claimBeforeSend, claimAbandonBatch, claimLoseReply} {
				mode := mode
				t.Run("fault P "+string(mode), func(t *testing.T) {
					runClaimCase(t, c, &claimFault{position: publishAt, mode: mode})
				})
			}
		})
	}
}

func TestClaimContention(t *testing.T) {
	t.Run("same configuration", func(t *testing.T) {
		fixture := newClaimFixture(t)
		before := claimSnapshot(t, fixture)
		beforeSequence := claimStreamLastSequence(t, fixture)
		config := claimConfig("0192e6a0-0000-8000-8000-000000001030", "org-contention-same")
		const count = 8
		results := make(chan *identityv1.TenantRecord, count)
		errsCh := make(chan error, count)
		var wg sync.WaitGroup
		for range count {
			wg.Add(1)
			go func() {
				defer wg.Done()
				record, err := fixture.store.Create(context.Background(), config)
				results <- record
				errsCh <- err
			}()
		}
		wg.Wait()
		close(results)
		close(errsCh)
		for err := range errsCh {
			if err != nil {
				t.Fatalf("same-config contention error: %v", err)
			}
		}
		var created *identityv1.TenantRecord
		for record := range results {
			if record == nil {
				t.Fatal("same-config contention returned nil record")
			}
			if created == nil {
				created = record
			} else if !proto.Equal(created.GetState().GetCreatedAt(), record.GetState().GetCreatedAt()) {
				t.Fatal("same-config contention returned different created_at values")
			}
		}
		if msg := rawLast(t, fixture.stream, config.GetRef().GetTenant().GetId()); msg == nil {
			t.Fatal("same-config contention wrote no record")
		}
		assertClaimCallPreservesState(t, fixture, before, beforeSequence)
		assertClaimInvariant(t, fixture, claimInvariantOptions{target: config, targetCommitted: true})
	})

	t.Run("same id", func(t *testing.T) {
		fixture := newClaimFixture(t)
		before := claimSnapshot(t, fixture)
		beforeSequence := claimStreamLastSequence(t, fixture)
		const count = 8
		ids := make([]string, count)
		for i := range ids {
			ids[i] = fmt.Sprintf("0192e6a0-0000-8000-8000-%012d", 1040+i)
		}
		var wg sync.WaitGroup
		results := make(chan error, count)
		for i := range ids {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				org := fmt.Sprintf("org-contention-%d", i)
				_, err := fixture.store.Create(context.Background(), claimConfig(ids[0], org))
				results <- err
			}(i)
		}
		wg.Wait()
		close(results)
		successes := 0
		for err := range results {
			if err == nil {
				successes++
			} else if code, _ := errs.CodeOf(err); code != ErrCodeAlreadyExists {
				t.Fatalf("contention error code = %v, want %v: %v", code, ErrCodeAlreadyExists, err)
			}
		}
		if successes != 1 {
			t.Fatalf("contention successes = %d, want 1", successes)
		}
		assertClaimCallPreservesState(t, fixture, before, beforeSequence)
		snapshot := claimSnapshot(t, fixture)
		records, indexes, markers := 0, 0, 0
		for key, message := range snapshot {
			if message.marker {
				markers++
				continue
			}
			if strings.HasPrefix(key, orgIndexPrefix) {
				indexes++
			} else {
				records++
			}
		}
		if markers != 0 || records != 1 || indexes != 1 {
			t.Fatalf("contention subjects = %d records, %d indexes, %d markers, want one each and no markers", records, indexes, markers)
		}
		assertClaimInvariant(t, fixture, claimInvariantOptions{target: claimConfig(ids[0], "org-contention-0")})
	})
}

type claimCreateResult struct {
	id     string
	record *identityv1.TenantRecord
	err    error
}

func TestClaimContentionWithLostReplies(t *testing.T) {
	fixture := newClaimFixture(t)
	before := claimSnapshot(t, fixture)
	beforeSequence := claimStreamLastSequence(t, fixture)
	const count = 8
	ids := make([]string, count)
	for i := range ids {
		ids[i] = fmt.Sprintf("0192e6a0-0000-8000-8000-%012d", 1080+i)
	}
	realPublish := fixture.store.publish
	fixture.store.publish = func(ctx context.Context, messages []*nats.Msg) (*jetstreamext.BatchAck, error) {
		ack, err := realPublish(ctx, messages)
		if err != nil {
			return ack, err
		}
		return nil, context.DeadlineExceeded
	}
	var wg sync.WaitGroup
	results := make(chan claimCreateResult, count)
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			record, err := fixture.store.Create(context.Background(), claimConfig(id, "org-lost-replies"))
			results <- claimCreateResult{id: id, record: record, err: err}
		}(id)
	}
	wg.Wait()
	close(results)
	byID := make(map[string]claimCreateResult, count)
	for result := range results {
		byID[result.id] = result
		if result.record != nil {
			t.Fatalf("initial lost-reply Create for %s returned %v", result.id, result.record)
		}
	}
	fixture.store.publish = realPublish
	assertClaimCallPreservesState(t, fixture, before, beforeSequence)
	initial := claimSnapshot(t, fixture)
	winnerID := ""
	for key, message := range initial {
		if message.marker || strings.HasPrefix(key, orgIndexPrefix) {
			continue
		}
		if winnerID != "" {
			t.Fatalf("lost-reply contention created records for %s and %s", winnerID, key)
		}
		winnerID = key
	}
	if winnerID == "" {
		t.Fatal("lost-reply contention created no winner record")
	}
	winnerMessage := rawLast(t, fixture.stream, winnerID)
	winnerRecord := &identityv1.TenantRecord{}
	if err := proto.Unmarshal(winnerMessage.Data, winnerRecord); err != nil {
		t.Fatalf("decode lost-reply winner: %v", err)
	}
	for _, id := range ids {
		result := byID[id]
		code, _ := errs.CodeOf(result.err)
		if id == winnerID {
			if code != ErrCodeStore {
				t.Fatalf("winner %s initial error code = %v, want %v: %v", id, code, ErrCodeStore, result.err)
			}
		} else if code != ErrCodeAlreadyExists {
			t.Fatalf("loser %s initial error code = %v, want %v: %v", id, code, ErrCodeAlreadyExists, result.err)
		}
	}
	assertClaimInvariant(t, fixture, claimInvariantOptions{target: claimConfig(winnerID, "org-lost-replies"), targetCommitted: true})
	for _, id := range ids {
		beforeRetry := claimSnapshot(t, fixture)
		beforeRetrySequence := claimStreamLastSequence(t, fixture)
		retry, err := fixture.store.Create(context.Background(), claimConfig(id, "org-lost-replies"))
		if id == winnerID {
			if err != nil {
				t.Fatalf("winner %s retry: %v", id, err)
			}
			if retry == nil {
				t.Fatalf("winner %s retry returned nil record", id)
			}
			if !proto.Equal(retry.GetState().GetCreatedAt(), winnerRecord.GetState().GetCreatedAt()) {
				t.Fatalf("winner %s retry changed created_at", id)
			}
		} else {
			if code, _ := errs.CodeOf(err); code != ErrCodeAlreadyExists {
				t.Fatalf("loser %s retry error code = %v, want %v: %v", id, code, ErrCodeAlreadyExists, err)
			}
			if retry != nil {
				t.Fatalf("loser %s retry returned %v with an AlreadyExists error", id, retry)
			}
			if message := rawLast(t, fixture.stream, id); message != nil {
				t.Fatalf("loser %s has record message at sequence %d", id, message.Sequence)
			}
			got, err := fixture.store.Get(context.Background(), id)
			if err != nil {
				t.Fatalf("Get loser %s: %v", id, err)
			}
			if got != nil {
				t.Fatalf("Get loser %s returned %v", id, got)
			}
		}
		assertClaimCallPreservesState(t, fixture, beforeRetry, beforeRetrySequence)
	}
	assertClaimInvariant(t, fixture, claimInvariantOptions{target: claimConfig(winnerID, "org-lost-replies"), targetCommitted: true})
}
