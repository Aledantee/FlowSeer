"""Judge the paths a file edit names against repository policy.

The PreToolUse hook of every edit tool. Usage:

    run.py hooks pre-tool-policy

Reads the event on standard input and prints a `deny` decision for generated
output, `buf.lock`, and stray files under `spec/proto/`, and an `ask`
decision for a policy surface. It prints nothing when the edit is allowed, or
when the session is outside a repository. A call that names no path, a path
that cannot be resolved safely, malformed input, and an unexpected error all
deny, since a guard that cannot decide must not let the edit through.
"""

from lib import hookio

MALFORMED = "The edit hook received malformed input and failed closed."
FAILED = "The edit hook failed closed on an unexpected error."
NO_PATH = (
    "The edit hook could not determine the target path. Use a supported file edit whose "
    "destination can be checked against repository policy."
)

PROTO_FIXTURE_DIRECTORIES = ("/_test_fixtures/", "/fixtures/", "/testdata/")

# Policy surfaces stay editable, but the person running the session approves
# each edit. test/conformance/a11y holds the axe rules the audit may disable,
# the web analogue of .golangci.yml. tools/scripts/verify/ is the gate an
# agent's own work must pass.
POLICY_FILES = frozenset(
    {
        "AGENTS.md",
        "CLAUDE.md",
        "buf.yaml",
        ".golangci.yml",
        ".claude/settings.json",
        ".codex/hooks.json",
        "tools/scripts/run.py",
    }
)
POLICY_DIRECTORIES = (
    "test/conformance/a11y/",
    "tools/scripts/lib/",
    "tools/scripts/verify/",
    "tools/scripts/hooks/",
)


def verdict(relative: str) -> str | None:
    """Return the reason to deny a path relative to a checkout, or None."""
    if relative.startswith(("generated/", "frontend/web/generated/")):
        return (
            f"{relative} is generated output. Change spec/proto or the generator configuration, "
            "then run 'buf generate'."
        )
    if relative == "buf.lock":
        return "buf.lock is managed by buf. Change dependencies in buf.yaml, then run 'buf dep update'."
    if relative.startswith("spec/proto/"):
        if any(directory in f"/{relative}/" for directory in PROTO_FIXTURE_DIRECTORIES):
            return (
                f"{relative} is a test artifact inside the Buf source tree. "
                "Put schema tests and fixtures under test/conformance/proto instead."
            )
        if relative.rpartition("/")[2] != "README.md" and not relative.endswith(".proto"):
            return (
                f"{relative} is not a production schema or package README. spec/proto is source-only; "
                "put executable tests under test/conformance/proto."
            )
    return None


def is_policy_surface(relative: str) -> bool:
    return relative in POLICY_FILES or relative.startswith(POLICY_DIRECTORIES)


def decide(event: hookio.Event) -> None:
    """Print the decision for an event, or nothing when the edit is allowed."""
    paths = hookio.tool_paths(event.data)
    if not paths:
        hookio.deny(NO_PATH)
        return
    surfaces = []
    for path in paths:
        resolved = hookio.relative_path(event, path)
        if resolved.kind is hookio.Kind.UNSAFE:
            hookio.deny(
                f"{path} cannot be resolved safely inside the repository. "
                "Use a normalized repository-relative path."
            )
            return
        if resolved.kind is hookio.Kind.OUTSIDE:
            continue
        reason = verdict(resolved.path)
        if reason:
            hookio.deny(reason)
            return
        if is_policy_surface(resolved.path):
            surfaces.append(resolved.path)
    if surfaces:
        hookio.ask(
            f"{', '.join(surfaces)} is a policy surface (AGENTS.md, Hard boundaries). "
            "Approve only if this edit was requested as a guardrail change; otherwise propose it separately."
        )


def main(argv: list[str]) -> int:
    try:
        event = hookio.read_event()
        if event is not None:
            decide(event)
    except hookio.MalformedEvent:
        hookio.deny(MALFORMED)
    except Exception:
        hookio.deny(FAILED)
    return 0
