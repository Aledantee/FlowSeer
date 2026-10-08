#!/usr/bin/env bash
# FlowSeer history store benchmark driver (single-node ClickHouse 26.8 in Docker).
#
#   run.sh all          start, init, steps s1 s1t s4 s16, scope sweep, redelivery, summary
#   run.sh start        start (or restart) the pinned container
#   run.sh init         schema, users, generator setup, results/meta.env
#   run.sh step <s>     generate step s (s1|s1t|s4|s16), settle, measure, collect
#   run.sh sweep        scope sweep on the current data (run after s16)
#   run.sh redeliver    dedup snapshot, 1 % redelivery of T0, snapshots after and settled
#   run.sh summary      flatness, model, partner, dedup tables from the logs
#   run.sh stop         stop the container (the named volume keeps the data)
#   run.sh destroy      remove the container and the volume
#
# Results land in results/ as TSV. Environment overrides: CONTAINER, VOLUME, QUERY_RUNS
# (default 5, the first run of each query is discarded), SETTLE_MAX seconds (default 1800).
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
IMAGE="clickhouse/clickhouse-server@sha256:9b61e3c635c04ad5bb521eb4f6e61ce7585b5580814e51c25bb9e8292ce43364"
CONTAINER="${CONTAINER:-fsbench-ch}"
VOLUME="${VOLUME:-fsbench-data}"
CH_PASSWORD="bench"
RESULTS="${RESULTS:-$HERE/results}"
QUERY_RUNS="${QUERY_RUNS:-5}"
SETTLE_MAX="${SETTLE_MAX:-1800}"
QDIR="$RESULTS/queries"
STEPS=(s1 s1t s4 s16)
SWEEP_SIZES=(10 25 50 100 200)
SWEEP_QUERIES=(M05 M06 M09N M14 M15)
export CONTAINER RESULTS CH_PASSWORD

log() { printf '%s run %s\n' "$(date -u +%H:%M:%S)" "$*" >&2; }

# ch [client args...]: clickhouse-client as the default user, no stdin.
ch() { docker exec "$CONTAINER" clickhouse-client --prefer_column_name_to_alias=1 --password "$CH_PASSWORD" "$@"; }

# ch_file <file>: run a SQL file through stdin, continuing past rejected statements.
ch_file() {
    docker exec -i "$CONTAINER" clickhouse-client --prefer_column_name_to_alias=1 --password "$CH_PASSWORD" --ignore-error < "$1"
}

# chu <user> [client args...]
chu() {
    local user="$1"
    shift
    docker exec "$CONTAINER" clickhouse-client --prefer_column_name_to_alias=1 --user "$user" --password "$CH_PASSWORD" "$@"
}

# ---------------------------------------------------------------------------
# Container
# ---------------------------------------------------------------------------

cmd_start() {
    mkdir -p "$RESULTS" "$HERE/.config"
    # part_log is needed for parts per insert; flush intervals shortened so collect sees
    # the last step. cpu_slot_preemption: dossier 11 section 5.1 sets it so CPU slots are
    # shared fairly; the workload page states it is on by default, so this is a no-op there.
    cat > "$HERE/.config/zz-bench.xml" <<'XML'
<clickhouse>
    <part_log>
        <database>system</database>
        <table>part_log</table>
        <flush_interval_milliseconds>2000</flush_interval_milliseconds>
    </part_log>
    <query_log>
        <database>system</database>
        <table>query_log</table>
        <flush_interval_milliseconds>2000</flush_interval_milliseconds>
    </query_log>
    <cpu_slot_preemption>true</cpu_slot_preemption>
</clickhouse>
XML
    if docker container inspect "$CONTAINER" >/dev/null 2>&1; then
        docker start "$CONTAINER" >/dev/null
    else
        docker volume create "$VOLUME" >/dev/null
        docker run -d --name "$CONTAINER" \
            --memory 7g --memory-swap 7g --cpus 11 \
            --ulimit nofile=262144:262144 \
            -v "$VOLUME:/var/lib/clickhouse" \
            -v "$HERE/.config/zz-bench.xml:/etc/clickhouse-server/config.d/zz-bench.xml:ro" \
            -e CLICKHOUSE_PASSWORD="$CH_PASSWORD" \
            -e CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT=1 \
            "$IMAGE" >/dev/null
    fi
    local i
    for ((i = 0; i < 120; i++)); do
        if ch --query "SELECT 1" >/dev/null 2>&1; then
            ch --query "SELECT version(), uptime(), getSetting('max_threads')" > "$RESULTS/server.tsv"
            log "server up: $(cut -f1 "$RESULTS/server.tsv")"
            return 0
        fi
        sleep 1
    done
    echo "run.sh: server did not answer within 120 s" >&2
    docker logs --tail 50 "$CONTAINER" >&2 || true
    exit 1
}

cmd_stop() { docker stop "$CONTAINER" >/dev/null; }

cmd_destroy() {
    docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
    docker volume rm "$VOLUME" >/dev/null 2>&1 || true
}

# ---------------------------------------------------------------------------
# Schema, users, generator setup, time window
# ---------------------------------------------------------------------------

cmd_init() {
    mkdir -p "$RESULTS"
    if [[ "$(ch --query "EXISTS TABLE flowseer.interface_samples")" == "1" ]]; then
        log "schema present, skipping 00 and 01"
    else
        # --ignore-error keeps going past a statement 26.8 rejects (the MAY REJECT lines);
        # the failures are logged and the rest of the file still applies.
        ch_file "$HERE/00_schema.sql" 2>> "$RESULTS/client_errors.log" \
            || log "00_schema.sql had errors, see client_errors.log"
        ch_file "$HERE/01_users.sql" 2>> "$RESULTS/client_errors.log" \
            || log "01_users.sql had errors, see client_errors.log"
    fi
    "$HERE/gen.sh" setup
    if [[ ! -f "$RESULTS/meta.env" ]]; then
        # The window ends at the most recent UTC midnight, so TTLs (90 days and more) never
        # fire during the run. The values are fixed once; reruns reuse them.
        local end start backfill
        end="$(ch --query "SELECT toUnixTimestamp(toStartOfDay(now('UTC')))")"
        start=$((end - 7 * 86400))
        backfill=$((start - 7 * 86400))
        {
            echo "END=$end"
            echo "START=$start"
            echo "BACKFILL=$backfill"
        } > "$RESULTS/meta.env"
    fi
    split_queries
}

meta_value() { sed -n "s/^$1=//p" "$RESULTS/meta.env"; }

# Split 03_queries.sql into results/queries/<id>.sql and <id>.meta.
split_queries() {
    rm -rf "$QDIR"
    mkdir -p "$QDIR"
    awk -v dir="$QDIR" '
        /^-- Q:/ {
            id = substr($2, 3)
            sqlf = dir "/" id ".sql"; metaf = dir "/" id ".meta"
            printf "pattern=%s\nshape=%s\n", $3, $4 > metaf
            next
        }
        id == "" { next }
        /^-- user: / { sub(/^-- user: /, ""); print "user=" $0 >> metaf; next }
        /^-- kind: / { sub(/^-- kind: /, ""); print "kind=" $0 >> metaf; next }
        /^-- expect: / { sub(/^-- expect: /, ""); print "expect=" $0 >> metaf; next }
        /^-- model_sql: / { sub(/^-- model_sql: /, ""); print "model_sql=" $0 >> metaf; next }
        /^-- / { next }
        /^-- ?=+/ { next }
        { print >> sqlf }
    ' "$HERE/03_queries.sql"
}

qmeta() { sed -n "s/^$2=//p" "$QDIR/$1.meta"; }
qsql() { sed -e 's/;[[:space:]]*$//' "$QDIR/$1.sql"; }
query_ids() { find "$QDIR" -name '*.meta' -exec basename {} .meta \; | sort; }

# ---------------------------------------------------------------------------
# Fixed query parameters (computed once after s1; T0 data never changes)
# ---------------------------------------------------------------------------

cmd_params() {
    local f="$RESULTS/params.env"
    if [[ -f "$f" ]]; then
        return 0
    fi
    local end di model_row probe_row tenants
    end="$(meta_value END)"
    di="$(ch --format TSV --query "
        SELECT device_id, interface_name FROM bench.interfaces FINAL
        WHERE tenant_idx = 0 AND site_id = 't0000-s00' AND err_old > 0 AND rate_Bps >= 125000
        ORDER BY role = 'access' DESC, dev_idx, if_idx LIMIT 1")"
    if [[ -z "$di" ]]; then
        di="$(ch --format TSV --query "
            SELECT device_id, interface_name FROM bench.interfaces FINAL
            WHERE tenant_idx = 0 AND site_id = 't0000-s00' AND err_old > 0
            ORDER BY dev_idx, if_idx LIMIT 1")"
    fi
    model_row="$(ch --format TSV --query "
        SELECT model, topK(1)(sw_old)[1], 'cisco-9.4' FROM bench.devices FINAL
        WHERE tenant_idx = 0 AND upgrade_at > 0 GROUP BY model ORDER BY count() DESC, model LIMIT 1")"
    probe_row="$(ch --format TSV --query "
        SELECT device_id, toString(target_ip) FROM bench.probe_targets FINAL
        WHERE tenant_idx = 0 AND site_id = 't0000-s00' AND target_idx = 1")"
    tenants="[$(for n in $(seq 0 19); do printf "'t%04d'," "$n"; done | sed 's/,$//')]"
    {
        echo "t=t0000"
        echo "site=t0000-s00"
        echo "d=$(cut -f1 <<< "$di")"
        echo "i=$(cut -f2 <<< "$di")"
        echo "now=$(ch --query "SELECT toString(toDateTime($end, 'UTC'))")"
        echo "T=$(ch --query "SELECT toString(toDateTime($end - 30 * 3600 + 420, 'UTC'))")"
        echo "devices=$(ch --format TSVRaw --query "
            SELECT concat('[', arrayStringConcat(arrayMap(x -> concat('''', toString(x), ''''), groupArray(device_id)), ','), ']')
            FROM (SELECT device_id FROM bench.devices FINAL WHERE tenant_idx = 0 AND site_id = 't0000-s00' ORDER BY dev_idx)")"
        echo "mac=$(ch --query "
            SELECT mac FROM flowseer.fdb_presence_by_mac
            WHERE tenant_id = 't0000' AND day >= toDate(toDateTime($(meta_value START), 'UTC'))
            GROUP BY mac ORDER BY uniqExact(device_id) DESC, uniqExact(day) DESC, mac LIMIT 1")"
        echo "model=$(cut -f1 <<< "$model_row")"
        echo "sw_a=$(cut -f2 <<< "$model_row")"
        echo "sw_b=$(cut -f3 <<< "$model_row")"
        echo "tenants=$tenants"
        echo "region=$(ch --query "SELECT cityHash64('t0000', 't0000-r0')")"
        echo "probe_dev=$(cut -f1 <<< "$probe_row")"
        echo "target=$(cut -f2 <<< "$probe_row")"
        echo "t1=t0001"
        echo "t1_dev=$(ch --query "SELECT device_id FROM bench.devices FINAL WHERE tenant_idx = 1 ORDER BY dev_idx LIMIT 1")"
    } > "$f"
    log "params written to $f"
}

# load_params [key=value ...]: PARAM_ARGS from params.env, with the given keys replaced,
# so no parameter is passed to the client twice.
PARAM_ARGS=()
load_params() {
    PARAM_ARGS=()
    local line key value o
    while IFS= read -r line; do
        [[ -z "$line" ]] && continue
        key="${line%%=*}"
        value="${line#*=}"
        for o in "$@"; do
            if [[ "${o%%=*}" == "$key" ]]; then
                value="${o#*=}"
            fi
        done
        PARAM_ARGS+=("--param_${key}=${value}")
    done < "$RESULTS/params.env"
}

# Device list of the first n T0 devices (scope sweep).
devices_first() {
    ch --format TSVRaw --query "
        SELECT concat('[', arrayStringConcat(arrayMap(x -> concat('''', toString(x), ''''), groupArray(device_id)), ','), ']')
        FROM (SELECT device_id FROM bench.devices FINAL WHERE tenant_idx = 0 ORDER BY dev_idx LIMIT $1)"
}

# ---------------------------------------------------------------------------
# Settle: no OPTIMIZE; wait until no merge runs for three polls in a row, bounded.
# ---------------------------------------------------------------------------

settle() {
    local step="$1" t0 quiet=0 n
    t0="$(date +%s)"
    while :; do
        n="$(ch --query "SELECT count() FROM system.merges WHERE database = 'flowseer'")"
        if [[ "$n" == "0" ]]; then
            quiet=$((quiet + 1))
            [[ "$quiet" -ge 3 ]] && break
        else
            quiet=0
        fi
        if (( $(date +%s) - t0 > SETTLE_MAX )); then
            log "settle: still $n merges after ${SETTLE_MAX}s, measuring anyway"
            break
        fi
        sleep 10
    done
    printf '%s\t%s\t%s\n' "$step" "$(( $(date +%s) - t0 ))" "$n" >> "$RESULTS/settle.tsv"
    log "settled $step in $(( $(date +%s) - t0 ))s"
}

# ---------------------------------------------------------------------------
# Measurement
# ---------------------------------------------------------------------------

# run_query <user> <log_comment> <sql> [key=value parameter overrides...]
run_query() {
    local user="$1" comment="$2" sql="$3"
    shift 3
    load_params "$@"
    local extra=()
    if [[ "$user" == "default" ]]; then
        extra=(--use_query_cache=0 --use_query_condition_cache=0)
    fi
    # ${a[@]+"${a[@]}"} keeps empty arrays safe under set -u on macOS bash 3.2.
    chu "$user" --format Null --log_comment="$comment" ${extra[@]+"${extra[@]}"} "${PARAM_ARGS[@]}" --query "$sql" \
        2>> "$RESULTS/client_errors.log" || log "query failed: $comment (see client_errors.log)"
    load_params
}

measure() {
    local step="$1" id user kind sql model_sql r v tenant
    load_params
    mkdir -p "$RESULTS/explain/$step"
    for id in $(query_ids); do
        kind="$(qmeta "$id" kind)"
        kind="${kind:-measure}"
        user="$(qmeta "$id" user)"
        sql="$(qsql "$id")"
        case "$kind" in
            measure)
                for ((r = 1; r <= QUERY_RUNS; r++)); do
                    run_query "$user" "Q=${id};step=${step};run=${r}" "$sql"
                done
                ;;
            partner)
                for ((r = 1; r <= QUERY_RUNS; r++)); do
                    run_query "$user" "Q=${id};step=${step};run=${r}" "$sql"
                done
                # Per-tenant decomposition: two runs per tenant of P, the second is kept.
                for tenant in $(seq -f 't%04g' 0 19); do
                    for r in 1 2; do
                        run_query "$user" "Q=${id};step=${step};run=${r};tenant=${tenant}" "$sql" \
                            "tenants=['${tenant}']"
                    done
                done
                ;;
            check)
                {
                    printf '## %s %s\n' "$id" "$(qmeta "$id" shape)"
                    chu "$user" --format TSVWithNames "${PARAM_ARGS[@]}" --query "$sql" 2>&1 || true
                } >> "$RESULTS/checks_${step}.tsv"
                continue
                ;;
            dedup)
                continue
                ;;
        esac
        model_sql="$(qmeta "$id" model_sql)"
        if [[ -n "$model_sql" ]]; then
            v="$(ch "${PARAM_ARGS[@]}" --query "$model_sql" 2>> "$RESULTS/client_errors.log" || echo "")"
            if [[ -n "$v" ]]; then
                ch --query "INSERT INTO bench.model VALUES ('${step}', '${id}', ${v})"
            fi
        fi
        if [[ "$id" == M* ]]; then
            ch "${PARAM_ARGS[@]}" --use_query_condition_cache=0 --use_skip_indexes_on_data_read=0 \
                --query "EXPLAIN indexes = 1 $sql" > "$RESULTS/explain/$step/$id.txt" 2>&1 || true
        fi
    done
}

cmd_sweep() {
    local n id user sql devs
    load_params
    for n in "${SWEEP_SIZES[@]}"; do
        devs="$(devices_first "$n")"
        for id in "${SWEEP_QUERIES[@]}"; do
            user="$(qmeta "$id" user)"
            sql="$(qsql "$id")"
            for ((r = 1; r <= QUERY_RUNS; r++)); do
                run_query "$user" "Q=${id};step=sweep${n};run=${r}" "$sql" "devices=${devs}"
            done
        done
    done
    ch --query "SYSTEM FLUSH LOGS"
    ch --format TSVWithNames --query "
        SELECT extract(log_comment, 'Q=([^;]+)') AS qid,
               toUInt32(extract(log_comment, 'step=sweep([0-9]+)')) AS scope_devices,
               median(read_rows) AS read_rows, quantileExact(0.95)(query_duration_ms) AS p95_ms,
               max(memory_usage) AS memory_usage_max
        FROM system.query_log
        WHERE type = 'QueryFinish' AND is_initial_query AND log_comment LIKE 'Q=%;step=sweep%'
          AND toUInt32OrZero(extract(log_comment, 'run=([0-9]+)')) > 1
        GROUP BY qid, scope_devices
        ORDER BY qid, scope_devices" > "$RESULTS/sweep.tsv"
}

# ---------------------------------------------------------------------------
# Collection
# ---------------------------------------------------------------------------

collect() {
    local step="$1" tbl
    ch --query "SYSTEM FLUSH LOGS"
    ch --format TSVWithNames --query "
        SELECT '${step}' AS step, extract(log_comment, 'Q=([^;]+)') AS qid, user, count() AS runs,
               median(read_rows) AS read_rows, max(read_rows) - min(read_rows) AS read_rows_spread,
               median(read_bytes) AS read_bytes, median(result_rows) AS result_rows,
               max(memory_usage) AS memory_usage_max,
               quantileExact(0.5)(query_duration_ms) AS p50_ms, quantileExact(0.95)(query_duration_ms) AS p95_ms
        FROM system.query_log
        WHERE type = 'QueryFinish' AND is_initial_query AND log_comment LIKE 'Q=%'
          AND extract(log_comment, 'step=([^;]+)') = '${step}'
          AND log_comment NOT LIKE '%tenant=%'
          AND toUInt32OrZero(extract(log_comment, 'run=([0-9]+)')) > 1
        GROUP BY qid, user
        ORDER BY qid" > "$RESULTS/query_stats_${step}.tsv"

    ch --format TSVWithNames --query "
        SELECT '${step}' AS step, extract(log_comment, 'Q=([^;]+)') AS qid, user, type, exception_code,
               substring(replaceAll(exception, '\n', ' '), 1, 400) AS exception
        FROM system.query_log
        WHERE type IN ('ExceptionBeforeStart', 'ExceptionWhileProcessing') AND log_comment LIKE 'Q=%'
          AND extract(log_comment, 'step=([^;]+)') = '${step}'
        ORDER BY qid" > "$RESULTS/query_errors_${step}.tsv"

    ch --format TSVWithNames --query "
        WITH base AS
        (
            SELECT extract(log_comment, 'Q=([^;]+)') AS qid, extract(log_comment, 'tenant=([^;]+)') AS tenant,
                   toUInt32OrZero(extract(log_comment, 'run=([0-9]+)')) AS run, read_rows, query_duration_ms
            FROM system.query_log
            WHERE type = 'QueryFinish' AND is_initial_query AND log_comment LIKE 'Q=P%'
              AND extract(log_comment, 'step=([^;]+)') = '${step}'
        )
        SELECT '${step}' AS step, qid,
               medianIf(read_rows, tenant = '' AND run > 1) AS read_rows_P,
               sumIf(read_rows, tenant != '' AND run = 2) AS read_rows_sum_per_tenant,
               round(read_rows_P / greatest(read_rows_sum_per_tenant, 1), 3) AS ratio,
               abs(ratio - 1) <= 0.10 AS additive_within_10pct,
               quantileExactIf(0.95)(query_duration_ms, tenant = '' AND run > 1) AS p95_ms_P,
               sumIf(query_duration_ms, tenant != '' AND run = 2) AS sum_ms_per_tenant
        FROM base
        GROUP BY qid
        ORDER BY qid" > "$RESULTS/partner_${step}.tsv"

    ch --format TSVWithNames --query "
        WITH q AS
        (
            SELECT query_id, extract(log_comment, 'table=([^;]+)') AS section, query_duration_ms, memory_usage
            FROM system.query_log
            WHERE type = 'QueryFinish' AND query_kind = 'Insert' AND log_comment LIKE 'gen;step=${step};%'
        ),
        secs AS
        (
            SELECT section, count() AS inserts, sum(query_duration_ms) / 1000 AS seconds,
                   max(memory_usage) AS insert_memory_max
            FROM q GROUP BY section
        )
        SELECT '${step}' AS step, q.section AS section, p.table AS table,
               any(s.inserts) AS n_inserts, count() AS new_parts,
               round(new_parts / n_inserts, 2) AS parts_per_insert,
               sum(p.rows) AS n_rows, round(n_rows / greatest(any(s.seconds), 0.001)) AS rows_per_s,
               round(any(s.seconds), 1) AS insert_seconds, any(s.insert_memory_max) AS insert_memory_max
        FROM system.part_log AS p
        INNER JOIN q ON p.query_id = q.query_id
        INNER JOIN secs AS s ON s.section = q.section
        WHERE p.event_type = 'NewPart' AND p.database = 'flowseer'
        GROUP BY q.section, p.table
        ORDER BY q.section, p.table" > "$RESULTS/inserts_${step}.tsv"

    ch --format TSVWithNames --query "
        SELECT '${step}' AS step, table, sum(rows) AS rows, count() AS parts, uniqExact(partition) AS partitions,
               sum(bytes_on_disk) AS bytes_on_disk, sum(data_compressed_bytes) AS compressed,
               sum(data_uncompressed_bytes) AS uncompressed,
               round(sum(data_compressed_bytes) / greatest(sum(rows), 1), 2) AS bytes_per_row,
               round(sum(bytes_on_disk) / greatest(sum(rows), 1), 2) AS disk_bytes_per_row,
               sum(primary_key_bytes_in_memory) AS pk_bytes_in_memory,
               sum(secondary_indices_compressed_bytes) AS skip_index_bytes,
               max(rows) AS largest_part_rows
        FROM system.parts
        WHERE active AND database = 'flowseer'
        GROUP BY table
        ORDER BY table" > "$RESULTS/tables_${step}.tsv"

    ch --format TSVWithNames --query "
        SELECT '${step}' AS step, c.table AS table, c.name AS column, c.type AS type,
               c.data_compressed_bytes AS compressed, c.data_uncompressed_bytes AS uncompressed,
               round(c.data_compressed_bytes / greatest(r.rows, 1), 3) AS bytes_per_row
        FROM system.columns AS c
        LEFT JOIN (SELECT table, sum(rows) AS rows FROM system.parts WHERE active AND database = 'flowseer' GROUP BY table) AS r
            ON r.table = c.table
        WHERE c.database = 'flowseer' AND c.data_compressed_bytes > 0
        ORDER BY c.table, c.data_compressed_bytes DESC" > "$RESULTS/columns_${step}.tsv"

    ch --format TSVWithNames --query "
        SELECT '${step}' AS step, table, partition, count() AS parts, sum(rows) AS rows
        FROM system.parts
        WHERE active AND database = 'flowseer'
        GROUP BY table, partition
        ORDER BY table, partition" > "$RESULTS/partitions_${step}.tsv"

    ch --format TSVWithNames --query "
        SELECT '${step}' AS step, database, name, status, element_count, bytes_allocated, loading_duration,
               last_exception
        FROM system.dictionaries
        ORDER BY database, name" > "$RESULTS/dictionaries_${step}.tsv"

    {
        printf 'step\ttable\tt0_rows\ttotal_rows\ttenants\n'
        for tbl in interface_samples interface_hourly interface_hourly_by_time interface_daily interface_changes \
                   fdb_presence fdb_presence_by_mac fdb_vlan_daily set_walks syslog syslog_by_scope syslog_daily \
                   probe_intervals probe_hourly; do
            ch --format TSV --query "
                SELECT '${step}', '${tbl}', countIf(tenant_id = 't0000'), count(), uniqExact(tenant_id)
                FROM flowseer.${tbl}"
        done
    } > "$RESULTS/rows_${step}.tsv"

    printf 'step\tvolume_bytes\n%s\t%s\n' "$step" \
        "$(docker exec "$CONTAINER" du -sb /var/lib/clickhouse | cut -f1)" > "$RESULTS/disk_${step}.tsv"

    if [[ -d "$RESULTS/explain/$step" ]]; then
        {
            printf 'step\tqid\tline\n'
            for f in "$RESULTS/explain/$step"/*.txt; do
                grep -E 'Granules|Parts|Condition|Name:|Ranges' "$f" \
                    | sed -e "s/^[[:space:]]*//" -e "s/^/${step}\t$(basename "$f" .txt)\t/" || true
            done
        } > "$RESULTS/granules_${step}.tsv"
    fi
    log "collected $step"
}

cmd_step() {
    local step="$1"
    "$HERE/gen.sh" "$step"
    settle "$step"
    cmd_params
    measure "$step"
    collect "$step"
}

# ---------------------------------------------------------------------------
# Redelivery and dedup
# ---------------------------------------------------------------------------

dedup_snapshot() {
    local phase="$1" id out
    load_params
    for id in $(query_ids); do
        [[ "$(qmeta "$id" kind)" == "dedup" ]] || continue
        out="$(ch --format TSV "${PARAM_ARGS[@]}" --query "$(qsql "$id")" 2>&1 | tr '\t\n' ' |' | sed 's/|$//')"
        printf '%s\t%s\t%s\t%s\n' "$phase" "$id" "$(qmeta "$id" expect)" "$out" >> "$RESULTS/dedup_raw.tsv"
    done
}

cmd_redeliver() {
    : > "$RESULTS/dedup_raw.tsv"
    dedup_snapshot before
    "$HERE/gen.sh" redeliver
    dedup_snapshot after
    settle redeliver
    dedup_snapshot settled
    collect redeliver
    awk -F '\t' '
        { v[$2, $1] = $4; e[$2] = $3; ids[$2] = 1 }
        END {
            print "qid\texpect\tbefore\tafter\tsettled\tafter_equal\tsettled_equal\tpass"
            for (id in ids) {
                ae = (v[id, "before"] == v[id, "after"]); se = (v[id, "before"] == v[id, "settled"])
                if (e[id] == "identical") pass = (ae && se) ? "yes" : "NO"
                else pass = "info"
                printf "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", id, e[id], v[id, "before"], v[id, "after"], v[id, "settled"], ae, se, pass
            }
        }' "$RESULTS/dedup_raw.tsv" | sort > "$RESULTS/dedup.tsv"
    log "dedup results in $RESULTS/dedup.tsv"
}

# ---------------------------------------------------------------------------
# Summary across steps
# ---------------------------------------------------------------------------

cmd_summary() {
    ch --query "SYSTEM FLUSH LOGS"
    ch --format TSVWithNames --query "
        WITH s AS
        (
            SELECT extract(log_comment, 'Q=([^;]+)') AS qid, extract(log_comment, 'step=([^;]+)') AS step,
                   any(user) AS user, median(read_rows) AS rr, quantileExact(0.95)(query_duration_ms) AS p95,
                   max(memory_usage) AS mem
            FROM system.query_log
            WHERE type = 'QueryFinish' AND is_initial_query AND log_comment LIKE 'Q=%'
              AND log_comment NOT LIKE '%tenant=%'
              AND toUInt32OrZero(extract(log_comment, 'run=([0-9]+)')) > 1
              AND extract(log_comment, 'step=([^;]+)') IN ('s1', 's1t', 's4', 's16')
            GROUP BY qid, step
        ),
        sm AS
        (
            SELECT s.*, m.model_rows
            FROM s LEFT JOIN (SELECT step, qid, model_rows FROM bench.model FINAL) AS m
                ON m.qid = s.qid AND m.step = s.step
        )
        SELECT qid, any(user) AS user,
               anyIf(rr, step = 's1') AS rr_s1, anyIf(rr, step = 's1t') AS rr_s1t,
               anyIf(rr, step = 's4') AS rr_s4, anyIf(rr, step = 's16') AS rr_s16,
               round(max(rr) / greatest(min(rr), 1), 3) AS rr_max_over_min,
               rr_max_over_min <= 1.10 AS rr_flat_10pct,
               round(max(rr / nullIf(model_rows, 0)), 2) AS rr_over_model_max,
               rr_over_model_max <= 2 AS within_2x_model,
               anyIf(p95, step = 's1') AS p95_s1, anyIf(p95, step = 's1t') AS p95_s1t,
               anyIf(p95, step = 's4') AS p95_s4, anyIf(p95, step = 's16') AS p95_s16,
               round(max(p95) / greatest(min(p95), 1), 2) AS p95_max_over_min,
               p95_max_over_min <= 1.5 AS p95_flat_1_5x,
               max(mem) AS memory_usage_max
        FROM sm
        GROUP BY qid
        ORDER BY qid" > "$RESULTS/summary.tsv"
    cat "$RESULTS"/partner_s*.tsv 2>/dev/null | awk 'NR == 1 || $1 != "step"' > "$RESULTS/partner.tsv" || true
    cat "$RESULTS"/tables_*.tsv 2>/dev/null | awk 'NR == 1 || $1 != "step"' > "$RESULTS/tables.tsv" || true
    cat "$RESULTS"/inserts_*.tsv 2>/dev/null | awk 'NR == 1 || $1 != "step"' > "$RESULTS/inserts.tsv" || true
    log "summary in $RESULTS/summary.tsv"
}

cmd_all() {
    cmd_start
    cmd_init
    local s
    for s in "${STEPS[@]}"; do
        cmd_step "$s"
    done
    cmd_sweep
    cmd_redeliver
    cmd_summary
}

main() {
    local cmd="${1:-all}"
    case "$cmd" in
        all) cmd_all ;;
        start) cmd_start ;;
        init) cmd_init ;;
        step) cmd_step "${2:?step name}" ;;
        collect) collect "${2:?step name}" ;;
        sweep) cmd_sweep ;;
        redeliver) cmd_redeliver ;;
        summary) cmd_summary ;;
        stop) cmd_stop ;;
        destroy) cmd_destroy ;;
        *) echo "usage: run.sh all|start|init|step <s1|s1t|s4|s16>|sweep|redeliver|summary|stop|destroy" >&2; exit 2 ;;
    esac
}

main "$@"
