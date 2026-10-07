"""Run a calibration lane and retain its raw output and accounting evidence."""

import argparse
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import time


def parse_result(cli: str, raw: Path, lane: str, model: str, effort: str,
                 wall: int, code: int) -> dict:
    usage = {"input": 0, "output": 0, "cache_read": 0, "reasoning": 0}
    cost = error = None
    # Tool denials can come from repository hooks, so they remain separate
    # from provider refusals when grading a lane.
    served, refused, refuse_reason, denied = set(), False, None, None
    refusal_text = re.compile(r"could not be submitted|sensitive words|prohibited|I can't help|I cannot help|I won't", re.I)

    def add(k, v):
        usage[k] += int(v or 0)

    try:
        text = raw.read_bytes().decode("utf-8", errors="replace")
    except OSError:
        text = None

    if text is None or (text == "" and int(code) != 0):
        usage = None
    else:
        if cli == "claude":
            try:
                d = json.loads(text)
                u = d.get("usage", {})
                add("input", u.get("input_tokens"))
                add("output", u.get("output_tokens"))
                add("cache_read", u.get("cache_read_input_tokens"))
                add("reasoning", (u.get("output_tokens_details") or {}).get("thinking_tokens"))
                cost = d.get("total_cost_usd")
                # modelUsage keys and canonicalModel name the model that answered,
                # so a cyber reroute to another model shows up here.
                for mid, mu in (d.get("modelUsage") or {}).items():
                    served.add(mid)
                    if isinstance(mu, dict) and mu.get("canonicalModel"):
                        served.add(mu["canonicalModel"])
                if d.get("stop_reason") == "refusal" or d.get("subtype") == "refusal":
                    refused, refuse_reason = True, "stop_reason=refusal"
                # Whether a hook's deny lands in `permission_denials` is
                # unverified, so a denial is recorded, not graded as a refusal.
                if d.get("permission_denials"):
                    denied = sorted({p.get("tool_name", "?") for p in d["permission_denials"]})
            except json.JSONDecodeError:
                pass
        elif cli == "codex":
            for line in text.splitlines():
                try:
                    e = json.loads(line)
                except json.JSONDecodeError:
                    continue
                if isinstance(e, dict) and e.get("model"):
                    served.add(e["model"])
                if e.get("type") == "turn.completed":
                    u = e.get("usage", {})
                    add("input", u.get("input_tokens"))
                    add("output", u.get("output_tokens"))
                    add("cache_read", u.get("cached_input_tokens"))
                    add("reasoning", u.get("reasoning_output_tokens"))
        elif cli == "agy":
            try:
                j = json.loads(text)
                u = j.get("usage", {})
                add("input", u.get("input_tokens"))
                add("output", u.get("output_tokens"))
                add("cache_read", u.get("cache_read_tokens"))
                add("reasoning", u.get("thinking_tokens"))
                if j.get("model"):
                    served.add(j["model"])
                # A prompt Google's filter rejects returns status SUCCESS, exit 0,
                # zero usage, and the refusal as `response` text in under 5 s, so
                # the exit code alone reports it as a clean run (.claude/models/evidence.md).
                resp = j.get("response") or ""
                if (str(j.get("status")).upper() == "SUCCESS" and not any(usage.values())
                        and refusal_text.search(resp)):
                    refused, refuse_reason = True, "google filter: " + resp[:120]
            except json.JSONDecodeError:
                pass
        elif cli == "omp":
            # A JSON-lines stream; each assistant message_end carries usage with an
            # inline cost, the model that answered, and the stop reason.
            for line in text.splitlines():
                try:
                    e = json.loads(line)
                except json.JSONDecodeError:
                    continue
                if e.get("type") != "message_end":
                    continue
                msg = e.get("message") or {}
                if msg.get("role") != "assistant":
                    continue
                u = msg.get("usage") or {}
                add("input", u.get("input"))
                add("output", u.get("output"))
                add("cache_read", u.get("cacheRead"))
                add("reasoning", u.get("reasoning"))
                total = (u.get("cost") or {}).get("total")
                if isinstance(total, (int, float)):
                    cost = (cost or 0) + total
                if msg.get("model"):
                    served.add(msg["model"])
                if msg.get("stopReason") in ("refusal", "content_filter", "safety"):
                    refused, refuse_reason = True, "stopReason=" + msg["stopReason"]

    if error is None and int(code) != 0:
        try:
            error = Path(str(raw) + ".err").read_bytes().decode("utf-8", errors="replace").strip() or None
        except OSError:
            pass

    # A safety reroute or fallback answers under a different model id. Compare on
    # a base id so a dated Claude snapshot (`-YYYYMMDD`) is not read as a change.
    # omp pins its model and does not reroute, so its downgrade check is left null.
    def base_id(m):
        return re.sub(r"-\d{8}$", "", (m or "").rsplit("/", 1)[-1])

    served_list = sorted(served)
    # A reroute can affect only some turns. Any foreign served id flags it.
    downgraded = None
    served_foreign = None
    if cli in ("claude", "codex", "agy") and served_list:
        want = base_id(model)
        foreign = sorted({s for s in served_list if base_id(s) != want})
        downgraded = bool(foreign)
        served_foreign = foreign or None

    result = {"lane": lane, "cli": cli, "model": model, "effort": effort or None,
              "wall_s": int(wall), "exit": int(code), "usage": usage, "cost_usd_reported": cost,
              "served_model": served_list or None, "served_foreign": served_foreign,
              "downgraded": downgraded, "refused": refused, "refuse_reason": refuse_reason,
              "denied_tools": denied}
    if error is not None:
        result["error"] = error
    return result


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="run.py tune bench")
    for name in ("lane", "cli", "model", "brief", "dir", "out"):
        parser.add_argument("--" + name, required=True)
    parser.add_argument("--effort", default="")
    args = parser.parse_args(argv)
    for name in ("lane", "cli", "model", "brief", "dir", "out"):
        if not getattr(args, name):
            parser.error("--" + name + " is required")
    if args.cli not in ("claude", "codex", "agy", "omp"):
        parser.error("unknown cli " + args.cli)
    if args.effort and args.cli == "agy":
        parser.error("--effort cannot be applied on agy; agy takes the level in the model id")
    try:
        prompt = Path(args.brief).read_bytes().decode("utf-8").rstrip("\n")
        directory = Path(args.dir).resolve()
        out = Path(args.out)
        if not out.is_absolute():
            out = directory / out
        raw = Path(str(out).removesuffix(".json") + ".raw")
        err = Path(str(raw) + ".err")
        for path in (raw, err, out):
            path.unlink(missing_ok=True)
        env = os.environ.copy()
        if args.cli == "claude":
            env.pop("CLAUDECODE", None)
            env.pop("CLAUDE_CODE_ENTRYPOINT", None)
            command = ["claude", "-p", prompt, "--model", args.model]
            if args.effort:
                command += ["--effort", args.effort]
            command += ["--output-format", "json", "--dangerously-skip-permissions"]
        elif args.cli == "codex":
            command = ["codex", "exec", "--json", "--skip-git-repo-check",
                       "--dangerously-bypass-approvals-and-sandbox", "-c",
                       'plugins."compound-engineering@compound-engineering-plugin".enabled=false',
                       "-m", args.model]
            if args.effort:
                command += ["-c", "model_reasoning_effort=" + args.effort]
            command += [prompt]
        elif args.cli == "agy":
            command = ["agy", "-p", prompt, "--model", args.model, "--output-format", "json",
                       "--dangerously-skip-permissions", "--print-timeout", "60m"]
        else:
            command = ["omp", "-p", "--model", args.model]
            if args.effort:
                command += ["--thinking", args.effort]
            command += ["--auto-approve", "--mode", "json", "--no-session", "--no-title", prompt]
        command[0] = shutil.which(args.cli) or args.cli
        start = int(time.time())
        with raw.open("wb") as stdout, err.open("wb") as stderr:
            try:
                child = subprocess.run(command, cwd=directory, env=env, stdin=subprocess.DEVNULL,
                                       stdout=stdout, stderr=stderr, check=False)
                code = child.returncode
            except OSError as error:
                stderr.write(str(error).encode("utf-8"))
                code = 127
        result = parse_result(args.cli, raw, args.lane, args.model, args.effort,
                              int(time.time()) - start, code)
        rendered = json.dumps(result, indent=1)
        out.write_text(rendered, encoding="utf-8")
        print(rendered)
        if code:
            print(result.get("error") or f"{args.cli} exited {code}", file=sys.stderr)
            return 1
        return 0
    except (OSError, UnicodeError) as error:
        print(f"bench: {error}", file=sys.stderr)
        return 1
