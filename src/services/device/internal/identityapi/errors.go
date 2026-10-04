package identityapi

import (
	"context"
	"errors"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/tenant"
	"go.aledante.io/FlowSeer/src/services/device/internal/accessstore"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
	"go.aledante.io/FlowSeer/src/services/device/internal/connecterr"
	"go.aledante.io/FlowSeer/src/services/device/internal/tenantstore"
)

var (
	// ErrCodeNotAMember refuses a grant without an enrollment in this tenant.
	ErrCodeNotAMember = errs.NewCode("identityapi/not-a-member")
	// ErrCodeRoleAssigned refuses deletion while a member names the role.
	ErrCodeRoleAssigned = errs.NewCode("identityapi/role-assigned")
	// ErrCodeUnknownRole refuses assignment of a role absent from this tenant.
	ErrCodeUnknownRole = errs.NewCode("identityapi/unknown-role")
	// ErrCodeUnknownIssuer refuses an identity from an unconfigured issuer.
	ErrCodeUnknownIssuer = errs.NewCode("identityapi/unknown-issuer")
)

// ClientErrors defines the client-safe status and message for store and handler
// failures. It is read-only and safe for concurrent use.
var ClientErrors = connecterr.Table{
	ErrCodeNotAMember:                {Code: connect.CodeFailedPrecondition, UserMsg: "the operator is not enrolled in this tenant"},
	ErrCodeRoleAssigned:              {Code: connect.CodeFailedPrecondition, UserMsg: "the role is assigned to a member"},
	ErrCodeUnknownRole:               {Code: connect.CodeNotFound, UserMsg: "no such role in this tenant"},
	ErrCodeUnknownIssuer:             {Code: connect.CodeInvalidArgument, UserMsg: "the issuer is not configured"},
	accessstore.ErrCodeStore:         {Code: connect.CodeUnavailable, UserMsg: "the access store cannot be reached right now"},
	accessstore.ErrCodeConflict:      {Code: connect.CodeUnavailable, UserMsg: "the access record is being written concurrently; retry"},
	accessstore.ErrCodeDecode:        {Code: connect.CodeInternal},
	accessstore.ErrCodeNotFound:      {Code: connect.CodeNotFound, UserMsg: "no such access record"},
	accessstore.ErrCodeRoleAssigned:  {Code: connect.CodeFailedPrecondition, UserMsg: "the role is assigned to a member"},
	tenantstore.ErrCodeStore:         {Code: connect.CodeUnavailable, UserMsg: "the tenant store cannot be reached right now"},
	tenantstore.ErrCodeConflict:      {Code: connect.CodeUnavailable, UserMsg: "the tenant record is being written concurrently; retry"},
	tenantstore.ErrCodeDecode:        {Code: connect.CodeInternal},
	tenantstore.ErrCodeAlreadyExists: {Code: connect.CodeAlreadyExists, UserMsg: "a tenant already holds this organization"},
	tenantstore.ErrCodeNotFound:      {Code: connect.CodeNotFound, UserMsg: "no such tenant"},
	tenantstore.ErrCodeInvalidConfig: {Code: connect.CodeInvalidArgument, UserMsg: "the tenant configuration is not well-formed"},
	tenant.ErrCodeNoTenant:           {Code: connect.CodeUnauthenticated, UserMsg: "the call is not authenticated"},
	tenant.ErrCodeInvalidTenant:      {Code: connect.CodeInvalidArgument, UserMsg: "the tenant identifier is not well-formed"},
}

func connectErr(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	var connectError *connect.Error
	if errors.As(err, &connectError) {
		return err
	}
	code, _ := errs.CodeOf(err)
	switch code {
	case accessstore.ErrCodeStore, accessstore.ErrCodeConflict, tenantstore.ErrCodeStore, tenantstore.ErrCodeConflict:
		err = errs.From(err).Retryable().Msg("identity store is unavailable")
	}
	return ClientErrors.Wrap(err)
}

func invalidRequest(err error) error {
	return connecterr.WrapAs(connect.CodeInvalidArgument, "the request is not well-formed", err)
}

func validateRequest(ctx context.Context, msg proto.Message) (authn.Principal, error) {
	p, ok := authn.FromContext(ctx)
	if !ok || p.ID == "" {
		return authn.Principal{}, connecterr.WrapAs(connect.CodeUnauthenticated, "the call is not authenticated", errs.Msg("no authenticated principal"))
	}
	if err := protovalidate.Validate(msg); err != nil {
		return authn.Principal{}, invalidRequest(err)
	}
	return p, nil
}

func adminRequest(ctx context.Context, msg proto.Message) (authn.Principal, string, error) {
	p, err := validateRequest(ctx, msg)
	if err != nil {
		return authn.Principal{}, "", err
	}
	tenantID, err := tenant.FromContext(ctx)
	if err != nil {
		return authn.Principal{}, "", connectErr(ctx, err)
	}
	return p, tenantID, nil
}

func projectionError(err error) error {
	if err == nil {
		return nil
	}
	return connecterr.WrapAs(connect.CodeUnavailable, "authorization is unavailable; retry", errs.From(err).Retryable().Msg("project access records"))
}

func memberError(ctx context.Context, err error) error {
	if code, _ := errs.CodeOf(err); code == accessstore.ErrCodeNotFound {
		err = errs.From(err).Code(ErrCodeNotAMember).Msg("operator has no enrollment")
	}
	return connectErr(ctx, err)
}
