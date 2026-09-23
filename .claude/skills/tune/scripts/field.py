#!/usr/bin/env python3
"""Score local agent transcripts against run log grades and registry prices."""

import argparse
from collections import defaultdict
from datetime import datetime, timezone
import json
from pathlib import Path
import re
import sqlite3
import statistics
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "delegate" / "scripts"))
import runlog

from catalogue import registry_file_models


HOME = Path.home()
EMPTY_TOKENS = ("input", "cache_read", "cache_write_5m", "cache_write_1h", "output", "reasoning", "total")


def instant(value):
    if isinstance(value, (int, float)):
        return datetime.fromtimestamp(value / 1000 if value > 10**11 else value, timezone.utc)
    if isinstance(value, str):
        try:
            return datetime.fromisoformat(value.replace("Z", "+00:00")).astimezone(timezone.utc)
        except ValueError:
            pass
    return None


def seconds(start, end):
    return max(0, round((end - start).total_seconds(), 3)) if start and end else None


def records(path):
    try:
        with path.open(encoding="utf-8", errors="replace") as stream:
            for line in stream:
                try:
                    record = json.loads(line)
                except ValueError:
                    continue
                if isinstance(record, dict):
                    yield record
    except OSError:
        return


def tokens(**values):
    result = {key: int(values.get(key) or 0) for key in EMPTY_TOKENS if key != "total"}
    result["total"] = sum(value for key, value in result.items() if key != "reasoning")
    return result


def add_tokens(left, right):
    return tokens(**{key: left[key] + right[key] for key in left if key != "total"})


def cost_estimate(usage, price):
    """Apply the shared calibration formula to normalized token counts."""
    if price is None or usage is None:
        return None
    input_price, output_price = price
    input_units = (usage["input"] + 0.1 * usage["cache_read"]
                   + 1.25 * usage["cache_write_5m"] + 2 * usage["cache_write_1h"])
    return round((input_units * input_price + usage["output"] * output_price) / 1000000, 8)


def registry(paths):
    models = {}
    for path in paths:
        if Path(path).exists():
            models.update(registry_file_models(path))
    prices = {}
    for model, fields in models.items():
        raw = fields.get("price", "")
        match = re.fullmatch(r"\[\s*([0-9.]+)\s*,\s*([0-9.]+)\s*\]", raw)
        prices[model] = tuple(map(float, match.groups())) if match else None
    return models, prices


def model_for(model, agent, models):
    if model in models:
        return model
    for name, fields in models.items():
        pattern = fields.get("id_format", "").strip('"')
        if pattern and "<effort>" in pattern and model and re.fullmatch(
                re.escape(pattern).replace(re.escape("<effort>"), r"[a-z]+"), model):
            return name
        if agent and fields.get("agent") == agent:
            return name
    return model or agent


def claude_directory(cwd):
    return re.sub(r"[^A-Za-z0-9]", "-", cwd)


def active_time(items, opens):
    total = 0.0
    began = last = None
    for item in items:
        at = instant(item.get("timestamp"))
        if at is None:
            continue
        if opens(item):
            if began is not None:
                total += seconds(began, last)
            began = at
        if began is not None:
            last = at
    if began is not None:
        total += seconds(began, last)
    return round(total, 3) if began is not None else None


def claude_open(item):
    if item.get("type") != "user" or item.get("isMeta"):
        return False
    content = (item.get("message") or {}).get("content")
    return not (isinstance(content, list) and content and all(
        isinstance(part, dict) and part.get("type") == "tool_result" for part in content))


def claude_session(path):
    items = list(records(path))
    dated = [item for item in items if instant(item.get("timestamp"))]
    if not dated:
        return None
    messages = {}
    model = cwd = entrypoint = agent = None
    errors = 0
    for item in dated:
        cwd = cwd or item.get("cwd")
        entrypoint = entrypoint or item.get("entrypoint")
        agent = agent or item.get("agentId")
        message = item.get("message") or {}
        if item.get("type") == "assistant" and message.get("model") not in (None, "<synthetic>"):
            model = message["model"]
            usage = message.get("usage") or {}
            if usage:
                key = message.get("id") or item.get("uuid")
                messages[key] = usage
        if item.get("type") == "user":
            content = message.get("content")
            if isinstance(content, list):
                errors += sum(bool(part.get("is_error")) for part in content
                              if isinstance(part, dict) and part.get("type") == "tool_result")
    usage = tokens()
    for item in messages.values():
        cache = item.get("cache_creation") or {}
        usage = add_tokens(usage, tokens(
            input=item.get("input_tokens"), cache_read=item.get("cache_read_input_tokens"),
            cache_write_5m=cache.get("ephemeral_5m_input_tokens"),
            cache_write_1h=cache.get("ephemeral_1h_input_tokens"),
            output=item.get("output_tokens"),
            reasoning=(item.get("output_tokens_details") or {}).get("thinking_tokens")))
    return {"id": path.stem, "cwd": cwd, "started": instant(dated[0]["timestamp"]),
            "ended": instant(dated[-1]["timestamp"]),
            "entrypoint": entrypoint, "agent": agent, "model": model,
            "active_s": active_time(dated, claude_open), "tokens": usage, "tool_errors": errors}


def codex_open(item):
    if item.get("type") != "response_item":
        return False
    payload = item.get("payload") or {}
    if payload.get("type") != "message" or payload.get("role") != "user":
        return False
    kinds = (payload.get("internal_chat_message_metadata_passthrough") or {}).get("content_item_kinds")
    return not kinds or any(kind.startswith("user.") for kind in kinds)


def codex_session(path):
    items = list(records(path))
    meta = next((item for item in items if item.get("type") == "session_meta"), None)
    if meta is None:
        return None
    payload = meta.get("payload") or {}
    latest = {}
    model = None
    for item in items:
        data = item.get("payload") or {}
        if item.get("type") == "event_msg" and data.get("type") == "token_count":
            latest = (data.get("info") or {}).get("total_token_usage") or latest
        if item.get("type") == "turn_context":
            model = data.get("model") or model
        if item.get("type") == "event_msg" and data.get("type") == "turn_context":
            model = data.get("model") or model
    dated = [instant(item.get("timestamp")) for item in items]
    ended = max((at for at in dated if at is not None), default=None)
    result = tokens(input=max(0, (latest.get("input_tokens") or 0) - (latest.get("cached_input_tokens") or 0)),
                    cache_read=latest.get("cached_input_tokens"),
                    output=latest.get("output_tokens"), reasoning=latest.get("reasoning_output_tokens"))
    return {"id": payload.get("id") or path.stem, "cwd": payload.get("cwd"),
            "started": instant(payload.get("timestamp") or meta.get("timestamp")),
            "ended": ended,
            "model": model, "active_s": active_time(items, codex_open),
            "tokens": result, "tool_errors": None}


def opencode_sessions(path, match):
    if not path.exists():
        return []
    sessions = []
    with sqlite3.connect("file:" + str(path) + "?mode=ro", uri=True) as db:
        for sid, cwd, created, agent, model in db.execute(
                "SELECT id, directory, time_created, agent, model FROM session"):
            if match not in (cwd or ""):
                continue
            rows = db.execute("SELECT data, time_created, time_updated FROM message WHERE session_id=? ORDER BY time_created", (sid,))
            usage, cost = tokens(), 0.0
            ended = instant(created)
            timeline = []
            for raw, at, updated in rows:
                try:
                    item = json.loads(raw)
                except (TypeError, ValueError):
                    continue
                part = item.get("tokens") or {}
                cache = part.get("cache") or {}
                usage = add_tokens(usage, tokens(input=part.get("input"), output=part.get("output"),
                                                reasoning=part.get("reasoning"), cache_read=cache.get("read"),
                                                cache_write_5m=cache.get("write")))
                cost += item.get("cost") or 0
                timeline.append({"timestamp": at, "role": item.get("role")})
                if updated and updated != at:
                    timeline.append({"timestamp": updated, "role": None})
                message_end = instant(updated or at)
                if message_end and (ended is None or message_end > ended):
                    ended = message_end
                agent = item.get("agent") or agent
            sessions.append({"id": sid, "cwd": cwd, "started": instant(created), "agent": agent,
                             "ended": ended,
                             "model": model, "active_s": active_time(timeline, lambda item: item["role"] == "user"),
                             "tokens": usage,
                             "cost_usd": round(cost, 8), "tool_errors": None})
    return sessions


def joinable(session, start, end, cwd):
    at = session.get("started")
    last = session.get("ended") or at
    return session.get("cwd") == cwd and at is not None and at <= end and last >= start


def empty_run(source, model, role, cli, outcome=None, verify=None):
    return {"source": source, "model": model, "role": role, "cli": cli,
            "outcome": outcome, "verify": verify, "elapsed_s": None, "active_s": None,
            "tokens": None, "cost_usd": None, "est": True, "tool_errors": None,
            "findings": 0, "held": 0}


def apply_sessions(run, sessions, prices):
    if not sessions:
        return
    active_values = [item["active_s"] for item in sessions if item.get("active_s") is not None]
    run["active_s"] = round(sum(active_values), 3) if active_values else None
    token_sessions = [item["tokens"] for item in sessions if item.get("tokens") is not None]
    if token_sessions:
        run["tokens"] = tokens()
        for t in token_sessions:
            run["tokens"] = add_tokens(run["tokens"], t)
    else:
        run["tokens"] = None
    if run["cli"] == "claude":
        run["model"] = next((item["model"] for item in sessions if item.get("model")), run["model"])
        error_values = [item["tool_errors"] for item in sessions if item.get("tool_errors") is not None]
        run["tool_errors"] = sum(error_values) if error_values else 0
    elif run["cli"] == "opencode":
        cost_values = [item["cost_usd"] for item in sessions if item.get("cost_usd") is not None]
        if cost_values:
            run["cost_usd"] = round(sum(cost_values), 8)
            run["est"] = False
    if run["est"] and run.get("tokens") is not None:
        run["cost_usd"] = cost_estimate(run["tokens"], prices.get(run["model"]))


def groups_for(runs):
    buckets = defaultdict(list)
    for run in runs:
        buckets[(run["model"], run["role"], run["source"])].append(run)
    groups = []
    for (model, role, source), rows in sorted(buckets.items()):
        counts = {name: sum(row["outcome"] == name for row in rows)
                  for name in ("accepted", "amended", "rejected", "blocked")}
        graded = counts["accepted"] + counts["amended"] + counts["rejected"]
        findings = sum(row["findings"] for row in rows)
        held = sum(row["held"] for row in rows)
        verified = sum(row["verify"] in ("pass", "fail") for row in rows)
        medians = {}
        for field, values in (("elapsed_s", [r["elapsed_s"] for r in rows]),
                              ("active_s", [r["active_s"] for r in rows]),
                              ("tokens", [r["tokens"]["total"] if r.get("tokens") else None for r in rows]),
                              ("cost_usd", [r["cost_usd"] for r in rows])):
            present = [value for value in values if value is not None]
            medians[field] = statistics.median(present) if present else None
        group = {"model": model, "role": role, "source": source, "runs": len(rows),
                 "counts": counts, "graded": graded, "verify_pass": sum(r["verify"] == "pass" for r in rows),
                 "findings": findings, "held": held,
                 "accepted_rate": counts["accepted"] / graded if graded else None,
                 "amended_rejected_rate": (counts["amended"] + counts["rejected"]) / graded if graded else None,
                 "verify_pass_rate": sum(r["verify"] == "pass" for r in rows) / verified if verified else None,
                 "held_rate": held / findings if findings else None, "medians": medians}
        if graded < 5 and not (role.startswith("review") and findings >= 10):
            group["sample"] = "small"
        groups.append(group)
    return groups


def score(args):
    since = datetime.fromisoformat(args.since).replace(tzinfo=timezone.utc) if args.since else datetime(1970, 1, 1, tzinfo=timezone.utc)
    now = datetime.now(timezone.utc)
    models, prices = registry(args.registry)
    roles = set()
    for path in args.registry:
        if not path.exists():
            continue
        in_roles = False
        for line in path.read_text().splitlines():
            if line and not line[0].isspace() and not line.startswith("#"):
                in_roles = line.startswith("roles:")
            elif in_roles:
                match = re.match(r"^  ([a-z][a-z-]*):", line)
                if match:
                    roles.add(match.group(1))
    log = runlog.read(args.runlog)
    starts, grades, ends, reviews = {}, {}, {}, []
    for event in log:
        kind, rid = event.get("event"), event.get("run")
        if kind == "start":
            starts[rid] = event
        elif kind == "grade":
            grades[rid] = event
        elif kind == "end":
            ends[rid] = event
        elif kind == "review":
            reviews.append(event)
    claude = []
    lane_directories = {claude_directory(event.get("worktree", "")) for event in starts.values()
                        if event.get("cli") == "claude"}
    for directory in args.claude_projects.glob("*"):
        if (directory.name not in lane_directories and args.match not in directory.name
                and claude_directory(args.match) not in directory.name):
            continue
        for path in directory.glob("*.jsonl"):
            if path.stat().st_mtime < since.timestamp():
                continue
            session = claude_session(path)
            if session:
                claude.append(session)
        for path in directory.glob("*/subagents/*.jsonl"):
            if path.stat().st_mtime < since.timestamp():
                continue
            session = claude_session(path)
            if session:
                meta = path.with_suffix(".meta.json")
                try:
                    session["agent_type"] = json.loads(meta.read_text()).get("agentType")
                except (OSError, ValueError):
                    session["agent_type"] = None
                session["agent"] = session["agent"] or path.stem.removeprefix("agent-")
                session["native"] = True
                claude.append(session)
    codex = [session for path in args.codex_sessions.rglob("*.jsonl") if path.stat().st_mtime >= since.timestamp()
             if (session := codex_session(path)) and args.match in (session.get("cwd") or "")]
    opencode = opencode_sessions(args.opencode_db, args.match)
    runs, unmatched, joined = [], [], set()
    if log.skipped:
        unmatched.append({"reason": "malformed_runlog_lines", "count": log.skipped})
    lane_windows = defaultdict(list)
    for rid, start in starts.items():
        at = instant(start.get("at"))
        cwd = start.get("worktree", "")
        if at is None or args.match not in cwd:
            continue
        grade = grades.get(rid, {})
        end = instant((ends.get(rid) or grade).get("at")) or now
        lane_windows[cwd].append((at, end, rid, start.get("cli")))

    ambiguous_runs = set()
    for cli_name, candidates in (("claude", claude), ("codex", codex), ("opencode", opencode)):
        for session in candidates:
            if session.get("native"):
                continue
            scwd = session.get("cwd") or ""
            overlapping_lanes = [rid for at, end, rid, ccli in lane_windows.get(scwd, [])
                                 if ccli == cli_name and joinable(session, at, end, scwd)]
            if len(overlapping_lanes) > 1:
                ambiguous_runs.update(overlapping_lanes)

    for rid, start in starts.items():
        at = instant(start.get("at"))
        cwd = start.get("worktree", "")
        if at is None or at < since or args.match not in cwd:
            continue
        grade = grades.get(rid, {})
        end = instant((ends.get(rid) or grade).get("at")) or now
        cli = start.get("cli")
        model = model_for(start.get("model"), start.get("agent"), models)
        run = empty_run("orca", model, start.get("role"), cli,
                        grade.get("outcome"), grade.get("verify"))
        run["run"] = rid
        run["elapsed_s"] = seconds(at, instant(grade.get("at")))
        if roles and run["role"] not in roles:
            unmatched.append({"run": rid, "reason": "role_not_in_registry", "role": run["role"]})
        candidates = {"claude": claude, "codex": codex, "opencode": opencode}.get(cli, [])
        matches = [item for item in candidates if not item.get("native") and joinable(item, at, end, cwd)]
        for item in matches:
            joined.add((cli, item["id"]))
        if cli == "agy":
            run["transcript"] = "unsupported"
        elif rid in ambiguous_runs:
            run["transcript"] = "ambiguous"
            unmatched.append({"run": rid, "reason": "transcript_ambiguous"})
        elif not matches:
            unmatched.append({"run": rid, "reason": "transcript_missing"})
        else:
            apply_sessions(run, matches, prices)
        for review in reviews:
            if review.get("agent") == rid:
                run["findings"] += review.get("findings", 0)
                run["held"] += review.get("held", 0)
        runs.append(run)
    for session in claude:
        if ("claude", session["id"]) in joined:
            continue
        if session.get("started") is None or session["started"] < since or args.match not in (session.get("cwd") or ""):
            continue
        if session.get("native"):
            agent_type = session.get("agent_type")
            role = {"Explore": "lookup", "repo-researcher": "research",
                    "independent-reviewer": "review"}.get(agent_type, "native-other")
            for review in reviews:
                if review.get("agent") == session.get("agent"):
                    role = review.get("role") if agent_type == "independent-reviewer" else role
            source = "native"
        elif session.get("entrypoint") in ("cli", "sdk-cli"):
            source = "coordinator" if session["entrypoint"] == "cli" else "headless"
            role = "coordinator" if source == "coordinator" else "headless"
        else:
            continue
        if not session.get("model"):
            unmatched.append({"session": session["id"], "reason": "model_missing"})
            continue
        run = empty_run(source, session.get("model"), role, "claude")
        run["session"] = session["id"]
        apply_sessions(run, [session], prices)
        if source == "native":
            for review in reviews:
                if review.get("agent") == session.get("agent"):
                    run["findings"] += review.get("findings", 0)
                    run["held"] += review.get("held", 0)
        runs.append(run)
    return {"as_of": now.date().isoformat(), "since": since.date().isoformat(),
            "runs": runs, "groups": groups_for(runs), "unmatched": unmatched}


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--since", help="Include runs started on or after this UTC date (YYYY-MM-DD)")
    parser.add_argument("--match", default="FlowSeer")
    parser.add_argument("--runlog", type=Path, default=runlog.log_path())
    parser.add_argument("--claude-projects", type=Path, default=HOME / ".claude/projects")
    parser.add_argument("--codex-sessions", type=Path, default=HOME / ".codex/sessions")
    parser.add_argument("--opencode-db", type=Path, default=HOME / ".local/share/opencode/opencode.db")
    parser.add_argument("--registry", type=Path, nargs="+", action="append", default=None)
    args = parser.parse_args(argv)
    args.registry = [path for group in args.registry for path in group] if args.registry else [HOME / ".claude/models/registry.yaml"]
    print(json.dumps(score(args), sort_keys=True))


if __name__ == "__main__":
    main()
