"""Report installed agent CLIs, Orca reachability, and prepaid pool rows."""

import argparse
from contextlib import redirect_stdout
from datetime import datetime, timezone
import io
import json
import re
import shutil
import subprocess
import sys
import traceback

from skills.delegate import pool_usage


def output(argv: list[str], *, require_success: bool = False) -> str:
    try:
        child = subprocess.run(argv, stdin=subprocess.DEVNULL, capture_output=True, check=False)
        if require_success and child.returncode:
            return ""
        return child.stdout.decode("utf-8", errors="replace")
    except OSError:
        return ""


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="run.py tune discover-host")
    parser.parse_args(argv)
    print("generated: " + datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"))
    print("clis:")
    for name in ("claude", "codex", "agy", "omp"):
        path = shutil.which(name)
        if path:
            version = output([path, "--version"]).split("\n", 1)[0]
            print(f"  {name}: {{path: {path}, version: {json.dumps(version)}}}")
        else:
            print(f"  {name}: null")
    print("orca:")
    orca = shutil.which("orca")
    reachable = False
    if orca:
        try:
            reachable = bool(json.loads(output([orca, "status", "--json"], require_success=True))["result"]["runtime"]["reachable"])
        except (ValueError, KeyError, TypeError):
            pass
    print("  reachable: " + str(reachable).lower())
    if reachable:
        help_text = output([orca, "orchestration", "worker-start", "--help"])
        pins = []
        for line in help_text.split("\n"):
            match = re.search(r".*--model supports (.*) opaque provider model ids.*", line)
            if match:
                pins.append(match[1].replace(",", "").lower().replace(" and ", " "))
        print("  model_pinnable: [" + ", ".join("".join(pins).split(" ")) + "]")
    print("pools:")
    rows = io.StringIO()
    failure = None
    # The reader prints each pool as it goes and raises on a reply of an
    # unexpected shape, so the rows already printed stay in the report.
    with redirect_stdout(rows):
        try:
            pool_usage.main([])
        except Exception:
            failure = traceback.format_exc()
    for row in rows.getvalue().split("\n"):
        if row:
            print("  " + row)
    if failure:
        print("discover-host: pool usage failed\n" + failure, end="", file=sys.stderr)
    omp = shutil.which("omp")
    if omp:
        try:
            models = json.loads(output([omp, "models", "synthetic", "--json"])).get("models", [])
            if not isinstance(models, list):
                raise ValueError("unreadable models")
            ids = [model["id"] for model in models
                   if isinstance(model, dict) and isinstance(model.get("id"), str) and model["id"]]
        except (ValueError, AttributeError, TypeError):
            ids = []
        print("omp_models:")
        print("  synthetic: [" + ", ".join(json.dumps(model) for model in ids) + "]")
    else:
        print("omp_models: {synthetic: []}")
    return 1 if failure else 0
