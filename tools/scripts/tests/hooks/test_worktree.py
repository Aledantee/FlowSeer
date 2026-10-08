import shutil
import unittest
from pathlib import Path

from tests.hooks.support import HookCase, across

EXTERNAL_ROOT = "external-worktrees"
NAME_LIMIT = 128

# Bash commands in the primary checkout of a repository on a protected branch,
# with the decision the shell script gives and, for a denial, the reason it
# names. @ROOT@ is the checkout, @OUT@ a directory beside it, and `~` is a
# directory outside it. The guard reads a command crudely, and a row that
# looks wrong says what the shell does: the port keeps the decision.
REDIRECT = "it redirects output into a file"
MODIFIES = "it modifies files on disk"
IN_PLACE = "it edits files in place"
REWRITES = "it rewrites the working tree or history"
REGENERATES = "it regenerates or rewrites tracked files"

BASH_TABLE = [
    # Commands that write nothing.
    ("ls", "allow", ""),
    ("git status", "allow", ""),
    ("git log --oneline | head -3", "allow", ""),
    ("cat README.md", "allow", ""),
    ("git add -A && git commit -m land", "allow", ""),
    ("git merge worktree-x", "allow", ""),
    ("git branch", "allow", ""),
    ("git worktree list", "allow", ""),
    ("go test ./...", "allow", ""),
    ("go build ./...", "allow", ""),
    ("go vet ./...", "allow", ""),
    ("buf lint", "allow", ""),
    ("npm test", "allow", ""),
    ("pnpm run build", "allow", ""),
    ("git -C . push", "allow", ""),
    # Redirections that do not write a file are scrubbed first.
    ("echo hi 2>&1", "allow", ""),
    ("ls 2>err.log", "allow", ""),
    ("ls 2> /dev/null", "allow", ""),
    ("ls > /dev/null", "allow", ""),
    ("ls 1>/dev/null", "allow", ""),
    ("ls >&2", "allow", ""),
    ("git log --format=<placeholder>", "allow", ""),
    ("git commit -m 'Co-Authored-By: A <a@example.invalid>'", "allow", ""),
    ("echo a -> b", "allow", ""),
    ("echo a=>b", "allow", ""),
    ("[ 2 >= 1 ]", "allow", ""),
    # Quoted spans are dropped only when the command holds no double quote and no escaped apostrophe.
    ("grep -h '^> Implemented' docs/*.md", "allow", ""),
    ("grep '^> x' docs/a.md > out.txt", "deny", REDIRECT),
    ('grep "^> x" docs/a.md', "deny", REDIRECT),
    ("echo \"it's\" > README.md; echo 'x'", "deny", REDIRECT),
    ("echo don\\'t > README.md; echo \\'x\\'", "deny", REDIRECT),
    # A redirection into a file.
    ("echo x > out.txt", "deny", REDIRECT),
    ("echo x >> out.txt", "deny", REDIRECT),
    ("cat a >out", "deny", REDIRECT),
    ("printf x > README.md", "deny", REDIRECT),
    ("echo a 2>&1 > out.txt", "deny", REDIRECT),
    # Verbs that change files, at the start of a command, after a separator, or after a space.
    *[
        (f"{verb} x", "deny", MODIFIES)
        for verb in "mv cp mkdir rmdir touch chmod chown ln patch tee dd truncate install".split()
    ],
    ("ls; touch x", "deny", MODIFIES),
    ("true && touch x", "deny", MODIFIES),
    ("false || touch x", "deny", MODIFIES),
    ("ls | tee x", "deny", MODIFIES),
    ("(touch x)", "deny", MODIFIES),
    ("echo touch", "deny", MODIFIES),
    ("echo touchy", "allow", ""),
    ("mytouch x", "allow", ""),
    ('git commit -m "rm"', "allow", ""),
    ("echo rm -rf", "deny", MODIFIES),
    # A word that only contains a verb is not one.
    ("grep -rn mkdirs docs", "allow", ""),
    ("ls cpx", "allow", ""),
    # In-place edits.
    ("sed -i s/a/b/ README.md", "deny", IN_PLACE),
    ("sed -n -i 1p README.md", "deny", IN_PLACE),
    ("sed -e s/a/b/ -i README.md", "deny", IN_PLACE),
    ("perl -i -pe s/a/b/ README.md", "deny", IN_PLACE),
    ("ruby -i x", "deny", IN_PLACE),
    ("sed -i.bak s/a/b/ README.md", "deny", IN_PLACE),
    # The flag is looked for as the two characters -i, so a cluster that holds an i is not found.
    ("perl -pi -e s/a/b/ README.md", "allow", ""),
    ("sed -ni 1p README.md", "allow", ""),
    ("sed s/a/b/ README.md", "allow", ""),
    ("sed -n 1p README.md | head -1", "allow", ""),
    ("sed -n 1p README.md; echo -i", "allow", ""),
    ("awk -i x", "allow", ""),
    # History and working-tree rewrites by git.
    *[
        (f"git {sub} x", "deny", REWRITES)
        for sub in "rebase cherry-pick revert apply am push reset checkout switch restore stash clean".split()
    ],
    # These two hold a file verb of their own, which is found first.
    ("git rm x", "deny", MODIFIES),
    ("git mv x", "deny", MODIFIES),
    # Generators and package installs.
    ("go mod tidy", "deny", REGENERATES),
    ("go  mod tidy", "deny", REGENERATES),
    ("go generate ./...", "deny", REGENERATES),
    ("go get example.invalid/x", "deny", REGENERATES),
    ("buf generate", "deny", REGENERATES),
    ("npm install", "deny", MODIFIES),
    ("pnpm add x", "deny", REGENERATES),
    ("yarn remove x", "deny", REGENERATES),
    ("bun install", "deny", MODIFIES),
    ("npm run build", "allow", ""),
    # A command of two lines is read one line at a time.
    ("echo hi\ntouch x", "deny", MODIFIES),
    ("echo hi\necho there", "allow", ""),
    ("echo 'a\n> b'", "deny", REDIRECT),
    ("sed s/a/b/ README.md\necho -i", "allow", ""),
    ("echo hi\nrm README.md", "deny", MODIFIES),
    # rm of a file git does not track is allowed when the command is plain and every operand is inside.
    ("rm untracked.txt", "allow", ""),
    ("rm -f untracked.txt", "allow", ""),
    ("rm -- untracked.txt", "allow", ""),
    ("rm ./untracked.txt", "allow", ""),
    ("rm 'untracked.txt'", "allow", ""),
    ('rm "untracked.txt"', "allow", ""),
    ("rm -rf scratch", "allow", ""),
    ("rm scratch/one.txt scratch/two.txt", "allow", ""),
    ("rm @ROOT@/untracked.txt", "allow", ""),
    ("rm", "allow", ""),
    # A tracked operand, or a tracked file below a directory operand, denies it.
    ("rm README.md", "deny", MODIFIES),
    ("rm docs/kept.md", "deny", MODIFIES),
    ("rm -rf docs", "deny", MODIFIES),
    ("rm untracked.txt README.md", "deny", MODIFIES),
    ("rm -r .", "deny", MODIFIES),
    ("rm @ROOT@/README.md", "deny", MODIFIES),
    # A pattern is expanded in the session directory and judged by what it matches.
    ("rm *.tmp", "allow", ""),
    ("rm ?.tmp", "allow", ""),
    ("rm [ab].tmp", "allow", ""),
    ("rm scratch/*", "allow", ""),
    ("rm nomatch*.zzz", "allow", ""),
    ("rm docs/*", "deny", MODIFIES),
    ("rm *.md", "deny", MODIFIES),
    ("rm docs/k*", "deny", MODIFIES),
    # A shell metacharacter, or a relative operand that leaves the checkout, keeps the denial.
    ("rm untracked.txt; echo done", "deny", MODIFIES),
    ("rm untracked.txt && true", "deny", MODIFIES),
    ("echo y | rm untracked.txt", "deny", MODIFIES),
    ("rm $(echo untracked.txt)", "deny", MODIFIES),
    ("rm `echo untracked.txt`", "deny", MODIFIES),
    ("rm ../outside.txt", "deny", MODIFIES),
    # A write whose only absolute paths lie outside the checkout cannot clobber a session here.
    ("rm @OUT@/x", "allow", ""),
    ("touch @OUT@/x", "allow", ""),
    ('touch "@OUT@/x"', "allow", ""),
    ("touch '@OUT@/x'", "allow", ""),
    ("cp README.md @OUT@/copy", "allow", ""),
    ("cp @OUT@/a b", "allow", ""),
    ("cp @OUT@/a @ROOT@/b", "deny", MODIFIES),
    ("touch @ROOT@/new", "deny", MODIFIES),
    ("touch @OUT@/x @ROOT@/y", "deny", MODIFIES),
    ("rm @OUT@/x ../y", "allow", ""),
    ("echo x > @OUT@/f", "allow", ""),
    ("echo x >@OUT@/f", "allow", ""),
    ("echo x >> @OUT@/f", "allow", ""),
    ("echo x >>@OUT@/f", "deny", REDIRECT),
    ("echo x > @ROOT@/f", "deny", REDIRECT),
    ("echo x > ~/f", "allow", ""),
    ("sed -i s/a/b/ ~/.codex/config.toml", "allow", ""),
    ("touch ~/x", "allow", ""),
    ("touch ~x", "deny", MODIFIES),
    ("git -C @OUT@ checkout x", "allow", ""),
    ("git checkout @ROOT@/README.md", "deny", REWRITES),
]

# (tool, tool_input, decision) in the primary checkout.
TOOL_TABLE = [
    ("Write", {"file_path": "@ROOT@/README.md"}, "deny"),
    ("Write", {"file_path": "README.md"}, "deny"),
    ("Edit", {"file_path": "docs/new/dir/x.md"}, "deny"),
    ("Edit", {"file_path": "@ROOT@/docs/new/dir/x.md"}, "deny"),
    ("NotebookEdit", {"notebook_path": "@ROOT@/n.ipynb"}, "deny"),
    ("NotebookEdit", {"notebook_path": "n.ipynb"}, "deny"),
    ("Write", {"file_path": "@OUT@/x.md"}, "allow"),
    ("Write", {"file_path": "../outside.md"}, "allow"),
    ("Edit", {"file_path": "@OUT@/missing/dir/x.md"}, "allow"),
    ("Write", {}, "allow"),
    ("MultiEdit", {"file_path": "@ROOT@/README.md"}, "allow"),
    ("Read", {"file_path": "@ROOT@/README.md"}, "allow"),
    ("Task", {}, "allow"),
    ("", {}, "allow"),
]


class WorktreeCase(HookCase):
    def name_payload(self, name, cwd: Path | None = None):
        return {"cwd": str(cwd or self.fixture), "hook_event_name": "WorktreeCreate", "name": name}

    def external_root(self) -> Path:
        return self.parent / EXTERNAL_ROOT

    def root_env(self, root: Path | str | None = None):
        return self.env_with(FLOWSEER_WORKTREE_ROOT=str(self.external_root() if root is None else root))

    def worktrees(self, repo: Path | None = None) -> list[str]:
        listing = self.git(repo or self.fixture, "worktree", "list", "--porcelain")
        return [line.removeprefix("worktree ") for line in listing.splitlines() if line.startswith("worktree ")]


class CreateWorktreeTest(WorktreeCase):
    @across("create-worktree")
    def test_worktree_create_defaults_to_a_sibling_worktree_directory(self, hook):
        """WorktreeCreate defaults to a sibling worktree directory"""
        result = hook.run(self.name_payload("default-checkout"))
        expected = self.parent / "worktrees" / self.fixture.name / "default-checkout"
        self.assertEqual((result.code, result.stdout), (0, f"{expected}\n"), result.stderr)
        self.assertEqual(self.git(expected, "symbolic-ref", "--short", "HEAD"), "worktree-default-checkout")

    @across("create-worktree")
    def test_worktree_create_rejects_unsafe_names(self, hook):
        """WorktreeCreate rejects unsafe names"""
        result = hook.run(self.name_payload("../escape"), env=self.root_env())
        self.assertEqual(result.code, 1)
        self.assertEqual(result.stderr, "WorktreeCreate received an unsafe worktree name.\n")
        self.assertEqual(self.worktrees(), [str(self.fixture)])

    @across("create-worktree")
    def test_worktree_create_rejects_repository_local_roots(self, hook):
        """WorktreeCreate rejects repository-local roots"""
        result = hook.run(self.name_payload("inside-checkout"), env=self.root_env(self.fixture))
        self.assertEqual(result.code, 1)
        self.assertIn("refuses to place a worktree inside the repository or Git data directory", result.stderr)
        self.assertEqual(self.worktrees(), [str(self.fixture)])

    @across("create-worktree")
    def test_worktree_create_rejects_roots_inside_another_registered_checkout(self, hook):
        """WorktreeCreate rejects roots inside another registered checkout"""
        root = self.fixture / "nested-worktrees"
        result = hook.run(self.name_payload("inside-checkout"), env=self.root_env(root))
        self.assertNotEqual(result.code, 0)
        self.assertFalse(root.exists())

    @across("create-worktree")
    def test_worktree_create_rejects_symlinked_roots_without_creating_them(self, hook):
        """WorktreeCreate rejects symlinked roots without creating them"""
        alias = self.parent / "repository-alias"
        alias.symlink_to(self.fixture)
        result = hook.run(self.name_payload("inside-checkout"), env=self.root_env(alias / "nested-through-symlink"))
        self.assertNotEqual(result.code, 0)
        self.assertFalse((self.fixture / "nested-through-symlink").exists())

    @across("create-worktree")
    def test_a_root_inside_a_linked_checkout_is_rejected_too(self, hook):
        """WorktreeCreate rejects a root inside a linked worktree"""
        root = self.linked / "nested"
        result = hook.run(self.name_payload("inside-linked"), env=self.root_env(root))
        self.assertEqual(result.code, 1)
        self.assertIn("inside an existing checkout", result.stderr)
        self.assertFalse(root.exists())

    @across("create-worktree")
    def test_flowseer_worktree_root_names_the_directory_the_worktree_goes_under(self, hook):
        """FLOWSEER_WORKTREE_ROOT places the worktree under the named root"""
        result = hook.run(self.name_payload("external-checkout"), env=self.root_env())
        created = self.external_root() / "external-checkout"
        self.assertEqual((result.code, result.stdout), (0, f"{created}\n"), result.stderr)
        self.assertEqual(self.git(created, "symbolic-ref", "--short", "HEAD"), "worktree-external-checkout")
        self.assertIn(str(created), self.worktrees())

    @across("create-worktree")
    def test_an_empty_worktree_root_variable_means_the_default(self, hook):
        """an empty FLOWSEER_WORKTREE_ROOT is the same as an unset one"""
        result = hook.run(self.name_payload("empty-root"), env=self.root_env(""))
        expected = self.parent / "worktrees" / self.fixture.name / "empty-root"
        self.assertEqual((result.code, result.stdout), (0, f"{expected}\n"), result.stderr)

    @across("create-worktree")
    def test_a_worktree_root_must_be_absolute(self, hook):
        """WorktreeCreate requires an absolute worktree root"""
        result = hook.run(self.name_payload("relative-root"), env=self.root_env("relative/root"))
        self.assertEqual((result.code, result.stdout), (1, ""))
        self.assertEqual(result.stderr, "WorktreeCreate requires an absolute worktree root.\n")

    @across("create-worktree")
    def test_names_are_checked_against_the_safe_pattern_and_the_length_limit(self, hook):
        """WorktreeCreate accepts a safe name of up to 128 characters and rejects the rest"""
        for name in ("a" * (NAME_LIMIT + 1), "", ".hidden", "-flag", "a/b", "a b", "a\nb", "a$b"):
            with self.subTest(name=name):
                result = hook.run(self.name_payload(name), env=self.root_env())
                self.assertEqual((result.code, result.stdout), (1, ""))
                self.assertIn("name", result.stderr)
        for name in ("a" * NAME_LIMIT, "A1", "a.b_c-d"):
            with self.subTest(name=name):
                result = hook.run(self.name_payload(name), env=self.root_env())
                self.assertEqual(result.code, 0, result.stderr)

    @across("create-worktree")
    def test_a_name_that_cannot_form_a_branch_is_rejected(self, hook):
        """WorktreeCreate rejects a name that passes the pattern but cannot form a Git branch"""
        result = hook.run(self.name_payload("a..b"), env=self.root_env())
        self.assertEqual((result.code, result.stdout), (1, ""))
        self.assertTrue(
            result.stderr.endswith("WorktreeCreate received a name that cannot form a Git branch.\n"), result.stderr
        )

    @across("create-worktree")
    def test_cwd_and_name_must_be_non_empty_strings(self, hook):
        """WorktreeCreate requires string cwd and name fields"""
        payloads = [
            "",
            "not json",
            "null",
            {"name": "x"},
            {"cwd": str(self.fixture)},
            {"cwd": "", "name": "x"},
            {"cwd": str(self.fixture), "name": ""},
            {"cwd": 7, "name": "x"},
            {"cwd": str(self.fixture), "name": 7},
            {"cwd": str(self.fixture), "name": ["x"]},
        ]
        for payload in payloads:
            with self.subTest(payload=payload):
                result = hook.run(payload, env=self.root_env())
                self.assertEqual((result.code, result.stdout), (1, ""))
                self.assertEqual(result.stderr, "WorktreeCreate requires string cwd and name fields.\n")

    @across("create-worktree")
    def test_a_failing_git_rev_parse_ends_the_hook_with_gits_status(self, hook):
        """a working directory outside any repository ends WorktreeCreate with git's exit status and no output"""
        for cwd in (self.parent, self.parent / "missing"):
            with self.subTest(cwd=cwd):
                result = hook.run(self.name_payload("nowhere", cwd), env=self.root_env())
                self.assertEqual((result.code, result.stdout), (128, ""))
                self.assertFalse(self.external_root().exists())

    @across("create-worktree")
    def test_a_registered_worktree_whose_directory_is_gone_ends_the_hook(self, hook):
        """WorktreeCreate stops without output when a registered worktree's directory no longer exists"""
        shutil.rmtree(self.linked)
        result = hook.run(self.name_payload("after-loss"), env=self.root_env())
        self.assertEqual((result.code, result.stdout), (1, ""))
        self.assertFalse((self.external_root() / "after-loss").exists())
        self.assertEqual(self.git(self.fixture, "branch", "--list", "worktree-after-loss"), "")

    @across("create-worktree")
    def test_a_name_that_is_already_a_worktree_is_refused_without_output(self, hook):
        """WorktreeCreate refuses a name whose worktree already exists"""
        first = hook.run(self.name_payload("twice"), env=self.root_env())
        self.assertEqual(first.code, 0, first.stderr)
        second = hook.run(self.name_payload("twice"), env=self.root_env())
        self.assertEqual((second.code, second.stdout), (1, ""))
        self.assertEqual(second.stderr, "WorktreeCreate refuses to place a worktree inside an existing checkout.\n")

    @across("create-worktree")
    def test_a_worktree_made_from_a_linked_checkout_goes_beside_the_primary_one_and_branches_from_head(self, hook):
        """WorktreeCreate from a linked worktree uses the primary checkout's sibling directory and the linked HEAD"""
        self.git(self.linked, "commit", "-q", "--allow-empty", "-m", "linked work")
        result = hook.run(self.name_payload("from-linked", self.linked))
        expected = self.parent / "worktrees" / self.fixture.name / "from-linked"
        self.assertEqual((result.code, result.stdout), (0, f"{expected}\n"), result.stderr)
        self.assertEqual(self.git(expected, "rev-parse", "HEAD"), self.git(self.linked, "rev-parse", "HEAD"))

    @across("create-worktree")
    def test_git_chatter_goes_to_standard_error_and_the_path_is_the_only_output(self, hook):
        """WorktreeCreate prints only the target path on standard output"""
        result = hook.run(self.name_payload("quiet"), env=self.root_env())
        self.assertEqual(result.stdout, f"{self.external_root() / 'quiet'}\n")
        self.assertIn("Preparing worktree", result.stderr)


class GuardCase(WorktreeCase):
    def fresh(self):
        super().fresh()
        self.out = self.parent / "elsewhere"
        self.out.mkdir()

    def checkout(self) -> Path:
        """A primary checkout on main whose path holds no space, with tracked, untracked and ignored files."""
        repo = self.make_repo("checkout")
        self.write(repo / "README.md", "# fixture\n")
        self.write(repo / "docs" / "kept.md", "kept\n")
        self.git(repo, "add", "-A")
        self.git(repo, "commit", "-qm", "init")
        for path in ("untracked.txt", "a.tmp", "b.tmp", "scratch/one.txt", "scratch/two.txt", "docs/loose.txt"):
            self.write(repo / path, "x\n")
        return repo

    def fill(self, text: str, repo: Path) -> str:
        return text.replace("@ROOT@", str(repo)).replace("@OUT@", str(self.out))

    def bash(self, command: str, repo: Path):
        return self.guard("Bash", repo, command=self.fill(command, repo))


class WorktreeGuardTest(GuardCase):
    @across("worktree-guard")
    def test_worktree_guard_denies_primary_checkout_writes_allows_commits_and_worktree_edits(self, hook):
        """worktree guard denies primary-checkout writes, allows commits and worktree edits"""
        write = self.guard("Write", file_path=str(self.fixture / "README.md"))
        self.assertEqual(hook.decide(write), "deny")
        bash = {
            "printf x > README.md": "deny",
            "grep -h '^> Implemented' docs/*.md": "allow",
            "grep '^> x' docs/a.md > out.txt": "deny",
            "echo \"it's\" > README.md; echo 'x'": "deny",
            "echo don\\'t > README.md; echo \\'x\\'": "deny",
            "git add -A && git commit -m land": "allow",
        }
        for command, decision in bash.items():
            with self.subTest(command=command):
                self.assertEqual(hook.decide(self.guard("Bash", command=command)), decision)
        linked = self.guard("Write", self.linked, file_path=str(self.linked / "README.md"))
        self.assertEqual(hook.decide(linked), "allow")
        escape = self.guard("Write", self.linked, file_path=str(self.fixture / "README.md"))
        self.assertEqual(hook.decide(escape), "deny")
        git_dir = self.git(self.linked, "rev-parse", "--absolute-git-dir")
        ledger = self.guard("Write", self.linked, file_path=f"{git_dir}/flowseer-plan-status.json")
        self.assertEqual(hook.decide(ledger), "allow")
        parent_git = self.guard("Write", self.linked, file_path=str(self.fixture / ".git" / "config"))
        self.assertEqual(hook.decide(parent_git), "deny")
        start = hook.context(self.guard("", event="SessionStart"))
        self.assertIn("EnterWorktree", start)

    @across("worktree-guard")
    def test_every_alternative_of_each_pattern_has_the_decision_the_shell_gives(self, hook):
        """each row of the Bash table gets the decision and the reason the shell script gives"""
        repo = self.checkout()
        env = self.env_with(HOME=str(self.home))
        for command, decision, reason in BASH_TABLE:
            with self.subTest(command=command):
                payload = self.bash(command, repo)
                if decision == "allow":
                    self.assertEqual(hook.decide(payload, env=env), "allow")
                    continue
                denied = hook.output(payload, env=env)["hookSpecificOutput"]
                self.assertEqual(denied["permissionDecision"], "deny")
                self.assertIn(
                    f"because {reason}, and the session is in the primary checkout {repo}",
                    denied["permissionDecisionReason"],
                )

    @across("worktree-guard")
    def test_a_caret_negates_a_bracket_expression_as_bash_expands_it(self, hook):
        """`rm [^a].tmp` names b.tmp, which is tracked, so it is denied"""
        repo = self.checkout()
        self.git(repo, "add", "b.tmp")
        self.git(repo, "commit", "-qm", "track b")
        denied = hook.output(self.bash("rm [^a].tmp", repo), env=self.env_with(HOME=str(self.home)))["hookSpecificOutput"]
        self.assertEqual(denied["permissionDecision"], "deny")
        self.assertIn(
            f"because {MODIFIES}, and the session is in the primary checkout {repo}",
            denied["permissionDecisionReason"],
        )

    @across("worktree-guard")
    def test_every_file_tool_has_the_decision_the_shell_gives(self, hook):
        """each row of the file-tool table gets the decision the shell script gives"""
        repo = self.checkout()
        for tool, tool_input, decision in TOOL_TABLE:
            with self.subTest(tool=tool, tool_input=tool_input):
                filled = {key: self.fill(value, repo) for key, value in tool_input.items()}
                self.assertEqual(hook.decide(self.guard(tool, repo, **filled)), decision)

    @across("worktree-guard")
    def test_a_file_denial_names_the_target_and_the_checkout(self, hook):
        """a denied edit names the target as given, the checkout, and the branch"""
        repo = self.checkout()
        denied = hook.output(self.guard("Write", repo, file_path="README.md"))["hookSpecificOutput"]
        reason = denied["permissionDecisionReason"]
        self.assertTrue(
            reason.startswith(f"Refusing to modify README.md: this session is in the primary checkout {repo} on branch 'main'."),
            reason,
        )
        self.assertIn('EnterWorktree name:"fix-snmp-timeout"', reason)

    @across("worktree-guard")
    def test_a_path_through_a_directory_with_a_space_is_read_as_two_words(self, hook):
        """a command naming an absolute path through a directory with a space is judged outside the checkout"""
        command = f"touch {self.fixture}/x"
        self.assertEqual(hook.decide(self.guard("Bash", command=command)), "allow")
        self.assertEqual(hook.decide(self.guard("Bash", command="touch x")), "deny")

    @across("worktree-guard")
    def test_session_start_injects_the_worktree_instruction_on_a_protected_branch_only(self, hook):
        """SessionStart tells a session in the primary checkout to enter a worktree, and others nothing"""
        repo = self.checkout()
        context = hook.context(self.guard("", repo, event="SessionStart"))
        self.assertIn(f"This session started in the primary checkout {repo} on branch 'main', which is protected.", context)
        self.git(repo, "checkout", "-q", "-b", "feature")
        hook.silent(self.guard("", repo, event="SessionStart"))
        hook.silent(self.guard("", self.linked, event="SessionStart"))

    @across("worktree-guard")
    def test_only_session_start_and_pre_tool_use_are_judged(self, hook):
        """events other than SessionStart and PreToolUse are allowed whatever they carry"""
        repo = self.checkout()
        for event in ("PostToolUse", "Stop", "UserPromptSubmit", ""):
            with self.subTest(event=event):
                hook.silent(self.guard("Write", repo, event=event, file_path=str(repo / "README.md")))
        hook.silent({"cwd": str(repo), "tool_name": "Write", "tool_input": {"file_path": "README.md"}})

    @across("worktree-guard")
    def test_a_branch_that_is_not_protected_is_left_alone(self, hook):
        """a primary checkout on a branch outside the protected list is not guarded"""
        repo = self.checkout()
        self.git(repo, "checkout", "-q", "-b", "feature")
        hook.silent(self.bash("echo x > out.txt", repo))
        hook.silent(self.guard("Write", repo, file_path="README.md"))

    @across("worktree-guard")
    def test_each_default_protected_branch_is_guarded(self, hook):
        """main, master, develop, and trunk are protected by default"""
        repo = self.checkout()
        for branch in ("master", "develop", "trunk", "main"):
            with self.subTest(branch=branch):
                self.git(repo, "checkout", "-q", "-B", branch)
                self.assertEqual(hook.decide(self.bash("echo x > out.txt", repo)), "deny")

    @across("worktree-guard")
    def test_a_detached_head_is_protected(self, hook):
        """a detached HEAD in the primary checkout is guarded and named so"""
        repo = self.checkout()
        self.git(repo, "checkout", "-q", "--detach")
        denied = hook.output(self.bash("echo x > out.txt", repo))["hookSpecificOutput"]
        self.assertIn("on a detached HEAD", denied["permissionDecisionReason"])
        self.assertIn("a detached HEAD", hook.context(self.guard("", repo, event="SessionStart")))

    @across("worktree-guard")
    def test_claude_worktree_guard_branches_replaces_the_protected_list(self, hook):
        """CLAUDE_WORKTREE_GUARD_BRANCHES names the protected branches in place of the default list"""
        repo = self.checkout()
        env = self.env_with(CLAUDE_WORKTREE_GUARD_BRANCHES="release  hotfix")
        hook.silent(self.bash("echo x > out.txt", repo), env=env)
        for branch in ("release", "hotfix"):
            with self.subTest(branch=branch):
                self.git(repo, "checkout", "-q", "-b", branch)
                self.assertEqual(hook.decide(self.bash("echo x > out.txt", repo), env=env), "deny")
        self.git(repo, "checkout", "-q", "main")
        empty = self.env_with(CLAUDE_WORKTREE_GUARD_BRANCHES="")
        self.assertEqual(hook.decide(self.bash("echo x > out.txt", repo), env=empty), "deny")

    @across("worktree-guard")
    def test_claude_worktree_guard_off_disables_the_guard(self, hook):
        """CLAUDE_WORKTREE_GUARD=off turns the guard off, and no other value does"""
        repo = self.checkout()
        off = self.env_with(CLAUDE_WORKTREE_GUARD="off")
        hook.silent(self.bash("echo x > out.txt", repo), env=off)
        hook.silent(self.guard("", repo, event="SessionStart"), env=off)
        for value in ("", "OFF", "0", "false", "on"):
            with self.subTest(value=value):
                env = self.env_with(CLAUDE_WORKTREE_GUARD=value)
                self.assertEqual(hook.decide(self.bash("echo x > out.txt", repo), env=env), "deny")

    @across("worktree-guard")
    def test_a_no_worktree_guard_file_disables_the_guard_for_that_repository(self, hook):
        """.claude/no-worktree-guard turns the guard off for the checkout that holds it"""
        repo = self.checkout()
        self.write(repo / ".claude" / "no-worktree-guard")
        hook.silent(self.bash("echo x > out.txt", repo))
        hook.silent(self.guard("Write", repo, file_path="README.md"))
        hook.silent(self.guard("", repo, event="SessionStart"))
        other = self.make_repo("other")
        self.assertEqual(hook.decide(self.bash("echo x > out.txt", other), env=self.env), "deny")

    @across("worktree-guard")
    def test_a_missing_directory_or_a_directory_outside_a_repository_is_allowed(self, hook):
        """the guard allows a call whose working directory is missing, absent, or outside any repository"""
        hook.silent(self.guard("Write", self.parent, file_path="x"))
        hook.silent(self.guard("Write", self.parent / "missing", file_path="x"))
        hook.silent({"hook_event_name": "PreToolUse", "tool_name": "Write", "tool_input": {"file_path": "x"}})
        hook.silent({"cwd": "", "hook_event_name": "PreToolUse"})
        hook.silent("")
        hook.silent("not json")

    @across("worktree-guard")
    def test_a_subdirectory_of_the_primary_checkout_is_the_primary_checkout(self, hook):
        """a working directory below the checkout root is guarded like the root"""
        repo = self.checkout()
        self.assertEqual(hook.decide(self.guard("Bash", repo / "docs", command="touch x")), "deny")
        self.assertEqual(hook.decide(self.guard("Write", repo / "docs", file_path="x.md")), "deny")
        self.assertEqual(hook.decide(self.guard("Bash", repo / "docs", command="rm loose.txt")), "allow")
        self.assertEqual(hook.decide(self.guard("Bash", repo / "docs", command="rm kept.md")), "deny")

    @across("worktree-guard")
    def test_a_linked_worktree_is_guarded_only_against_writes_into_its_parent(self, hook):
        """in a linked worktree only Edit, Write, and NotebookEdit aimed at the parent checkout are denied"""
        parent, linked = self.fixture, self.linked
        outside = self.out / "x.md"
        rows = [
            ("Write", {"file_path": "README.md"}, "allow"),
            ("Write", {"file_path": str(linked / "docs/new/x.md")}, "allow"),
            ("Write", {"file_path": str(parent / "README.md")}, "deny"),
            ("Edit", {"file_path": str(parent / "docs/new/x.md")}, "deny"),
            ("NotebookEdit", {"notebook_path": str(parent / "n.ipynb")}, "deny"),
            ("Write", {"file_path": "../repository with spaces/README.md"}, "deny"),
            ("Write", {"file_path": str(parent / ".git" / "config")}, "deny"),
            ("Write", {"file_path": str(parent / ".git" / "worktrees" / "stray")}, "deny"),
            ("Write", {"file_path": str(outside)}, "allow"),
            ("Write", {"file_path": "../../elsewhere/x.md"}, "allow"),
            ("Write", {}, "allow"),
            ("MultiEdit", {"file_path": str(parent / "README.md")}, "allow"),
            ("Bash", {"command": f"touch {parent}/x"}, "allow"),
            ("Read", {"file_path": str(parent / "README.md")}, "allow"),
        ]
        for tool, tool_input, decision in rows:
            with self.subTest(tool=tool, tool_input=tool_input):
                self.assertEqual(hook.decide(self.guard(tool, linked, **tool_input)), decision)
        git_dir = self.git(linked, "rev-parse", "--absolute-git-dir")
        for name in ("flowseer-plan-status.json", "flowseer-verification-receipt"):
            with self.subTest(name=name):
                self.assertEqual(hook.decide(self.guard("Write", linked, file_path=f"{git_dir}/{name}")), "allow")
        hook.silent(self.guard("Write", linked, event="PostToolUse", file_path=str(parent / "README.md")))
        hook.silent(self.guard("", linked, event="SessionStart"))

    @across("worktree-guard")
    def test_a_linked_worktree_denial_names_the_worktree_and_the_parent(self, hook):
        """the denial for a write into the parent names the worktree, the target, and the parent"""
        target = self.fixture / "README.md"
        denied = hook.output(self.guard("Write", self.linked, file_path=str(target)))["hookSpecificOutput"]
        self.assertTrue(
            denied["permissionDecisionReason"].startswith(
                f"This session is isolated in the worktree {self.linked}, but the edit target is {target} — inside the parent checkout {self.fixture}."
            ),
            denied["permissionDecisionReason"],
        )

    @across("worktree-guard")
    def test_the_guard_switches_apply_in_a_linked_worktree_too(self, hook):
        """the off switch and the opt-out file also silence the linked-worktree check"""
        payload = self.guard("Write", self.linked, file_path=str(self.fixture / "README.md"))
        hook.silent(payload, env=self.env_with(CLAUDE_WORKTREE_GUARD="off"))
        self.write(self.linked / ".claude" / "no-worktree-guard")
        hook.silent(payload)


if __name__ == "__main__":
    unittest.main()
