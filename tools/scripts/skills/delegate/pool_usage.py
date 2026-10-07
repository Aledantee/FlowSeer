"""Read five prepaid pool rows, retaining provider failures as row data."""

import argparse
import ast
import datetime
import json
import math
import os
from pathlib import Path
import queue
import re
import shutil
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request


def run(cmd, timeout=90, ok_codes=(0,), cwd=None):
    try:
        p = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout,
                           stdin=subprocess.DEVNULL, cwd=cwd)
    except (OSError, subprocess.TimeoutExpired) as e:
        return None, str(e)
    if p.returncode not in ok_codes:
        lines = (p.stderr or p.stdout).strip().splitlines()
        return None, lines[-1] if lines else "exit %d" % p.returncode
    return p.stdout, None


def scan_flow(text):
    depth, quote, escaped = 0, None, False
    for index, char in enumerate(text):
        quoted = quote is not None
        yield index, char, depth, quoted
        if quote:
            if escaped:
                escaped = False
            elif char == "\\":
                escaped = True
            elif char == quote:
                quote = None
        elif char in ("'", '"'):
            quote = char
        elif char in "[{":
            depth += 1
        elif char in "]}":
            depth -= 1


def without_comment(text):
    for index, char, _, quoted in scan_flow(text):
        if not quoted and char == "#":
            return text[:index]
    return text


def split_flow(text):
    parts, start = [], 0
    for index, char, depth, quoted in scan_flow(text):
        if not quoted and char == "," and depth == 0:
            parts.append(text[start:index])
            start = index + 1
    parts.append(text[start:])
    return parts


def split_flow_pair(text):
    for index, char, depth, quoted in scan_flow(text):
        if not quoted and char == ":" and depth == 0:
            return text[:index], text[index + 1:]
    return text, ""


def flow_value(text):
    text = without_comment(text).strip()
    if text.startswith("{") and text.endswith("}"):
        value = {}
        for part in split_flow(text[1:-1]):
            if not part.strip():
                continue
            key, item = split_flow_pair(part)
            value[str(flow_value(key))] = flow_value(item)
        return value
    if text.startswith("[") and text.endswith("]"):
        return [flow_value(part) for part in split_flow(text[1:-1]) if part.strip()]
    if text in ("null", "Null", "NULL", "~"):
        return None
    if text in ("true", "True", "TRUE"):
        return True
    if text in ("false", "False", "FALSE"):
        return False
    if len(text) >= 2 and text[0] in ("'", '"') and text[-1] == text[0]:
        try:
            return ast.literal_eval(text)
        except (SyntaxError, ValueError):
            return text[1:-1]
    try:
        return int(text)
    except ValueError:
        try:
            return float(text)
        except ValueError:
            return text


def read_registry(path):
    try:
        with Path(path).open(encoding="utf-8") as registry:
            lines = registry.readlines()
    except (OSError, ValueError):
        return {}
    pools = {}
    in_pools = False
    for raw in lines:
        line = without_comment(raw.rstrip())
        if not line.strip():
            continue
        indent = len(line) - len(line.lstrip())
        stripped = line.strip()
        if indent == 0:
            in_pools = stripped == "pools:"
            continue
        if in_pools and indent == 2:
            match = re.match(r"([^:]+):\s*(\{.*\})\s*$", stripped)
            if match:
                pools[match.group(1).strip()] = flow_value(match.group(2))
    return pools


def registry_pools():
    pools = {}
    paths = [Path("~/.claude/models/registry.yaml").expanduser(),
             Path(os.getcwd()) / ".claude/models/registry.yaml"]
    for path in paths:
        pools.update(read_registry(path))
    return pools


def finite_number(value):
    if not isinstance(value, (int, float)) or isinstance(value, bool):
        return False
    try:
        return math.isfinite(value)
    except (OverflowError, ValueError):
        return False


def valid_capacity(value):
    if finite_number(value):
        return True
    return isinstance(value, dict) and all(finite_number(item)
                                           for item in value.values())


def pool_capacity(pool, plan):
    config = registry_pools().get(pool) or {}
    limit = config.get("usable_below", 85)
    if not finite_number(limit):
        limit = 85
    capacity, plan_unlisted = 1, False
    if plan is not None:
        plans = config.get("plans") or {}
        entry = plans.get(plan) if isinstance(plans, dict) else None
        candidate = entry.get("capacity") if isinstance(entry, dict) else None
        if valid_capacity(candidate):
            capacity = candidate
        else:
            plan_unlisted = True
    return capacity, limit, plan_unlisted


def slots_for(windows, capacity, limit):
    slots = {}
    for name, used in windows.items():
        value = capacity.get(name, 1) if isinstance(capacity, dict) else capacity
        if used >= limit:
            slots[name] = 0
            continue
        slots[name] = min(6, max(1, math.ceil(value * (100 - used) / 50)))
    return slots


def emit(pool, signed_in, source, windows=None, resets=None, error=None, *, plan=None,
         models=None):
    plan = plan if isinstance(plan, str) else None
    capacity, limit, plan_unlisted = pool_capacity(pool, plan)
    row = {"signed_in": signed_in, "windows": windows, "plan": plan,
           "capacity": capacity}
    if plan_unlisted:
        row["plan_unlisted"] = True
    if models is not None:
        row["models"] = models
    if windows:
        worst = max(windows, key=windows.get)
        row["worst"] = {"window": worst, "used": windows[worst]}
        if resets and resets.get(worst):
            row["worst"]["resets"] = resets[worst]
    row["slots"] = None if windows is None else slots_for(windows, capacity, limit)
    row["source"] = source
    if error:
        row["error"] = error
    print("%s: %s" % (pool, json.dumps(row)))


def iso(ms):
    return datetime.datetime.fromtimestamp(ms / 1000, datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def orca_pools():
    result = None
    orca = shutil.which("orca")
    if orca:
        out, _ = run([orca, "account", "list", "--json"])
        try:
            result = json.loads(out)["result"]
            if not isinstance(result, dict) or not isinstance(result.get("rateLimits"), dict):
                result = None
        except (TypeError, ValueError, KeyError):
            pass
    for pool, native in (("claude", claude_pool), ("codex", codex_pool)):
        rl = (result or {}).get("rateLimits", {}).get(pool) or {}
        windows, resets = {}, {}
        if isinstance(rl, dict) and rl.get("status") == "ok":
            for name, w in rl.items():
                if isinstance(w, dict) and isinstance(w.get("usedPercent"), (int, float)):
                    windows[name] = w["usedPercent"]
                    if w.get("resetsAt"):
                        resets[name] = iso(w["resetsAt"])
        if windows:
            if pool == "claude":
                plan, plan_error = claude_plan()
                emit(pool, True, "orca", windows, resets,
                     rl.get("error") or plan_error, plan=plan)
            else:
                info = codex_read(quota=False)
                emit(pool, True, "orca", windows, resets,
                     rl.get("error") or (info["error"] if info["signed_in"] is not False else None),
                     plan=info["plan"], models=info["models"])
        else:
            native()


def claude_auth():
    claude = shutil.which("claude")
    if not claude:
        return None
    out, _ = run([claude, "auth", "status"], ok_codes=(0, 1))
    try:
        auth = json.loads(out)
        if not isinstance(auth.get("loggedIn"), bool):
            raise ValueError("unreadable sign-in state")
        return auth
    except (AttributeError, TypeError, ValueError, KeyError):
        return None


def claude_plan():
    auth = claude_auth()
    if auth is None:
        return None, "unreadable claude auth status"
    return auth.get("subscriptionType"), None


def claude_pool():
    claude = shutil.which("claude")
    if not claude:
        emit("claude", None, "claude-cli", error="claude not installed")
        return
    auth = claude_auth()
    if auth is None:
        emit("claude", None, "claude-cli", error="unreadable claude auth status")
        return
    signed_in = auth["loggedIn"]
    if not signed_in or auth.get("authMethod") not in ("claude.ai", "oauth_token"):
        emit("claude", False, "claude-cli", error="no claude.ai subscription sign-in")
        return
    plan = auth.get("subscriptionType")
    # /usage is a local command. Stream output carries its structured report,
    # while ordinary JSON output keeps only the rendered text.
    with tempfile.TemporaryDirectory(prefix="flowseer-pool-usage-") as scratch:
        out, _ = run([claude, "-p", "/usage", "--output-format", "stream-json",
                      "--verbose", "--no-session-persistence", "--tools", ""], cwd=scratch)
    try:
        messages = [json.loads(line) for line in out.splitlines() if line.strip()]
        if not any(m.get("type") == "result" and m.get("local_command") == "usage"
                   and m.get("num_turns") == 0 and not m.get("is_error") for m in messages):
            raise ValueError("missing local command result")
        report = next(m["usage_report"] for m in messages if m.get("usage_report"))
        limits = report["rate_limits"]["limits"]
        windows, resets = {}, {}
        names = {"session": "session", "weekly_all": "weekly"}
        for limit in limits or []:
            name = names.get(limit.get("kind"))
            if limit.get("kind") == "weekly_scoped":
                model = (limit.get("scope") or {}).get("model") or {}
                label = model.get("display_name", "")
                # Delegate meters Fable separately. Keep other model scopes
                # distinct so they cannot overwrite the all-model weekly row.
                name = "fableWeekly" if "fable" in label.lower() else label + "Weekly"
            percent = limit.get("percent")
            if name and finite_number(percent):
                windows[name] = percent
                if limit.get("resets_at"):
                    resets[name] = limit["resets_at"]
    except (AttributeError, TypeError, ValueError, KeyError, StopIteration):
        emit("claude", True, "claude-cli", error="unreadable claude /usage report",
             plan=plan)
        return
    emit("claude", True, "claude-cli", windows or None, resets,
         None if windows else "claude /usage returned no quota windows",
         plan=plan)


def codex_read(timeout=30, quota=True):
    info = {"signed_in": None, "plan": None, "models": None,
            "windows": None, "resets": {}, "error": None}
    codex = shutil.which("codex")
    if not codex:
        info["error"] = "codex not installed"
        return info
    proc = None
    reader = None
    try:
        proc = subprocess.Popen([codex, "app-server"], stdin=subprocess.PIPE,
                                stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                                bufsize=0)
        chunks = queue.Queue()
        stdout = proc.stdout

        def read_stdout():
            try:
                while chunk := stdout.read(65536):
                    chunks.put(chunk)
            except OSError:
                pass
            finally:
                stdout.close()
                chunks.put(b"")

        reader = threading.Thread(target=read_stdout, daemon=True)
        reader.start()
        buffer = b""

        def request(rid, method, params=None):
            nonlocal buffer
            message = {"id": rid, "method": method}
            if params is not None:
                message["params"] = params
            proc.stdin.write((json.dumps(message) + "\n").encode())
            proc.stdin.flush()
            deadline = time.monotonic() + timeout
            while True:
                while b"\n" in buffer:
                    line, buffer = buffer.split(b"\n", 1)
                    reply = json.loads(line)
                    if reply.get("id") == rid:
                        if "error" in reply:
                            raise ValueError(reply["error"].get("message", "request failed"))
                        return reply["result"]
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise TimeoutError("codex app-server timed out")
                try:
                    chunk = chunks.get(timeout=remaining)
                except queue.Empty:
                    raise TimeoutError("codex app-server timed out") from None
                if not chunk:
                    raise OSError("codex app-server exited")
                buffer += chunk

        request(1, "initialize", {"clientInfo": {"name": "flowseer_pool_usage", "version": "1"}})
        proc.stdin.write(b'{"method":"initialized","params":{}}\n')
        proc.stdin.flush()
        account = request(2, "account/read", {"refreshToken": False})["account"]
        info["signed_in"] = isinstance(account, dict) and account.get("type") == "chatgpt"
        if not info["signed_in"]:
            info["error"] = "no ChatGPT subscription sign-in"
            return info
        info["plan"] = account.get("planType")
        info["models"], info["error"] = codex_models(request)
        if not quota:
            return info
        quota_reply = request(100, "account/rateLimits/read")
        buckets = quota_reply.get("rateLimitsByLimitId") or {}
        snapshot = buckets.get("codex") or quota_reply["rateLimits"]
        windows, resets = {}, {}
        for key in ("primary", "secondary"):
            w = snapshot.get(key)
            if not isinstance(w, dict) or not finite_number(w.get("usedPercent")):
                continue
            # Primary can be weekly on accounts without a session window.
            duration = w.get("windowDurationMins")
            name = {300: "session", 10080: "weekly"}.get(duration, key)
            windows[name] = w["usedPercent"]
            if w.get("resetsAt"):
                resets[name] = iso(w["resetsAt"] * 1000)
        info["windows"] = windows or None
        info["resets"] = resets
        if not windows:
            info["error"] = "codex returned no quota windows"
    except (OSError, TypeError, ValueError, KeyError, AttributeError, TimeoutError):
        info["error"] = ("unreadable codex app-server quota" if quota
                          else "unreadable codex app-server account")
    finally:
        if proc is not None:
            proc.terminate()
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait()
            try:
                proc.stdin.close()
            except OSError:
                pass
            # A descendant can hold stdout open after the server is reaped.
            # Only its reader closes it, so cleanup cannot block on the read.
            if reader is not None:
                reader.join(timeout=5)
    return info


def codex_models(request):
    models = []
    cursor = None
    seen_cursors = set()
    request_id = 3
    while True:
        params = {} if cursor is None else {"cursor": cursor}
        try:
            result = request(request_id, "model/list", params)
        except (OSError, TypeError, ValueError, KeyError, AttributeError, TimeoutError):
            return None, "unreadable codex app-server model list"
        request_id += 1
        if not isinstance(result, dict) or not isinstance(result.get("data"), list):
            return None, "unreadable codex app-server model list"
        for model in result["data"]:
            if not isinstance(model, dict) or not isinstance(model.get("id"), str):
                return None, "unreadable codex app-server model list"
            models.append(model["id"])
        cursor = result.get("nextCursor")
        if cursor is None:
            return models or None, None
        if not isinstance(cursor, str):
            return None, "unreadable codex app-server model cursor"
        if cursor in seen_cursors:
            return None, "repeated codex app-server model cursor"
        seen_cursors.add(cursor)


def codex_pool(timeout=30):
    info = codex_read(timeout=timeout)
    emit("codex", info["signed_in"], "codex-app-server", info["windows"],
         info["resets"], info["error"], plan=info["plan"], models=info["models"])


def google_pool():
    agy = shutil.which("agy")
    if not agy:
        emit("google", False, "agy", error="agy not installed")
        return
    out, err = run([agy, "-p", "/quota", "--output-format", "json", "--print-timeout", "60s"])
    try:
        groups = json.loads(out)["command"]["data"]["groups"]
    except (TypeError, ValueError, KeyError):
        # A readable model list still establishes sign-in when quota is unreadable.
        models, _ = run([agy, "models"])
        emit("google", models is not None, "agy", error=err or "unreadable /quota reply")
        return
    windows, resets = {}, {}
    for g in groups:
        for b in g.get("buckets", []):
            windows[b["id"]] = round((1 - b["remaining_fraction"]) * 100)
            resets[b["id"]] = b.get("reset_time")
    emit("google", True, "agy", windows or None, resets)


def synthetic_key():
    key = os.environ.get("SYNTHETIC_API_KEY")
    if key:
        return key
    try:
        auth = json.loads(Path("~/.local/share/opencode/auth.json").expanduser().read_text(encoding="utf-8"))
        return auth["synthetic"]["key"]
    except (OSError, ValueError, KeyError, TypeError):
        return None


def synthetic_pool():
    key = synthetic_key()
    if not key:
        emit("synthetic", False, "synthetic-api",
             error="no key in SYNTHETIC_API_KEY or opencode auth.json")
        return
    req = urllib.request.Request("https://api.synthetic.new/v2/quotas",
                                 headers={"Authorization": "Bearer " + key})
    # The documented `subscription.requests` counter stays at 0 across real
    # requests. The two limits below are the ones that move. Both
    # refill continuously, so there is no reset time to report.
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            quota = json.load(resp)
        five = quota["rollingFiveHourLimit"]
        windows = {
            "5h": round((1 - five["remaining"] / five["max"]) * 100),
            "week": round(100 - quota["weeklyTokenLimit"]["percentRemaining"]),
        }
        if five.get("limited"):
            windows["5h"] = 100
    except urllib.error.HTTPError as e:
        # A rejected key is the API saying the pool is signed out.
        emit("synthetic", e.code not in (401, 403), "synthetic-api", error="HTTP %d" % e.code)
        return
    except (OSError, ValueError, KeyError, TypeError, ZeroDivisionError) as e:
        emit("synthetic", None, "synthetic-api", error=str(e) or "unreadable quota reply")
        return
    emit("synthetic", True, "synthetic-api", windows)


def zai_pool():
    # Z.ai (GLM) is served through omp, which reads its own credential store
    # and exposes the plan's quota through `omp usage --json`. omp owns the
    # numbers, so read them from it rather than calling api.z.ai directly.
    omp = shutil.which("omp")
    if not omp:
        emit("zai", None, "omp", error="omp not installed")
        return
    out, err = run([omp, "usage", "--provider", "zai", "--json"])
    plan = None
    try:
        reports = json.loads(out)["reports"]
        report = next(r for r in reports if r.get("provider") == "zai")
        metadata = report.get("metadata")
        plan = metadata.get("planType") if isinstance(metadata, dict) else None
        limits = report["limits"]
    except (TypeError, ValueError, KeyError, StopIteration):
        # No zai report means omp has no Z.ai credential signed in.
        emit("zai", False, "omp", error=err or "no zai account in omp", plan=plan)
        return
    windows, resets = {}, {}
    # Name the windows as the registry's zai pool expects: 5h and week.
    name_map = {"5h": "5h", "1w": "week", "weekly": "week"}
    for lim in limits:
        wid = lim.get("window", {}).get("id") or lim.get("scope", {}).get("windowId")
        amount = lim.get("amount", {})
        frac = amount.get("usedFraction")
        if wid is None or frac is None:
            continue
        key = name_map.get(wid, wid)
        windows[key] = round(frac * 100)
        r = lim.get("window", {}).get("resetsAt")
        if r:
            resets[key] = iso(r)
    emit("zai", True, "omp", windows or None, resets, plan=plan)


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="run.py delegate pool-usage")
    parser.parse_args(argv)
    orca_pools()
    google_pool()
    synthetic_pool()
    zai_pool()
    return 0
