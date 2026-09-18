#!/usr/bin/env bash
# Print, as one YAML flow map per line, the sign-in state and used percent of
# every window of the four prepaid pools. Each pool is read from the source
# that owns its numbers, because no single tool sees all four:
#
#   claude, codex  orca account list --json      (rateLimits)
#   google         agy -p /quota                 (answers without a model turn)
#   go             opencode's local database     (dollars spent against the caps)
#
# Orca also lists `antigravity` and `opencodeGo` with status `unavailable`.
# That status means Orca cannot read their usage, not that the pools are down,
# so it is never consulted for them. A source that fails leaves its pool with
# `windows: null` and an `error`; it does not mark the pool signed out unless
# the CLI itself says so.
#
# Run unsandboxed: orca uses a local socket, agy the network and the keyring,
# and opencode writes a log file under ~/.local/share.

set -uo pipefail

registry="$(cd "$(dirname "$0")/../../.." && pwd)/models/registry.yaml"

REGISTRY="$registry" python3 - <<'PY'
import datetime
import json
import os
import re
import shutil
import subprocess


def run(cmd, timeout=90):
    try:
        p = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout,
                           stdin=subprocess.DEVNULL)
    except (OSError, subprocess.TimeoutExpired) as e:
        return None, str(e)
    if p.returncode != 0:
        lines = (p.stderr or p.stdout).strip().splitlines()
        return None, lines[-1] if lines else "exit %d" % p.returncode
    return p.stdout, None


def emit(pool, signed_in, source, windows=None, resets=None, error=None):
    row = {"signed_in": signed_in, "windows": windows}
    if windows:
        worst = max(windows, key=windows.get)
        row["worst"] = {"window": worst, "used": windows[worst]}
        if resets and resets.get(worst):
            row["worst"]["resets"] = resets[worst]
    row["source"] = source
    if error:
        row["error"] = error
    print("%s: %s" % (pool, json.dumps(row)))


def iso(ms):
    return datetime.datetime.fromtimestamp(ms / 1000, datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def orca_pools():
    if not shutil.which("orca"):
        for pool in ("claude", "codex"):
            emit(pool, None, "orca", error="orca not installed")
        return
    out, err = run(["orca", "account", "list", "--json"])
    try:
        result = json.loads(out)["result"]
    except (TypeError, ValueError, KeyError):
        for pool in ("claude", "codex"):
            emit(pool, None, "orca", error=err or "unreadable account list")
        return
    for pool in ("claude", "codex"):
        rl = result.get("rateLimits", {}).get(pool) or {}
        windows, resets = {}, {}
        for name, w in rl.items():
            if isinstance(w, dict) and "usedPercent" in w:
                windows[name] = w["usedPercent"]
                if w.get("resetsAt"):
                    resets[name] = iso(w["resetsAt"])
        signed_in = rl.get("status") == "ok"
        if pool == "codex":
            signed_in = signed_in or bool(result.get("codex", {}).get("systemDefault", {}).get("hasAuth"))
        emit(pool, signed_in, "orca", windows or None, resets, rl.get("error"))


def google_pool():
    if not shutil.which("agy"):
        emit("google", False, "agy", error="agy not installed")
        return
    out, err = run(["agy", "-p", "/quota", "--output-format", "json", "--print-timeout", "60s"])
    try:
        groups = json.loads(out)["command"]["data"]["groups"]
    except (TypeError, ValueError, KeyError):
        # The quota command is newer than the sign-in check it replaces here.
        models, _ = run(["agy", "models"])
        emit("google", models is not None, "agy", error=err or "unreadable /quota reply")
        return
    windows, resets = {}, {}
    for g in groups:
        for b in g.get("buckets", []):
            windows[b["id"]] = round((1 - b["remaining_fraction"]) * 100)
            resets[b["id"]] = b.get("reset_time")
    emit("google", True, "agy", windows or None, resets)


def go_caps():
    caps = {"5h": 12, "week": 30, "month": 60}
    try:
        text = open(os.environ["REGISTRY"]).read()
        m = re.search(r"^\s*go:.*caps:\s*\{([^}]*)\}", text, re.M)
        for k, v in re.findall(r"(\w+):\s*([\d.]+)", m.group(1)):
            caps[k] = float(v)
    except (OSError, AttributeError, KeyError):
        pass
    return caps


def go_pool():
    if not shutil.which("opencode"):
        emit("go", False, "opencode-db", error="opencode not installed")
        return
    models, err = run(["opencode", "models"])
    if models is None or "opencode-go/" not in models:
        emit("go", False, "opencode-db", error=err)
        return
    # The pool meters rolling windows in dollars. The database holds only this
    # machine's sessions, so spend from another host is not counted.
    spans = {"5h": 5 * 3600, "week": 7 * 86400, "month": 30 * 86400}
    cols = ", ".join(
        "sum(case when time_created > (strftime('%%s','now') - %d) * 1000 "
        "then json_extract(data, '$.cost') else 0 end) as \"%s\"" % (secs, name)
        for name, secs in spans.items())
    query = ("select %s from message "
             "where json_extract(data, '$.providerID') = 'opencode-go'" % cols)
    out, err = run(["opencode", "db", query, "--format", "json"])
    try:
        spent = json.loads(out)[0]
    except (TypeError, ValueError, IndexError):
        emit("go", True, "opencode-db", error=err or "unreadable database reply")
        return
    caps = go_caps()
    windows = {name: round((spent.get(name) or 0) / caps[name] * 100) for name in spans}
    emit("go", True, "opencode-db", windows)


orca_pools()
google_pool()
go_pool()
PY
