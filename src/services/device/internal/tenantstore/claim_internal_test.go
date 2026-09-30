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

func assertNoNewMarkers(t *testing.T, fixture *claimFixture, before map[string]claimMessageSnapshot) {
	t.Helper()
	after := claimSnapshot(t, fixture)
	for key, current := range after {
		if !current.marker {
			continue
		}
		previous, existed := before[key]
		if !existed || !previous.marker || previous.sequence != current.sequence {
			t.Fatalf("Create added delete marker at %s, sequence %d", key, current.sequence)
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
	name       string
	config     *identityv1.TenantConfig
	expectedOK bool
	setup      func(*testing.T, *claimFixture)
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
			name:       "record without index",
			config:     target,
			expectedOK: true,
			setup: func(t *testing.T, f *claimFixture) {
				seedClaimRecord(t, f, target)
			},
		},
		{
			name:   "organization index without record",
			config: target,
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
			_, err := fixture.store.Create(context.Background(), state.config)
			if state.expectedOK {
				if err != nil {
					t.Fatalf("Create: %v", err)
				}
			} else if code, _ := errs.CodeOf(err); code != ErrCodeAlreadyExists {
				t.Fatalf("Create error code = %v, want %v: %v", code, ErrCodeAlreadyExists, err)
			}
			assertNoNewMarkers(t, fixture, before)
			assertCommittedSnapshotUnchanged(t, fixture, before)
			assertClaimInvariant(t, fixture, state.config)
		})
	}
}

func assertClaimInvariant(t *testing.T, fixture *claimFixture, target *identityv1.TenantConfig) {
	t.Helper()
	_ = target
	ctx := context.Background()
	committed := make(map[string]*identityv1.TenantConfig)
	info, err := fixture.stream.Info(ctx, jetstream.WithSubjectFilter(claimSubject(">")))
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	for subject := range info.State.Subjects {
		key := strings.TrimPrefix(subject, claimSubject(""))
		msg := rawLast(t, fixture.stream, key)
		if msg == nil {
			continue
		}
		if isMarker(msg) {
			continue
		}
		if msg.Header.Get("KV-Operation") != "" {
			t.Fatalf("live subject %s has a delete operation header", key)
		}
		if strings.HasPrefix(key, orgIndexPrefix) {
			id := string(msg.Data)
			recordMsg := rawLast(t, fixture.stream, id)
			if recordMsg == nil || isMarker(recordMsg) {
				continue
			}
			record := &identityv1.TenantRecord{}
			if err := proto.Unmarshal(recordMsg.Data, record); err != nil {
				t.Fatalf("decode record %s: %v", id, err)
			}
			if OrgIndexKey(record.GetConfig().GetIssuer(), record.GetConfig().GetOrganizationClaimValue()) != key {
				t.Fatalf("index %s does not match record %s", key, id)
			}
			committed[id] = record.GetConfig()
			continue
		}

		record := &identityv1.TenantRecord{}
		if err := proto.Unmarshal(msg.Data, record); err != nil {
			t.Fatalf("decode record %s: %v", key, err)
		}
		if record.GetConfig().GetRef().GetTenant().GetId() != key {
			t.Fatalf("record %s contains tenant %s", key, record.GetConfig().GetRef().GetTenant().GetId())
		}
		orgKey := OrgIndexKey(record.GetConfig().GetIssuer(), record.GetConfig().GetOrganizationClaimValue())
		indexMsg := rawLast(t, fixture.stream, orgKey)
		if indexMsg == nil || isMarker(indexMsg) {
			continue
		}
		if string(indexMsg.Data) != key {
			t.Fatalf("live record %s has a mismatched live index", key)
		}
		committed[key] = record.GetConfig()
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
		lookup, err := fixture.store.LookupByOrg(ctx, config.GetIssuer(), config.GetOrganizationClaimValue())
		if err != nil {
			t.Fatalf("LookupByOrg %s: %v", id, err)
		}
		if lookup == nil || lookup.GetConfig().GetRef().GetTenant().GetId() != id {
			t.Fatalf("LookupByOrg for %s returned %v", id, lookup)
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
}

func TestClaimReadFaultPositions(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run(fmt.Sprintf("read %d", failAt), func(t *testing.T) {
			fixture := newClaimFixture(t)
			config := claimConfig("0192e6a0-0000-7000-8000-000000001010", "org-read-fault")
			realLastMsg := fixture.store.lastMsg
			calls := 0
			fixture.store.lastMsg = func(ctx context.Context, subject string) (*jetstream.RawStreamMsg, error) {
				calls++
				if calls == failAt {
					return nil, errors.New("injected read failure")
				}
				return realLastMsg(ctx, subject)
			}
			_, err := fixture.store.Create(context.Background(), config)
			if code, _ := errs.CodeOf(err); code != ErrCodeStore {
				t.Fatalf("Create error code = %v, want %v: %v", code, ErrCodeStore, err)
			}
			fixture.store.lastMsg = realLastMsg
			if _, err := fixture.store.Create(context.Background(), config); err != nil {
				t.Fatalf("retry Create: %v", err)
			}
			assertClaimInvariant(t, fixture, config)
		})
	}
}

func TestClaimBatchFaults(t *testing.T) {
	t.Run("before send", func(t *testing.T) {
		fixture := newClaimFixture(t)
		config := claimConfig("0192e6a0-0000-8000-8000-000000001011", "org-batch-before")
		realPublish := fixture.store.publish
		fixture.store.publish = func(context.Context, []*nats.Msg) (*jetstreamext.BatchAck, error) {
			return nil, errors.New("injected batch failure")
		}
		_, err := fixture.store.Create(context.Background(), config)
		if code, _ := errs.CodeOf(err); code != ErrCodeStore {
			t.Fatalf("Create error code = %v, want %v: %v", code, ErrCodeStore, err)
		}
		fixture.store.publish = realPublish
		if _, err := fixture.store.Create(context.Background(), config); err != nil {
			t.Fatalf("retry Create: %v", err)
		}
		assertClaimInvariant(t, fixture, config)
	})

	t.Run("staged and abandoned", func(t *testing.T) {
		fixture := newClaimFixture(t)
		config := claimConfig("0192e6a0-0000-8000-8000-000000001012", "org-batch-staged")
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
		_, err = fixture.store.Create(context.Background(), config)
		if code, _ := errs.CodeOf(err); code != ErrCodeStore {
			t.Fatalf("Create error code = %v, want %v: %v", code, ErrCodeStore, err)
		}
		fixture.store.publish = realPublish
		if _, err := fixture.store.Create(context.Background(), config); err != nil {
			t.Fatalf("retry Create: %v", err)
		}
		assertClaimInvariant(t, fixture, config)
	})

	t.Run("lost reply", func(t *testing.T) {
		fixture := newClaimFixture(t)
		config := claimConfig("0192e6a0-0000-8000-8000-000000001013", "org-batch-lost")
		realPublish := fixture.store.publish
		fixture.store.publish = func(ctx context.Context, messages []*nats.Msg) (*jetstreamext.BatchAck, error) {
			ack, err := realPublish(ctx, messages)
			if err != nil {
				return ack, err
			}
			return nil, context.DeadlineExceeded
		}
		_, err := fixture.store.Create(context.Background(), config)
		if code, _ := errs.CodeOf(err); code != ErrCodeStore {
			t.Fatalf("Create error code = %v, want %v: %v", code, ErrCodeStore, err)
		}
		fixture.store.publish = realPublish
		if _, err := fixture.store.Create(context.Background(), config); err != nil {
			t.Fatalf("retry Create: %v", err)
		}
		assertClaimInvariant(t, fixture, config)
	})
}

func TestClaimConflictSettlement(t *testing.T) {
	for _, tc := range []struct {
		name string
		want string
		seed func(*testing.T, *claimFixture)
	}{
		{
			name: "organization",
			want: "0192e6a0-0000-8000-8000-000000001020",
			seed: func(t *testing.T, f *claimFixture) {
				if _, err := f.store.Create(context.Background(), claimConfig("0192e6a0-0000-8000-8000-000000001021", "org-conflict")); err != nil {
					t.Fatalf("seed conflicting organization: %v", err)
				}
			},
		},
		{
			name: "same id",
			want: "0192e6a0-0000-8000-8000-000000001022",
			seed: func(t *testing.T, f *claimFixture) {
				if _, err := f.store.Create(context.Background(), claimConfig("0192e6a0-0000-8000-8000-000000001022", "org-other-id")); err != nil {
					t.Fatalf("seed conflicting id: %v", err)
				}
			},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			fixture := newClaimFixture(t)
			config := claimConfig(tc.want, "org-conflict")
			realPublish := fixture.store.publish
			seeded := false
			fixture.store.publish = func(ctx context.Context, messages []*nats.Msg) (*jetstreamext.BatchAck, error) {
				if !seeded {
					seeded = true
					tc.seed(t, fixture)
				}
				return realPublish(ctx, messages)
			}
			_, err := fixture.store.Create(context.Background(), config)
			if code, _ := errs.CodeOf(err); code != ErrCodeAlreadyExists {
				t.Fatalf("Create error code = %v, want %v: %v", code, ErrCodeAlreadyExists, err)
			}
			fixture.store.publish = realPublish
			assertClaimInvariant(t, fixture, config)
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
			assertClaimInvariant(t, fixture, claimConfig(ids[0], "org-contention"))
		})
	}
}

func TestClaimContentionWithLostReplies(t *testing.T) {
	fixture := newClaimFixture(t)
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
	results := make(chan error, count)
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, err := fixture.store.Create(context.Background(), claimConfig(id, "org-lost-replies"))
			results <- err
		}(id)
	}
	wg.Wait()
	close(results)
	storeErrors := 0
	alreadyExists := 0
	for err := range results {
		code, _ := errs.CodeOf(err)
		switch code {
		case ErrCodeStore:
			storeErrors++
		case ErrCodeAlreadyExists:
			alreadyExists++
		default:
			t.Fatalf("lost-reply error code = %v, want store or already-exists: %v", code, err)
		}
	}
	if storeErrors != 1 || alreadyExists != count-1 {
		t.Fatalf("lost-reply outcomes = store %d, already-exists %d, want 1 and %d", storeErrors, alreadyExists, count-1)
	}
	fixture.store.publish = realPublish
	for _, id := range ids {
		_, _ = fixture.store.Create(context.Background(), claimConfig(id, "org-lost-replies"))
	}
	assertClaimInvariant(t, fixture, claimConfig(ids[0], "org-lost-replies"))
}
