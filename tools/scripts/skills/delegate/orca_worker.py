"""Supervise an Orca lane, retaining recoverable state when cleanup fails."""

import argparse
import contextlib
import io
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys
import time

from skills.delegate import model_check, runlog


class WorkerError(Exception):
    pass


def windows() -> bool:
    return os.name == "nt"


def launch_line(cli: str, model: str, effort: str = "") -> str:
    if windows():
        raise WorkerError("Windows terminal launches are unsupported")
    if not model:
        raise WorkerError(f"--model is required for {cli}")
    if cli == "claude":
        args = ["claude", "--model", model, "--dangerously-skip-permissions", "--settings", '{"switchModelsOnFlag":false}']
        if effort:
            args += ["--effort", effort]
    elif cli == "codex":
        args = ["codex", "-a", "never", "--sandbox", "danger-full-access", "-c", "check_for_update_on_startup=false", "-c", "background_terminal_max_timeout=3600000", "-m", model]
        if effort:
            args += ["-c", f"model_reasoning_effort={effort}"]
    elif cli == "agy":
        if effort:
            raise WorkerError("--effort does not apply to agy: it is part of the model id")
        args = ["agy", "--model", model, "--dangerously-skip-permissions"]
    elif cli == "omp":
        args = ["omp", "--model", model]
        if effort:
            args += ["--thinking", effort]
    else:
        raise WorkerError(f"unknown cli {cli}")
    return shlex.join(["env", "-u", "CLAUDECODE", "-u", "CLAUDE_CODE_ENTRYPOINT", "-u", "CLAUDE_CODE_CHILD_SESSION", *args])


def run(args: list[str], cwd: Path | None = None) -> subprocess.CompletedProcess:
    # Screens retain CR bytes because hint matching and settled-state equality
    # operate on the terminal's LF-delimited output.
    result = subprocess.run(args, cwd=cwd, capture_output=True, check=False)
    return subprocess.CompletedProcess(args, result.returncode, result.stdout.decode("utf-8"), result.stderr.decode("utf-8"))


def checked(args: list[str], cwd: Path | None = None) -> str:
    result = run(args, cwd)
    if result.returncode:
        raise WorkerError((result.stdout + result.stderr).rstrip("\n"))
    return result.stdout.rstrip("\n")


def reply(*args: str) -> dict:
    return json.loads(checked(["orca", *args]))["result"]


def screen(term: str) -> str:
    if not term:
        raise WorkerError("empty terminal")
    return checked(["orca", "terminal", "read", "--terminal", term, "--screen"])


def working(value: str) -> bool:
    return re.search(r"esc( to)? (interrupt|cancel)", value, re.I) is not None


def tail(value: str, count: int = 30) -> str:
    return "\n".join([line for line in value.split("\n") if line.strip()][-count:])


def limited(value: str) -> bool:
    return re.search(r"rate.?limit|usage limit|limit reached|quota.{0,20}(exceeded|exhausted|reached)|(exceeded|exhausted|reached).{0,20}quota|try again (in|at)|resets? (in|at)", tail(value), re.I) is not None


class Worker:
    def __init__(self):
        self.common = Path(checked(["git", "rev-parse", "--path-format=absolute", "--git-common-dir"]))
        self.state_dir = self.common / "orca-workers"

    def state(self, lane: str) -> dict:
        try:
            return json.loads((self.state_dir / f"{lane}.json").read_text(encoding="utf-8"))
        except (OSError, ValueError):
            raise WorkerError(f"no lane named {lane}; status lists them") from None

    def save(self, state: dict):
        self.state_dir.mkdir(parents=True, exist_ok=True)
        (self.state_dir / f"{state['name']}.json").write_text(json.dumps(state), encoding="utf-8")

    def lanes_on(self, wt: str) -> list[dict]:
        return [self.state(path.stem) for path in sorted(self.state_dir.glob("*.json")) if self.state(path.stem).get("worktree") == wt]

    def live_on(self, wt: str, exclude: str = "") -> dict | None:
        return next((lane for lane in self.lanes_on(wt) if lane["name"] != exclude and not lane.get("kept")), None)

    def children(self, wt: str) -> list[str]:
        try:
            children = reply("worktree", "show", "--worktree", f"id:{wt}", "--json")["worktree"]["childWorktreeIds"]
        except (KeyError, TypeError, ValueError) as error:
            raise WorkerError(f"child worktree check failed: {error}") from error
        if not isinstance(children, list) or not all(isinstance(child, str) for child in children):
            raise WorkerError("child worktree check returned no list of ids")
        return children

    def child_info(self, wt: str) -> tuple[str, str]:
        lanes = self.lanes_on(wt)
        live = self.live_on(wt)
        if live:
            return live["name"], live.get("terminal", "")
        return (lanes[0]["name"] if lanes else wt), ""

    def log(self, *args: str) -> str:
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            code = runlog.main(list(args))
        if code:
            raise WorkerError(out.getvalue().rstrip("\n"))
        return out.getvalue().rstrip("\n")

    def check(self, state: dict):
        cli, path, run_id = (state.get(key) for key in ("cli", "path", "run"))
        if not all((cli, path, run_id)):
            raise WorkerError(f"lane {state['name']} has incomplete state")
        if cli != "claude":
            print(f"not checked: {cli}")
            return
        start = runlog.last_start(run_id).split("\t")
        if len(start) != 2 or not all(start):
            raise WorkerError(f"lane {state['name']} has no start model and time")
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            code = model_check.main([path, *start])
        if code:
            raise WorkerError(out.getvalue().rstrip("\n") or "model check failed")
        print(out.getvalue(), end="")

    def deliver(self, term: str, path: str, file: str, name: str, *, start: bool = False):
        exclude = self.common / "info" / "exclude"
        try:
            existing = exclude.read_text(encoding="utf-8") if exclude.exists() else ""
            if f"/{name}" not in existing.split("\n"):
                exclude.parent.mkdir(parents=True, exist_ok=True)
                with exclude.open("a", encoding="utf-8") as stream:
                    stream.write(f"/{name}\n")
        except OSError as error:
            raise WorkerError(f"cannot write git exclude file: {error}") from error
        try:
            shutil.copyfile(file, Path(path) / name)
        except OSError as error:
            raise WorkerError(f"cannot write the brief into {path}: {error}") from error
        pointer = f"Read {name} in this directory and carry it out. Never commit or delete that file."
        before = 0 if start else sum(f"Read {name}" in line for line in screen(term).split("\n"))
        send_error = ""
        for _ in range(2):
            result = run(["orca", "terminal", "send", "--terminal", term, "--text", pointer, "--enter", "--wait-submit", "20", "--json"])
            if start and result.returncode:
                send_error = f"; send failed: {(result.stdout + result.stderr).rstrip()}"
                continue
            time.sleep(2)
            if sum(f"Read {name}" in line for line in screen(term).split("\n")) > before:
                return
            time.sleep(2)
        label = "pointer" if start else "message"
        raise WorkerError(f"{label} not on screen after two submissions{send_error}: {tail(screen(term), 8)}")

    def start(self, args):
        line = launch_line(args.cli, args.model, args.effort)
        if args.agent:
            raise WorkerError(f"--agent does not apply to {args.cli}")
        if args.join and args.base:
            raise WorkerError("--join and --base are exclusive: a joined lane starts from its worktree's HEAD")
        if not re.fullmatch(r"[a-z][a-z0-9_-]{0,31}", args.lane):
            raise WorkerError("lane must match [a-z][a-z0-9_-]{0,31}")
        brief = Path(args.brief)
        if not brief.is_file():
            raise WorkerError(f"brief {brief} not found")
        if not brief.stat().st_size:
            raise WorkerError(f"brief {brief} is empty")
        if not reply("status", "--json")["runtime"]["reachable"]:
            raise WorkerError("Orca runtime not reachable; sandboxed calls report this too")
        if (self.state_dir / f"{args.lane}.json").exists():
            raise WorkerError(f"a lane named {args.lane} exists; stop it or pick another slug")
        if args.join:
            joined = self.state(args.join)
            wt, path, branch = (joined[key] for key in ("worktree", "path", "branch"))
            live = self.live_on(wt)
            if live:
                raise WorkerError(f"lane {live['name']} still has a live terminal in {path}; stop it with --keep-worktree first")
            dirty = checked(["git", "-C", path, "status", "--porcelain"])
            if dirty:
                raise WorkerError(f"{path} is dirty; commit or discard before joining: {dirty}")
        else:
            base = args.base or checked(["git", "branch", "--show-current"])
            if not base:
                raise WorkerError("detached HEAD: pass --base REF")
            created = reply("worktree", "create", "--name", args.lane, "--parent-worktree", "active", "--base-branch", base, "--setup", "skip", "--comment", f"delegate lane {args.lane} ({args.cli})", "--json")["worktree"]
            wt, path, branch = created["id"], created["path"], created["branch"].removeprefix("refs/heads/")
            if not all((wt, path, branch)):
                raise WorkerError("worktree create returned no id, path, or branch")
        state = dict(name=args.lane, cli=args.cli, terminal="", worktree=wt, path=path, branch=branch, run="")
        base_sha = ""
        try:
            base_sha = checked(["git", "-C", path, "rev-parse", "HEAD"])
            try:
                state["terminal"] = reply("terminal", "create", "--worktree", f"id:{wt}", "--title", args.lane, "--command", line, "--json")["terminal"]["handle"]
            except WorkerError as error:
                raise WorkerError(f"terminal create failed: {error}") from error
            term = state["terminal"]
            if not term:
                raise WorkerError("terminal create returned no handle")
            waited = run(["orca", "terminal", "wait", "--terminal", term, "--for", "tui-idle", "--timeout-ms", "90000", "--json"])
            try:
                unsatisfied = json.loads(waited.stdout)["result"]["wait"].get("satisfied") is False
            except (ValueError, KeyError):
                unsatisfied = False
            if unsatisfied:
                run(["orca", "terminal", "wait", "--terminal", term, "--for", "tui-idle", "--timeout-ms", "180000", "--json"])
            if args.cli == "codex":
                for _ in range(4):
                    time.sleep(2)
                    value = screen(term)
                    if "restart Codex" in value:
                        raise WorkerError("Codex updated itself and exited at startup")
                    if "Skip until next version" in value:
                        raise WorkerError("Codex showed its update offer")
                    if not re.search("hooks need review", value, re.I):
                        break
                    number = re.search(r"([0-9])\. Trust all and continue", value)
                    if not number:
                        raise WorkerError('hooks review has no "Trust all and continue" option')
                    checked(["orca", "terminal", "send", "--terminal", term, "--text", number[1], "--json"])
                    time.sleep(1)
                    checked(["orca", "terminal", "send", "--terminal", term, "--text", "", "--enter", "--json"])
                time.sleep(2)
                if re.search("hooks need review", screen(term), re.I):
                    raise WorkerError("still shows the hooks review after four rounds")
            try:
                shown = reply("terminal", "show", "--terminal", term, "--json")
            except WorkerError as error:
                raise WorkerError(f"terminal show failed: {error}") from error
            status = shown["terminal"].get("status") or shown.get("status")
            if not status:
                raise WorkerError("terminal show returned no status")
            if status == "exited":
                raise WorkerError(f"{args.cli} exited at startup: {tail(screen(term), 5)}")
            log_args = ["start", "--lane", args.lane, "--cli", args.cli, "--role", args.role, "--worktree", path, "--branch", branch, "--base", base_sha]
            for key in ("model", "effort", "agent", "plan", "unit"):
                if getattr(args, key):
                    log_args += [f"--{key}", getattr(args, key)]
            try:
                state["run"] = self.log(*log_args)
            except WorkerError as error:
                raise WorkerError(f"run log start failed: {error}") from error
            self.deliver(term, path, args.brief, ".orca-brief.md", start=True)
            try:
                self.save(state)
            except OSError as error:
                raise WorkerError(f"cannot write lane state for {args.lane}: {error}") from error
        except (WorkerError, OSError, ValueError, KeyError, TypeError, AttributeError) as error:
            self.undo(state, args.join, base_sha, str(error))
        print(json.dumps({key: state[key] for key in ("name", "terminal", "worktree", "path", "branch", "run")}))

    def undo(self, state: dict, join: str, base: str, reason: str):
        if state["run"]:
            head = run(["git", "-C", state["path"], "rev-parse", "HEAD"])
            try:
                self.log("end", "--run", state["run"], "--head", head.stdout.rstrip("\n") if not head.returncode else base)
            except WorkerError as error:
                reason += f"; run log end failed: {error}"
        failure = ""
        if state["terminal"] and run(["orca", "terminal", "close", "--terminal", state["terminal"], "--json"]).returncode:
            failure = "terminal close failed"
        if not failure and join:
            (self.state_dir / f"{state['name']}.json").unlink(missing_ok=True)
            raise WorkerError(reason)
        if not failure:
            try:
                children = self.children(state["worktree"])
                if children:
                    failure = f"it has child worktrees: {' '.join(children)}"
            except (WorkerError, ValueError, KeyError):
                failure = "child worktree check failed"
        if not failure and run(["orca", "worktree", "rm", "--worktree", f"id:{state['worktree']}", "--force", "--json"]).returncode:
            failure = "worktree removal failed"
        if failure:
            try:
                self.save(state)
            except OSError as error:
                reason += f"; cannot save lane state for {state['name']}: {error}"
            raise WorkerError(f"{reason}; {failure}; lane {state['name']} is still live and must be removed by hand")
        try:
            (self.state_dir / f"{state['name']}.json").unlink(missing_ok=True)
        except OSError:
            # Failed state-directory creation leaves no state file to remove.
            pass
        raise WorkerError(reason)

    def wait(self, args):
        state = self.state(args.lane)
        term, wt, path = (state.get(key) for key in ("terminal", "worktree", "path"))
        if not all((term, wt, path)):
            raise WorkerError(f"no lane named {args.lane}; status lists them")
        deadline = time.monotonic() + args.max
        since, last = time.monotonic(), ""
        show = True
        while True:
            waited = run(["orca", "terminal", "wait", "--terminal", term, "--for", "tui-idle", "--timeout-ms", str(min(60000, max(1, int((deadline - time.monotonic()) * 1000)))), "--json"])
            try:
                status = json.loads(waited.stdout)["result"]["wait"]["status"]
            except (ValueError, KeyError):
                status = ""
            if status == "exited":
                outcome = "exited"
                break
            try:
                children = [self.child_info(child) for child in self.children(wt)]
            except (WorkerError, ValueError, KeyError):
                children = [("unreadable", "")]
            first = []
            for _, child_term in children:
                try:
                    first.append(screen(child_term))
                except WorkerError:
                    first.append(None)
            try:
                s1 = screen(term)
            except WorkerError:
                outcome, show = "exited", False
                break
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                outcome = "timeout"
                break
            time.sleep(min(5, remaining))
            try:
                s2 = screen(term)
            except WorkerError:
                outcome, show = "exited", False
                break
            active = False
            for (_, child_term), value in zip(children, first):
                if value is None:
                    continue
                try:
                    second = screen(child_term)
                except WorkerError:
                    continue
                active |= working(value) or working(second) or value != second
            idle = not working(s1) and not working(s2) and s1 == s2
            now = time.monotonic()
            if now >= deadline:
                outcome = "timeout"
                break
            if s1 != last or s1 != s2 or active:
                since = now
            last = s1
            if idle and not active:
                if args.until is not None:
                    try:
                        result = subprocess.run(args.until, cwd=path, shell=False, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=max(0, deadline - time.monotonic()), check=False)
                    except subprocess.TimeoutExpired:
                        outcome = "timeout"
                        break
                    except OSError:
                        result = None
                    if result is not None and result.returncode == 0:
                        outcome = "done"
                        break
                if not children:
                    outcome = "limited" if limited(s2) else "idle"
                    break
                if args.stall > 0 and now - since >= args.stall:
                    outcome = "idle-children " + " ".join(name for name, _ in children)
                    break
            elif not idle and not active and args.stall > 0 and now - since >= args.stall:
                outcome = "stalled"
                break
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                outcome = "timeout"
                break
            time.sleep(min(5, remaining))
        print(outcome)
        if show:
            print(tail(screen(term)))

    def stop(self, args):
        state = self.state(args.lane)
        wt, path, term, run_id = (state.get(key, "") for key in ("worktree", "path", "terminal", "run"))
        kept = state.get("kept", False)
        if kept and args.keep_worktree:
            raise WorkerError(f"lane {args.lane} is already kept; stop it without --keep-worktree to remove the worktree")
        if not run_id:
            raise WorkerError(f"lane {args.lane} has no run; cannot verify grade")
        if not runlog.has_grade(run_id):
            raise WorkerError(f"lane {args.lane} has no grade event; grade it before stop")
        if not args.keep_worktree:
            other = self.live_on(wt, args.lane)
            if other:
                raise WorkerError(f"lane {other['name']} still has a live terminal in {path}; stop it first, nothing removed")
        if not kept and not args.stalled and working(screen(term)):
            raise WorkerError(f"{args.lane} is still working; wait for it, or pass --stalled after wait printed stalled")
        if not args.keep_worktree:
            try:
                children = self.children(wt)
            except WorkerError:
                raise WorkerError(f"cannot read the child worktrees of {args.lane}; nothing removed") from None
            if children:
                raise WorkerError(f"{args.lane} has child worktrees of its own; merge and remove them first, nothing removed: {' '.join(children)}")
        for name in (".orca-brief.md", ".orca-note.md"):
            (Path(path) / name).unlink(missing_ok=True)
        dirty = checked(["git", "-C", path, "status", "--porcelain"])
        if dirty:
            raise WorkerError(f"{path} is dirty; nothing removed: {dirty}")
        branch = state["branch"]
        if not args.keep_worktree and run(["git", "merge-base", "--is-ancestor", branch, "HEAD"]).returncode and run(["git", "merge-base", "--is-ancestor", branch, f"refs/heads/parked/{args.lane}"]).returncode:
            raise WorkerError(f"{branch} has commits that are not merged into {checked(['git', 'branch', '--show-current'])} or kept on parked/{args.lane}; merge it first, park it with 'git branch parked/{args.lane} {branch}', or remove the lane by hand in Orca")
        head = checked(["git", "rev-parse", branch])
        if not kept:
            if run(["orca", "terminal", "close", "--terminal", term, "--json"]).returncode:
                raise WorkerError(f"terminal close failed for {args.lane}; nothing removed")
            self.log("end", "--run", run_id, "--head", head)
        if args.keep_worktree:
            state.update(terminal="", kept=True)
            self.save(state)
            return
        children = self.children(wt)
        if children:
            raise WorkerError(f"{args.lane} started child worktrees before its terminal closed; merge and remove them, then remove {args.lane} in Orca: {' '.join(children)}")
        try:
            checked(["orca", "worktree", "rm", "--worktree", f"id:{wt}", "--json"])
        except WorkerError as error:
            raise WorkerError(f"worktree rm refused: {error}") from error
        for lane in self.lanes_on(wt):
            (self.state_dir / f"{lane['name']}.json").unlink()


def until_argv(value: str) -> list[str]:
    try:
        args = json.loads(value)
    except ValueError:
        raise argparse.ArgumentTypeError("--until must be a nonempty JSON argv array") from None
    if not isinstance(args, list) or not args or not all(isinstance(arg, str) for arg in args) or not args[0]:
        raise argparse.ArgumentTypeError("--until must be a nonempty JSON argv array with a nonempty executable")
    return args


def parser() -> argparse.ArgumentParser:
    cli = argparse.ArgumentParser(prog="run.py delegate orca-worker", description=__doc__)
    commands = cli.add_subparsers(dest="command", required=True)
    for name in ("line", "start"):
        sub = commands.add_parser(name)
        for flag in ("cli", "model"):
            sub.add_argument(f"--{flag}", required=True)
        sub.add_argument("--effort", default="")
        if name == "start":
            for flag in ("lane", "role", "brief"):
                sub.add_argument(f"--{flag}", required=True)
            for flag in ("agent", "plan", "unit", "base", "join"):
                sub.add_argument(f"--{flag}", default="")
    for name in ("wait", "read", "keys", "tell", "check", "grade", "stop"):
        sub = commands.add_parser(name)
        sub.add_argument("lane")
        if name == "wait":
            sub.add_argument("--until", type=until_argv)
            sub.add_argument("--max", type=int, default=3600)
            sub.add_argument("--stall", type=int, default=1200)
        elif name == "read":
            sub.add_argument("--lines", type=int, default=200)
        elif name == "keys":
            sub.add_argument("text")
        elif name == "tell":
            sub.add_argument("file")
        elif name == "grade":
            sub.add_argument("--outcome", required=True)
            sub.add_argument("--verify", required=True)
            sub.add_argument("--note", default="")
        elif name == "stop":
            sub.add_argument("--stalled", action="store_true")
            sub.add_argument("--keep-worktree", action="store_true")
    commands.add_parser("status")
    return cli


def main(argv: list[str]) -> int:
    try:
        args = parser().parse_args(argv)
        if args.command == "line":
            print(launch_line(args.cli, args.model, args.effort))
            return 0
        if args.command == "start" and windows():
            raise WorkerError("Windows terminal launches are unsupported")
        if args.command == "wait" and (args.max <= 0 or args.stall < 0):
            raise WorkerError("--max must be a positive integer and --stall a non-negative integer")
        worker = Worker()
        if args.command in {"start", "wait", "stop"}:
            getattr(worker, args.command)(args)
        elif args.command == "status":
            files = sorted(worker.state_dir.glob("*.json"))
            if not files:
                print("no live lanes")
            for path in files:
                state = worker.state(path.stem)
                if state.get("kept"):
                    status = "kept"
                elif not state.get("terminal"):
                    status = "unavailable-terminal"
                else:
                    try:
                        status = "working" if working(screen(state["terminal"])) else "idle"
                    except WorkerError:
                        status = "gone"
                print(f"{path.stem} {state['cli']} {status} {state['path']}")
        else:
            state = worker.state(args.lane)
            if args.command == "check":
                worker.check(state)
            elif args.command == "grade":
                if not state.get("run"):
                    raise WorkerError(f"lane {args.lane} has no run")
                if args.outcome in {"accepted", "amended"}:
                    worker.check(state)
                grade = ["grade", "--run", state["run"], "--outcome", args.outcome, "--verify", args.verify]
                if args.note:
                    grade += ["--note", args.note]
                worker.log(*grade)
            elif args.command == "tell":
                worker.deliver(state["terminal"], state["path"], args.file, ".orca-note.md")
            elif args.command == "read":
                print(checked(["orca", "terminal", "read", "--terminal", state["terminal"], "--screen", "--limit", str(args.lines)]))
            elif args.command == "keys":
                if len(args.text) > 200:
                    raise WorkerError("keys takes 200 characters at most; write the text to a file and use tell")
                checked(["orca", "terminal", "send", "--terminal", state["terminal"], "--text", args.text, "--json"])
        return 0
    except SystemExit as error:
        return int(error.code)
    except (WorkerError, OSError, ValueError, KeyError, TypeError, AttributeError) as error:
        print(f"orca-worker: {str(error).removeprefix('orca-worker: ')}", file=sys.stderr)
        return 1
