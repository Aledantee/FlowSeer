package gnmi

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/protocol/yang"
)

// Session is a gNMI client over one gRPC channel. Construct via
// [Dial] (or [NewSession] over an existing connection — the test
// seam); see the package documentation for the lifecycle contract.
// Safe for concurrent use. The zero value is unusable.
type Session struct {
	cc     *grpc.ClientConn
	client gpb.GNMIClient
	opts   Options
	tracer trace.Tracer

	caps     Capabilities
	encoding gpb.Encoding

	mu     sync.Mutex // guards closed
	closed bool
}

// Capabilities is the peer's advertised surface, recorded at
// establishment. Its slices are read-only and may be read concurrently.
type Capabilities struct {
	// Models lists the supported (name, organization, version)
	// triples.
	Models []Model
	// Encodings lists the supported wire encodings by proto name.
	Encodings []string
	// Version is the peer's gNMI service version.
	Version string
}

// Model identifies one supported YANG model. Version is what the peer
// advertises, which for an OpenConfig model is its openconfig-version
// semantic version rather than an RFC 7950 revision date, so it is not
// comparable with a vendored revision by equality.
// Values may be read concurrently while unmodified.
type Model struct {
	Name         string
	Organization string
	Version      string
}

// ModelRevisions maps model names to the versions the peer advertises
// — the advertised side of the revision-drift check; compare against
// yang.ParseLockfileRevisions with yang.DiffRevisions, which separates
// the OpenConfig semantic versions here from the revision dates the
// lockfile holds instead of reporting them all as drift.
func (c Capabilities) ModelRevisions() map[string]string {
	out := make(map[string]string, len(c.Models))
	for _, m := range c.Models {
		out[m.Name] = m.Version
	}
	return out
}

// Dial establishes the channel, records Capabilities, and negotiates
// the encoding (JSON_IETF preferred, then PROTO, then JSON). Caller
// cancellation returns ctx.Err(); other failures carry an error code.
func Dial(ctx context.Context, target string, opts Options) (*Session, error) {
	opts = opts.withDefaults()
	creds, err := opts.transportCredentials()
	if err != nil {
		return nil, err
	}
	cc, err := grpc.NewClient(target, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeTransport).Msgf("dial %s", target)
	}
	s, err := NewSession(ctx, cc, opts)
	if err != nil {
		_ = cc.Close()
		return nil, err
	}
	return s, nil
}

// NewSession wraps an established gRPC connection: it issues
// Capabilities and negotiates the encoding. The transport seam for
// tests and callers with their own channel plumbing.
// A successful session owns cc and closes it in [Session.Close];
// on failure the caller remains responsible for closing cc.
func NewSession(ctx context.Context, cc *grpc.ClientConn, opts Options) (*Session, error) {
	opts = opts.withDefaults()
	tp := opts.TracerProvider
	if tp == nil {
		tp = tracenoop.NewTracerProvider()
	}
	s := &Session{
		cc:     cc,
		client: gpb.NewGNMIClient(cc),
		opts:   opts,
		tracer: tp.Tracer("go.aledante.io/FlowSeer/src/protocol/gnmi", trace.WithSchemaURL(semconv.SchemaURL)),
	}

	capCtx, cancel := context.WithTimeout(ctx, opts.DialTimeout)
	defer cancel()
	resp, err := s.client.Capabilities(s.withCreds(capCtx), &gpb.CapabilityRequest{})
	if err != nil {
		return nil, s.mapError(capCtx, "Capabilities", err)
	}
	for _, m := range resp.GetSupportedModels() {
		s.caps.Models = append(s.caps.Models, Model{
			Name: m.GetName(), Organization: m.GetOrganization(), Version: m.GetVersion(),
		})
	}
	for _, e := range resp.GetSupportedEncodings() {
		s.caps.Encodings = append(s.caps.Encodings, e.String())
	}
	s.caps.Version = resp.GetGNMIVersion()

	s.encoding, err = negotiateEncoding(resp.GetSupportedEncodings())
	if err != nil {
		return nil, err
	}
	return s, nil
}

// negotiateEncoding picks JSON_IETF when offered, PROTO otherwise.
// An empty advertisement counts as JSON_IETF: several
// implementations omit the field yet accept it.
func negotiateEncoding(offered []gpb.Encoding) (gpb.Encoding, error) {
	if len(offered) == 0 {
		return gpb.Encoding_JSON_IETF, nil
	}
	fallback := gpb.Encoding(-1)
	for _, e := range offered {
		switch e {
		case gpb.Encoding_JSON_IETF:
			return gpb.Encoding_JSON_IETF, nil
		case gpb.Encoding_PROTO:
			fallback = gpb.Encoding_PROTO
		case gpb.Encoding_JSON:
			if fallback == gpb.Encoding(-1) {
				fallback = gpb.Encoding_JSON
			}
		}
	}
	if fallback == gpb.Encoding(-1) {
		return 0, errs.New().Code(ErrCodeEncoding).Msgf("peer offers none of JSON_IETF, JSON, or PROTO (got %v)", offered)
	}
	return fallback, nil
}

// Capabilities returns the peer surface recorded at establishment.
// The returned slices share session storage and must not be modified.
func (s *Session) Capabilities() Capabilities { return s.caps }

// Encoding returns the negotiated encoding's proto name.
func (s *Session) Encoding() string { return s.encoding.String() }

// Close tears the channel down. Idempotent.
func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	if err := s.cc.Close(); err != nil {
		return errs.From(err).Code(ErrCodeTransport).Msg("close channel")
	}
	return nil
}

func (s *Session) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// withCreds stamps username/password metadata onto the outgoing
// context when configured.
func (s *Session) withCreds(ctx context.Context) context.Context {
	if s.opts.Username == "" {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, "username", s.opts.Username, "password", s.opts.Password.RevealString())
}

// unaryCtx prepares a unary RPC context: closed check, timeout
// defaulting, credentials, span.
func (s *Session) unaryCtx(ctx context.Context, op string) (context.Context, func(error), error) {
	if s.isClosed() {
		return nil, nil, ErrSessionClosed
	}
	ctx, cancel := context.WithTimeout(ctx, s.opts.RPCTimeout)
	ctx, span := s.tracer.Start(ctx, "gnmi."+op,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(semconv.RPCSystemNameGRPC, semconv.RPCMethod(op)))
	finish := func(err error) {
		if err != nil {
			span.SetAttributes(semconv.ErrorTypeKey.String(errorType(err)))
			span.SetStatus(codes.Error, "rpc failed")
		}
		span.End()
		cancel()
	}
	return s.withCreds(ctx), finish, nil
}

// mapError translates gRPC failures, keeping caller cancellation
// unwrapped.
func (s *Session) mapError(ctx context.Context, op string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if st, ok := status.FromError(err); ok {
		return errs.From(err).
			Code(ErrCodeRPC).
			Attr("grpc_code", st.Code().String()).
			Msgf("%s rejected: %s", op, st.Message())
	}
	return errs.From(err).Code(ErrCodeTransport).Msgf("%s transport failed", op)
}

// Update is one decoded gNMI update: the full path plus either raw
// JSON_IETF bytes (JSON encodings; decode with the generated codecs)
// or a typed scalar.
// Referenced values must not be modified during concurrent reads.
type Update struct {
	Path      yang.Path
	Timestamp time.Time
	// JSON carries the update payload under the JSON encodings.
	JSON []byte
	// Value carries scalar TypedValues (PROTO encoding).
	Value *yang.Value
	// Values carries a leaf-list's elements in wire order (PROTO
	// encoding). It is non-nil exactly when the update held a
	// leaflist_val, empty slice included, and Value is then nil.
	// Leaf-list multiplicity stops here rather than widening
	// [yang.Value], which the NETCONF and RESTCONF codecs share and
	// which express repetition structurally instead.
	Values []yang.Value
}

// Get issues one Get for the given paths and flattens the reply's
// notifications into updates (prefixes resolved).
func (s *Session) Get(ctx context.Context, paths ...yang.Path) ([]Update, error) {
	ctx, finish, err := s.unaryCtx(ctx, "Get")
	if err != nil {
		return nil, err
	}
	req := &gpb.GetRequest{Encoding: s.encoding}
	for _, p := range paths {
		req.Path = append(req.Path, ToProtoPath(p))
	}
	resp, err := s.client.Get(ctx, req)
	if err != nil {
		err = s.mapError(ctx, "Get", err)
		finish(err)
		return nil, err
	}
	finish(nil)
	var out []Update
	for _, n := range resp.GetNotification() {
		ts := time.Unix(0, n.GetTimestamp())
		prefix := FromProtoPath(n.GetPrefix())
		for _, u := range n.GetUpdate() {
			upd, err := decodeUpdate(prefix, u, ts)
			if err != nil {
				return nil, err
			}
			out = append(out, upd)
		}
	}
	return out, nil
}

// SetRequest is one Set transaction: deletes, then replaces, then
// updates, per the gNMI specification's ordering.
// Fields and referenced values must remain unchanged until Set returns.
type SetRequest struct {
	Deletes  []yang.Path
	Replaces []PathValue
	Updates  []PathValue
}

// PathValue pairs a path with its payload: JSON for subtree values,
// Value for typed scalars.
// Exactly one payload must be present. Referenced values must not be
// modified during concurrent reads.
type PathValue struct {
	Path  yang.Path
	JSON  []byte
	Value *yang.Value
	// Values writes a leaf-list, in wire order. It mirrors
	// [Update.Values] so a value read from a device round-trips back
	// to it; a non-nil empty slice writes an empty leaf-list. At most
	// one of JSON, Value, and Values carries the payload.
	Values []yang.Value
}

// Set issues one Set transaction. A rejection surfaces as
// [ErrCodeRPC]; when the peer reports per-path failure detail, the
// failing path rides along as an attribute.
func (s *Session) Set(ctx context.Context, req SetRequest) error {
	ctx, finish, err := s.unaryCtx(ctx, "Set")
	if err != nil {
		return err
	}
	preq := &gpb.SetRequest{}
	for _, p := range req.Deletes {
		preq.Delete = append(preq.Delete, ToProtoPath(p))
	}
	for _, pv := range req.Replaces {
		u, err := pv.proto()
		if err != nil {
			finish(err)
			return err
		}
		preq.Replace = append(preq.Replace, u)
	}
	for _, pv := range req.Updates {
		u, err := pv.proto()
		if err != nil {
			finish(err)
			return err
		}
		preq.Update = append(preq.Update, u)
	}

	resp, err := s.client.Set(ctx, preq)
	if err != nil {
		err = s.setError(ctx, err, resp)
		finish(err)
		return err
	}
	finish(nil)
	// Deprecated per-result messages are still what several
	// implementations use for partial failure; surface the first.
	for _, r := range resp.GetResponse() {
		if msg := r.GetMessage(); msg != nil && msg.GetMessage() != "" { //nolint:staticcheck // deprecated upstream but the only per-path failure channel real devices use
			return errs.New().
				Code(ErrCodeRPC).
				Attr("failed_path", FromProtoPath(r.GetPath()).String()).
				Msgf("Set failed for %s: %s", FromProtoPath(r.GetPath()).String(), msg.GetMessage())
		}
	}
	return nil
}

// setError decorates a Set rejection with the failing path when the
// error detail carries one.
func (s *Session) setError(ctx context.Context, err error, resp *gpb.SetResponse) error {
	mapped := s.mapError(ctx, "Set", err)
	if resp != nil {
		for _, r := range resp.GetResponse() {
			if msg := r.GetMessage(); msg != nil && msg.GetMessage() != "" { //nolint:staticcheck // deprecated upstream but the only per-path failure channel real devices use
				return errs.From(mapped).
					Attr("failed_path", FromProtoPath(r.GetPath()).String()).
					Msgf("Set failed for %s", FromProtoPath(r.GetPath()).String())
			}
		}
	}
	return mapped
}

// proto renders one PathValue as a gNMI Update.
func (pv PathValue) proto() (*gpb.Update, error) {
	u := &gpb.Update{Path: ToProtoPath(pv.Path)}
	set := 0
	for _, carried := range []bool{pv.JSON != nil, pv.Value != nil, pv.Values != nil} {
		if carried {
			set++
		}
	}
	if set > 1 {
		return nil, errs.New().Code(ErrCodeEncoding).Msgf("path %s carries more than one payload", pv.Path)
	}
	switch {
	case pv.JSON != nil:
		u.Val = &gpb.TypedValue{Value: &gpb.TypedValue_JsonIetfVal{JsonIetfVal: pv.JSON}}
	case pv.Value != nil:
		tv, err := toTypedValue(*pv.Value)
		if err != nil {
			return nil, err
		}
		u.Val = tv
	case pv.Values != nil:
		arr := &gpb.ScalarArray{Element: make([]*gpb.TypedValue, 0, len(pv.Values))}
		for i, elem := range pv.Values {
			tv, err := toTypedValue(elem)
			if err != nil {
				return nil, errs.From(err).Code(ErrCodeEncoding).Msgf("leaf-list element %d of %s", i, pv.Path)
			}
			arr.Element = append(arr.Element, tv)
		}
		u.Val = &gpb.TypedValue{Value: &gpb.TypedValue_LeaflistVal{LeaflistVal: arr}}
	default:
		return nil, errs.New().Code(ErrCodeEncoding).Msgf("path %s carries no value", pv.Path)
	}
	return u, nil
}

// decodeUpdate resolves the prefix and converts the TypedValue.
func decodeUpdate(prefix yang.Path, u *gpb.Update, ts time.Time) (Update, error) {
	full := joinPaths(prefix, FromProtoPath(u.GetPath()))
	out := Update{Path: full, Timestamp: ts}
	switch v := u.GetVal().GetValue().(type) {
	case *gpb.TypedValue_JsonIetfVal:
		out.JSON = v.JsonIetfVal
	case *gpb.TypedValue_JsonVal:
		out.JSON = v.JsonVal
	case *gpb.TypedValue_LeaflistVal:
		elems := v.LeaflistVal.GetElement()
		vals := make([]yang.Value, 0, len(elems))
		for i, el := range elems {
			val, err := fromTypedValue(el)
			if err != nil {
				return Update{}, errs.Wrapf(err, "update %s element %d", full, i)
			}
			vals = append(vals, val)
		}
		out.Values = vals
	case nil:
		return out, nil
	default:
		val, err := fromTypedValue(u.GetVal())
		if err != nil {
			return Update{}, errs.Wrapf(err, "update %s", full)
		}
		out.Value = &val
	}
	return out, nil
}

// joinPaths concatenates a notification prefix with an update path.
func joinPaths(prefix, p yang.Path) yang.Path {
	if len(prefix.Segments) == 0 {
		return p
	}
	segs := make([]yang.Segment, 0, len(prefix.Segments)+len(p.Segments))
	segs = append(segs, prefix.Segments...)
	segs = append(segs, p.Segments...)
	return yang.Path{Segments: segs}
}

// ToProtoPath maps a [yang.Path] onto the gNMI Path proto:
// segment-for-PathElem with keys. Module qualifiers do not travel —
// gNMI paths are name-based.
func ToProtoPath(p yang.Path) *gpb.Path {
	out := &gpb.Path{}
	for _, seg := range p.Segments {
		elem := &gpb.PathElem{Name: seg.Name}
		if len(seg.Keys) > 0 {
			elem.Key = make(map[string]string, len(seg.Keys))
			for _, kv := range seg.Keys {
				elem.Key[kv.Name] = kv.Value
			}
		}
		out.Elem = append(out.Elem, elem)
	}
	return out
}

// FromProtoPath maps a gNMI Path proto back onto a [yang.Path]. Key
// order within an element follows sorted key names (the proto map
// carries no order).
func FromProtoPath(p *gpb.Path) yang.Path {
	if p == nil {
		return yang.Path{}
	}
	var out yang.Path
	for _, elem := range p.GetElem() {
		seg := yang.Segment{Name: elem.GetName()}
		if len(elem.GetKey()) > 0 {
			names := make([]string, 0, len(elem.GetKey()))
			for k := range elem.GetKey() {
				names = append(names, k)
			}
			slices.Sort(names)
			for _, k := range names {
				seg.Keys = append(seg.Keys, yang.KeyValue{Name: k, Value: elem.GetKey()[k]})
			}
		}
		out.Segments = append(out.Segments, seg)
	}
	return out
}

// errorType classifies an error for the bounded error.type span
// attribute: the errs code when the error carries one, otherwise the
// concrete Go type. It never exposes error text.
func errorType(err error) string {
	if code, ok := errs.CodeOf(err); ok {
		return code.String()
	}
	return fmt.Sprintf("%T", err)
}

// toTypedValue maps a [yang.Value] onto a scalar TypedValue.
func toTypedValue(v yang.Value) (*gpb.TypedValue, error) {
	switch v.Type.Kind {
	case yang.TypeInt8, yang.TypeInt16, yang.TypeInt32, yang.TypeInt64:
		return &gpb.TypedValue{Value: &gpb.TypedValue_IntVal{IntVal: v.Int}}, nil
	case yang.TypeUint8, yang.TypeUint16, yang.TypeUint32, yang.TypeUint64:
		return &gpb.TypedValue{Value: &gpb.TypedValue_UintVal{UintVal: v.Uint}}, nil
	case yang.TypeBool:
		return &gpb.TypedValue{Value: &gpb.TypedValue_BoolVal{BoolVal: v.Bool}}, nil
	case yang.TypeBinary:
		return &gpb.TypedValue{Value: &gpb.TypedValue_BytesVal{BytesVal: v.Bytes}}, nil
	default:
		canon, err := v.Canonical()
		if err != nil {
			return nil, errs.From(err).Code(ErrCodeEncoding).Msg("render typed value")
		}
		return &gpb.TypedValue{Value: &gpb.TypedValue_StringVal{StringVal: canon}}, nil
	}
}

// fromTypedValue maps a scalar TypedValue onto a [yang.Value]. The
// receiver has no schema, so integers keep 64-bit kinds and strings
// stay strings; the caller re-parses under the leaf's declared type
// where it needs exact kinds.
func fromTypedValue(tv *gpb.TypedValue) (yang.Value, error) {
	switch v := tv.GetValue().(type) {
	case *gpb.TypedValue_IntVal:
		return yang.Value{Type: yang.Type{Kind: yang.TypeInt64}, Int: v.IntVal}, nil
	case *gpb.TypedValue_UintVal:
		return yang.Value{Type: yang.Type{Kind: yang.TypeUint64}, Uint: v.UintVal}, nil
	case *gpb.TypedValue_BoolVal:
		return yang.Value{Type: yang.Type{Kind: yang.TypeBool}, Bool: v.BoolVal}, nil
	case *gpb.TypedValue_StringVal:
		return yang.Value{Type: yang.Type{Kind: yang.TypeString}, String: v.StringVal}, nil
	case *gpb.TypedValue_AsciiVal:
		return yang.Value{Type: yang.Type{Kind: yang.TypeString}, String: v.AsciiVal}, nil
	case *gpb.TypedValue_BytesVal:
		return yang.Value{Type: yang.Type{Kind: yang.TypeBinary}, Bytes: v.BytesVal}, nil
	case *gpb.TypedValue_DoubleVal:
		return yang.Value{Type: yang.Type{Kind: yang.TypeString}, String: fmt.Sprintf("%g", v.DoubleVal)}, nil
	default:
		return yang.Value{}, errs.New().Code(ErrCodeEncoding).Msgf("unsupported TypedValue variant %T", tv.GetValue())
	}
}
