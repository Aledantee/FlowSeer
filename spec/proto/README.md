# Protobuf Schemas

Protobuf definitions under one buf workspace (`buf.yaml` at the repo root),
split into two modules that share the `spec/proto/` root:

```
spec/proto/
├── ruckus/      Vendored Ruckus SmartZone GPB telemetry schemas (see ruckus/SOURCES.md)
└── flowseer/    FlowSeer-owned schemas — edition 2024, conventions in docs/code-style-proto.md
```

Both modules are edition 2024. The `ruckus/` module is a vendored mirror
converted mechanically from the vendor's proto2 with feature pins that keep
the wire format byte-identical; it carries relaxed lint rules and `WIRE`
breaking checks. The `flowseer/` module is held to the full style guide in
[`docs/code-style-proto.md`](../../docs/code-style-proto.md).

The SNMP MIBs for the same vendors live in [`../mib/README.md`](../mib/README.md),
the YANG models in [`../yang/README.md`](../yang/README.md), and the OpenAPI
specs in [`../openapi/README.md`](../openapi/README.md).

## Licensing

Same policy as `../mib/`: `ruckus/` is a vendored cache of vendor-published
interface definitions under the vendor's terms (the files carry Ruckus
confidentiality headers — do not redistribute). `nanopb/nanopb.proto` is from
the nanopb project (zlib license).
