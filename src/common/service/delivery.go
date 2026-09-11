package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	servicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/service/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

const (
	settlementSubjectRoot       = metadataSubject + ".settlement.v1"
	mailboxSequenceHeader       = "FlowSeer-Mailbox-Sequence"
	defaultRetryBackoff         = 100 * time.Millisecond
	maximumRetryBackoff         = 30 * time.Second
	defaultProgressInterval     = 5 * time.Second
	confirmedTerminationPayload = "+TERM"
)

var (
	errCodeDelivery      = errs.NewCode("service/delivery")
	errSettlementChanged = errors.New("durable settlement changed concurrently")
)

type deliveryHandler struct {
	handle  HandlerFunc
	retries int
}

// runDelivery owns one attempt's pull loop and fixed worker pool. It returns
// only for cancellation or a bus-infrastructure failure; handler outcomes are
// settled on the delivery path.
func (r *messageRuntime) runDelivery(ctx context.Context, module plannedModule, handlers []Handler) error {
	if r == nil {
		<-ctx.Done()
		return nil
	}
	return r.runDeliveryWithTelemetry(ctx, module, handlers, r.telemetry.view(r.telemetry.availableSignals()))
}

func (r *messageRuntime) runDeliveryWithTelemetry(ctx context.Context, module plannedModule, handlers []Handler, telemetry telemetryView) error {
	if r == nil || module.leaf == nil || len(module.leaf.subscriptions) == 0 {
		<-ctx.Done()
		return nil
	}
	progressInterval := r.deliveryProgressInterval()
	consumer, err := r.resources.mailbox.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Name:            module.durableName,
		Durable:         module.durableName,
		DeliverPolicy:   jetstream.DeliverAllPolicy,
		AckPolicy:       jetstream.AckExplicitPolicy,
		AckWait:         progressInterval * 3,
		MaxDeliver:      -1,
		FilterSubject:   mailboxSubjectRoot + ".v1." + module.pathToken + ".>",
		ReplayPolicy:    jetstream.ReplayInstantPolicy,
		MaxWaiting:      1,
		MaxAckPending:   module.leaf.deliveryConcurrency,
		MaxRequestBatch: module.leaf.deliveryConcurrency,
		Replicas:        1,
		MemoryStorage:   false,
	})
	if err != nil {
		return deliveryError(module.path, "create durable consumer", err)
	}
	messages, err := consumer.Messages(jetstream.PullMaxMessages(module.leaf.deliveryConcurrency))
	if err != nil {
		return deliveryError(module.path, "start durable pull", err)
	}
	defer messages.Stop()

	handlerSet := bindDeliveryHandlers(module, handlers)
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan jetstream.Msg)
	failures := make(chan error, 1)
	var workers sync.WaitGroup
	for range module.leaf.deliveryConcurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for message := range jobs {
				if err := r.deliver(workerCtx, module, handlerSet, message, telemetry); err != nil {
					select {
					case failures <- err:
						cancel()
					default:
					}
					return
				}
			}
		}()
	}
	defer func() {
		cancel()
		messages.Stop()
		close(jobs)
		workers.Wait()
	}()

	for {
		message, err := messages.Next(jetstream.NextContext(workerCtx))
		if err != nil {
			select {
			case failure := <-failures:
				return failure
			default:
			}
			if workerCtx.Err() != nil {
				if ctx.Err() != nil {
					return nil
				}
				select {
				case failure := <-failures:
					return failure
				default:
					return deliveryError(module.path, "durable pull stopped", err)
				}
			}
			return deliveryError(module.path, "read durable mailbox", err)
		}
		select {
		case jobs <- message:
		case failure := <-failures:
			return failure
		case <-workerCtx.Done():
			if ctx.Err() != nil {
				return nil
			}
		}
	}
}

func bindDeliveryHandlers(module plannedModule, handlers []Handler) map[messageKey]deliveryHandler {
	bindings := make(map[messageKey]deliveryHandler, len(handlers))
	retries := make(map[messageKey]int, len(module.leaf.subscriptions))
	for _, subscription := range module.leaf.subscriptions {
		retries[messageKey{kind: subscription.kind, fullName: subscription.fullName}] = subscription.retries
	}
	for _, handler := range handlers {
		name := handler.Message.ProtoReflect().Descriptor().FullName()
		key := messageKey{kind: handler.Kind, fullName: name}
		bindings[key] = deliveryHandler{handle: handler.Handle, retries: retries[key]}
	}
	return bindings
}

// deliver records durable retry or terminal intent before settling the mailbox
// record. Redelivery resumes a recorded acknowledgement or discard without
// invoking the handler again.
func (r *messageRuntime) deliver(ctx context.Context, module plannedModule, handlers map[messageKey]deliveryHandler, brokerMessage jetstream.Msg, telemetry telemetryView) error {
	metadata, err := brokerMessage.Metadata()
	if err != nil {
		return deliveryError(module.path, "read broker delivery metadata", err)
	}
	envelope, payload, handler, malformed := r.decodeDelivery(module, handlers, brokerMessage)
	messageID := envelope.GetMessageId()
	if messageID == "" {
		messageID = deterministicUUID(module.path, strconv.FormatUint(metadata.Sequence.Stream, 10), "malformed")
	}
	typeName := envelope.GetTypeName()
	if malformed != nil {
		typeName = "unknown"
	}
	settlementSubject := settlementSubject(module.path, messageID)
	current, currentSequence, err := r.loadSettlement(ctx, settlementSubject)
	if err != nil {
		return deliveryError(module.path, "read durable settlement", err)
	}
	attempt := &deliveryAttempt{
		runtime:           r,
		module:            module,
		telemetry:         telemetry,
		brokerMessage:     brokerMessage,
		envelope:          envelope,
		payload:           payload,
		handler:           handler,
		typeName:          typeName,
		messageID:         messageID,
		settlementSubject: settlementSubject,
		streamSequence:    metadata.Sequence.Stream,
		numDelivered:      metadata.NumDelivered,
		retry:             retryCount(current),
		sequence:          currentSequence,
	}
	if settled, err := attempt.resume(ctx, current); settled {
		return err
	}
	if malformed != nil {
		return attempt.discardMalformed(ctx)
	}
	for {
		again, err := attempt.run(ctx)
		if !again {
			return err
		}
	}
}

// deliveryAttempt holds the per-message state the settlement paths share, plus
// the retry count and settlement revision each redelivery pass advances.
type deliveryAttempt struct {
	runtime           *messageRuntime
	module            plannedModule
	telemetry         telemetryView
	brokerMessage     jetstream.Msg
	envelope          *servicev1.Message
	payload           proto.Message
	handler           deliveryHandler
	typeName          string
	messageID         string
	settlementSubject string
	streamSequence    uint64
	numDelivered      uint64
	retry             uint32
	sequence          uint64
}

// resume replays a settlement an earlier delivery already recorded so the
// handler does not run twice. It reports whether current settles the message;
// any other recorded state leaves the message to the delivery loop.
func (a *deliveryAttempt) resume(ctx context.Context, current *servicev1.Settlement) (bool, error) {
	var discard bool
	switch current.GetState() {
	case servicev1.SettlementState_SETTLEMENT_STATE_ACKNOWLEDGE:
	case servicev1.SettlementState_SETTLEMENT_STATE_DISCARD:
		discard = true
	default:
		return false, nil
	}
	a.telemetry.recordDisposition(ctx, a.module.path, a.typeName, a.envelope.GetKind(), current)
	return true, a.runtime.finishSettlement(ctx, a.brokerMessage, a.settlementSubject, discard)
}

// discardMalformed durably discards a message that could not be decoded, which
// no handler can be offered.
func (a *deliveryAttempt) discardMalformed(ctx context.Context) error {
	desired := newSettlement(a.module.path, a.messageID, a.retry, servicev1.SettlementState_SETTLEMENT_STATE_DISCARD)
	if _, err := a.runtime.commitSettlement(ctx, a.settlementSubject, a.sequence, a.streamSequence, desired); err != nil {
		return deliveryError(a.module.path, "record malformed-message settlement", err)
	}
	a.telemetry.recordDisposition(ctx, a.module.path, a.typeName, a.envelope.GetKind(), desired)
	return a.runtime.finishSettlement(ctx, a.brokerMessage, a.settlementSubject, true)
}

// run performs one delivery pass: it invokes the handler and records the
// resulting settlement. It reports whether the message must be delivered again.
func (a *deliveryAttempt) run(ctx context.Context) (bool, error) {
	deliveryCtx, span := a.runtime.deliveryTrace(ctx, a.envelope, a.numDelivered, a.telemetry)
	defer span.End()
	deliveryCtx = withDeliveryContext(deliveryCtx, deliveryContext{
		messageID:     a.envelope.GetMessageId(),
		correlationID: a.envelope.GetCorrelationId(),
		sourcePath:    a.envelope.GetSourcePath(),
	})
	a.telemetry.recordMessage(deliveryCtx, a.module.path, a.envelope.GetTypeName(), a.envelope.GetKind(), messageDelivered)
	panicked, handlerErr, progressErr := callHandlerWithProgress(deliveryCtx, a.brokerMessage, a.runtime.deliveryProgressInterval(), a.handler.handle, proto.Clone(a.payload))
	if progressErr != nil {
		recordSpanError(span, "message progress failed", progressErr)
		if ctx.Err() != nil {
			return false, nil
		}
		return false, deliveryError(a.module.path, "extend delivery during handler execution", progressErr)
	}
	if handlerErr != nil {
		recordSpanError(span, "message handler failed", handlerErr)
	}
	if ctx.Err() != nil {
		return false, nil
	}
	switch deliverySettlementState(panicked, handlerErr, a.retry, a.handler.retries) {
	case servicev1.SettlementState_SETTLEMENT_STATE_ACKNOWLEDGE:
		return false, a.settleTerminal(ctx, deliveryCtx, span, servicev1.SettlementState_SETTLEMENT_STATE_ACKNOWLEDGE, "record acknowledgement settlement")
	case servicev1.SettlementState_SETTLEMENT_STATE_RETRY:
		return a.settleRetry(ctx, deliveryCtx, span)
	}
	return false, a.settleTerminal(ctx, deliveryCtx, span, servicev1.SettlementState_SETTLEMENT_STATE_DISCARD, "record discard settlement")
}

// settleTerminal records the acknowledgement or discard and then releases the
// mailbox record. A settlement another delivery has changed under us belongs to
// that delivery, so this one returns without settling.
func (a *deliveryAttempt) settleTerminal(
	ctx, deliveryCtx context.Context,
	span trace.Span,
	state servicev1.SettlementState,
	action string,
) error {
	desired := newSettlement(a.module.path, a.messageID, a.retry, state)
	if _, err := a.runtime.commitSettlement(ctx, a.settlementSubject, a.sequence, a.streamSequence, desired); err != nil {
		if errors.Is(err, errSettlementChanged) {
			return nil
		}
		recordSpanError(span, "message settlement failed", err)
		return deliveryError(a.module.path, action, err)
	}
	a.telemetry.recordDisposition(deliveryCtx, a.module.path, a.envelope.GetTypeName(), a.envelope.GetKind(), desired)
	err := a.runtime.finishSettlement(ctx, a.brokerMessage, a.settlementSubject, state == servicev1.SettlementState_SETTLEMENT_STATE_DISCARD)
	if err != nil {
		recordSpanError(span, "message settlement failed", err)
	}
	return err
}

// settleRetry waits out the backoff and records the retry, reporting whether
// the message should be delivered again.
func (a *deliveryAttempt) settleRetry(ctx, deliveryCtx context.Context, span trace.Span) (bool, error) {
	if err := waitForRetry(ctx, a.brokerMessage, retryBackoff(a.retry+1)); err != nil {
		if ctx.Err() != nil {
			return false, nil
		}
		recordSpanError(span, "message progress failed", err)
		return false, deliveryError(a.module.path, "extend delivery during retry backoff", err)
	}
	a.retry++
	desired := newSettlement(a.module.path, a.messageID, a.retry, servicev1.SettlementState_SETTLEMENT_STATE_RETRY)
	storedSequence, err := a.runtime.commitSettlement(ctx, a.settlementSubject, a.sequence, a.streamSequence, desired)
	if err != nil {
		if errors.Is(err, errSettlementChanged) {
			return false, nil
		}
		recordSpanError(span, "message settlement failed", err)
		return false, deliveryError(a.module.path, "record retry settlement", err)
	}
	a.sequence = storedSequence
	a.telemetry.recordMessage(deliveryCtx, a.module.path, a.envelope.GetTypeName(), a.envelope.GetKind(), messageRetry)
	return true, nil
}

// deliverySettlementState retries only panics and retryable handler errors
// within the subscription limit; all other failures are discarded.
func deliverySettlementState(panicked bool, handlerErr error, retry uint32, maximumRetries int) servicev1.SettlementState {
	if handlerErr == nil {
		return servicev1.SettlementState_SETTLEMENT_STATE_ACKNOWLEDGE
	}
	if (panicked || errs.Retryable(handlerErr)) && retry < uint32(maximumRetries) {
		return servicev1.SettlementState_SETTLEMENT_STATE_RETRY
	}
	return servicev1.SettlementState_SETTLEMENT_STATE_DISCARD
}

// callHandlerWithProgress keeps the broker delivery alive while the handler
// runs, then waits for its progress goroutine to exit.
func callHandlerWithProgress(
	ctx context.Context,
	message jetstream.Msg,
	progressInterval time.Duration,
	handler HandlerFunc,
	payload proto.Message,
) (panicked bool, handlerErr error, progressErr error) {
	progressCtx, cancel := context.WithCancel(ctx)
	progressDone := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(progressInterval)
		defer ticker.Stop()
		for {
			select {
			case <-progressCtx.Done():
				progressDone <- nil
				return
			case <-ticker.C:
				if err := message.InProgress(); err != nil {
					progressDone <- err
					return
				}
			}
		}
	}()
	panicked, handlerErr = callHandler(ctx, handler, payload)
	cancel()
	return panicked, handlerErr, <-progressDone
}

// decodeDelivery validates a persisted envelope, resolves aliases to their
// canonical payload type, and selects the attempt-local handler.
func (r *messageRuntime) decodeDelivery(module plannedModule, handlers map[messageKey]deliveryHandler, brokerMessage jetstream.Msg) (*servicev1.Message, proto.Message, deliveryHandler, error) {
	envelope := &servicev1.Message{}
	if err := proto.Unmarshal(brokerMessage.Data(), envelope); err != nil {
		return envelope, nil, deliveryHandler{}, err
	}
	if err := validatePersistedEnvelope(envelope); err != nil {
		return envelope, nil, deliveryHandler{}, err
	}
	if envelope.GetTargetPath() != module.path {
		return envelope, nil, deliveryHandler{}, fmt.Errorf("target path does not match mailbox")
	}
	wantSubject, err := mailboxSubject(envelope.GetTargetPath(), envelope.GetKind(), envelope.GetTypeName())
	if err != nil || brokerMessage.Subject() != wantSubject {
		return envelope, nil, deliveryHandler{}, fmt.Errorf("persisted subject does not match envelope")
	}
	payload, canonical, ok := r.registry.resolver.resolve(protoreflect.FullName(envelope.GetTypeName()))
	if !ok {
		return envelope, nil, deliveryHandler{}, fmt.Errorf("persisted protobuf type is not registered")
	}
	handler, ok := handlers[messageKey{kind: envelope.GetKind(), fullName: canonical}]
	if !ok {
		return envelope, nil, deliveryHandler{}, fmt.Errorf("persisted message has no attempt handler")
	}
	if err := proto.Unmarshal(envelope.GetPayload(), payload); err != nil {
		return envelope, nil, deliveryHandler{}, err
	}
	return envelope, payload, handler, nil
}

func validatePersistedEnvelope(message *servicev1.Message) error {
	if message == nil || !message.HasKind() || messageKindToken(message.GetKind()) == "unknown" {
		return fmt.Errorf("message kind is missing or unsupported")
	}
	if !message.HasMessageId() || !validUUID(message.GetMessageId()) {
		return fmt.Errorf("message identifier is missing or malformed")
	}
	if message.HasCorrelationId() && !validUUID(message.GetCorrelationId()) {
		return fmt.Errorf("correlation identifier is malformed")
	}
	if message.HasCausationId() && !validUUID(message.GetCausationId()) {
		return fmt.Errorf("causation identifier is malformed")
	}
	if !message.HasSourcePath() || !validModulePath(message.GetSourcePath()) || !message.HasTargetPath() || !validModulePath(message.GetTargetPath()) {
		return fmt.Errorf("message source or target is missing")
	}
	name := protoreflect.FullName(message.GetTypeName())
	if !message.HasTypeName() || !name.IsValid() || !strings.Contains(message.GetTypeName(), ".") || len(name) > maxMessageNameLength || !message.HasPayload() {
		return fmt.Errorf("message payload identity is missing or malformed")
	}
	if !message.HasPublishedAt() || !message.GetPublishedAt().IsValid() {
		return fmt.Errorf("message publish time is missing or malformed")
	}
	return nil
}

func validModulePath(path string) bool {
	if path == "" || len(path) > maxModulePathLength {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		if validateIdentitySegment("module path", segment) != nil {
			return false
		}
	}
	return true
}

func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for index, char := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if !strings.ContainsRune("0123456789abcdefABCDEF", char) {
			return false
		}
	}
	return true
}

// deliveryTrace extracts durable trace context and links it to the consumer span
// instead of making the persisted message its parent.
func (r *messageRuntime) deliveryTrace(ctx context.Context, envelope *servicev1.Message, delivered uint64, telemetry telemetryView) (context.Context, trace.Span) {
	carrier := caseInsensitiveHeaderCarrier(nats.Header{})
	if envelope.HasTraceparent() {
		carrier.Set("traceparent", envelope.GetTraceparent())
	}
	if envelope.HasTracestate() {
		carrier.Set("tracestate", envelope.GetTracestate())
	}
	ctx = trace.ContextWithSpanContext(ctx, trace.SpanContext{})
	propagator := telemetry.propagator
	if propagator == nil {
		propagator = propagation.TraceContext{}
	}
	extracted := propagator.Extract(ctx, carrier)
	spanContext := trace.SpanContextFromContext(extracted)
	if !telemetry.policy.traces {
		return extracted, trace.SpanFromContext(extracted)
	}
	options := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String(modulePathKey, envelope.GetTargetPath()),
			attribute.String(messageTypeKey, envelope.GetTypeName()),
			attribute.String(messageKindKey, messageKindToken(envelope.GetKind())),
			attribute.Int64(messageDeliveryAttemptKey, int64(delivered)),
		),
	}
	if spanContext.IsValid() {
		options = append(options, trace.WithLinks(trace.Link{SpanContext: spanContext}))
	}
	return telemetry.tracer.Start(ctx, deliverySpanName, options...)
}

// callHandler converts a handler panic into an error so delivery can apply its
// retry policy.
func callHandler(ctx context.Context, handler HandlerFunc, payload proto.Message) (panicked bool, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			panicked = true
			err = fmt.Errorf("message handler panic: %v", recovered)
		}
	}()
	return false, handler(ctx, payload)
}

// retryBackoff is the capped exponential delay before redelivering a message
// for the given retry, sharing the supervisor's doubling helper.
func retryBackoff(retry uint32) time.Duration {
	return exponentialBackoff(Backoff{Initial: defaultRetryBackoff, Maximum: maximumRetryBackoff}, int(retry)-1)
}

// waitForRetry renews broker delivery progress until delay elapses so the
// message remains owned during retry backoff.
func waitForRetry(ctx context.Context, message jetstream.Msg, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	ticker := time.NewTicker(min(defaultProgressInterval, delay))
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		case <-ticker.C:
			if err := message.InProgress(); err != nil {
				return err
			}
		}
	}
}

func settlementSubject(path, messageID string) string {
	return settlementSubjectRoot + "." + encodeSubjectToken(path) + "." + encodeSubjectToken(messageID)
}

func retryCount(settlement *servicev1.Settlement) uint32 {
	if settlement == nil {
		return 0
	}
	return settlement.GetRetryCount()
}

// newSettlement derives a deterministic disposition identity so repeated
// settlement commits are idempotent.
func newSettlement(path, messageID string, retries uint32, state servicev1.SettlementState) *servicev1.Settlement {
	dispositionID := deterministicUUID(path, messageID, strconv.FormatUint(uint64(retries), 10), state.String())
	settlement := &servicev1.Settlement{}
	settlement.SetTargetPath(path)
	settlement.SetMessageId(messageID)
	settlement.SetRetryCount(retries)
	settlement.SetDispositionId(dispositionID)
	settlement.SetState(state)
	return settlement
}

// loadSettlement returns the latest valid settlement and its stream sequence. A
// missing settlement returns nil and zero.
func (r *messageRuntime) loadSettlement(ctx context.Context, subject string) (*servicev1.Settlement, uint64, error) {
	message, err := r.resources.metadata.GetLastMsgForSubject(ctx, subject)
	if errors.Is(err, jetstream.ErrMsgNotFound) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	settlement := &servicev1.Settlement{}
	if err := proto.Unmarshal(message.Data, settlement); err != nil {
		return nil, 0, err
	}
	if !validModulePath(settlement.GetTargetPath()) || !validUUID(settlement.GetMessageId()) ||
		!validUUID(settlement.GetDispositionId()) || settlement.GetRetryCount() > maxSubscriptionRetries {
		return nil, 0, fmt.Errorf("durable settlement identity is malformed")
	}
	switch settlement.GetState() {
	case servicev1.SettlementState_SETTLEMENT_STATE_RETRY,
		servicev1.SettlementState_SETTLEMENT_STATE_ACKNOWLEDGE,
		servicev1.SettlementState_SETTLEMENT_STATE_DISCARD:
	default:
		return nil, 0, fmt.Errorf("durable settlement state is unsupported")
	}
	return settlement, message.Sequence, nil
}

// commitSettlement conditionally appends a settlement and treats an identical
// stored record as a successful idempotent commit.
func (r *messageRuntime) commitSettlement(ctx context.Context, subject string, previousSequence, mailboxSequence uint64, settlement *servicev1.Settlement) (uint64, error) {
	data, err := (proto.MarshalOptions{Deterministic: true}).Marshal(settlement)
	if err != nil {
		return 0, err
	}
	message := nats.NewMsg(subject)
	message.Data = data
	message.Header.Set(mailboxSequenceHeader, strconv.FormatUint(mailboxSequence, 10))
	ack, err := r.resources.jetStream.PublishMsg(ctx, message,
		jetstream.WithExpectStream(metadataStreamName),
		jetstream.WithExpectLastSequencePerSubject(previousSequence),
		jetstream.WithMsgID(recordID(settlement.GetDispositionId(), subject)),
	)
	if err == nil {
		return ack.Sequence, nil
	}
	stored, sequence, readErr := r.loadSettlement(ctx, subject)
	if readErr == nil && proto.Equal(stored, settlement) {
		return sequence, nil
	}
	if readErr == nil && stored != nil {
		return 0, errSettlementChanged
	}
	return 0, err
}

func (r *messageRuntime) finishSettlement(ctx context.Context, message jetstream.Msg, subject string, discard bool) error {
	var err error
	if discard {
		_, err = r.resources.connection.RequestWithContext(ctx, message.Reply(), []byte(confirmedTerminationPayload))
	} else {
		err = message.DoubleAck(ctx)
	}
	if err != nil {
		// The durable intent remains. Redelivery resumes confirmation without
		// invoking the handler again.
		return nil
	}
	if err := r.resources.metadata.Purge(ctx, jetstream.WithPurgeSubject(subject)); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

func deliveryError(path, detail string, cause error) error {
	return errs.From(cause).Code(errCodeDelivery).Attr("module_path", path).Attr("delivery_detail", detail).Msg(detail)
}
