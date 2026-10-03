---
title: A Custom MethodOptions Extension Resolves Only Where Its Generated Package Is Linked
date: 2026-10-03
last_verified: 2026-10-03
category: conventions
module: src/services/device/internal/authz
problem_type: convention
component: service_layer
severity: high
applies_when:
  - "Reading custom protobuf MethodOptions or other descriptor extensions from connect.Spec.Schema or reflection"
  - "Adding or testing an interceptor that inspects protobuf options declared on service methods"
  - "Investigating why proto.HasExtension or proto.GetExtension returns false for a method option declared in schema"
related_components: [api_layer, conformance_gate, code_generation]
tags: [protobuf, connect, method-options, extensions, protoregistry]
---

# A custom MethodOptions extension resolves only where its generated package is linked

Protobuf options declared on RPC methods (`google.protobuf.MethodOptions`) store custom extension data on method descriptors. When Connect mounts a service handler, it supplies the method descriptor to `req.Spec().Schema` through `connect.WithSchema`. Reading that extension with `proto.HasExtension` or `proto.GetExtension` succeeds only if the Go package defining the extension is linked into the binary.

The protobuf compiler (`protoc-gen-go`) does not generate Go imports for `.proto` files imported solely for custom options. In `spec/proto/flowseer/api/capture/v1/capture_service.proto:8,18-23`, the schema imports `flowseer/authz/v1/rule.proto` to annotate RPCs with `(flowseer.authz.v1.rule)`. The generated Go file `generated/go/proto/flowseer/api/capture/v1/capture_service.pb.go:13-21` imports domain models, but contains no import for `generated/go/proto/flowseer/authz/v1`.

At runtime, `google.golang.org/protobuf` decodes descriptor options lazily against `protoregistry.GlobalTypes`. Because the generated service package does not import the option package, the extension symbol is never registered unless another package imports it. Without that import, protobuf-go treats the option bytes as unmarshaled unknown fields. `proto.HasExtension` returns false, and `proto.GetExtension` returns the default value.

```go
// src/services/device/internal/authz/interceptor.go:67-77
opts, ok := md.Options().(*descriptorpb.MethodOptions)
if !ok || opts == nil || !proto.HasExtension(opts, authzv1.E_Rule) {
	return nil, permissionDenied(errs.New().Code(ErrCodeUnsupportedRule).
		Msg("method carries no authorization rule"))
}

rule, ok := proto.GetExtension(opts, authzv1.E_Rule).(*authzv1.Rule)
if !ok || rule == nil {
	return nil, permissionDenied(errs.New().Code(ErrCodeUnsupportedRule).
		Msg("method carries no authorization rule"))
}
```

To resolve the extension:
1. Ensure the package inspecting descriptor options imports the generated extension package (`src/services/device/internal/authz/interceptor.go:12`). Naming `authzv1.E_Rule` links the package and registers the extension in `protoregistry.GlobalTypes`.
2. Conformance tests that walk global descriptors (`test/conformance/proto/api_authorization_test.go:20,78-85`) must import the generated extension package directly, alongside the service packages they inspect.
3. If an interceptor or reflection helper runs without a direct reference to the extension package, add a blank import (`_ "go.aledante.io/FlowSeer/generated/go/proto/..."`) to link its descriptor registration into the binary.

## Evidence

- `spec/proto/flowseer/api/capture/v1/capture_service.proto:8,18-23` imports `flowseer/authz/v1/rule.proto` and sets `option (flowseer.authz.v1.rule)`.
- `generated/go/proto/flowseer/api/capture/v1/capture_service.pb.go:13-21` shows generated Go imports omit the extension package.
- `generated/go/proto/flowseer/api/capture/v1/capturev1connect/capture_service.connect.go:206` sets `connect.WithSchema` with the method descriptor.
- `src/services/device/internal/authz/interceptor.go:12,68,73` links `authzv1` by naming `authzv1.E_Rule` to read `Rule` from `req.Spec().Schema`.
- `test/conformance/proto/api_authorization_test.go:20,78-85` imports `authzv1` and validates rules on method descriptors across `flowseer.api.`.
- `docs/plans/2026-09-30-1139-feat-operator-authorization-phase1-plan.md:37-49` documents that the extension resolves without a separate startup registry because `authzv1.E_Rule` is linked.

## What this does not cover

This convention does not apply to standard protobuf message fields, which generate explicit Go imports in dependent packages. It does not alter protovalidate constraints, which use their own descriptor registry mechanisms.
