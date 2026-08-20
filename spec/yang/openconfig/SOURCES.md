# OpenConfig standard YANG modules

Upstream OpenConfig (and bundled third-party IETF) files that the
Aruba AOS-CX 10.17 tree references without shipping: the
`openconfig-platform-common` submodule its `openconfig-platform`
0.30.0 includes, `ietf-yang-types`/`ietf-inet-types`, and the full
import closure of its `openconfig-system` 2.3.0 (which pulls
`openconfig-network-instance` and therefore the BGP / MPLS / ISIS /
OSPF / RIB / policy model families).

- Upstream: https://github.com/openconfig/public (Apache-2.0)
- Commit: `03f1c1fe582aeabbae67e80f74e099507a3e2343` — the release
  where `openconfig-system.yang` carries `openconfig-version 2.3.0`,
  matching the Aruba bundle's copy. `openconfig-platform-common.yang`
  alone comes from commit
  `960cfd9366d78c8ab61facf2655dfc3e7f4169f1`, whose parent
  `openconfig-platform.yang` matches Aruba's 0.30.0.
- Vendored: 2026-08-20, exactly the transitive import closure the
  Aruba set needs (90 files) — not the full upstream release.
- Files under `release/models/**` and `third_party/ietf/` upstream;
  flattened here into one directory, filenames unchanged.

A vendor's own tree, listed earlier in `yanggen.yaml`, shadows any
same-named module here.

## Refresh

Re-run the closure fetch against the pinned commit (or a newer one if
the Aruba bundle moves): add this directory to the vendor's search
paths, run `yanggen -verify`, and fetch each reported missing module
from the pinned commit until verify is clean.
