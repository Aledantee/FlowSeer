import json
import re
import unittest
from concurrent.futures import ThreadPoolExecutor

from tests.hooks.support import (
    HOOKS_DIR,
    IDLE_BOUND,
    IDLE_TIMED_OUT,
    REPO,
    HookCase,
    decision_of,
    implementations,
    run_idle,
)

# Every hook but the ones with a case of their own below. Each ends the call quietly
# when it learns nothing from standard input.
QUIET_HOOKS = (
    "worktree-guard",
    "go-format",
    "proto-check",
    "suppression-warn",
    "mark-verification-dirty",
    "commit-message-check",
)
BARE_READ = re.compile(r"^[^#]*(\$\(cat( -)?\)|\$\(<\s*/dev/stdin\))")


class IdleStdinTest(HookCase):
    """A standard input that stays open with nothing written must not hold a hook, and the tool call behind it, for good.

    Each hook gets IDLE_BOUND seconds. The hooks run side by side because each
    one waits five seconds for its first byte.
    """

    def idle_all(self, names, cwd=None, env=None):
        jobs = []
        for name in names:
            commands = implementations(name)
            self.assertTrue(commands, f"no implementation of the hook {name}")
            jobs += [(name, command) for command in commands]
        with ThreadPoolExecutor(max_workers=len(jobs)) as pool:
            futures = [
                (name, command, pool.submit(run_idle, command, cwd=cwd or self.fixture, env=env or self.env))
                for name, command in jobs
            ]
            return [(name, " ".join(command), future.result()) for name, command, future in futures]

    def test_guards_deny_a_stdin_that_stays_open_and_silent(self):
        """guards deny a stdin that stays open and silent"""
        for name, command, result in self.idle_all(["pre-tool-policy", "protect-generated-bash"]):
            with self.subTest(hook=command):
                self.assertNotEqual(result.code, IDLE_TIMED_OUT, f"{name} outlived {IDLE_BOUND} seconds")
                self.assertEqual(result.code, 0)
                self.assertEqual(decision_of(result), "deny")

    def test_worktree_create_fails_on_a_stdin_that_stays_open_and_silent(self):
        """WorktreeCreate fails on a stdin that stays open and silent"""
        for name, command, result in self.idle_all(["create-worktree"]):
            with self.subTest(hook=command):
                self.assertEqual(result.code, 1, f"{name} exited {result.code}")

    def test_stop_skips_its_gates_on_a_stdin_that_stays_open_and_silent(self):
        """Stop skips its gates on a stdin that stays open and silent"""
        # Each part of the fixture keeps the case from passing vacuously: outside a
        # repository Stop prints {} whatever its input, and without a go.mod and a
        # gate directory a hook that ran its gates would block without calling go.
        repo = self.make_repo("idle-stop")
        self.write(repo / "go.mod", "module example.invalid/idle\n\ngo 1.27\n")
        (repo / "test/conformance/panic").mkdir(parents=True)
        called = self.parent / "idle-go-called"
        stubs = self.parent / "idle-go-bin"
        self.stub(stubs, "go", f': >"{called}"\nexit 1')
        env = self.env_with(PATH=self.path_with(stubs))
        for name, command, result in self.idle_all(["stop-check"], cwd=repo, env=env):
            with self.subTest(hook=command):
                self.assertNotEqual(result.code, IDLE_TIMED_OUT, f"{name} outlived {IDLE_BOUND} seconds")
                self.assertEqual(result.code, 0)
                self.assertEqual(json.loads(result.stdout), {})
                self.assertFalse(called.exists())

    def test_every_other_hook_ends_quietly_on_a_stdin_that_stays_open_and_silent(self):
        """the remaining hooks exit 0 with no output on a stdin that stays open and silent"""
        for name, command, result in self.idle_all(QUIET_HOOKS):
            with self.subTest(hook=command):
                self.assertNotEqual(result.code, IDLE_TIMED_OUT, f"{name} outlived {IDLE_BOUND} seconds")
                self.assertEqual((result.code, result.stdout), (0, ""))

    def test_no_hook_reads_stdin_through_a_bare_cat_or_dev_stdin(self):
        """no hook reads stdin through a bare cat or /dev/stdin"""
        found = []
        scripts = sorted(HOOKS_DIR.glob("*.sh"))
        for script in scripts:
            for number, line in enumerate(script.read_text().splitlines(), 1):
                if BARE_READ.match(line):
                    found.append(f"{script.name}:{number}: {line.strip()}")
        modules = sorted((REPO / "tools" / "scripts" / "hooks").glob("*.py"))
        for module in modules:
            if "sys.stdin" in module.read_text():
                found.append(f"{module.name} names sys.stdin")
        self.assertEqual(found, [])
        self.assertTrue(scripts or modules, "no hook source was searched")


if __name__ == "__main__":
    unittest.main()
