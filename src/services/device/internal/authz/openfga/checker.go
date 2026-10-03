package openfga

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
	nooptrace "go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/secret"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz"
	"go.aledante.io/FlowSeer/src/services/device/internal/credential"
	"go.aledante.io/FlowSeer/src/services/device/internal/telemetry"
)

// Authorization engine error codes.
var (
	// ErrCodeStoreMismatch indicates that the engine does not hold the configured store.
	ErrCodeStoreMismatch = errs.NewCode("authz/engine-store-mismatch")
	// ErrCodeModelMismatch indicates that the engine model differs from the embedded model.
	ErrCodeModelMismatch = errs.NewCode("authz/engine-model-mismatch")
	// ErrCodeConfig indicates an invalid configuration parameter.
	ErrCodeConfig = errs.NewCode("authz/engine-config")
	// ErrCodeRefused indicates authentication or permission failure on the engine.
	ErrCodeRefused = errs.NewCode("authz/engine-refused")
	// ErrCodeUnreachable indicates the engine is unavailable or timed out.
	ErrCodeUnreachable = errs.NewCode("authz/engine-unreachable")
	// ErrCodeProtocol indicates an unexpected engine response or protocol failure.
	ErrCodeProtocol = errs.NewCode("authz/engine-protocol")
	// ErrCodeConflict indicates a concurrent write conflict on the engine.
	ErrCodeConflict = errs.NewCode("authz/engine-conflict")
	// ErrCodeInvalidTuple indicates an invalid tuple that fails identifier validation.
	ErrCodeInvalidTuple = errs.NewCode("authz/engine-invalid-tuple")
)

const (
	defaultTimeout = 5 * time.Second

	// Byte limits of OpenFGA's request validation: a user is 2 to 512 bytes
	// and an object 2 to 256, a relation 1 to 50.
	maxObjectBytes   = 256
	maxUserBytes     = 512
	maxRelationBytes = 50

	// maxBatchSize is the most checks OpenFGA accepts in one BatchCheck call.
	maxBatchSize = 50
)

// errCallTimeout is the cause of a call context that ran out of the Checker's
// own timeout, which tells it apart from a caller's cancel or deadline.
var errCallTimeout = errs.New().Code(ErrCodeUnreachable).Retryable().Msg("engine call timed out")

// Options configures an OpenFGA authorization Checker.
type Options struct {
	Endpoint       string
	StoreID        string
	ModelID        string
	KeyFile        string
	CAFile         string
	Timeout        time.Duration
	Clock          func() time.Time
	View           *telemetry.View
	TracerProvider trace.TracerProvider
	Propagator     propagation.TextMapPropagator
}

// Checker implements authz.Engine using an OpenFGA service client over gRPC.
type Checker struct {
	storeID    string
	modelID    string
	timeout    time.Duration
	clock      func() time.Time
	view       *telemetry.View
	tracer     trace.Tracer
	propagator propagation.TextMapPropagator
	conn       *grpc.ClientConn
	client     openfgav1.OpenFGAServiceClient

	verifyMu       sync.Mutex
	verified       bool
	lastVerifyErr  error
	lastVerifyTime time.Time
}

var _ authz.Engine = (*Checker)(nil)

// presharedKeyCredential sends the key as a bearer token. Key is exported
// because fmt consults a secret.Value's redaction only for an exported field.
type presharedKeyCredential struct {
	Key secret.Value
}

func (c presharedKeyCredential) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	return map[string]string{
		"authorization": "Bearer " + c.Key.RevealString(),
	}, nil
}

func (c presharedKeyCredential) RequireTransportSecurity() bool {
	return true
}

type metadataCarrier metadata.MD

func (m metadataCarrier) Get(key string) string {
	values := metadata.MD(m).Get(key)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func (m metadataCarrier) Set(key, value string) {
	metadata.MD(m).Set(key, value)
}

func (m metadataCarrier) Keys() []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// New constructs a Checker connected to OpenFGA, verifies the configured store
// and authorization model against the engine, and returns the ready Checker.
func New(_ context.Context, opts Options) (*Checker, error) {
	req := &openfgav1.ReadAuthorizationModelRequest{
		StoreId: opts.StoreID,
		Id:      opts.ModelID,
	}
	if err := req.Validate(); err != nil {
		return nil, errs.From(err).Code(ErrCodeConfig).Msg("invalid store or model id")
	}

	u, err := url.Parse(opts.Endpoint)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeConfig).Msg("invalid endpoint URL")
	}
	if u.Scheme != "https" {
		return nil, errs.New().Code(ErrCodeConfig).Msg("endpoint must have https scheme")
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil || host == "" || port == "" {
		return nil, errs.New().Code(ErrCodeConfig).Msg("endpoint must have host and port")
	}
	if u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errs.New().Code(ErrCodeConfig).Msg("endpoint must not have path, query, or user")
	}

	keyVal, err := credential.ReadKeyFile(opts.KeyFile)
	if err != nil {
		return nil, err
	}

	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: host,
	}
	if opts.CAFile != "" {
		caPEM, err := os.ReadFile(opts.CAFile)
		if err != nil {
			return nil, errs.From(err).Code(ErrCodeConfig).Msg("read CA file")
		}
		certPool := x509.NewCertPool()
		if !certPool.AppendCertsFromPEM(caPEM) {
			return nil, errs.New().Code(ErrCodeConfig).Msg("failed to parse CA certificate")
		}
		tlsConfig.RootCAs = certPool
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	tracerProvider := opts.TracerProvider
	if tracerProvider == nil {
		tracerProvider = nooptrace.NewTracerProvider()
	}
	tracer := tracerProvider.Tracer("go.aledante.io/FlowSeer/src/services/device", trace.WithSchemaURL(semconv.SchemaURL))

	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}

	checker := &Checker{
		storeID:    opts.StoreID,
		modelID:    opts.ModelID,
		timeout:    timeout,
		clock:      clock,
		view:       opts.View,
		tracer:     tracer,
		propagator: opts.Propagator,
	}

	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
		grpc.WithNoProxy(),
		grpc.WithDisableServiceConfig(),
		grpc.WithDisableRetry(),
		grpc.WithPerRPCCredentials(presharedKeyCredential{Key: keyVal}),
		grpc.WithUnaryInterceptor(checker.unaryClientInterceptor),
	}

	conn, err := grpc.NewClient(u.Host, dialOpts...)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeConfig).Msg("create grpc client")
	}
	checker.conn = conn
	checker.client = openfgav1.NewOpenFGAServiceClient(conn)

	return checker, nil
}

// Verify runs the start check against OpenFGA, verifying the configured store and
// authorization model against the engine.
func (c *Checker) Verify(ctx context.Context) error {
	return c.verify(ctx)
}

func (c *Checker) verify(ctx context.Context) error {
	c.verifyMu.Lock()
	defer c.verifyMu.Unlock()

	if c.verified {
		return nil
	}

	now := c.clock()
	if !c.lastVerifyTime.IsZero() && now.Sub(c.lastVerifyTime) < 5*time.Second {
		return c.lastVerifyErr
	}

	err := c.doVerify(ctx)
	if err == nil {
		c.verified = true
		c.lastVerifyErr = nil
		c.lastVerifyTime = time.Time{}
		return nil
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	c.lastVerifyErr = err
	c.lastVerifyTime = now
	return err
}

func (c *Checker) doVerify(ctx context.Context) error {
	callCtx, cancel := context.WithTimeoutCause(ctx, c.timeout, errCallTimeout)
	storeResp, err := c.client.GetStore(callCtx, &openfgav1.GetStoreRequest{StoreId: c.storeID})
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		st, _ := grpcstatus.FromError(err)
		if st.Code() == 5002 || st.Code() == 5 /* codes.NotFound */ {
			return errs.From(err).Code(ErrCodeStoreMismatch).Msg("store not found on engine")
		}
		return c.classifyError(err)
	}
	if storeResp.GetId() != c.storeID {
		return errs.New().Code(ErrCodeStoreMismatch).Msg("store id mismatch")
	}

	callCtx, cancel = context.WithTimeoutCause(ctx, c.timeout, errCallTimeout)
	modelResp, err := c.client.ReadAuthorizationModel(callCtx, &openfgav1.ReadAuthorizationModelRequest{
		StoreId: c.storeID,
		Id:      c.modelID,
	})
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		st, _ := grpcstatus.FromError(err)
		if st.Code() == 2001 || st.Code() == 5 /* codes.NotFound */ {
			return errs.From(err).Code(ErrCodeModelMismatch).Msg("model not found on engine")
		}
		return c.classifyError(err)
	}
	serverModel := modelResp.GetAuthorizationModel()
	if serverModel == nil || serverModel.GetId() != c.modelID {
		return errs.New().Code(ErrCodeModelMismatch).Msg("model id mismatch")
	}

	embeddedModel, err := Model()
	if err != nil {
		return errs.From(err).Code(ErrCodeConfig).Msg("load embedded model")
	}
	cloned := proto.Clone(serverModel).(*openfgav1.AuthorizationModel)
	cloned.Id = ""
	if !proto.Equal(embeddedModel, cloned) {
		return errs.New().Code(ErrCodeModelMismatch).Msg("stored authorization model does not match embedded model")
	}

	return nil
}

func (c *Checker) unaryClientInterceptor(
	ctx context.Context,
	method string,
	req, reply any,
	cc *grpc.ClientConn,
	invoker grpc.UnaryInvoker,
	opts ...grpc.CallOption,
) error {
	start := time.Now()
	cleanMethod := strings.TrimPrefix(method, "/")

	ctx, span := c.tracer.Start(ctx, cleanMethod,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			semconv.RPCSystemNameGRPC,
			semconv.RPCMethodKey.String(cleanMethod),
		),
	)
	defer span.End()

	if c.propagator != nil {
		md, ok := metadata.FromOutgoingContext(ctx)
		if !ok {
			md = metadata.New(nil)
		} else {
			md = md.Copy()
		}
		c.propagator.Inject(ctx, metadataCarrier(md))
		ctx = metadata.NewOutgoingContext(ctx, md)
	}

	err := invoker(ctx, method, req, reply, cc, opts...)
	duration := time.Since(start).Seconds()

	var errType string
	if err != nil {
		errType = c.callErrorType(ctx, err)
		span.SetAttributes(semconv.ErrorTypeKey.String(errType))
		span.SetStatus(codes.Error, "engine call failed")
	}
	if c.view != nil {
		c.view.RecordEngineCall(ctx, cleanMethod, duration, errType)
	}
	return err
}

// callErrorType is the bounded error.type of a failed engine call. A call
// that ended because its caller canceled or ran out of its own deadline is the
// caller's outcome, not the engine's, so it takes the context cause. The
// Checker's own timeout reaches classifyError as the engine being unreachable.
func (c *Checker) callErrorType(callCtx context.Context, err error) string {
	if ctxErr := callCtx.Err(); ctxErr != nil && !errors.Is(context.Cause(callCtx), errCallTimeout) {
		return telemetry.ErrorType(ctxErr)
	}
	return telemetry.ErrorType(c.classifyError(err))
}

func (c *Checker) classifyError(err error) error {
	if err == nil {
		return nil
	}
	st, ok := grpcstatus.FromError(err)
	if !ok {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return errs.From(err).Code(ErrCodeUnreachable).Retryable().Msg("engine call timed out")
		case errors.Is(err, context.Canceled):
			return err
		default:
			return errs.From(err).Code(ErrCodeProtocol).Msg("engine protocol error")
		}
	}

	code := st.Code()
	codeInt := int(code)
	if code == 10 /* codes.Aborted */ {
		return errs.From(err).Code(ErrCodeConflict).Retryable().Msg("engine write conflict")
	}
	if (codeInt >= 1000 && codeInt <= 1999) || code == 16 /* codes.Unauthenticated */ || code == 7 /* codes.PermissionDenied */ {
		return errs.From(err).Code(ErrCodeRefused).Msg("engine refused call")
	}
	if code == 14 /* codes.Unavailable */ || code == 4 /* codes.DeadlineExceeded */ {
		return errs.From(err).Code(ErrCodeUnreachable).Retryable().Msg("engine unreachable")
	}
	return errs.From(err).Code(ErrCodeProtocol).Msg("engine protocol error")
}

func (c *Checker) handleError(callerCtx context.Context, err error) error {
	if callerCtx.Err() != nil {
		return callerCtx.Err()
	}
	return c.classifyError(err)
}

// isRefusedRune reports a rune OpenFGA's tuple.IsValidObject refuses in an
// object or user: a control character, '#', or a space. Other Unicode
// whitespace passes, as it does there.
func isRefusedRune(r rune) bool {
	return unicode.IsControl(r) || r == '#' || r == ' '
}

// isValidTypedID reports whether s is a type:id of at most maxBytes that the
// engine accepts as an object or a user. It also refuses the id "*", which
// the engine reads as a wildcard, and '#', which makes a userset.
func isValidTypedID(s string, maxBytes int) bool {
	if len(s) < 2 || len(s) > maxBytes || strings.ContainsFunc(s, isRefusedRune) {
		return false
	}
	t, id, ok := strings.Cut(s, ":")
	return ok && t != "" && id != "" && id != "*" && !strings.Contains(id, ":")
}

func isValidObject(s string) bool {
	return isValidTypedID(s, maxObjectBytes)
}

func isValidUser(s string) bool {
	return isValidTypedID(s, maxUserBytes)
}

// isValidRelation mirrors tuple.IsValidRelation: a relation also refuses ':'
// and '@', which an object or user may hold.
func isValidRelation(s string) bool {
	if len(s) < 1 || len(s) > maxRelationBytes {
		return false
	}
	return !strings.ContainsFunc(s, func(r rune) bool {
		return isRefusedRune(r) || r == ':' || r == '@'
	})
}

func isValidTuple(t authz.Tuple) bool {
	return isValidObject(t.Object) && isValidRelation(t.Relation) && isValidUser(t.User)
}

func isValidQuery(q authz.Query) bool {
	if !isValidTuple(authz.Tuple{Object: q.Object, Relation: q.Relation, User: q.User}) {
		return false
	}
	return !slices.ContainsFunc(q.ContextualTuples, func(t authz.Tuple) bool { return !isValidTuple(t) })
}

// Check evaluates a single authorization query. An invalid identifier OpenFGA
// refuses is answered false without a network call.
func (c *Checker) Check(ctx context.Context, q authz.Query) (bool, error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return false, ctxErr
	}
	if !isValidQuery(q) {
		return false, nil
	}
	if err := c.verify(ctx); err != nil {
		return false, err
	}

	req := &openfgav1.CheckRequest{
		StoreId:              c.storeID,
		AuthorizationModelId: c.modelID,
		TupleKey: &openfgav1.CheckRequestTupleKey{
			Object:   q.Object,
			Relation: q.Relation,
			User:     q.User,
		},
	}
	if len(q.ContextualTuples) > 0 {
		tuples := make([]*openfgav1.TupleKey, len(q.ContextualTuples))
		for i, t := range q.ContextualTuples {
			tuples[i] = &openfgav1.TupleKey{
				Object:   t.Object,
				Relation: t.Relation,
				User:     t.User,
			}
		}
		req.ContextualTuples = &openfgav1.ContextualTupleKeys{
			TupleKeys: tuples,
		}
	}

	callCtx, cancel := context.WithTimeoutCause(ctx, c.timeout, errCallTimeout)
	resp, err := c.client.Check(callCtx, req)
	cancel()
	if err != nil {
		return false, c.handleError(ctx, err)
	}

	return resp.GetAllowed(), nil
}

// BatchCheck evaluates a list of authorization queries in query order, sending at most
// 50 checks per gRPC call. Invalid queries are answered false without a call.
func (c *Checker) BatchCheck(ctx context.Context, queries []authz.Query) ([]bool, error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err := c.verify(ctx); err != nil {
		return nil, err
	}
	results := make([]bool, len(queries))
	if len(queries) == 0 {
		return results, nil
	}

	type pendingCheck struct {
		queryIndex int
		item       *openfgav1.BatchCheckItem
	}

	var pending []pendingCheck
	for i, q := range queries {
		if !isValidQuery(q) {
			results[i] = false
			continue
		}
		item := &openfgav1.BatchCheckItem{
			CorrelationId: strconv.Itoa(i),
			TupleKey: &openfgav1.CheckRequestTupleKey{
				Object:   q.Object,
				Relation: q.Relation,
				User:     q.User,
			},
		}
		if len(q.ContextualTuples) > 0 {
			tuples := make([]*openfgav1.TupleKey, len(q.ContextualTuples))
			for j, t := range q.ContextualTuples {
				tuples[j] = &openfgav1.TupleKey{
					Object:   t.Object,
					Relation: t.Relation,
					User:     t.User,
				}
			}
			item.ContextualTuples = &openfgav1.ContextualTupleKeys{
				TupleKeys: tuples,
			}
		}
		pending = append(pending, pendingCheck{queryIndex: i, item: item})
	}

	if len(pending) == 0 {
		return results, nil
	}

	for start := 0; start < len(pending); start += maxBatchSize {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		end := start + maxBatchSize
		if end > len(pending) {
			end = len(pending)
		}
		chunk := pending[start:end]

		checks := make([]*openfgav1.BatchCheckItem, len(chunk))
		for j, p := range chunk {
			checks[j] = p.item
		}

		req := &openfgav1.BatchCheckRequest{
			StoreId:              c.storeID,
			AuthorizationModelId: c.modelID,
			Checks:               checks,
		}

		callCtx, cancel := context.WithTimeoutCause(ctx, c.timeout, errCallTimeout)
		resp, err := c.client.BatchCheck(callCtx, req)
		cancel()
		if err != nil {
			return nil, c.handleError(ctx, err)
		}

		for _, p := range chunk {
			single, ok := resp.GetResult()[p.item.CorrelationId]
			if !ok {
				return nil, errs.New().Code(ErrCodeProtocol).Msgf("batch check response missing correlation id %s", p.item.CorrelationId)
			}
			if single.GetError() != nil {
				return nil, errs.New().Code(ErrCodeProtocol).Msgf("batch check item %s returned error: %s", p.item.CorrelationId, single.GetError().GetMessage())
			}
			results[p.queryIndex] = single.GetAllowed()
		}
	}

	return results, nil
}

// Close closes the underlying gRPC connection.
func (c *Checker) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
