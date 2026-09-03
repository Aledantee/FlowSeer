package service

import (
	"context"
	"reflect"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	servicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/service/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

const (
	maxSubscriptionRetries    = 100
	maxDeliveryConcurrency    = 64
	maxMessageNameLength      = 256
	maxSubscriptionAliasCount = 256
)

var (
	errCodeSubscription = errs.NewCode("service/subscription")
	errCodeMessageType  = errs.NewCode("service/message-type")
	errCodeAdmission    = errs.NewCode("service/admission")
)

// Subscription declares one static protobuf handler and its durable delivery
// policy. Retries counts committed retries after the initial handler call.
type Subscription struct {
	// Kind selects command, event, or reply routing.
	Kind servicev1.MessageKind
	// Message is a non-nil prototype whose full name is the canonical persisted type.
	Message proto.Message
	// Aliases are prior persisted full names decoded as the canonical Message type.
	Aliases []protoreflect.FullName
	// Retries is the maximum committed retry count, from zero through 100.
	Retries int
}

// HandlerFunc handles one decoded protobuf message. It may be called more than
// once for the same logical message and need not be safe for concurrent use
// when its leaf's delivery concurrency is one.
type HandlerFunc func(ctx context.Context, message proto.Message) error

// Handler binds one attempt-local function to a canonical static subscription.
// Aliases never name handlers. The zero value is invalid.
type Handler struct {
	// Kind must match the static subscription kind.
	Kind servicev1.MessageKind
	// Message must name the static subscription's canonical protobuf type.
	Message proto.Message
	// Handle processes a decoded message.
	Handle HandlerFunc
}

type plannedSubscription struct {
	kind      servicev1.MessageKind
	fullName  protoreflect.FullName
	aliases   []protoreflect.FullName
	retries   int
	typeToken string
	typeOf    protoreflect.MessageType
}

type messageKey struct {
	kind     servicev1.MessageKind
	fullName protoreflect.FullName
}

type targetKey struct {
	path string
	messageKey
}

type staticModule struct {
	leaf bool
}

type staticRegistry struct {
	modules  map[string]staticModule
	targets  map[targetKey]struct{}
	events   map[protoreflect.FullName][]string
	resolver payloadResolver
}

type staticRegistryBuilder struct {
	modules  map[string]staticModule
	targets  map[targetKey]struct{}
	events   map[protoreflect.FullName][]string
	resolver payloadResolverBuilder
}

func newStaticRegistryBuilder() *staticRegistryBuilder {
	return &staticRegistryBuilder{
		modules: make(map[string]staticModule),
		targets: make(map[targetKey]struct{}),
		events:  make(map[protoreflect.FullName][]string),
		resolver: payloadResolverBuilder{
			entries: make(map[protoreflect.FullName]payloadType),
		},
	}
}

func (b *staticRegistryBuilder) addModule(path string, leaf bool) {
	b.modules[path] = staticModule{leaf: leaf}
}

func (b *staticRegistryBuilder) addSubscriptions(path string, subscriptions []Subscription) ([]plannedSubscription, error) {
	planned := make([]plannedSubscription, 0, len(subscriptions))
	seen := make(map[messageKey]struct{}, len(subscriptions))
	for _, subscription := range subscriptions {
		item, err := validateSubscription(path, subscription)
		if err != nil {
			return nil, err
		}
		key := messageKey{kind: item.kind, fullName: item.fullName}
		if _, ok := seen[key]; ok {
			return nil, subscriptionError(path, "subscription is declared more than once").
				Attr("message_kind", item.kind.String()).
				Attr("message_type", string(item.fullName)).
				Msgf("module %s has a duplicate subscription", path)
		}
		seen[key] = struct{}{}

		if err := b.resolver.add(path, item); err != nil {
			return nil, err
		}
		switch item.kind {
		case servicev1.MessageKind_MESSAGE_KIND_EVENT:
			b.events[item.fullName] = append(b.events[item.fullName], path)
		case servicev1.MessageKind_MESSAGE_KIND_COMMAND, servicev1.MessageKind_MESSAGE_KIND_REPLY:
			b.targets[targetKey{path: path, messageKey: key}] = struct{}{}
		}
		planned = append(planned, item)
	}
	return planned, nil
}

func (b *staticRegistryBuilder) build() *staticRegistry {
	modules := make(map[string]staticModule, len(b.modules))
	for path, module := range b.modules {
		modules[path] = module
	}
	targets := make(map[targetKey]struct{}, len(b.targets))
	for key := range b.targets {
		targets[key] = struct{}{}
	}
	events := make(map[protoreflect.FullName][]string, len(b.events))
	for name, paths := range b.events {
		events[name] = append([]string(nil), paths...)
	}
	return &staticRegistry{modules: modules, targets: targets, events: events, resolver: b.resolver.build()}
}

func (r *staticRegistry) target(path string, kind servicev1.MessageKind, fullName protoreflect.FullName) bool {
	_, ok := r.targets[targetKey{path: path, messageKey: messageKey{kind: kind, fullName: fullName}}]
	return ok
}

func (r *staticRegistry) eventSubscribers(fullName protoreflect.FullName) []string {
	return append([]string(nil), r.events[fullName]...)
}

func validateSubscription(path string, subscription Subscription) (plannedSubscription, error) {
	switch subscription.Kind {
	case servicev1.MessageKind_MESSAGE_KIND_COMMAND,
		servicev1.MessageKind_MESSAGE_KIND_EVENT,
		servicev1.MessageKind_MESSAGE_KIND_REPLY:
	default:
		return plannedSubscription{}, subscriptionError(path, "subscription kind is unsupported").
			Attr("message_kind", subscription.Kind.String()).
			Msgf("module %s has an unsupported subscription kind", path)
	}
	if isNilMessage(subscription.Message) {
		return plannedSubscription{}, subscriptionError(path, "subscription prototype is nil").
			Msgf("module %s has a nil subscription prototype", path)
	}

	message := subscription.Message.ProtoReflect()
	fullName := message.Descriptor().FullName()
	if !fullName.IsValid() || len(fullName) > maxMessageNameLength {
		return plannedSubscription{}, subscriptionError(path, "subscription canonical full name is invalid").
			Attr("message_type", string(fullName)).
			Msgf("module %s has an invalid protobuf full name", path)
	}
	if subscription.Retries < 0 || subscription.Retries > maxSubscriptionRetries {
		return plannedSubscription{}, subscriptionError(path, "subscription retry count is outside its supported bounds").
			Attr("retry_count", subscription.Retries).
			Attr("maximum_retry_count", maxSubscriptionRetries).
			Msgf("module %s has an invalid subscription retry count", path)
	}
	if len(subscription.Aliases) > maxSubscriptionAliasCount {
		return plannedSubscription{}, subscriptionError(path, "subscription declares too many aliases").
			Attr("alias_count", len(subscription.Aliases)).
			Attr("maximum_alias_count", maxSubscriptionAliasCount).
			Msgf("module %s declares too many subscription aliases", path)
	}

	aliases := make([]protoreflect.FullName, 0, len(subscription.Aliases))
	seenAliases := make(map[protoreflect.FullName]struct{}, len(subscription.Aliases))
	for _, alias := range subscription.Aliases {
		if !alias.IsValid() || len(alias) > maxMessageNameLength || alias == fullName {
			return plannedSubscription{}, subscriptionError(path, "subscription alias is invalid").
				Attr("message_type", string(fullName)).
				Attr("message_alias", string(alias)).
				Msgf("module %s has an invalid subscription alias", path)
		}
		if _, ok := seenAliases[alias]; ok {
			return plannedSubscription{}, subscriptionError(path, "subscription alias is duplicated").
				Attr("message_type", string(fullName)).
				Attr("message_alias", string(alias)).
				Msgf("module %s has a duplicate subscription alias", path)
		}
		seenAliases[alias] = struct{}{}
		aliases = append(aliases, alias)
	}

	return plannedSubscription{
		kind:      subscription.Kind,
		fullName:  fullName,
		aliases:   aliases,
		retries:   subscription.Retries,
		typeToken: encodeSubjectToken(string(fullName)),
		typeOf:    message.Type(),
	}, nil
}

func isNilMessage(message proto.Message) bool {
	if message == nil {
		return true
	}
	value := reflect.ValueOf(message)
	return value.Kind() == reflect.Pointer && value.IsNil()
}

func subscriptionError(path, detail string) errs.Builder {
	return errs.New().
		Code(errCodeSubscription).
		Attr("module_path", path).
		Attr("validation_detail", detail)
}

func messageTypeError(path string, fullName protoreflect.FullName, detail string) errs.Builder {
	return errs.New().
		Code(errCodeMessageType).
		Attr("module_path", path).
		Attr("message_type", string(fullName)).
		Attr("validation_detail", detail)
}

type payloadType struct {
	canonical protoreflect.FullName
	typeOf    protoreflect.MessageType
}

type payloadResolver struct {
	entries map[protoreflect.FullName]payloadType
}

type payloadResolverBuilder struct {
	entries map[protoreflect.FullName]payloadType
}

func (b *payloadResolverBuilder) add(path string, subscription plannedSubscription) error {
	payload := payloadType{canonical: subscription.fullName, typeOf: subscription.typeOf}
	names := append([]protoreflect.FullName{subscription.fullName}, subscription.aliases...)
	for _, name := range names {
		if previous, ok := b.entries[name]; ok && previous.canonical != payload.canonical {
			return messageTypeError(path, subscription.fullName, "protobuf name resolves to conflicting canonical types").
				Attr("message_alias", string(name)).
				Attr("conflicting_message_type", string(previous.canonical)).
				Msgf("protobuf name %s resolves to conflicting message types", name)
		}
		b.entries[name] = payload
	}
	return nil
}

func (b *payloadResolverBuilder) build() payloadResolver {
	entries := make(map[protoreflect.FullName]payloadType, len(b.entries))
	for name, payload := range b.entries {
		entries[name] = payload
	}
	return payloadResolver{entries: entries}
}

func (r payloadResolver) resolve(name protoreflect.FullName) (proto.Message, protoreflect.FullName, bool) {
	payload, ok := r.entries[name]
	if !ok {
		return nil, "", false
	}
	return payload.typeOf.New().Interface(), payload.canonical, true
}

func validateAttemptHandlers(path string, subscriptions []plannedSubscription, handlers []Handler) error {
	expected := make(map[messageKey]struct{}, len(subscriptions))
	for _, subscription := range subscriptions {
		expected[messageKey{kind: subscription.kind, fullName: subscription.fullName}] = struct{}{}
	}
	actual := make(map[messageKey]struct{}, len(handlers))
	for _, handler := range handlers {
		if isNilMessage(handler.Message) || handler.Handle == nil {
			return handlerMismatch(path, "handler has a nil prototype or function", handler.Kind, "")
		}
		fullName := handler.Message.ProtoReflect().Descriptor().FullName()
		key := messageKey{kind: handler.Kind, fullName: fullName}
		if _, ok := expected[key]; !ok {
			return handlerMismatch(path, "handler is not statically declared", handler.Kind, fullName)
		}
		if _, ok := actual[key]; ok {
			return handlerMismatch(path, "handler is declared more than once", handler.Kind, fullName)
		}
		actual[key] = struct{}{}
	}
	if len(actual) != len(expected) {
		return errs.New().
			Code(errCodeHandlerMismatch).
			Attr("module_path", path).
			Attr("declared_handler_count", len(expected)).
			Attr("attempt_handler_count", len(actual)).
			Msgf("module %s attempt handlers do not match its static declaration", path)
	}
	return nil
}

func handlerMismatch(path, detail string, kind servicev1.MessageKind, fullName protoreflect.FullName) error {
	return errs.New().
		Code(errCodeHandlerMismatch).
		Attr("module_path", path).
		Attr("message_kind", kind.String()).
		Attr("message_type", string(fullName)).
		Attr("validation_detail", detail).
		Msgf("module %s attempt handlers do not match its static declaration", path)
}

type admissionPhase uint8

const (
	admissionPreflight admissionPhase = iota
	admissionActive
	admissionShuttingDown
)

type moduleState uint8

const (
	moduleRunning moduleState = iota
	moduleSetupFailed
	moduleRestarting
	moduleBackoff
	moduleDisabled
	moduleStopped
)

func (s moduleState) String() string {
	switch s {
	case moduleRunning:
		return "running"
	case moduleSetupFailed:
		return "setup_failed"
	case moduleRestarting:
		return "restarting"
	case moduleBackoff:
		return "backoff"
	case moduleDisabled:
		return "disabled"
	case moduleStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

type admissionRevision struct {
	number   uint64
	phase    admissionPhase
	registry *staticRegistry
	states   map[string]moduleState
}

func newAdmissionRevision(registry *staticRegistry) *admissionRevision {
	states := make(map[string]moduleState, len(registry.modules))
	for path := range registry.modules {
		states[path] = moduleDisabled
	}
	return &admissionRevision{number: 1, phase: admissionPreflight, registry: registry, states: states}
}

func (r *admissionRevision) withPhase(phase admissionPhase) *admissionRevision {
	return &admissionRevision{number: r.number + 1, phase: phase, registry: r.registry, states: r.states}
}

func (r *admissionRevision) withModuleState(path string, state moduleState) *admissionRevision {
	states := make(map[string]moduleState, len(r.states))
	for modulePath, current := range r.states {
		states[modulePath] = current
	}
	states[path] = state
	return &admissionRevision{number: r.number + 1, phase: r.phase, registry: r.registry, states: states}
}

func (r *admissionRevision) withModuleSnapshot(modules []plannedModule) *admissionRevision {
	states := make(map[string]moduleState, len(r.states))
	var addModules func([]plannedModule)
	addModules = func(items []plannedModule) {
		for _, module := range items {
			state := moduleDisabled
			if module.enabled {
				state = moduleRunning
			}
			states[module.path] = state
			addModules(module.children)
		}
	}
	addModules(modules)
	return &admissionRevision{number: r.number + 1, phase: admissionActive, registry: r.registry, states: states}
}

func (r *admissionRevision) admitTarget(path string, kind servicev1.MessageKind, fullName protoreflect.FullName) error {
	ok := r.registry.target(path, kind, fullName)
	if !ok {
		return messageTypeError(path, fullName, "message type is not registered for the addressed target").
			Attr("message_kind", kind.String()).
			Msgf("module %s does not accept %s message %s", path, kind.String(), fullName)
	}
	if r.phase != admissionActive {
		return admissionError(path, kind, fullName, r.phase.String(), "").
			Msgf("module %s is not accepting new messages", path)
	}
	state := r.states[path]
	if !isAdmitted(state) {
		return admissionError(path, kind, fullName, r.phase.String(), state.String()).
			Msgf("module %s is not accepting new messages", path)
	}
	return nil
}

func (r *admissionRevision) admitEvent(fullName protoreflect.FullName) ([]string, error) {
	if r.phase != admissionActive {
		return nil, admissionError("", servicev1.MessageKind_MESSAGE_KIND_EVENT, fullName, r.phase.String(), "").
			Msg("service is not accepting new events")
	}
	paths := r.registry.eventSubscribers(fullName)
	admitted := make([]string, 0, len(paths))
	for _, path := range paths {
		if isAdmitted(r.states[path]) {
			admitted = append(admitted, path)
		}
	}
	return admitted, nil
}

func admissionError(
	path string,
	kind servicev1.MessageKind,
	fullName protoreflect.FullName,
	phase string,
	state string,
) errs.Builder {
	return errs.New().
		Code(errCodeAdmission).
		Attr("module_path", path).
		Attr("message_kind", kind.String()).
		Attr("message_type", string(fullName)).
		Attr("admission_phase", phase).
		Attr("module_state", state)
}

func (p admissionPhase) String() string {
	switch p {
	case admissionPreflight:
		return "preflight"
	case admissionActive:
		return "active"
	case admissionShuttingDown:
		return "shutting_down"
	default:
		return "unknown"
	}
}

func isAdmitted(state moduleState) bool {
	switch state {
	case moduleRunning, moduleSetupFailed, moduleRestarting, moduleBackoff:
		return true
	case moduleDisabled, moduleStopped:
		return false
	default:
		return false
	}
}
