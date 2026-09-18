package service

import (
	"cmp"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"buf.build/go/protovalidate"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	runtimev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/runtime/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

const (
	storeProvenanceFile = ".flowseer-store.pb"
	manifestSubject     = metadataSubject + ".manifest.v1"
	manifestVersion     = 1
	subjectVersion      = 1
)

var (
	errCodeBusManifest  = errs.NewCode("service/bus-manifest")
	errCodeBusMigration = errs.NewCode("service/bus-migration-required")
)

// reconcileStoreProvenance prevents opening a populated store whose service,
// domain, format, or NATS version does not match this runtime.
func reconcileStoreProvenance(config normalizedBusConfig) error {
	want := runtimev1.StoreProvenance_builder{
		FormatVersion:    proto.Uint32(manifestVersion),
		NatsVersion:      proto.String(server.VERSION),
		ServiceNamespace: proto.String(config.serviceNamespace),
		ServiceName:      proto.String(config.serviceName),
		Domain:           proto.String(config.domain),
	}.Build()
	path := filepath.Join(config.storeDir, storeProvenanceFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if populated, inspectErr := storeContainsBrokerData(config.storeDir); inspectErr != nil {
			return busUnhealthy(inspectErr, "inspect local bus store provenance")
		} else if populated {
			return migrationRequired().Msg("local bus store has no pre-open NATS provenance")
		}
		return writeProvenance(path, want)
	}
	if err != nil {
		return busUnhealthy(err, "read local bus store provenance")
	}
	got := &runtimev1.StoreProvenance{}
	if err := proto.Unmarshal(data, got); err != nil {
		return errs.From(err).Code(errCodeBusMigration).Attr("nats_version", server.VERSION).Msg("local bus store provenance is malformed")
	}
	if err := protovalidate.Validate(got); err != nil {
		return errs.From(err).Code(errCodeBusMigration).Attr("nats_version", server.VERSION).Msg("local bus store provenance is invalid")
	}
	if !proto.Equal(got, want) {
		return migrationRequired().
			Attr("stored_nats_version", got.GetNatsVersion()).
			Attr("required_nats_version", server.VERSION).
			Msg("local bus store provenance does not match this service and NATS pin")
	}
	return nil
}

func storeContainsBrokerData(storeDir string) (bool, error) {
	entries, err := os.ReadDir(storeDir)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if name != ".lock" && name != storeProvenanceFile && !strings.HasPrefix(name, storeProvenanceFile+".new-") {
			return true, nil
		}
	}
	return false, nil
}

// writeProvenance publishes a fully written and synced temporary file by rename
// so readers never observe a partial store marker.
func writeProvenance(path string, provenance *runtimev1.StoreProvenance) error {
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(provenance)
	if err != nil {
		return busUnhealthy(err, "encode local bus store provenance")
	}
	file, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".new-*")
	if err != nil {
		return busUnhealthy(err, "create local bus store provenance")
	}
	temporary := file.Name()
	written := false
	defer func() {
		_ = file.Close()
		if !written {
			_ = os.Remove(temporary)
		}
	}()
	if _, err := file.Write(data); err != nil {
		return busUnhealthy(err, "write local bus store provenance")
	}
	if err := file.Sync(); err != nil {
		return busUnhealthy(err, "sync local bus store provenance")
	}
	if err := file.Close(); err != nil {
		return busUnhealthy(err, "close local bus store provenance")
	}
	if err := os.Rename(temporary, path); err != nil {
		return busUnhealthy(err, "commit local bus store provenance")
	}
	// The rename itself, not just the file's contents. Without this the
	// marker's directory entry is not durable, and a power cut soon after
	// bootstrap can leave a store holding broker files with no provenance —
	// which reconcileStoreProvenance reads as a store needing migration, so
	// the service refuses to start and demands one of an operator whose
	// store is intact.
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return busUnhealthy(err, "open local bus store directory")
	}
	defer func() { _ = dir.Close() }()
	if err := dir.Sync(); err != nil {
		return busUnhealthy(err, "sync local bus store directory")
	}
	written = true
	return nil
}

func runtimeManifest(config runtimeConfig) (*runtimev1.RuntimeManifest, error) {
	if config.bus == nil {
		return nil, errs.New().Code(errCodeBusManifest).Msg("build manifest for disabled local bus")
	}
	modules := manifestModules(config.modules)
	paths := config.modulePaths()
	slices.Sort(paths)
	manifest := runtimev1.RuntimeManifest_builder{
		ServiceNamespace:       proto.String(config.identity.Namespace),
		ServiceName:            proto.String(config.identity.Name),
		Domain:                 proto.String(config.bus.domain),
		EnvelopeType:           proto.String("flowseer.runtime.v1.Message"),
		EnvelopeVersion:        proto.Uint32(manifestVersion),
		SubjectVersion:         proto.Uint32(subjectVersion),
		NatsVersion:            proto.String(server.VERSION),
		Modules:                modules,
		ModulePaths:            paths,
		MailboxStream:          proto.String(mailboxStreamName),
		MetadataStream:         proto.String(metadataStreamName),
		MaxStoreBytes:          proto.Uint64(uint64(config.bus.maxStoreBytes)),
		MailboxMaxBytes:        proto.Uint64(uint64(config.bus.mailboxMaxBytes)),
		MetadataMaxBytes:       proto.Uint64(uint64(config.bus.metadataMaxBytes)),
		ReserveBytes:           proto.Uint64(uint64(config.bus.reserveBytes)),
		DuplicateWindowSeconds: proto.Uint64(uint64((24 * time.Hour).Seconds())),
	}.Build()
	if err := protovalidate.Validate(manifest); err != nil {
		return nil, errs.From(err).Code(errCodeBusManifest).Msg("validate desired local bus manifest")
	}
	return manifest, nil
}

func manifestModules(modules []plannedModule) []*runtimev1.ModuleContract {
	var contracts []*runtimev1.ModuleContract
	var appendModules func([]plannedModule)
	appendModules = func(items []plannedModule) {
		for _, module := range items {
			if module.leaf != nil {
				subscriptions := make([]*runtimev1.SubscriptionContract, 0, len(module.leaf.subscriptions))
				for _, subscription := range module.leaf.subscriptions {
					aliases := make([]string, len(subscription.aliases))
					for i, alias := range subscription.aliases {
						aliases[i] = string(alias)
					}
					slices.Sort(aliases)
					subscriptions = append(subscriptions, runtimev1.SubscriptionContract_builder{
						Kind:     subscription.kind.Enum(),
						TypeName: proto.String(string(subscription.fullName)),
						Aliases:  aliases,
						Retries:  proto.Uint32(uint32(subscription.retries)),
					}.Build())
				}
				slices.SortFunc(subscriptions, func(left, right *runtimev1.SubscriptionContract) int {
					if left.GetKind() != right.GetKind() {
						return cmp.Compare(left.GetKind(), right.GetKind())
					}
					return strings.Compare(left.GetTypeName(), right.GetTypeName())
				})
				contracts = append(contracts, runtimev1.ModuleContract_builder{
					Path:                proto.String(module.path),
					PathToken:           proto.String(module.pathToken),
					DurableName:         proto.String(module.durableName),
					Subscriptions:       subscriptions,
					DeliveryConcurrency: proto.Uint32(uint32(module.leaf.deliveryConcurrency)),
				}.Build())
			}
			appendModules(module.children)
		}
	}
	appendModules(modules)
	slices.SortFunc(contracts, func(left, right *runtimev1.ModuleContract) int {
		return strings.Compare(left.GetPath(), right.GetPath())
	})
	return contracts
}

// reconcileRuntimeManifest repairs or advances the PREPARED/COMMITTED journal
// before startup. It permits only additive changes that preserve persisted
// module and subscription identities.
func reconcileRuntimeManifest(config runtimeConfig) busReconciler {
	return func(ctx context.Context, resources busResources) error {
		desired, err := runtimeManifest(config)
		if err != nil {
			return err
		}
		current, sequence, err := readReconciliation(ctx, resources.metadata)
		if err != nil {
			return err
		}
		if current != nil {
			if err := validateReconciliation(current); err != nil {
				return err
			}
			if current.GetPhase() == runtimev1.ReconciliationPhase_RECONCILIATION_PHASE_COMMITTED && proto.Equal(current.GetDesired(), desired) {
				return reconcileSettlements(ctx, resources)
			}
			if current.GetPhase() == runtimev1.ReconciliationPhase_RECONCILIATION_PHASE_PREPARED && proto.Equal(current.GetPrevious(), desired) {
				committed, commitErr := newReconciliation(current.GetPrevious(), desired, runtimev1.ReconciliationPhase_RECONCILIATION_PHASE_COMMITTED)
				if commitErr != nil {
					return commitErr
				}
				if _, commitErr = publishReconciliation(ctx, resources, committed, sequence); commitErr != nil {
					return commitErr
				}
				return reconcileSettlements(ctx, resources)
			}
			if !manifestAdditionCompatible(current.GetDesired(), desired) {
				return migrationRequired().Msg("local bus manifest removes or changes a persisted identity")
			}
		}

		var previous *runtimev1.RuntimeManifest
		if current != nil {
			if current.GetPhase() == runtimev1.ReconciliationPhase_RECONCILIATION_PHASE_COMMITTED {
				previous = current.GetDesired()
			} else {
				previous = current.GetPrevious()
			}
		}
		prepared, err := newReconciliation(previous, desired, runtimev1.ReconciliationPhase_RECONCILIATION_PHASE_PREPARED)
		if err != nil {
			return err
		}
		sequence, err = publishReconciliation(ctx, resources, prepared, sequence)
		if err != nil {
			return err
		}
		committed, err := newReconciliation(previous, desired, runtimev1.ReconciliationPhase_RECONCILIATION_PHASE_COMMITTED)
		if err != nil {
			return err
		}
		if _, err = publishReconciliation(ctx, resources, committed, sequence); err != nil {
			return err
		}
		return reconcileSettlements(ctx, resources)
	}
}

// reconcileSettlements removes intents whose mailbox record was already
// acknowledged or terminated before the previous process could delete the
// metadata record. An invalid intent is also safe to remove: the extant
// mailbox record will be redelivered and reconstruct a valid settlement.
func reconcileSettlements(ctx context.Context, resources busResources) error {
	consumer, err := resources.metadata.OrderedConsumer(ctx, jetstream.OrderedConsumerConfig{
		FilterSubjects: []string{settlementSubjectRoot + ".>"},
		DeliverPolicy:  jetstream.DeliverAllPolicy,
		ReplayPolicy:   jetstream.ReplayInstantPolicy,
	})
	if err != nil {
		return busUnhealthy(err, "inspect local bus settlements")
	}
	info, err := consumer.Info(ctx)
	if err != nil {
		return busUnhealthy(err, "count local bus settlements")
	}
	for pending := info.NumPending; pending > 0; {
		batchSize := int(min(pending, uint64(256)))
		batch, err := consumer.Fetch(batchSize, jetstream.FetchMaxWait(time.Second))
		if err != nil {
			return busUnhealthy(err, "read local bus settlements")
		}
		processed := 0
		for message := range batch.Messages() {
			processed++
			metadata, metadataErr := message.Metadata()
			if metadataErr != nil {
				return busUnhealthy(metadataErr, "read local bus settlement sequence")
			}
			keep, keepErr := settlementMatchesMailbox(ctx, resources.mailbox, message)
			if keepErr != nil {
				return keepErr
			}
			if !keep {
				if err := resources.metadata.DeleteMsg(ctx, metadata.Sequence.Stream); err != nil {
					return busUnhealthy(err, "remove orphaned local bus settlement")
				}
			}
		}
		if err := batch.Error(); err != nil {
			return busUnhealthy(err, "read local bus settlement batch")
		}
		if processed == 0 {
			return busUnhealthy(nil, "local bus settlement scan made no progress")
		}
		pending -= uint64(processed)
	}
	return nil
}

func settlementMatchesMailbox(ctx context.Context, mailbox jetstream.Stream, message jetstream.Msg) (bool, error) {
	settlement := &runtimev1.Settlement{}
	if err := proto.Unmarshal(message.Data(), settlement); err != nil {
		return false, nil
	}
	sequence, err := strconv.ParseUint(message.Headers().Get(mailboxSequenceHeader), 10, 64)
	if err != nil || sequence == 0 {
		return false, nil
	}
	stored, err := mailbox.GetMsg(ctx, sequence)
	if errors.Is(err, jetstream.ErrMsgNotFound) {
		return false, nil
	}
	if err != nil {
		return false, busUnhealthy(err, "read mailbox record for local bus settlement")
	}
	envelope := &runtimev1.Message{}
	if err := proto.Unmarshal(stored.Data, envelope); err != nil {
		return false, nil
	}
	return envelope.GetMessageId() == settlement.GetMessageId() &&
		envelope.GetTargetPath() == settlement.GetTargetPath() &&
		message.Subject() == settlementSubject(settlement.GetTargetPath(), settlement.GetMessageId()), nil
}

// readReconciliation returns the latest manifest journal record and its
// sequence. An absent record returns nil and zero.
func readReconciliation(ctx context.Context, metadata jetstream.Stream) (*runtimev1.ReconciliationRecord, uint64, error) {
	message, err := metadata.GetLastMsgForSubject(ctx, manifestSubject)
	if errors.Is(err, jetstream.ErrMsgNotFound) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, busUnhealthy(err, "read local bus manifest")
	}
	record := &runtimev1.ReconciliationRecord{}
	if err := proto.Unmarshal(message.Data, record); err != nil {
		return nil, 0, errs.From(err).Code(errCodeBusMigration).Attr("nats_version", server.VERSION).Msg("local bus manifest journal is malformed")
	}
	return record, message.Sequence, nil
}

func newReconciliation(previous, desired *runtimev1.RuntimeManifest, phase runtimev1.ReconciliationPhase) (*runtimev1.ReconciliationRecord, error) {
	checksum, err := manifestChecksum(desired)
	if err != nil {
		return nil, err
	}
	record := runtimev1.ReconciliationRecord_builder{
		Previous:        previous,
		Desired:         desired,
		DesiredChecksum: checksum,
		Phase:           phase.Enum(),
	}.Build()
	if err := protovalidate.Validate(record); err != nil {
		return nil, errs.From(err).Code(errCodeBusManifest).Msg("validate local bus reconciliation record")
	}
	return record, nil
}

func validateReconciliation(record *runtimev1.ReconciliationRecord) error {
	if err := protovalidate.Validate(record); err != nil {
		return errs.From(err).Code(errCodeBusMigration).Attr("nats_version", server.VERSION).Msg("local bus manifest journal is invalid")
	}
	checksum, err := manifestChecksum(record.GetDesired())
	if err != nil {
		return err
	}
	if !slices.Equal(checksum, record.GetDesiredChecksum()) {
		return migrationRequired().Msg("local bus manifest journal checksum does not match")
	}
	return nil
}

func manifestChecksum(manifest *runtimev1.RuntimeManifest) ([]byte, error) {
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(manifest)
	if err != nil {
		return nil, errs.From(err).Code(errCodeBusManifest).Msg("encode local bus manifest")
	}
	sum := sha256.Sum256(data)
	return sum[:], nil
}

func publishReconciliation(ctx context.Context, resources busResources, record *runtimev1.ReconciliationRecord, previousSequence uint64) (uint64, error) {
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(record)
	if err != nil {
		return 0, errs.From(err).Code(errCodeBusManifest).Msg("encode local bus reconciliation record")
	}
	ack, err := resources.jetStream.Publish(
		ctx,
		manifestSubject,
		data,
		jetstream.WithExpectStream(metadataStreamName),
		jetstream.WithExpectLastSequencePerSubject(previousSequence),
	)
	if err != nil {
		return 0, busUnhealthy(err, "persist local bus reconciliation record")
	}
	return ack.Sequence, nil
}

// manifestAdditionCompatible permits only additive changes that preserve every
// persisted module path and contract.
func manifestAdditionCompatible(previous, desired *runtimev1.RuntimeManifest) bool {
	if previous == nil || desired == nil {
		return previous == nil
	}
	previousCopy := proto.Clone(previous).(*runtimev1.RuntimeManifest)
	desiredCopy := proto.Clone(desired).(*runtimev1.RuntimeManifest)
	previousModules := previousCopy.GetModules()
	desiredModules := desiredCopy.GetModules()
	previousPaths := previousCopy.GetModulePaths()
	desiredPaths := desiredCopy.GetModulePaths()
	previousCopy.SetModules(nil)
	desiredCopy.SetModules(nil)
	previousCopy.SetModulePaths(nil)
	desiredCopy.SetModulePaths(nil)
	if !proto.Equal(previousCopy, desiredCopy) || !containsAll(desiredPaths, previousPaths) {
		return false
	}
	desiredByPath := make(map[string]*runtimev1.ModuleContract, len(desiredModules))
	for _, module := range desiredModules {
		desiredByPath[module.GetPath()] = module
	}
	for _, oldModule := range previousModules {
		newModule := desiredByPath[oldModule.GetPath()]
		if newModule == nil || !moduleAdditionCompatible(oldModule, newModule) {
			return false
		}
	}
	return true
}

// moduleAdditionCompatible permits added subscriptions and aliases while
// preserving each persisted module contract.
func moduleAdditionCompatible(previous, desired *runtimev1.ModuleContract) bool {
	previousCopy := proto.Clone(previous).(*runtimev1.ModuleContract)
	desiredCopy := proto.Clone(desired).(*runtimev1.ModuleContract)
	previousSubscriptions := previousCopy.GetSubscriptions()
	desiredSubscriptions := desiredCopy.GetSubscriptions()
	previousCopy.SetSubscriptions(nil)
	desiredCopy.SetSubscriptions(nil)
	if !proto.Equal(previousCopy, desiredCopy) {
		return false
	}
	for _, oldSubscription := range previousSubscriptions {
		found := false
		for _, newSubscription := range desiredSubscriptions {
			if oldSubscription.GetKind() != newSubscription.GetKind() || oldSubscription.GetTypeName() != newSubscription.GetTypeName() {
				continue
			}
			oldAliases := oldSubscription.GetAliases()
			newAliases := newSubscription.GetAliases()
			oldCopy := proto.Clone(oldSubscription).(*runtimev1.SubscriptionContract)
			newCopy := proto.Clone(newSubscription).(*runtimev1.SubscriptionContract)
			oldCopy.SetAliases(nil)
			newCopy.SetAliases(nil)
			found = proto.Equal(oldCopy, newCopy) && containsAll(newAliases, oldAliases)
			break
		}
		if !found {
			return false
		}
	}
	return true
}

func containsAll(values, required []string) bool {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	for _, value := range required {
		if _, ok := set[value]; !ok {
			return false
		}
	}
	return true
}

func migrationRequired() errs.Builder {
	return errs.New().Code(errCodeBusMigration).Attr("nats_version", server.VERSION)
}
