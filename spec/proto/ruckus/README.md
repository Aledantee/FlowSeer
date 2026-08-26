# Ruckus SmartZone GPB schemas

The Ruckus definitions under `spec/proto/ruckus/` describe the SmartZone
streaming telemetry GPB interface. The controller publishes AP, client,
switch, event, and alarm telemetry to northbound consumers;
`sci-message.proto` is the top-level envelope.

| Property | Value |
| --- | --- |
| Release | SmartZone **7.2.0** |
| Origin | Vendor GPB bundle `ruckus_sz_7.2.0_protos.tar.gz` |
| Distribution | Ruckus support portal; no public URL |
| License | Vendor confidentiality and copyright terms — vendored cache, do not redistribute |

`nanopb/nanopb.proto` is third-party source from the
[nanopb project](https://github.com/nanopb/nanopb) under the zlib license;
Ruckus includes it because the AP schemas attach nanopb field options. The
bundle's copy of `google/protobuf/descriptor.proto` is not vendored; protoc and
Buf provide the well-known type.

## Layout

The upstream bundle is flat. Files are grouped under the shared module root so
imports resolve as `ruckus/<group>/<file>`:

```text
spec/proto/ruckus/
├── ap/        AP telemetry
├── sci/       SCI northbound messages
├── icx/       ICX switch telemetry
├── scg/       Controller-side messages
└── nanopb/    nanopb option extensions
```

Import statements were rewritten from flat paths to these module-rooted paths.
No source file was renamed.

## Edition 2024 conversion

The upstream definitions are proto2. Each file was converted to Edition 2024
with proto2-equivalent behavior:

- File features pin `enum_type = CLOSED`,
  `repeated_field_encoding = EXPANDED`, `utf8_validation = NONE`,
  `json_format = LEGACY_BEST_EFFORT`, and
  `enforce_naming_style = STYLE_LEGACY`.
- Proto2 `required` fields use
  `features.field_presence = LEGACY_REQUIRED`.
- Proto2 `optional` labels were removed because Edition 2024 tracks explicit
  presence by default.
- Everything else—names, numbers, defaults, extensions, and comments—was
  preserved.

The original and converted trees were compiled to descriptor sets and compared
across messages, fields, labels and presence, defaults, packedness, enums,
extensions, and extension ranges. The conversion was verified with protoc 35.1
and Buf 1.72.0.

## Refreshing the vendor schemas

1. Fetch the new SmartZone GPB bundle from the Ruckus support portal.
2. Extract it, omit `src/google/`, and group files under the directories above.
3. Reapply the Edition 2024 feature pins, label conversion, and import rewrite.
4. Compare descriptor sets against the pristine bundle.
5. Run `buf lint --path spec/proto/ruckus` and `buf build`.
