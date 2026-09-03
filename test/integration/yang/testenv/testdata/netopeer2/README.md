# t1 NETCONF reference-server build context

The `fixture-*.yang` files are copies of
`src/common/yang/cmd/yanggen/testdata/modules/` (Docker build contexts
cannot reach outside their directory). The yanggen testdata is the
source of truth; `TestFixtureModulesInSync` in the testenv package
fails when these copies drift.
