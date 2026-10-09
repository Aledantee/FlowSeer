#!/usr/bin/env bash
# Runs the sections of 02_generate.sql for one benchmark step.
#
#   gen.sh setup       create the bench database, generator functions, helper tables
#   gen.sh s1          T0 + unit 1 (40 tenants), the 7-day window
#   gen.sh s1t         time step: the 7 days before the window for T0 + unit 1
#   gen.sh s4          units 2..4, the 7-day window
#   gen.sh s16         units 5..16, the 7-day window
#   gen.sh redeliver   T0 only, 1 % of rows of every raw table, all 14 days
#
# Inserts go into the raw tables only, one INSERT per table per hour (samples, syslog,
# probes), per six-hour block (FDB presence), or per day (changes, link-flap syslog, walk
# markers), so the materialized views run per insert block as they do behind the sink.
# Every INSERT carries log_comment "gen;step=<step>;table=<section>" for run.sh's
# throughput numbers. Memory per INSERT is capped at 4 GB.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONTAINER="${CONTAINER:-fsbench-ch}"
CH_PASSWORD="${CH_PASSWORD:-bench}"
RESULTS="${RESULTS:-$HERE/results}"
META="$RESULTS/meta.env"
GEN_SQL="$HERE/02_generate.sql"

log() { printf '%s gen %s\n' "$(date -u +%H:%M:%S)" "$*" >&2; }

section() {
    awk -v name="$1" '
        $0 == "-- @" name { on = 1; next }
        /^-- @/ { on = 0 }
        on { print }
    ' "$GEN_SQL"
}

# run_section <step> <section> [--param_x=v ...]
run_section() {
    local step="$1" name="$2"
    shift 2
    local sql
    sql="$(section "$name")"
    if [[ -z "$sql" ]]; then
        echo "gen.sh: section $name not found in $GEN_SQL" >&2
        exit 1
    fi
    printf '%s\n' "$sql" | docker exec -i "$CONTAINER" clickhouse-client --prefer_column_name_to_alias=1 \
        --password "$CH_PASSWORD" \
        --log_comment="gen;step=${step};table=${name}" \
        --workload=ingestion \
        --max_memory_usage=4000000000 \
        --max_insert_threads=4 \
        --max_threads=8 \
        "$@"
}

load_meta() {
    if [[ ! -f "$META" ]]; then
        echo "gen.sh: $META missing; run.sh init writes it" >&2
        exit 1
    fi
    START="$(sed -n 's/^START=//p' "$META")"
    END="$(sed -n 's/^END=//p' "$META")"
    BACKFILL="$(sed -n 's/^BACKFILL=//p' "$META")"
}

# meta <step> <unit_lo> <unit_hi>
meta() {
    local step="$1" ulo="$2" uhi="$3"
    log "$step meta units $ulo..$uhi"
    for s in tenants devices interfaces probe_targets dims; do
        run_section "$step" "$s" --param_u_lo="$ulo" --param_u_hi="$uhi" --param_start="$START"
    done
}

# window <step> <tenant_lo> <tenant_hi> <from_unix> <to_unix> <redeliver 0|1>
window() {
    local step="$1" lo="$2" hi="$3" from="$4" to="$5" rd="$6"
    local d h b
    for ((d = from; d < to; d += 86400)); do
        log "$step tenants $lo..$hi day $(( (d - from) / 86400 + 1 )) of $(( (to - from) / 86400 )) rd=$rd"
        for ((h = d; h < d + 86400; h += 3600)); do
            run_section "$step" samples --param_lo="$lo" --param_hi="$hi" --param_h="$h" --param_rd="$rd"
            run_section "$step" syslog  --param_lo="$lo" --param_hi="$hi" --param_h="$h" --param_rd="$rd"
            run_section "$step" probes  --param_lo="$lo" --param_hi="$hi" --param_h="$h" --param_rd="$rd"
            # The FDB evidence for a six-hour block is written once the block has passed.
            if (( (h - d) % 21600 == 18000 )); then
                b=$(( (h - d) / 21600 ))
                run_section "$step" fdb --param_lo="$lo" --param_hi="$hi" --param_d="$d" --param_b="$b" --param_rd="$rd"
            fi
        done
        run_section "$step" changes      --param_lo="$lo" --param_hi="$hi" --param_d="$d" --param_rd="$rd"
        run_section "$step" syslog_flaps --param_lo="$lo" --param_hi="$hi" --param_d="$d" --param_rd="$rd"
        if [[ "$rd" == 0 ]]; then
            run_section "$step" walks --param_lo="$lo" --param_hi="$hi" --param_d="$d"
        fi
    done
}

main() {
    local step="${1:-}"
    case "$step" in
        setup)
            run_section setup setup
            ;;
        s1)
            load_meta
            meta s1 0 1
            window s1 0 40 "$START" "$END" 0
            ;;
        s1t)
            load_meta
            window s1t 0 40 "$BACKFILL" "$START" 0
            ;;
        s4)
            load_meta
            meta s4 2 4
            window s4 41 160 "$START" "$END" 0
            ;;
        s16)
            load_meta
            meta s16 5 16
            window s16 161 640 "$START" "$END" 0
            ;;
        redeliver)
            load_meta
            window redeliver 0 0 "$BACKFILL" "$END" 1
            ;;
        *)
            echo "usage: gen.sh setup|s1|s1t|s4|s16|redeliver" >&2
            exit 2
            ;;
    esac
}

main "$@"
