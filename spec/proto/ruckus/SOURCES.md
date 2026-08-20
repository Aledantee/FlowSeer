# Ruckus SmartZone GPB Schemas — Sources

Google Protocol Buffer definitions for the RUCKUS SmartZone (SZ/vSZ) streaming
telemetry interface ("GPB Interface"): the controller publishes AP, client,
switch, event, and alarm telemetry as GPB messages (`sci-message.proto` is the
top-level envelope) to northbound consumers such as SmartCellInsight / RUCKUS
Analytics.

## Upstream

| What | Value |
|------|-------|
| Release | SmartZone **7.2.0** |
| Origin | Vendor-published GPB spec bundle `ruckus_sz_7.2.0_protos.tar.gz` (flat `src/` directory). Ruckus distributes it behind the support-portal login; no public URL. |
| License | Files carry Ruckus Wireless confidentiality/copyright headers — vendored cache, do not redistribute. |

`nanopb/nanopb.proto` is third-party (the [nanopb](https://github.com/nanopb/nanopb)
project, zlib license); Ruckus ships it in the bundle because the AP schemas
attach nanopb field options. The bundle's `google/protobuf/descriptor.proto`
copy is **not** vendored — protoc and buf resolve that import from their
built-in well-known types.

## Layout

The upstream bundle is flat; files are grouped here so imports resolve from
the buf module root (`spec/proto/`) as `ruckus/<group>/<file>`:

```
spec/proto/ruckus/
├── ap/        AP telemetry: ap_common + status/report/client/mesh/rogue/avc/
│              wired_client/hccd_report/peerlist
├── sci/       SCI northbound envelope + event/configuration/alarm/pci/rogue
├── icx/       ICX switch telemetry (switches.proto, switch_all.proto)
├── scg/       Controller-side messages: query/storage commons, simple-storage
│              options, ScgSessMgrPubIpc + session_manager
└── nanopb/    nanopb option extensions (third-party, imported by ap/)
```

Import statements were rewritten from the flat `import "x.proto"` to the
module-rooted `import "ruckus/<group>/x.proto"`. No file was renamed.

## Edition 2024 conversion

Upstream is proto2 (explicitly or by implicit default). Each file was
converted to `edition = "2024"` with proto2-equivalent feature pins so the
descriptors — and therefore the wire format — stay identical:

- File level: `features.enum_type = CLOSED`,
  `features.repeated_field_encoding = EXPANDED`,
  `features.utf8_validation = NONE`,
  `features.json_format = LEGACY_BEST_EFFORT`,
  `features.enforce_naming_style = STYLE_LEGACY`.
- Field level: `required` → `[features.field_presence = LEGACY_REQUIRED]`;
  the `optional` label dropped (edition 2024 defaults to explicit presence).
- `syntax = "proto2";` lines replaced by the edition declaration; everything
  else (names, numbers, defaults, extensions, comments) untouched.

Equivalence was verified by compiling the original bundle and the converted
tree to `FileDescriptorSet`s and diffing every message, field (number, type,
label/presence, default, packedness), enum, extension, and extension range:
identical. The edition 2024 toolchain floor is documented in
[`docs/code-style-proto.md`](../../../docs/code-style-proto.md) (buf ≥ 1.68.1,
protoc ≥ 35.x); this conversion was verified with protoc 35.1 and buf 1.72.0.

## Refresh

1. Fetch the GPB spec bundle for the new SmartZone release from the Ruckus
   support portal.
2. Extract, drop `src/google/`, sort files into the groups above (new files:
   group by prefix — `ap_*` → `ap/`, `sci-*` → `sci/`, switch → `icx/`,
   controller-side → `scg/`).
3. Re-apply the conversion above (header swap, feature pins, label rewrite,
   import rewrite).
4. Re-run the descriptor-set diff against the pristine bundle, then
   `buf lint --path spec/proto/ruckus` and `buf build`.
