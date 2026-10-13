package integration_test

// clickHouseImage is the ClickHouse server the history store tests and the lab
// run. The digest is the image the history benchmark measured
// (docs/research/clickhouse-history-store/benchmark/README.md), so a change
// here moves the benchmark's evidence with it. The lab fixture test checks
// deploy/lab/compose.yaml against it.
const clickHouseImage = "clickhouse/clickhouse-server:26.8@sha256:9b61e3c635c04ad5bb521eb4f6e61ce7585b5580814e51c25bb9e8292ce43364"
