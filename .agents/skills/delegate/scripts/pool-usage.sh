#!/usr/bin/env bash
# Print, as one YAML flow map per line, the sign-in state and used percent of
# every window of the prepaid pools (claude, codex, google, synthetic, zai). Each pool is read from the source
# that owns its numbers, because no single tool sees them all:
#
#   claude, codex  orca account list --json      (rateLimits), falling back
#                  to each CLI's own token when Orca has no account for it
#   google         agy -p /quota                 (answers without a model turn)
#   synthetic      GET api.synthetic.new/v2/quotas (rolling five-hour request
#                  limit and weekly credit limit; the call is not counted)
#   zai            omp usage --provider zai --json (Z.ai GLM Lite plan quota;
#                  omp owns the credential and the numbers)
#
# Orca also lists `antigravity` with status `unavailable`. That status means
# Orca cannot read its usage, not that the pool is down, so it is never
# consulted for it. A source that fails leaves its pool with `windows: null`
# and an `error`; it does not mark the pool signed out unless the source
# itself says so.
#
# The synthetic pool is served through omp, but omp keeps no auth file, so
# the key still comes from SYNTHETIC_API_KEY, else from the `synthetic` entry
# that resides in ~/.local/share/opencode/auth.json. It is sent only to
# api.synthetic.new and never printed.
#
# Run unsandboxed: orca uses a local socket, agy the network and the keyring,
# and the synthetic read needs the network.

set -uo pipefail

python3 - <<'PY'
import datetime
import json
import os
import shutil
import subprocess
import urllib.error
import urllib.request


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


def orca_unreadable(error):
    # Without Orca's rows the claude pool still has the CLI's own token.
    # A token found is signed in. No token file is not a sign-out, since
    # the CLI may keep it elsewhere, so the state stays unknown (null).
    emit("claude", claude_cli_signed_in() or None, "orca", error=error)
    emit("codex", None, "orca", error=error)


def orca_pools():
    if not shutil.which("orca"):
        orca_unreadable("orca not installed")
        return
    out, err = run(["orca", "account", "list", "--json"])
    try:
        result = json.loads(out)["result"]
    except (TypeError, ValueError, KeyError):
        orca_unreadable(err or "unreadable account list")
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
        if pool == "claude":
            # Orca knows only about accounts registered with Orca. The CLI
            # keeps its own OAuth token, and a host where `claude` is signed
            # in but Orca has no Claude account reads as signed out, which
            # takes every Claude model out of every fit set.
            # `codex` has had the same fallback all along, through
            # systemDefault.hasAuth.
            signed_in = signed_in or claude_cli_signed_in()
        note = rl.get("error")
        if pool == "claude" and signed_in and not windows and rl.get("status") != "ok":
            # Signed in on the CLI's own token, so the pool is usable, but
            # Orca is the only source of the windows and it has nothing.
            note = note or "signed in on the claude CLI token; orca has no account for it, so no windows"
        emit(pool, signed_in, "orca", windows or None, resets, note)


def claude_cli_signed_in():
    """True when the `claude` CLI holds an OAuth token that has not expired.

    A refresh token that is still valid counts: the CLI renews the access
    token on its own, so an expired access token alone is not a sign-out.
    """
    try:
        auth = json.load(open(os.path.expanduser("~/.claude/.credentials.json")))
        oauth = auth["claudeAiOauth"]
    except (OSError, ValueError, KeyError, TypeError):
        return False
    now = datetime.datetime.now(datetime.timezone.utc).timestamp() * 1000
    for field in ("expiresAt", "refreshTokenExpiresAt"):
        expiry = oauth.get(field)
        if isinstance(expiry, (int, float)) and expiry > now:
            return True
    return False


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


def synthetic_key():
    key = os.environ.get("SYNTHETIC_API_KEY")
    if key:
        return key
    try:
        auth = json.load(open(os.path.expanduser("~/.local/share/opencode/auth.json")))
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
    if not shutil.which("omp"):
        emit("zai", None, "omp", error="omp not installed")
        return
    out, err = run(["omp", "usage", "--provider", "zai", "--json"])
    try:
        reports = json.loads(out)["reports"]
        report = next(r for r in reports if r.get("provider") == "zai")
        limits = report["limits"]
    except (TypeError, ValueError, KeyError, StopIteration):
        # No zai report means omp has no Z.ai credential signed in.
        emit("zai", False, "omp", error=err or "no zai account in omp")
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
    emit("zai", True, "omp", windows or None, resets)


orca_pools()
google_pool()
synthetic_pool()
zai_pool()
PY
