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
	return identityv1.TenantConfig_builder{
		Ref: identityv1.TenantGlobalRef_builder{
			Tenant: identityv1.TenantLocalRef_builder{Id: proto.String(id)}.Build(),
		}.Build(),
		Issuer:                 proto.String(claimIssuer),
		OrganizationClaimName:  proto.String("org_id"),
		OrganizationClaimValue: proto.String(org),
		Name:                   proto.String("Claim test tenant"),
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
	data, err := proto.Marshal(claimRecord(config))
	if err != nil {
		t.Fatalf("marshal seeded record: %v", err)
	}
	if _, err := fixture.kv.Create(context.Background(), config.GetRef().GetTenant().GetId(), data); err != nil {
		t.Fatalf("seed record: %v", err)
	}
}

type claimState struct {
	name           string
	config         *identityv1.TenantConfig
	expectedOK     bool
	setup          func(*testing.T, *claimFixture)
	orphanKeys     []string
	preserveRecord bool
}

func claimStates() []claimState {
	target := claimConfig("0192e6a0-0000-7000-8000-000000001001", "org-claim-target")
	return []claimState{
		{name: "empty", config: target, expectedOK: true},
		{
			name:       "same tenant committed",
			config:     target,
			expectedOK: true,
			setup: func(t *testing.T, f *claimFixture) {
				if _, err := f.store.Create(context.Background(), target); err != nil {
					t.Fatalf("seed committed tenant: %v", err)
				}
			},
		},
		{
			name:   "id committed for another organization",
			config: target,
			setup: func(t *testing.T, f *claimFixture) {
				if _, err := f.store.Create(context.Background(), claimConfig(target.GetRef().GetTenant().GetId(), "org-other")); err != nil {
					t.Fatalf("seed tenant with other organization: %v", err)
				}
			},
		},
		{
			name:   "organization committed by another id",
			config: target,
			setup: func(t *testing.T, f *claimFixture) {
				if _, err := f.store.Create(context.Background(), claimConfig("0192e6a0-0000-7000-8000-000000001002", target.GetOrganizationClaimValue())); err != nil {
					t.Fatalf("seed organization owner: %v", err)
				}
			},
		},
		{
			name:       "both keys deleted",
			config:     target,
			expectedOK: true,
			setup: func(t *testing.T, f *claimFixture) {
				if _, err := f.store.Create(context.Background(), target); err != nil {
					t.Fatalf("seed tenant: %v", err)
				}
				orgKey := OrgIndexKey(claimIssuer, target.GetOrganizationClaimValue())
				for _, key := range []string{target.GetRef().GetTenant().GetId(), orgKey} {
					if err := f.kv.Delete(context.Background(), key); err != nil {
						t.Fatalf("delete %s: %v", key, err)
					}
				}
			},
		},
		{
			name:           "record without index",
			config:         target,
			expectedOK:     true,
			orphanKeys:     []string{target.GetRef().GetTenant().GetId()},
			preserveRecord: true,
			setup: func(t *testing.T, f *claimFixture) {
				seedClaimRecord(t, f, target)
			},
		},
		{
			name:       "organization index without record",
			config:     target,
			orphanKeys: []string{OrgIndexKey(claimIssuer, target.GetOrganizationClaimValue())},
			setup: func(t *testing.T, f *claimFixture) {
				orgKey := OrgIndexKey(claimIssuer, target.GetOrganizationClaimValue())
				if _, err := f.kv.Create(context.Background(), orgKey, []byte("0192e6a0-0000-7000-8000-000000001099")); err != nil {
					t.Fatalf("seed orphaned organization index: %v", err)
				}
			},
		},
	}
}

func TestClaimProperties(t *testing.T) {
	for _, state := range claimStates() {
		state := state
		t.Run(state.name, func(t *testing.T) {
			fixture := newClaimFixture(t)
			if state.setup != nil {
				state.setup(t, fixture)
			}
			before := claimSnapshot(t, fixture)
			beforeSequence := claimStreamLastSequence(t, fixture)
			record, err := fixture.store.Create(context.Background(), state.config)
			if state.expectedOK {
				if err != nil {
					t.Fatalf("Create: %v", err)
				}
				if record == nil {
					t.Fatal("Create returned nil record")
				}
			} else if code, _ := errs.CodeOf(err); code != ErrCodeAlreadyExists {
				t.Fatalf("Create error code = %v, want %v: %v", code, ErrCodeAlreadyExists, err)
			} else if record != nil {
				t.Fatalf("Create returned %v with an AlreadyExists error", record)
			}
			assertClaimCallPreservesState(t, fixture, before, beforeSequence)
			assertClaimInvariant(t, fixture, claimInvariantOptions{
				target:          state.config,
				targetCommitted: state.expectedOK,
				allowedOrphans:  claimSeededOrphans(t, before, state.orphanKeys),
			})
			if state.preserveRecord {
				assertSeededRecordPreserved(t, fixture, before, state.config)
			}
		})
	}
}

func assertSeededRecordPreserved(t *testing.T, fixture *claimFixture, before map[string]claimMessageSnapshot, config *identityv1.TenantConfig) {
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
	currentRecord := &identityv1.TenantRecord{}
	if err := proto.Unmarshal(current.Data, currentRecord); err != nil {
		t.Fatalf("decode completed record %s: %v", id, err)
	}
	if !proto.Equal(seededRecord.GetState().GetCreatedAt(), currentRecord.GetState().GetCreatedAt()) {
		t.Fatalf("completed record %s changed created_at from %v to %v", id, seededRecord.GetState().GetCreatedAt(), currentRecord.GetState().GetCreatedAt())
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
		if msg.Header.Get("KV-Operation") != "" {
			t.Fatalf("live subject %s has a delete operation header", key)
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
			if ok {
				t.Fatalf("live record %s has a mismatched live index", id)
			}
			if !claimOrphanMatches(live[id], options.allowedOrphans, id) {
				t.Fatalf("live record %s has no live organization index", id)
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
		if got == nil {
			t.Fatalf("Get %s returned nil for committed pair", id)
		}
		if !proto.Equal(got.GetConfig(), config) {
			t.Fatalf("Get %s returned a different configuration", id)
		}
		lookup, err := fixture.store.LookupByOrg(ctx, config.GetIssuer(), config.GetOrganizationClaimValue())
		if err != nil {
			t.Fatalf("LookupByOrg %s: %v", id, err)
		}
		if lookup == nil || lookup.GetConfig().GetRef().GetTenant().GetId() != id {
			t.Fatalf("LookupByOrg for %s returned %v", id, lookup)
		}
		if !proto.Equal(lookup.GetConfig(), config) {
			t.Fatalf("LookupByOrg for %s returned a different configuration", id)
		}
	}
	slices.Sort(listIDs)
	slices.Sort(expectedIDs)
	if !slices.Equal(listIDs, expectedIDs) {
		t.Fatalf("List IDs = %v, want %v", listIDs, expectedIDs)
	}
	for _, record := range list {
		if record.GetConfig().GetRef().GetTenant().GetId() == "" {
			t.Fatal("List returned a tenant without an id")
		}
	}
	if options.targetCommitted {
		targetID := options.target.GetRef().GetTenant().GetId()
		config, ok := committed[targetID]
		if !ok || !proto.Equal(config, options.target) {
			t.Fatalf("target %s is not committed with the requested configuration", targetID)
		}
		got, err := fixture.store.Get(ctx, targetID)
		if err != nil {
			t.Fatalf("Get target %s: %v", targetID, err)
		}
		if got == nil || !proto.Equal(got.GetConfig(), options.target) {
			t.Fatalf("Get target %s is not visible with the requested configuration", targetID)
		}
		lookup, err := fixture.store.LookupByOrg(ctx, options.target.GetIssuer(), options.target.GetOrganizationClaimValue())
		if err != nil {
			t.Fatalf("LookupByOrg target %s: %v", targetID, err)
		}
		if lookup == nil || !proto.Equal(lookup.GetConfig(), options.target) {
			t.Fatalf("LookupByOrg target %s is not visible with the requested configuration", targetID)
		}
	}
}

func claimPathCallCount(t *testing.T, state claimState) int {
	t.Helper()
	fixture := newClaimFixture(t)
	if state.setup != nil {
		state.setup(t, fixture)
	}
	calls := 0
	realLastMsg := fixture.store.lastMsg
	realPublish := fixture.store.publish
	fixture.store.lastMsg = func(ctx context.Context, subject string) (*jetstream.RawStreamMsg, error) {
		calls++
		return realLastMsg(ctx, subject)
	}
	fixture.store.publish = func(ctx context.Context, messages []*nats.Msg) (*jetstreamext.BatchAck, error) {
		calls++
		return realPublish(ctx, messages)
	}
	if _, err := fixture.store.Create(context.Background(), state.config); state.expectedOK {
		if err != nil {
			t.Fatalf("trace Create: %v", err)
		}
	} else if code, _ := errs.CodeOf(err); code != ErrCodeAlreadyExists {
		t.Fatalf("trace Create error code = %v, want %v: %v", code, ErrCodeAlreadyExists, err)
	}
	return calls
}

func TestClaimFaultMatrix(t *testing.T) {
	for _, state := range claimStates() {
		state := state
		callCount := claimPathCallCount(t, state)
		if callCount == 0 {
			t.Fatalf("%s path made no calls", state.name)
		}
		for failAt := 1; failAt <= callCount; failAt++ {
			t.Run(fmt.Sprintf("%s call %d", state.name, failAt), func(t *testing.T) {
				fixture := newClaimFixture(t)
				if state.setup != nil {
					state.setup(t, fixture)
				}
				before := claimSnapshot(t, fixture)
				beforeSequence := claimStreamLastSequence(t, fixture)
				calls := 0
				realLastMsg := fixture.store.lastMsg
				realPublish := fixture.store.publish
				fixture.store.lastMsg = func(ctx context.Context, subject string) (*jetstream.RawStreamMsg, error) {
					calls++
					if calls == failAt {
						return nil, errors.New("injected claim fault")
					}
					return realLastMsg(ctx, subject)
				}
				fixture.store.publish = func(ctx context.Context, messages []*nats.Msg) (*jetstreamext.BatchAck, error) {
					calls++
					if calls == failAt {
						return nil, errors.New("injected claim fault")
					}
					return realPublish(ctx, messages)
				}
				first, err := fixture.store.Create(context.Background(), state.config)
				if code, _ := errs.CodeOf(err); code != ErrCodeStore {
					t.Fatalf("faulted Create error code = %v, want %v: %v", code, ErrCodeStore, err)
				}
				if first != nil {
					t.Fatalf("faulted Create returned %v", first)
				}
				fixture.store.lastMsg = realLastMsg
				fixture.store.publish = realPublish
				assertClaimCallPreservesState(t, fixture, before, beforeSequence)
				assertClaimInvariant(t, fixture, claimInvariantOptions{
					target:         state.config,
					allowedOrphans: claimSeededOrphans(t, before, state.orphanKeys),
				})

				afterFault := claimSnapshot(t, fixture)
				afterFaultSequence := claimStreamLastSequence(t, fixture)
				retry, retryErr := fixture.store.Create(context.Background(), state.config)
				if state.expectedOK {
					if retryErr != nil {
						t.Fatalf("retry Create: %v", retryErr)
					}
					if retry == nil {
						t.Fatal("retry Create returned nil record")
					}
				} else {
					if code, _ := errs.CodeOf(retryErr); code != ErrCodeAlreadyExists {
						t.Fatalf("retry Create error code = %v, want %v: %v", code, ErrCodeAlreadyExists, retryErr)
					}
					if retry != nil {
						t.Fatalf("retry Create returned %v with an AlreadyExists error", retry)
					}
				}
				assertClaimCallPreservesState(t, fixture, afterFault, afterFaultSequence)
				assertClaimInvariant(t, fixture, claimInvariantOptions{
					target:          state.config,
					targetCommitted: state.expectedOK,
					allowedOrphans:  claimSeededOrphans(t, before, state.orphanKeys),
				})
				if state.preserveRecord {
					assertSeededRecordPreserved(t, fixture, before, state.config)
				}
			})
		}
	}
}

func TestClaimBatchFaults(t *testing.T) {
	t.Run("before send", func(t *testing.T) {
		fixture := newClaimFixture(t)
		config := claimConfig("0192e6a0-0000-8000-8000-000000001011", "org-batch-before")
		before := claimSnapshot(t, fixture)
		beforeSequence := claimStreamLastSequence(t, fixture)
		realPublish := fixture.store.publish
		fixture.store.publish = func(context.Context, []*nats.Msg) (*jetstreamext.BatchAck, error) {
			return nil, errors.New("injected batch failure")
		}
		first, err := fixture.store.Create(context.Background(), config)
		if code, _ := errs.CodeOf(err); code != ErrCodeStore {
			t.Fatalf("Create error code = %v, want %v: %v", code, ErrCodeStore, err)
		}
		if first != nil {
			t.Fatalf("faulted Create returned %v", first)
		}
		fixture.store.publish = realPublish
		assertClaimCallPreservesState(t, fixture, before, beforeSequence)
		retryBefore := claimSnapshot(t, fixture)
		retryBeforeSequence := claimStreamLastSequence(t, fixture)
		retry, err := fixture.store.Create(context.Background(), config)
		if err != nil {
			t.Fatalf("retry Create: %v", err)
		}
		if retry == nil {
			t.Fatal("retry Create returned nil record")
		}
		assertClaimCallPreservesState(t, fixture, retryBefore, retryBeforeSequence)
		assertClaimInvariant(t, fixture, claimInvariantOptions{target: config, targetCommitted: true})
	})

	t.Run("staged and abandoned", func(t *testing.T) {
		fixture := newClaimFixture(t)
		config := claimConfig("0192e6a0-0000-8000-8000-000000001012", "org-batch-staged")
		before := claimSnapshot(t, fixture)
		beforeSequence := claimStreamLastSequence(t, fixture)
		publisher, err := jetstreamext.NewBatchPublisher(fixture.js, jetstreamext.BatchFlowControl{AckFirst: false})
		if err != nil {
			t.Fatalf("new batch publisher: %v", err)
		}
		realPublish := fixture.store.publish
		fixture.store.publish = func(_ context.Context, messages []*nats.Msg) (*jetstreamext.BatchAck, error) {
			if err := publisher.AddMsg(messages[0]); err != nil {
				return nil, err
			}
			return nil, errors.New("abandoned batch")
		}
		first, err := fixture.store.Create(context.Background(), config)
		if code, _ := errs.CodeOf(err); code != ErrCodeStore {
			t.Fatalf("Create error code = %v, want %v: %v", code, ErrCodeStore, err)
		}
		if first != nil {
			t.Fatalf("faulted Create returned %v", first)
		}
		fixture.store.publish = realPublish
		assertClaimCallPreservesState(t, fixture, before, beforeSequence)
		retryBefore := claimSnapshot(t, fixture)
		retryBeforeSequence := claimStreamLastSequence(t, fixture)
		retry, err := fixture.store.Create(context.Background(), config)
		if err != nil {
			t.Fatalf("retry Create: %v", err)
		}
		if retry == nil {
			t.Fatal("retry Create returned nil record")
		}
		assertClaimCallPreservesState(t, fixture, retryBefore, retryBeforeSequence)
		assertClaimInvariant(t, fixture, claimInvariantOptions{target: config, targetCommitted: true})
	})

	t.Run("lost reply", func(t *testing.T) {
		fixture := newClaimFixture(t)
		config := claimConfig("0192e6a0-0000-8000-8000-000000001013", "org-batch-lost")
		before := claimSnapshot(t, fixture)
		beforeSequence := claimStreamLastSequence(t, fixture)
		realPublish := fixture.store.publish
		fixture.store.publish = func(ctx context.Context, messages []*nats.Msg) (*jetstreamext.BatchAck, error) {
			ack, err := realPublish(ctx, messages)
			if err != nil {
				return ack, err
			}
			return nil, context.DeadlineExceeded
		}
		first, err := fixture.store.Create(context.Background(), config)
		if code, _ := errs.CodeOf(err); code != ErrCodeStore {
			t.Fatalf("Create error code = %v, want %v: %v", code, ErrCodeStore, err)
		}
		if first != nil {
			t.Fatalf("lost-reply Create returned %v", first)
		}
		fixture.store.publish = realPublish
		assertClaimCallPreservesState(t, fixture, before, beforeSequence)
		recordMessage := rawLast(t, fixture.stream, config.GetRef().GetTenant().GetId())
		indexMessage := rawLast(t, fixture.stream, OrgIndexKey(config.GetIssuer(), config.GetOrganizationClaimValue()))
		if recordMessage == nil || indexMessage == nil {
			t.Fatal("lost-reply Create committed an incomplete pair")
		}
		committed := &identityv1.TenantRecord{}
		if err := proto.Unmarshal(recordMessage.Data, committed); err != nil {
			t.Fatalf("decode lost-reply record: %v", err)
		}
		retryBefore := claimSnapshot(t, fixture)
		retryBeforeSequence := claimStreamLastSequence(t, fixture)
		retry, err := fixture.store.Create(context.Background(), config)
		if err != nil {
			t.Fatalf("retry Create: %v", err)
		}
		if retry == nil {
			t.Fatal("lost-reply retry returned nil record")
		}
		if !proto.Equal(retry.GetState().GetCreatedAt(), committed.GetState().GetCreatedAt()) {
			t.Fatalf("lost-reply retry created_at = %v, want %v", retry.GetState().GetCreatedAt(), committed.GetState().GetCreatedAt())
		}
		assertClaimCallPreservesState(t, fixture, retryBefore, retryBeforeSequence)
		for _, key := range []string{config.GetRef().GetTenant().GetId(), OrgIndexKey(config.GetIssuer(), config.GetOrganizationClaimValue())} {
			message := rawLast(t, fixture.stream, key)
			if message == nil || message.Sequence != retryBefore[key].sequence {
				t.Fatalf("lost-reply retry changed %s sequence", key)
			}
		}
		assertClaimInvariant(t, fixture, claimInvariantOptions{target: config, targetCommitted: true})
	})
}

type claimConflictCase struct {
	name       string
	target     *identityv1.TenantConfig
	competitor *identityv1.TenantConfig
	callCount  int
}

func claimConflictCases() []claimConflictCase {
	return []claimConflictCase{
		{
			name:       "organization",
			target:     claimConfig("0192e6a0-0000-8000-8000-000000001020", "org-conflict"),
			competitor: claimConfig("0192e6a0-0000-8000-8000-000000001021", "org-conflict"),
			callCount:  5,
		},
		{
			name:       "same id",
			target:     claimConfig("0192e6a0-0000-8000-8000-000000001022", "org-conflict"),
			competitor: claimConfig("0192e6a0-0000-8000-8000-000000001022", "org-other-id"),
			callCount:  4,
		},
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

func claimConflictCallCount(t *testing.T, tc claimConflictCase) int {
	t.Helper()
	fixture := newClaimFixture(t)
	calls := 0
	seeded := false
	realLastMsg := fixture.store.lastMsg
	realPublish := fixture.store.publish
	fixture.store.lastMsg = func(ctx context.Context, subject string) (*jetstream.RawStreamMsg, error) {
		calls++
		return realLastMsg(ctx, subject)
	}
	fixture.store.publish = func(ctx context.Context, messages []*nats.Msg) (*jetstreamext.BatchAck, error) {
		calls++
		if !seeded {
			seeded = true
			seedClaimConflict(t, fixture, tc.competitor)
		}
		return realPublish(ctx, messages)
	}
	_, err := fixture.store.Create(context.Background(), tc.target)
	if code, _ := errs.CodeOf(err); code != ErrCodeAlreadyExists {
		t.Fatalf("trace Create error code = %v, want %v: %v", code, ErrCodeAlreadyExists, err)
	}
	return calls
}

func TestClaimConflictSettlement(t *testing.T) {
	for _, tc := range claimConflictCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			callCount := claimConflictCallCount(t, tc)
			if callCount != tc.callCount {
				t.Fatalf("unfaulted conflict path calls = %d, want %d", callCount, tc.callCount)
			}
			for failAt := 1; failAt <= callCount; failAt++ {
				failAt := failAt
				t.Run(fmt.Sprintf("call %d", failAt), func(t *testing.T) {
					fixture := newClaimFixture(t)
					before := claimSnapshot(t, fixture)
					beforeSequence := claimStreamLastSequence(t, fixture)
					calls := 0
					seeded := false
					realLastMsg := fixture.store.lastMsg
					realPublish := fixture.store.publish
					seed := func() {
						if !seeded {
							seeded = true
							seedClaimConflict(t, fixture, tc.competitor)
						}
					}
					fixture.store.lastMsg = func(ctx context.Context, subject string) (*jetstream.RawStreamMsg, error) {
						calls++
						if calls == failAt {
							seed()
							return nil, errors.New("injected conflict fault")
						}
						return realLastMsg(ctx, subject)
					}
					fixture.store.publish = func(ctx context.Context, messages []*nats.Msg) (*jetstreamext.BatchAck, error) {
						calls++
						seed()
						if calls == failAt {
							return nil, errors.New("injected conflict fault")
						}
						return realPublish(ctx, messages)
					}
					first, err := fixture.store.Create(context.Background(), tc.target)
					if code, _ := errs.CodeOf(err); code != ErrCodeStore {
						t.Fatalf("faulted Create error code = %v, want %v: %v", code, ErrCodeStore, err)
					}
					if first != nil {
						t.Fatalf("faulted Create returned %v", first)
					}
					fixture.store.lastMsg = realLastMsg
					fixture.store.publish = realPublish
					assertClaimCallPreservesState(t, fixture, before, beforeSequence)
					assertClaimInvariant(t, fixture, claimInvariantOptions{target: tc.target})

					afterFault := claimSnapshot(t, fixture)
					afterFaultSequence := claimStreamLastSequence(t, fixture)
					retry, retryErr := fixture.store.Create(context.Background(), tc.target)
					if code, _ := errs.CodeOf(retryErr); code != ErrCodeAlreadyExists {
						t.Fatalf("retry Create error code = %v, want %v: %v", code, ErrCodeAlreadyExists, retryErr)
					}
					if retry != nil {
						t.Fatalf("retry Create returned %v with an AlreadyExists error", retry)
					}
					assertClaimCallPreservesState(t, fixture, afterFault, afterFaultSequence)
					assertClaimInvariant(t, fixture, claimInvariantOptions{target: tc.target})
				})
			}
		})
	}
}

func TestClaimContention(t *testing.T) {
	t.Run("same configuration", func(t *testing.T) {
		fixture := newClaimFixture(t)
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
	})

	for _, mode := range []string{"same id", "same organization"} {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			fixture := newClaimFixture(t)
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
					org := "org-contention"
					if mode == "same id" {
						org = fmt.Sprintf("org-contention-%d", i)
					}
					id := ids[i]
					if mode == "same id" {
						id = ids[0]
					}
					_, err := fixture.store.Create(context.Background(), claimConfig(id, org))
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
			snapshot := claimSnapshot(t, fixture)
			records, indexes := 0, 0
			for key, message := range snapshot {
				if message.marker {
					continue
				}
				if strings.HasPrefix(key, orgIndexPrefix) {
					indexes++
				} else {
					records++
				}
			}
			if records != 1 || indexes != 1 {
				t.Fatalf("contention live subjects = %d records, %d indexes, want one each", records, indexes)
			}
			assertClaimInvariant(t, fixture, claimInvariantOptions{target: claimConfig(ids[0], "org-contention")})
		})
	}
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
