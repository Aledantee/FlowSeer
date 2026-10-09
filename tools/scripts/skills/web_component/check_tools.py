"""Report web gate and optional review tools without installing software."""

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import urllib.error
import urllib.request
from pathlib import Path

from lib import repo


def version(argv: list[str]) -> str:
    try:
        result = subprocess.run(argv, capture_output=True, check=False)
    except OSError:
        return ""
    return result.stdout.decode("utf-8", errors="replace").rstrip("\n") if result.returncode == 0 else ""


def storybook_probe() -> int:
    try:
        with urllib.request.urlopen("http://127.0.0.1:6006/", timeout=2) as response:
            response.read()
        return 0
    except (OSError, urllib.error.URLError, ValueError) as error:
        print(f"check-tools: Storybook probe failed: {error}", file=sys.stderr)
        return 1


def storybook_running() -> bool:
    runner = Path(__file__).resolve().parents[2] / "run.py"
    try:
        result = subprocess.run(
            [sys.executable, str(runner), "web-component", "check-tools", "--storybook-probe"],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=2, check=False,
        )
        return result.returncode == 0
    except (OSError, subprocess.TimeoutExpired):
        return False


def report(status: str, name: str, purpose: str, install: str = "") -> None:
    print(f"{status:<8} {name:<18} {purpose}")
    if install:
        print(f"{'':<8} {'':<18} install: {install}")


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="run.py web-component check-tools")
    parser.add_argument("--storybook-probe", action="store_true")
    args = parser.parse_args(argv)
    if args.storybook_probe:
        return storybook_probe()
    try:
        web = repo.root() / "frontend" / "web"
        package = json.loads((web / "package.json").read_text(encoding="utf-8"))
        floor = package["engines"]["node"]
        match = re.fullmatch(r">=(\d+)\.(\d+)\.(\d+)", floor)
        if match is None:
            raise ValueError(f"unsupported Node engines floor: {floor}")
        minimum = tuple(int(part) for part in match.groups())
        pin = package["packageManager"]
    except (OSError, RuntimeError, ValueError, KeyError, TypeError) as error:
        print(f"check-tools: {error}", file=sys.stderr)
        return 1
    windows = sys.platform == "win32"
    missing = False
    node = shutil.which("node")
    node_version = version([node, "--version"]) if node else ""
    parsed = re.fullmatch(r"v(\d+)\.(\d+)\.(\d+)", node_version)
    node_install = f"install a Node {floor[2:]}+ runtime" if windows else f"nvm install {minimum[0]}"
    if not node_version:
        report("missing", "node", f"runs every web gate (package.json engines: {floor})", node_install)
        missing = True
    elif parsed is None:
        report("invalid", "node", f"malformed version: {node_version}", node_install)
        missing = True
    elif tuple(int(part) for part in parsed.groups()) < minimum:
        report("old", "node", f"{node_version} is below the engines floor {floor[2:]}", node_install)
        missing = True
    else:
        report("ok", "node", node_version)
    pnpm = shutil.which("pnpm.cmd" if windows else "pnpm")
    if pnpm:
        report("ok", "pnpm", version([pnpm, "--version"]))
    else:
        report("missing", "pnpm", f"package manager pinned in package.json: {pin}",
               f"corepack enable pnpm (package.json: {pin})")
        missing = True
    bins = web / "node_modules" / ".bin"
    suffix = ".cmd" if windows else ""
    if all(shutil.which(name + suffix, path=str(bins)) for name in ("vitest", "vue-tsc")):
        report("ok", "node_modules", "frontend/web dependencies installed")
    else:
        report("missing", "node_modules", "frontend/web dependencies from the lockfile",
               "cd frontend/web && pnpm install --frozen-lockfile (needs the npm registry, unsandboxed)")
        missing = True
    browser = shutil.which("agent-browser.cmd" if windows else "agent-browser")
    if browser:
        report("ok", "agent-browser", f"{version([browser, '--version'])} (screenshot review of stories)")
    else:
        hint = "install agent-browser and its browser runtime" if windows else "brew install agent-browser && agent-browser install"
        report("optional", "agent-browser", "screenshots stories in a real browser for layout, stacking, and motion checks", hint)
    impeccable = Path.home() / ".claude/plugins/marketplaces/impeccable/.claude/skills/impeccable/scripts/impeccable"
    probe = None
    if not windows and os.access(impeccable, os.X_OK):
        try:
            result = subprocess.run([str(impeccable), "engine-probe"], capture_output=True, check=False)
            if result.returncode == 0:
                probe = result.stdout.decode("utf-8", errors="replace").rstrip("\n")
        except OSError:
            pass
    if probe is not None:
        report("ok", "impeccable", f"{probe} (design anti-pattern detector at {impeccable})")
    else:
        purpose = "unsupported on Windows" if windows else "design anti-pattern detector used in review mode"
        hint = "" if windows else "claude plugin marketplace add pbakaus/impeccable && claude plugin install impeccable@impeccable"
        report("optional", "impeccable", purpose, hint)
    if storybook_running():
        report("ok", "storybook", "running on http://127.0.0.1:6006")
    else:
        report("optional", "storybook", "not running; screenshot review needs it",
               "cd frontend/web && pnpm storybook (keep it running in a background shell)")
    if missing:
        print("check-tools: required web tools are missing, outdated, or invalid", file=sys.stderr)
    return int(missing)
