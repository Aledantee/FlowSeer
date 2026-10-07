import unittest

from tests.hooks.support import HookCase, across

MUTATORS = (
    "rm mv cp touch truncate tee dd install chmod chown rsync patch apply_patch nano vim vi ex ed emacs".split()
)

# One row per alternative of each pattern in the guard, with the decision the
# shell script gives. A row whose command the guard reads crudely says so: the
# port keeps the decision, and a row that changes is a change of behavior.
TABLE = [
    # The command must name generated output or buf.lock at all.
    ("", "allow"),
    ("rm -rf build", "allow"),
    ("rm generated", "allow"),
    # The path must start a word: a letter, digit, underscore, dot, or hyphen in front hides it.
    ("rm generated/x", "deny"),
    ("rm ./generated/x", "deny"),
    ("rm 'generated/x'", "deny"),
    ("rm frontend/web/generated/x", "deny"),
    ("rm mygenerated/x", "allow"),
    ("rm my_generated/x", "allow"),
    ("rm 0generated/x", "allow"),
    ("rm .generated/x", "allow"),
    ("rm sub-generated/x", "allow"),
    ("rm buf.lock", "deny"),
    ("rm ./buf.lock", "deny"),
    ("rm xbuf.lock", "allow"),
    ("rm buf.lock.bak", "allow"),
    ("rm buf.lock-old", "allow"),
    ("rm buf.lock_old", "allow"),
    ("rm buf.lock0", "allow"),
    # Redirection and download targets.
    ("echo x > generated/y", "deny"),
    ("echo x >> generated/y", "deny"),
    ("echo x >generated/y", "deny"),
    ("echo x > 'generated/y'", "deny"),
    ('echo x > "generated/y"', "deny"),
    ("echo x > sub/dir/generated/y", "deny"),
    ("echo x > frontend/web/generated/y", "deny"),
    ("echo x > buf.lock", "deny"),
    ("curl -o buf.lock https://example.invalid/buf.lock", "deny"),
    ("curl -o generated/y https://example.invalid/y", "deny"),
    ("curl -O generated/y", "deny"),
    ("curl -o  buf.lock https://example.invalid/buf.lock", "deny"),
    ("cat generated/y > out.txt", "allow"),
    ("cat < generated/y", "allow"),
    ("cat generated/y -o out.txt", "allow"),
    # apply_patch heredocs name their files on "*** ... File:" lines.
    ("apply_patch <<'P'\n*** Update File: generated/y\nP", "deny"),
    ("apply_patch <<'P'\n*** Add File: frontend/web/generated/y\nP", "deny"),
    ("apply_patch <<'P'\n*** Delete File: buf.lock\nP", "deny"),
    ("*** Update File: sub/generated/y", "deny"),
    ("apply_patch <<'P'\n*** Move to: generated/y\nP", "allow"),
    ("echo '*** Add File: generated/y'", "allow"),
    ("apply_patch <<'P'\n*** Update File: README.md\nP", "allow"),
    # Every pattern applies to one line. A path on the line after a patch header is not its operand.
    ("*** Update File: README.md\ngenerated/y", "allow"),
    ("cd spec\nrm ../generated/y", "deny"),
    ("echo ok\nprintf x > generated/y", "deny"),
    ("rm README.md\ncat generated/y", "allow"),
    # Verbs that mutate, and the wrappers that are looked through once.
    *[(f"{verb} generated/y", "deny") for verb in MUTATORS],
    ("/bin/rm generated/y", "deny"),
    ("/usr/bin/tee buf.lock", "deny"),
    *[(f"{wrapper} rm generated/y", "deny") for wrapper in "sudo time command nice nohup env exec".split()],
    ("sudo env rm generated/y", "allow"),
    ("rmdir generated/y", "allow"),
    ("mkdir generated/y", "allow"),
    ("ln -s a generated/y", "allow"),
    ("cat generated/y", "allow"),
    ("ls generated/go", "allow"),
    ("head -3 generated/y", "allow"),
    ("grep -rn Facet generated/go", "allow"),
    ("wc -l generated/y", "allow"),
    ("diff generated/a generated/b", "allow"),
    ("go test ./generated/...", "allow"),
    ("buf generate", "allow"),
    ("cat buf.lock", "allow"),
    # sed and perl write in place with a flag that holds an i.
    ("sed -i '' -e s/old/new/ generated/y", "deny"),
    ("sed -i.bak s/old/new/ generated/y", "deny"),
    ("sed -ni 1p generated/y", "deny"),
    ("sed -n -i 1p generated/y", "deny"),
    ("perl -pi -e s/a/b/ generated/y", "deny"),
    ("sed -n 1,5p generated/y", "allow"),
    ("sed -E s/a/b/ generated/y", "allow"),
    ("sed -e s/a/b/ generated/y", "allow"),
    ("perl -e print generated/y", "allow"),
    ("sed --in-place s/a/b/ generated/y", "allow"),
    # python and ruby: a script file writes, a -c one-liner reads.
    ("python3 fix.py generated/y", "deny"),
    ("python fix.py generated/y", "deny"),
    ("ruby fix.rb generated/y", "deny"),
    ("python3 -c pass generated/y", "allow"),
    ("python -c pass generated/y", "allow"),
    ("ruby -c pass generated/y", "allow"),
    # The split on parentheses cuts the open() call apart before the pattern for it can see one.
    ("python3 -c \"open('generated/y','w').write('')\"", "allow"),
    ("perl6 fix.p6 generated/y", "allow"),
    # git rewrites the tree with these subcommands, after at most one -C directory.
    *[
        (f"git {sub} generated/y", "deny")
        for sub in "apply am checkout restore clean rm mv".split()
    ],
    ("git -C . restore generated/go", "deny"),
    ("git -C sub checkout generated/go", "deny"),
    ("git diff -- generated/y", "allow"),
    ("git add spec/proto generated/go", "allow"),
    ("git status generated", "allow"),
    ("git log -- generated/y", "allow"),
    ("git commit -m regenerate generated/y", "allow"),
    ("git -c core.x=1 checkout generated/y", "allow"),
    ("git -C . -C sub checkout generated/y", "allow"),
    # find mutates with these actions as a word of their own.
    ("find generated/go -name '*.pb.go' -delete", "deny"),
    ("find generated/go -exec rm {} +", "deny"),
    ("find generated/go -execdir rm {} +", "deny"),
    ("find generated/go -name x", "allow"),
    ("find generated/go -deleted", "allow"),
    # The command is split into simple commands on ; & | ( and ).
    ("ls; rm generated/y", "deny"),
    ("true && rm generated/y", "deny"),
    ("false || rm generated/y", "deny"),
    ("(rm generated/y)", "deny"),
    ("echo a | tee generated/y", "deny"),
    ("cat x | tee buf.lock", "deny"),
    ("rm -rf docs/attic; cat generated/y", "allow"),
    ("grep -rn Facet generated/go | sed -E 's/x/y/'", "allow"),
    ("ls generated/go; head -3 generated/y", "allow"),
    ("git add spec/proto generated/go && git commit -m regenerate", "allow"),
]


class ProtectGeneratedBashTest(HookCase):
    @across("protect-generated-bash")
    def test_bash_denies_direct_generated_file_mutation(self, hook):
        """Bash denies direct generated-file mutation"""
        payload = self.shell("sed -i '' -e s/old/new/ generated/device.pb.go")
        self.assertEqual(hook.decide(payload), "deny")

    @across("protect-generated-bash")
    def test_bash_allows_reads_and_commits_that_only_name_generated_output(self, hook):
        """Bash allows reads and commits that only name generated output"""
        for command in (
            "sed -n 1,5p generated/device.pb.go",
            "grep -rn Facet generated/go | sed -E 's/x/y/'",
            "git add spec/proto generated/go && git commit -m regenerate",
            "go test ./generated/...",
            "ls generated/go; head -3 generated/device.pb.go",
            "rm -rf docs/attic; cat generated/device.pb.go",
            "cat buf.lock",
        ):
            with self.subTest(command=command):
                self.assertEqual(hook.decide(self.shell(command)), "allow")

    @across("protect-generated-bash")
    def test_bash_denies_every_mutating_verb_that_names_generated_output(self, hook):
        """Bash denies every mutating verb that names generated output"""
        for command in (
            "rm generated/device.pb.go",
            "cd spec && cp x.go ../generated/device.pb.go",
            "git -C . restore generated/go",
            "perl -pi -e s/a/b/ generated/device.pb.go",
            "python3 fix.py generated/device.pb.go",
            "curl -o buf.lock https://example.invalid/buf.lock",
            "echo x | tee buf.lock",
            "find generated/go -name '*.pb.go' -delete",
        ):
            with self.subTest(command=command):
                self.assertEqual(hook.decide(self.shell(command)), "deny")

    @across("protect-generated-bash")
    def test_bash_denies_redirection_into_generated_output(self, hook):
        """Bash denies redirection into generated output"""
        self.assertEqual(hook.decide(self.shell("printf x > generated/device.pb.go")), "deny")

    @across("protect-generated-bash")
    def test_bash_denies_patching_generated_output(self, hook):
        """Bash denies patching generated output"""
        payload = self.shell("apply_patch <<'PATCH'\n*** Update File: generated/device.pb.go\nPATCH")
        self.assertEqual(hook.decide(payload), "deny")

    @across("protect-generated-bash")
    def test_bash_allows_generated_file_reads(self, hook):
        """Bash allows generated-file reads"""
        self.assertEqual(hook.decide(self.shell("git diff -- generated/device.pb.go")), "allow")

    @across("protect-generated-bash")
    def test_bash_allows_buf_generate(self, hook):
        """Bash allows buf generate"""
        self.assertEqual(hook.decide(self.shell("buf generate")), "allow")

    @across("protect-generated-bash")
    def test_a_command_of_two_lines_is_judged_line_by_line(self, hook):
        """a command of two lines is judged line by line"""
        self.assertEqual(hook.decide(self.shell("echo ok\nrm generated/y")), "deny")
        self.assertEqual(hook.decide(self.shell("*** Update File: README.md\ngenerated/y")), "allow")

    @across("protect-generated-bash")
    def test_a_missing_or_empty_command_is_allowed_and_malformed_input_is_denied(self, hook):
        """an empty command is allowed and malformed input is denied"""
        for payload in ({"cwd": str(self.fixture), "tool_input": {}}, self.shell(""), {}):
            self.assertEqual(hook.decide(payload), "allow")
        result = hook.output("not json")["hookSpecificOutput"]
        self.assertEqual(result["permissionDecision"], "deny")
        self.assertIn("malformed hook input", result["permissionDecisionReason"])

    @across("protect-generated-bash")
    def test_every_alternative_of_each_pattern_has_the_decision_the_shell_gives(self, hook):
        """each row of the table gets the decision the shell script gives"""
        for command, decision in TABLE:
            with self.subTest(command=command):
                self.assertEqual(hook.decide(self.shell(command)), decision)


if __name__ == "__main__":
    unittest.main()
