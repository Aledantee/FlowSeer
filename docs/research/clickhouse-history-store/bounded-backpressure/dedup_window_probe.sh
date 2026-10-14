#!/bin/sh
# Probes whether ALTER TABLE ... MODIFY SETTING can turn on insert
# deduplication on an existing non-replicated table in the pinned image.
# Runs one disposable container and removes it. Needs a Docker daemon.
set -eu
image="clickhouse/clickhouse-server:26.8@sha256:9b61e3c635c04ad5bb521eb4f6e61ce7585b5580814e51c25bb9e8292ce43364"
name="dedup-window-probe-$$"
docker run -d --rm --name "$name" -e CLICKHOUSE_SKIP_USER_SETUP=1 "$image" >/dev/null
trap 'docker stop "$name" >/dev/null 2>&1 || true' EXIT
q() { docker exec "$name" clickhouse-client -q "$1"; }
i=0
until q "SELECT 1" >/dev/null 2>&1; do
	i=$((i + 1))
	[ "$i" -lt 60 ] || { echo "server did not start" >&2; exit 1; }
	sleep 1
done
echo "version: $(q 'SELECT version()')"
q "CREATE TABLE t (id UInt32) ENGINE = ReplacingMergeTree ORDER BY id SETTINGS ttl_only_drop_parts = 1"
ins() { q "INSERT INTO t SETTINGS async_insert = 0, insert_deduplication_token = '$1' VALUES (1), (2)"; }
ins T
ins T
echo "same token twice, window unset: $(q 'SELECT count() FROM t') rows"
q "ALTER TABLE t MODIFY SETTING non_replicated_deduplication_window = 1000"
echo "engine after ALTER: $(q "SELECT engine_full FROM system.tables WHERE name = 't'")"
ins U
ins U
echo "token U twice after ALTER: $(q 'SELECT count() FROM t') rows"
ins V
echo "new token V: $(q 'SELECT count() FROM t') rows"
