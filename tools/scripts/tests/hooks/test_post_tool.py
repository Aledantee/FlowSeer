import json
import unittest

from tests.hooks.support import HookCase, across

BUF_STUB = """\
printf '%s\\n' "$*" >>"$BUF_LOG"
if [[ ${1:-} == lint && ${BUF_LINT_RC:-0} != 0 ]]; then
  echo "fixture lint failure" >&2
  exit "$BUF_LINT_RC"
fi
exit 0"""

SUPPRESSIONS = (
    "//nolint:errcheck",
    "// nolint",
    "// buf:lint:ignore RPC_REQUEST_STANDARD_NAME",
    "// buf:breaking:ignore FIELD_NO_DELETE",
    "# shellcheck disable=SC2086",
    "// #nosec G104",
    "# nosec",
    "/* eslint-disable no-console */",
    "// @ts-ignore",
    "// @ts-expect-error",
    "// lint:ignore SA1019 reason",
    "x = 1  # noqa: E501",
    "exclude-rules:",
    "skip-dirs:",
)


class ProtoCheckTest(HookCase):
    def fresh(self):
        super().fresh()
        self.proto = self.fixture / "spec/proto/widget.proto"
        self.write(self.proto, 'syntax = "proto3";\nmessage WidgetConfig {}\n')
        self.stubs = self.parent / "stubs"
        self.buf_log = self.parent / "buf.log"
        self.stub(self.stubs, "buf", BUF_STUB)
        self.buf_env = self.env_with(PATH=self.path_with(self.stubs), BUF_LOG=str(self.buf_log))

    @across("proto-check")
    def test_proto_hook_reports_partial_families_the_file_does_not_explain(self, hook):
        """proto hook reports partial families the file does not explain"""
        context = hook.context(self.edit(self.proto), env=self.buf_env)
        self.assertIn("WidgetState", context)
        self.assertIn("WidgetEvent", context)

    @across("proto-check")
    def test_proto_hook_stays_silent_about_members_the_file_level_comment_names(self, hook):
        """proto hook stays silent about members the file-level comment names"""
        self.proto.write_text(
            'syntax = "proto3";\n\n// The widget family is deliberately partial: nothing observes a widget,\n'
            "// so there is no WidgetState and no WidgetEvent.\nmessage WidgetConfig {}\n"
        )
        hook.silent(self.edit(self.proto), env=self.buf_env)
        self.proto.write_text(
            'syntax = "proto3";\n\n// Nothing observes a widget, so there is no WidgetState.\nmessage WidgetConfig {}\n'
        )
        context = hook.context(self.edit(self.proto), env=self.buf_env)
        self.assertIn("WidgetEvent", context)
        self.assertNotIn("WidgetState", context)

    @across("proto-check")
    def test_proto_hook_reports_missing_buf(self, hook):
        """proto hook reports missing buf"""
        path = self.bare_path("no-buf")
        context = hook.context(self.edit(self.proto), env=self.env_with(PATH=path))
        self.assertIn("buf is not on PATH", context)

    @across("proto-check")
    def test_proto_hook_blocks_failing_lint(self, hook):
        """proto hook blocks failing lint"""
        result = hook.run(self.edit(self.proto), env={**self.buf_env, "BUF_LINT_RC": "9"})
        self.assertEqual(result.code, 2)
        self.assertIn("fixture lint failure", result.stderr)

    @across("proto-check")
    def test_a_proto_file_at_any_depth_under_spec_proto_is_formatted_and_linted(self, hook):
        """proto-check formats and lints a schema at any depth, where a glob would stop at one level"""
        deep = self.write(self.fixture / "spec/proto/a/b/c/deep.proto", 'syntax = "proto3";\n')
        hook.silent(self.edit(deep), env=self.buf_env)
        calls = self.buf_log.read_text().splitlines()
        self.assertEqual(
            calls,
            [f"format -w {deep}", "lint --path spec/proto/a/b/c/deep.proto"],
        )

    @across("proto-check")
    def test_proto_check_leaves_other_files_and_other_trees_alone(self, hook):
        """proto-check ignores files that are not schemas under spec/proto"""
        for path in ("spec/proto/notes.md", "elsewhere/x.proto", "main.go"):
            with self.subTest(path=path):
                hook.silent(self.edit(self.write(self.fixture / path)), env=self.buf_env)
        self.assertFalse(self.buf_log.exists())

    @across("proto-check")
    def test_proto_check_names_every_lint_failure(self, hook):
        """proto-check reports each failing schema with the context that goes with it"""
        other = self.write(self.fixture / "spec/proto/other.proto", 'syntax = "proto3";\nmessage OtherConfig {}\n')
        patch = self.patch(
            "*** Update File: spec/proto/widget.proto",
            "*** Update File: spec/proto/other.proto",
        )
        result = hook.run(patch, env={**self.buf_env, "BUF_LINT_RC": "9"})
        self.assertEqual(result.code, 2)
        self.assertIn("buf lint failed on spec/proto/widget.proto:", result.stderr)
        self.assertIn("buf lint failed on spec/proto/other.proto:", result.stderr)
        self.assertIn("Message sync:", result.stderr)
        self.assertTrue(other.is_file())

    @across("proto-check")
    def test_proto_check_is_silent_without_a_schema_tree_or_a_repository(self, hook):
        """proto-check exits 0 with no output without a spec/proto directory or a repository"""
        bare = self.make_repo("bare")
        self.write(bare / "x.proto")
        hook.silent(self.edit(bare / "x.proto", cwd=bare), env=self.buf_env)
        outside = self.parent / "outside"
        outside.mkdir()
        hook.silent(self.edit(outside / "spec/proto/x.proto", cwd=outside), env=self.buf_env)

    @across("proto-check")
    def test_the_family_check_covers_the_ref_pair(self, hook):
        """proto-check asks for the LocalRef that goes with a GlobalRef"""
        self.proto.write_text('syntax = "proto3";\nmessage WidgetGlobalRef {}\n')
        context = hook.context(self.edit(self.proto), env=self.buf_env)
        self.assertIn("- Widget: no message found for WidgetLocalRef", context)


class GoFormatTest(HookCase):
    def formatters(self, *, fail=None, rewrite=None):
        """A stub go that names a GOPATH whose bin holds formatters that record each call."""
        gopath = self.parent / "gopath"
        calls = self.parent / "format.calls"
        self.stub(gopath / "stub", "go", f'[[ ${{2:-}} == GOPATH ]] && printf "%s\\n" "{gopath}"\nexit 0')
        for name in ("gofumpt", "goimports"):
            body = f'printf "%s %s\\n" {name} "$*" >>"{calls}"'
            if fail == name:
                body += f'\necho "{name} exploded" >&2\nexit 1'
            if rewrite == name:
                body += '\nprintf "package main // formatted\\n" >"$2"'
            self.stub(gopath / "bin", name, body)
        return self.env_with(PATH=self.path_with(gopath / "stub")), calls

    @across("go-format")
    def test_go_format_resolves_the_formatters_from_gopath_bin_when_path_lacks_it(self, hook):
        """go-format resolves the formatters from GOPATH/bin when PATH lacks it"""
        self.write(self.fixture / "main.go", "package main\n")
        env, calls = self.formatters()
        output = hook.run(self.edit(self.fixture / "main.go"), env=env)
        self.assertEqual(output.code, 0, output.stderr)
        self.assertEqual(
            calls.read_text().splitlines(),
            [f"gofumpt -w {self.fixture}/main.go", f"goimports -w {self.fixture}/main.go"],
        )
        self.assertNotIn("not on PATH", output.stdout)

    @across("go-format")
    def test_go_format_formats_go_files_inside_the_checkout_only(self, hook):
        """go-format skips generated output, other file types, and files that are gone"""
        env, calls = self.formatters()
        for path in ("generated/device.pb.go", "frontend/web/generated/x.go", "README.md", "gone.go"):
            with self.subTest(path=path):
                if path != "gone.go":
                    self.write(self.fixture / path)
                hook.silent(self.edit(self.fixture / path), env=env)
        self.assertFalse(calls.exists())
        self.write(self.fixture / "pkg/dir/a.go", "package a\n")
        hook.silent(self.edit("pkg/dir/a.go"), env=env)
        self.assertEqual(len(calls.read_text().splitlines()), 2)

    @across("go-format")
    def test_go_format_names_the_files_the_formatters_changed(self, hook):
        """go-format tells the agent which files it rewrote"""
        self.write(self.fixture / "a.go", "package main\n")
        self.write(self.fixture / "b.go", "package b\n")
        env, _ = self.formatters(rewrite="gofumpt")
        context = hook.context(
            self.patch("*** Update File: a.go", "*** Update File: b.go", "*** Update File: README.md"), env=env
        )
        self.assertEqual(
            context,
            "gofumpt + goimports reformatted a.go, b.go. Re-read those files before editing the same regions.",
        )

    @across("go-format")
    def test_go_format_exits_2_with_the_tool_output_when_a_formatter_fails(self, hook):
        """go-format blocks with the formatter's own output when it fails"""
        self.write(self.fixture / "a.go", "package main\n")
        for failing in ("gofumpt", "goimports"):
            with self.subTest(formatter=failing):
                env, _ = self.formatters(fail=failing)
                result = hook.run(self.edit("a.go"), env=env)
                self.assertEqual(result.code, 2)
                self.assertEqual(result.stderr, f"{failing} failed on a.go:\n{failing} exploded\n")

    @across("go-format")
    def test_go_format_reports_missing_formatters_by_name(self, hook):
        """go-format names the formatters that are not on PATH"""
        self.write(self.fixture / "a.go", "package main\n")
        context = hook.context(self.edit("a.go"), env=self.env_with(PATH=self.bare_path("bare-path")))
        self.assertTrue(context.startswith("gofumpt, goimports is not on PATH; edited Go files were not formatted."))

    @across("go-format")
    def test_go_format_is_silent_outside_a_repository(self, hook):
        """go-format exits 0 with no output outside a repository"""
        hook.silent(self.edit("a.go", cwd=self.parent), env=self.formatters()[0])


class SuppressionWarnTest(HookCase):
    @across("suppression-warn")
    def test_suppression_hook_reports_new_lint_suppressions_and_nothing_else(self, hook):
        """suppression hook reports new lint suppressions and nothing else"""
        path = self.fixture / "main.go"
        added = {
            "cwd": str(self.fixture),
            "tool_input": {
                "file_path": str(path),
                "old_string": "",
                "new_string": "func x() error { //nolint:errcheck\n\treturn nil\n}",
            },
        }
        self.assertIn("nolint", hook.context(added))
        plain = json.loads(json.dumps(added))
        plain["tool_input"]["new_string"] = "func x() error {\n\treturn nil\n}"
        self.assertEqual(hook.context(plain), "")

    @across("suppression-warn")
    def test_every_suppression_marker_is_reported(self, hook):
        """suppression-warn reports each marker the pattern names"""
        for marker in SUPPRESSIONS:
            with self.subTest(marker=marker):
                payload = self.edit("main.go")
                payload["tool_input"].update(new_string=f"line one\n{marker}\n")
                context = hook.context(payload)
                self.assertTrue(context.startswith("This edit to main.go adds a lint or check suppression:\n2:"))
                self.assertIn(marker, context)

    @across("suppression-warn")
    def test_suppressions_are_read_from_write_content_and_every_multi_edit(self, hook):
        """suppression-warn reads Write content and each MultiEdit new_string, and names at most five lines"""
        write = self.edit("main.go")
        write["tool_input"]["content"] = "ok\n// nolint\n"
        self.assertIn("2:// nolint", hook.context(write))
        multi = self.edit("main.go")
        multi["tool_input"]["edits"] = [{"new_string": "ok"}, {"new_string": "// nolint"}, {"old_string": "x"}]
        self.assertIn("2:// nolint", hook.context(multi))
        many = self.edit("main.go")
        many["tool_input"]["content"] = "\n".join(f"// nolint {n}" for n in range(8))
        self.assertEqual(hook.context(many).count("// nolint"), 5)

    @across("suppression-warn")
    def test_suppression_hook_names_the_notebook_or_the_edit_when_no_file_path_is_given(self, hook):
        """suppression-warn names the target by file path, then notebook path, then 'the edit'"""
        notebook = {"cwd": str(self.fixture), "tool_input": {"notebook_path": "n.ipynb", "new_string": "# noqa"}}
        self.assertTrue(hook.context(notebook).startswith("This edit to n.ipynb adds"))
        anonymous = {"cwd": str(self.fixture), "tool_input": {"new_string": "# noqa"}}
        self.assertTrue(hook.context(anonymous).startswith("This edit to the edit adds"))

    @across("suppression-warn")
    def test_suppression_hook_ignores_malformed_input_and_edits_without_text(self, hook):
        """suppression-warn exits 0 with no output for malformed input and for text-free edits"""
        hook.silent("not json")
        hook.silent(self.edit("main.go"))
        hook.silent({"tool_input": {"new_string": 7, "content": None}})


class CommitMessageCheckTest(HookCase):
    def fresh(self):
        super().fresh()
        self.repo = self.make_repo("commit-messages")

    def commit(self, *args, stdin=None):
        self.git(self.repo, "commit", "-q", "--allow-empty", *args, stdin=stdin)

    def called(self):
        return self.shell("git commit", cwd=self.repo)

    @across("commit-message-check")
    def test_commit_message_hook_reports_a_literal_backslash_n_paragraph_break_in_a_new_commit_once(self, hook):
        """commit message hook reports a literal backslash-n paragraph break in a new commit, once"""
        # The first call only records where the session started.
        self.commit("-m", r"an earlier commit\n\nnot written by this session")
        self.assertEqual(hook.context(self.called()), "")
        self.commit("-m", "subject", "-m", 'A body that quotes "\\n" and breaks its lines for real.')
        self.assertEqual(hook.context(self.called()), "")
        self.commit("-m", r"subject\n\nbody")
        self.assertIn("git commit --amend -F -", hook.context(self.called()))
        self.assertEqual(hook.context(self.called()), "")
        self.commit("--amend", "-F", "-", stdin="subject\n\nbody\n")
        self.assertEqual(hook.context(self.called()), "")

    @across("commit-message-check")
    def test_the_report_names_the_commit_by_its_short_hash(self, hook):
        """commit-message-check names the commit it found the literal break in"""
        self.commit("-m", "start")
        hook.silent(self.called())
        self.commit("-m", r"a\n\nb")
        short = self.git(self.repo, "rev-parse", "--short", "HEAD")
        self.assertTrue(hook.context(self.called()).startswith(f"The message of commit {short} holds a literal"))

    @across("commit-message-check")
    def test_commit_message_check_is_silent_without_a_repository_a_commit_or_a_cwd(self, hook):
        """commit-message-check exits 0 with no output without a repository, a commit, or a working directory"""
        hook.silent(self.shell("git commit", cwd=self.parent))
        hook.silent(self.shell("git commit", cwd=self.repo))
        hook.silent({"tool_input": {"command": "git commit"}})
        hook.silent("not json")


if __name__ == "__main__":
    unittest.main()
