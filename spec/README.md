# Protocol Specifications

This directory contains the machine-readable protocol and data-model
definitions that FlowSeer owns or consumes when communicating with network
devices. FlowSeer-owned schemas are source files; third-party definitions are
vendored references and retain their upstream licensing and provenance.

## Contents

| Directory | Description |
| --- | --- |
| [`mib/`](mib/README.md) | SNMP MIBs from standards bodies and device vendors. |
| [`openapi/`](openapi/README.md) | OpenAPI and Swagger documents for REST-managed devices. |
| [`proto/`](proto/README.md) | FlowSeer-owned protobuf schemas and vendored Ruckus telemetry schemas. |
| [`yang/`](yang/README.md) | YANG models used with NETCONF, RESTCONF, and gNMI. |

Each subtree documents its layout, upstream sources, refresh process, and
licensing constraints. Vendor-specific `SOURCES.md` files record exact source
URLs where applicable.

## Making changes

- Edit FlowSeer-owned definitions at their source under `spec/`; do not edit
  files under `generated/` or `frontend/web/generated/` by hand.
- Follow [`docs/code-style-proto.md`](../docs/code-style-proto.md) and
  [`docs/conventions/protobuf.md`](../docs/conventions/protobuf.md) when changing
  FlowSeer protobuf schemas.
- Preserve upstream content and update its source record when refreshing a
  vendored specification.
- Check the relevant subtree's licensing notes before redistributing vendor
  definitions.

The repository-level [`buf.yaml`](../buf.yaml) and
[`buf.gen.yaml`](../buf.gen.yaml) configure protobuf linting, compatibility
checks, and code generation.
