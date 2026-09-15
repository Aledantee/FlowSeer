#!/usr/bin/env bash

set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
# The hook resolves its target through pwd -P, so it answers with a physical
# path. TMPDIR is a symlink on macOS — /tmp is /private/tmp — so a fixture root
# taken straight from mktemp builds expectations that differ from the hook's
# answer by that prefix alone, and every path comparison below fails for a
# reason that has nothing to do with the hook. Resolve the roots once, here.
fixture_parent=$(cd "$(mktemp -d "${TMPDIR:-/tmp}/flowseer-hooks.XXXXXX")" && pwd -P)
fixture="$fixture_parent/repository with spaces"
project_dir_with_spaces="$fixture_parent/project root with spaces"
linked_worktree=$(cd "$(mktemp -d "${TMPDIR:-/tmp}/flowseer-hooks-worktree.XXXXXX")" && pwd -P)
external_worktree_root=$(cd "$(mktemp -d "${TMPDIR:-/tmp}/flowseer-external-worktrees.XXXXXX")" && pwd -P)
rmdir "$linked_worktree"
rmdir "$external_worktree_root"
trap 'rm -rf "$fixture_parent" "$linked_worktree" "$external_worktree_root"' EXIT

mkdir -p "$fixture"
git -C "$fixture" init -q
mkdir -p "$fixture/generated" "$fixture/spec/proto"
touch "$fixture/generated/device.pb.go"
printf '# hook fixture\n' >"$fixture/README.md"
git -C "$fixture" add README.md
git -C "$fixture" -c user.name=Hook -c user.email=hook@example.invalid commit -qm init
git -C "$fixture" worktree add -qb hook-test "$linked_worktree"
mkdir -p "$linked_worktree/generated"
ln -s "$repo_root" "$project_dir_with_spaces"

passed=0

ok() {
  passed=$((passed + 1))
  printf 'ok %d - %s\n' "$passed" "$1"
}

assert_deny() {
  local hook=$1
  local payload=$2
  local output
  output=$(printf '%s' "$payload" | "$hook") || {
    echo "hook failed: $hook" >&2
    exit 1
  }
  [[ $(decision <<<"$output") == deny ]]
}

assert_allow() {
  local hook=$1
  local payload=$2
  local output
  output=$(printf '%s' "$payload" | "$hook") || {
    echo "hook failed: $hook" >&2
    exit 1
  }
  [[ -z $output ]]
}

decision() {
  jq -r '.hookSpecificOutput.permissionDecision // "allow"'
}

assert_hook_mapping() {
  local config=$1
  local event=$2
  local matcher=$3
  shift 3
  local actual expected
  actual=$(jq -r --arg event "$event" --arg matcher "$matcher" '
    .hooks[$event][] |
    select(if $matcher == "<none>" then has("matcher") | not else .matcher == $matcher end) |
    .hooks[] | .command
  ' "$config")
  expected=$(printf '%s\n' "$@")
  if [[ $actual != "$expected" ]]; then
    echo "unexpected $event/$matcher commands in $config" >&2
    exit 1
  fi
}

assert_executable() {
  local relative_path=$1
  if [[ ! -x $repo_root/$relative_path ]]; then
    echo "configured hook is not executable: $relative_path" >&2
    exit 1
  fi
}

claude_config=$repo_root/.claude/settings.json
codex_config=$repo_root/.codex/hooks.json

jq -e '.hooks | keys | sort == ["PostToolUse", "PreToolUse", "SessionStart", "Stop", "WorktreeCreate"]' \
  "$claude_config" >/dev/null
jq -e '.hooks | keys | sort == ["PostToolUse", "PreToolUse", "SessionStart", "Stop"]' \
  "$codex_config" >/dev/null

claude_hook() { printf '"%s/tools/hooks/%s"' "\$CLAUDE_PROJECT_DIR" "$1"; }
codex_hook() { printf '"%s/tools/hooks/%s"' "\$(git rev-parse --show-toplevel)" "$1"; }

assert_hook_mapping "$claude_config" "SessionStart" "<none>" \
  "$(claude_hook worktree-guard.sh)"
assert_hook_mapping "$claude_config" "WorktreeCreate" "<none>" \
  "$(claude_hook create-worktree.sh)"
assert_hook_mapping "$claude_config" "PreToolUse" "Edit|Write|MultiEdit|NotebookEdit" \
  "$(claude_hook worktree-guard.sh)" \
  "$(claude_hook pre-tool-policy.sh)"
assert_hook_mapping "$claude_config" "PreToolUse" "Bash" \
  "$(claude_hook worktree-guard.sh)" \
  "$(claude_hook protect-generated-bash.sh)"
assert_hook_mapping "$claude_config" "PostToolUse" "Edit|Write|MultiEdit|NotebookEdit" \
  "$(claude_hook go-format.sh)" \
  "$(claude_hook proto-check.sh)" \
  "$(claude_hook suppression-warn.sh)" \
  "$(claude_hook mark-verification-dirty.sh)"
assert_hook_mapping "$claude_config" "PostToolUse" "Bash" \
  "$(claude_hook mark-verification-dirty.sh)"
assert_hook_mapping "$claude_config" "Stop" "<none>" \
  "$(claude_hook stop-check.sh)"

assert_hook_mapping "$codex_config" "SessionStart" "<none>" \
  "$(codex_hook worktree-guard.sh)"
assert_hook_mapping "$codex_config" "PreToolUse" "Edit|Write" \
  "$(codex_hook worktree-guard.sh)" \
  "$(codex_hook pre-tool-policy.sh)"
assert_hook_mapping "$codex_config" "PreToolUse" "Bash" \
  "$(codex_hook worktree-guard.sh)" \
  "$(codex_hook protect-generated-bash.sh)"
assert_hook_mapping "$codex_config" "PostToolUse" "Edit|Write" \
  "$(codex_hook go-format.sh)" \
  "$(codex_hook proto-check.sh)" \
  "$(codex_hook suppression-warn.sh)" \
  "$(codex_hook mark-verification-dirty.sh)"
assert_hook_mapping "$codex_config" "PostToolUse" "Bash" \
  "$(codex_hook mark-verification-dirty.sh)"
assert_hook_mapping "$codex_config" "Stop" "<none>" \
  "$(codex_hook stop-check.sh)"

for configured_hook in \
  tools/hooks/worktree-guard.sh \
  tools/hooks/create-worktree.sh \
  tools/hooks/pre-tool-policy.sh \
  tools/hooks/protect-generated-bash.sh \
  tools/hooks/go-format.sh \
  tools/hooks/proto-check.sh \
  tools/hooks/suppression-warn.sh \
  tools/hooks/mark-verification-dirty.sh \
  tools/hooks/stop-check.sh; do
  assert_executable "$configured_hook"
done
ok "settings map every event to executable hooks"

edit_input=$(jq -n --arg cwd "$fixture" --arg path "$fixture/generated/device.pb.go" \
  '{cwd:$cwd,tool_input:{file_path:$path}}')
assert_deny "$repo_root/tools/hooks/pre-tool-policy.sh" "$edit_input"
ok "Edit denies generated output"

claude_edit_command=$(jq -r '
  .hooks.PreToolUse[] |
  select(.matcher == "Edit|Write|MultiEdit|NotebookEdit") |
  .hooks[] | .command | select(contains("pre-tool-policy"))
' "$claude_config")
claude_edit_output=$(CLAUDE_PROJECT_DIR="$repo_root" bash -c "$claude_edit_command" <<<"$edit_input")
[[ $(decision <<<"$claude_edit_output") == deny ]]
codex_edit_command=$(jq -r '
  .hooks.PreToolUse[] |
  select(.matcher == "Edit|Write") |
  .hooks[] | .command | select(contains("pre-tool-policy"))
' "$codex_config")
codex_edit_output=$(cd "$repo_root" && bash -c "$codex_edit_command" <<<"$edit_input")
[[ $(decision <<<"$codex_edit_output") == deny ]]
ok "configured edit guards deny generated output"

new_dir_input=$(jq -n --arg cwd "$fixture" --arg path "$fixture/docs/new dir/deeper/notes.md" \
  '{cwd:$cwd,tool_input:{file_path:$path}}')
assert_allow "$repo_root/tools/hooks/pre-tool-policy.sh" "$new_dir_input"
new_dir_generated=$(jq -n --arg cwd "$fixture" --arg path "$fixture/generated/new/deeper/x.pb.go" \
  '{cwd:$cwd,tool_input:{file_path:$path}}')
assert_deny "$repo_root/tools/hooks/pre-tool-policy.sh" "$new_dir_generated"
ok "Edit resolves paths whose parent directories do not exist yet"

test_go_input=$(jq -n --arg cwd "$fixture" --arg file "spec/proto/rules_test.go" \
  '{cwd:$cwd,tool_input:{file_path:$file}}')
assert_deny "$repo_root/tools/hooks/pre-tool-policy.sh" "$test_go_input"
mixed_patch=$(jq -n --arg cwd "$fixture" --arg command $'*** Begin Patch\n*** Update File: spec/proto/flowseer/net/addr/v1/ip.proto\n*** Add File: spec/proto/layering_test.go\n*** End Patch' \
  '{cwd:$cwd,tool_input:{command:$command}}')
assert_deny "$repo_root/tools/hooks/pre-tool-policy.sh" "$mixed_patch"
schema_patch=$(jq -n --arg cwd "$fixture" --arg command $'*** Begin Patch\n*** Update File: spec/proto/flowseer/net/addr/v1/ip.proto\n*** End Patch' \
  '{cwd:$cwd,tool_input:{command:$command}}')
assert_allow "$repo_root/tools/hooks/pre-tool-policy.sh" "$schema_patch"
mkdir -p "$fixture/src/common"
traversal_patch=$(jq -n --arg cwd "$fixture/src/common" --arg command $'*** Begin Patch\n*** Add File: ../../spec/proto/bypass_test.go\n*** End Patch' \
  '{cwd:$cwd,tool_input:{command:$command}}')
assert_deny "$repo_root/tools/hooks/pre-tool-policy.sh" "$traversal_patch"
ok "Edit applies the spec/proto source-only rule to Claude paths and Codex patches"

for policy_path in AGENTS.md buf.yaml .claude/settings.json .codex/hooks.json tools/hooks/new-guard.sh; do
  policy_input=$(jq -n --arg cwd "$fixture" --arg path "$fixture/$policy_path" \
    '{cwd:$cwd,tool_input:{file_path:$path}}')
  policy_output=$(printf '%s' "$policy_input" | "$repo_root/tools/hooks/pre-tool-policy.sh")
  [[ $(decision <<<"$policy_output") == ask ]]
done
ok "Edit asks before touching a policy surface"

missing_input=$(jq -n --arg cwd "$fixture" --arg path "$fixture/generated/missing.pb.go" \
  '{cwd:$cwd,tool_input:{file_path:$path}}')
assert_deny "$repo_root/tools/hooks/pre-tool-policy.sh" "$missing_input"
ok "Edit denies a missing generated output path"

worktree_input=$(jq -n --arg cwd "$linked_worktree" --arg path "$linked_worktree/generated/device.pb.go" \
  '{cwd:$cwd,tool_input:{file_path:$path}}')
assert_deny "$repo_root/tools/hooks/pre-tool-policy.sh" "$worktree_input"
ok "Edit denies generated output in a linked worktree"

worktree_create_input=$(jq -n --arg cwd "$fixture" --arg name "external-checkout" \
  '{cwd:$cwd,hook_event_name:"WorktreeCreate",name:$name}')
worktree_create_command=$(jq -r '.hooks.WorktreeCreate[0].hooks[0].command' "$claude_config")
created_worktree=$(FLOWSEER_WORKTREE_ROOT="$external_worktree_root" \
  CLAUDE_PROJECT_DIR="$project_dir_with_spaces" \
  bash -c "$worktree_create_command" <<<"$worktree_create_input")
[[ $created_worktree == "$external_worktree_root/external-checkout" ]]
[[ $(git -C "$created_worktree" rev-parse --is-inside-work-tree) == true ]]
[[ $(git -C "$created_worktree" symbolic-ref --short HEAD) == worktree-external-checkout ]]
git -C "$fixture" worktree remove --force "$created_worktree"
ok "configured WorktreeCreate handles project and repository paths with spaces"

default_worktree_input=$(jq -n --arg cwd "$fixture" --arg name "default-checkout" \
  '{cwd:$cwd,hook_event_name:"WorktreeCreate",name:$name}')
default_worktree=$("$repo_root/tools/hooks/create-worktree.sh" <<<"$default_worktree_input")
expected_worktree="$(dirname "$fixture")/worktrees/$(basename "$fixture")/default-checkout"
[[ $default_worktree == "$expected_worktree" ]]
[[ $(git -C "$default_worktree" symbolic-ref --short HEAD) == worktree-default-checkout ]]
git -C "$fixture" worktree remove --force "$default_worktree"
ok "WorktreeCreate defaults to a sibling worktree directory"

unsafe_worktree_input=$(jq -n --arg cwd "$fixture" --arg name "../escape" \
  '{cwd:$cwd,hook_event_name:"WorktreeCreate",name:$name}')
if FLOWSEER_WORKTREE_ROOT="$external_worktree_root" \
  "$repo_root/tools/hooks/create-worktree.sh" <<<"$unsafe_worktree_input" >/dev/null 2>&1; then
  echo "unsafe worktree name was accepted" >&2
  exit 1
fi
ok "WorktreeCreate rejects unsafe names"

inside_worktree_input=$(jq -n --arg cwd "$fixture" --arg name "inside-checkout" \
  '{cwd:$cwd,hook_event_name:"WorktreeCreate",name:$name}')
if FLOWSEER_WORKTREE_ROOT="$fixture" \
  "$repo_root/tools/hooks/create-worktree.sh" <<<"$inside_worktree_input" >/dev/null 2>&1; then
  echo "repository-local worktree root was accepted" >&2
  exit 1
fi
ok "WorktreeCreate rejects repository-local roots"

cross_checkout_root=$fixture/nested-worktrees
if FLOWSEER_WORKTREE_ROOT="$cross_checkout_root" \
  "$repo_root/tools/hooks/create-worktree.sh" <<<"$inside_worktree_input" >/dev/null 2>&1; then
  echo "root inside another registered checkout was accepted" >&2
  exit 1
fi
[[ ! -e $cross_checkout_root ]]
ok "WorktreeCreate rejects roots inside another registered checkout"

symlinked_checkout=$fixture_parent/repository-alias
ln -s "$fixture" "$symlinked_checkout"
symlinked_root=$symlinked_checkout/nested-through-symlink
if FLOWSEER_WORKTREE_ROOT="$symlinked_root" \
  "$repo_root/tools/hooks/create-worktree.sh" <<<"$inside_worktree_input" >/dev/null 2>&1; then
  echo "symlinked root inside a registered checkout was accepted" >&2
  exit 1
fi
[[ ! -e $fixture/nested-through-symlink ]]
ok "WorktreeCreate rejects symlinked roots without creating them"

assert_deny "$repo_root/tools/hooks/pre-tool-policy.sh" '{malformed'
assert_deny "$repo_root/tools/hooks/protect-generated-bash.sh" '{malformed'
ok "generated guards fail closed on malformed JSON"

source_input=$(jq -n --arg cwd "$fixture" --arg path "$fixture/spec/proto/device.proto" \
  '{cwd:$cwd,tool_input:{file_path:$path}}')
assert_allow "$repo_root/tools/hooks/pre-tool-policy.sh" "$source_input"
ok "Edit allows protobuf source"

outside_input=$(jq -n --arg cwd "$fixture" --arg path "$(dirname "$fixture")/outside.txt" \
  '{cwd:$cwd,tool_input:{file_path:$path}}')
assert_allow "$repo_root/tools/hooks/pre-tool-policy.sh" "$outside_input"
ok "Edit allows absolute paths outside the repository"

dotfile_input=$(jq -n --arg cwd "$fixture" --arg path "$fixture/spec/proto/flowseer/api/v1/.gitkeep" \
  '{cwd:$cwd,tool_input:{file_path:$path}}')
mkdir -p "$fixture/spec/proto/flowseer/api/v1"
assert_allow "$repo_root/tools/hooks/pre-tool-policy.sh" "$dotfile_input"
ok "Edit allows dotfile placeholders under spec/proto"

stray_input=$(jq -n --arg cwd "$fixture" --arg path "$fixture/spec/proto/notes.txt" \
  '{cwd:$cwd,tool_input:{file_path:$path}}')
assert_deny "$repo_root/tools/hooks/pre-tool-policy.sh" "$stray_input"
ok "Edit denies stray non-schema files under spec/proto"

bash_edit=$(jq -n --arg cwd "$fixture" \
  --arg command "sed -i '' -e s/old/new/ generated/device.pb.go" \
  '{cwd:$cwd,tool_input:{command:$command}}')
assert_deny "$repo_root/tools/hooks/protect-generated-bash.sh" "$bash_edit"
ok "Bash denies direct generated-file mutation"

claude_bash_command=$(jq -r '
  .hooks.PreToolUse[] |
  select(.matcher == "Bash") |
  .hooks[] | .command | select(contains("protect-generated-bash"))
' "$claude_config")
claude_bash_output=$(CLAUDE_PROJECT_DIR="$repo_root" bash -c "$claude_bash_command" <<<"$bash_edit")
[[ $(decision <<<"$claude_bash_output") == deny ]]
ok "configured Bash guard denies generated-file mutation"

for read_command in \
  "sed -n 1,5p generated/device.pb.go" \
  "grep -rn Facet generated/go | sed -E 's/x/y/'" \
  "git add spec/proto generated/go && git commit -m regenerate" \
  "go test ./generated/..." \
  "ls generated/go; head -3 generated/device.pb.go" \
  "rm -rf docs/attic; cat generated/device.pb.go" \
  "cat buf.lock"; do
  bash_read_input=$(jq -n --arg cwd "$fixture" --arg command "$read_command" \
    '{cwd:$cwd,tool_input:{command:$command}}')
  assert_allow "$repo_root/tools/hooks/protect-generated-bash.sh" "$bash_read_input"
done
ok "Bash allows reads and commits that only name generated output"

for write_command in \
  "rm generated/device.pb.go" \
  "cd spec && cp x.go ../generated/device.pb.go" \
  "git -C . restore generated/go" \
  "perl -pi -e s/a/b/ generated/device.pb.go" \
  "python3 fix.py generated/device.pb.go" \
  "curl -o buf.lock https://example.invalid/buf.lock" \
  "echo x | tee buf.lock" \
  "find generated/go -name '*.pb.go' -delete"; do
  bash_write_input=$(jq -n --arg cwd "$fixture" --arg command "$write_command" \
    '{cwd:$cwd,tool_input:{command:$command}}')
  assert_deny "$repo_root/tools/hooks/protect-generated-bash.sh" "$bash_write_input"
done
ok "Bash denies every mutating verb that names generated output"

bash_redirect=$(jq -n --arg cwd "$fixture" \
  --arg command "printf x > generated/device.pb.go" \
  '{cwd:$cwd,tool_input:{command:$command}}')
assert_deny "$repo_root/tools/hooks/protect-generated-bash.sh" "$bash_redirect"
ok "Bash denies redirection into generated output"

bash_patch=$(jq -n --arg cwd "$fixture" \
  --arg command "apply_patch <<'PATCH'
*** Update File: generated/device.pb.go
PATCH" \
  '{cwd:$cwd,tool_input:{command:$command}}')
assert_deny "$repo_root/tools/hooks/protect-generated-bash.sh" "$bash_patch"
ok "Bash denies patching generated output"

bash_read=$(jq -n --arg cwd "$fixture" --arg command "git diff -- generated/device.pb.go" \
  '{cwd:$cwd,tool_input:{command:$command}}')
assert_allow "$repo_root/tools/hooks/protect-generated-bash.sh" "$bash_read"
ok "Bash allows generated-file reads"

bash_generate=$(jq -n --arg cwd "$fixture" --arg command "buf generate" \
  '{cwd:$cwd,tool_input:{command:$command}}')
assert_allow "$repo_root/tools/hooks/protect-generated-bash.sh" "$bash_generate"
ok "Bash allows buf generate"

proto=$fixture/spec/proto/widget.proto
printf 'syntax = "proto3";\nmessage WidgetConfig {}\n' >"$proto"
stub_bin=$fixture/stub-bin
mkdir -p "$stub_bin"
# These expressions belong to the generated fixture script, not this process.
# shellcheck disable=SC2016
printf '%s\n' '#!/usr/bin/env bash' \
  'if [[ ${1:-} == lint && ${BUF_LINT_RC:-0} != 0 ]]; then' \
  '  echo "fixture lint failure" >&2' \
  '  exit "$BUF_LINT_RC"' \
  'fi' \
  'exit 0' >"$stub_bin/buf"
chmod +x "$stub_bin/buf"
proto_input=$(jq -n --arg cwd "$fixture" --arg path "$proto" \
  '{cwd:$cwd,tool_input:{file_path:$path}}')
proto_output=$(PATH="$stub_bin:$PATH" "$repo_root/tools/hooks/proto-check.sh" <<<"$proto_input")
jq -e '.hookSpecificOutput.additionalContext | contains("WidgetState") and contains("WidgetEvent")' \
  <<<"$proto_output" >/dev/null
ok "proto hook reports partial families the file does not explain"

printf 'syntax = "proto3";\n\n// The widget family is deliberately partial: nothing observes a widget,\n// so there is no WidgetState and no WidgetEvent.\nmessage WidgetConfig {}\n' >"$proto"
proto_output=$(PATH="$stub_bin:$PATH" "$repo_root/tools/hooks/proto-check.sh" <<<"$proto_input")
[[ -z $proto_output ]]
printf 'syntax = "proto3";\n\n// Nothing observes a widget, so there is no WidgetState.\nmessage WidgetConfig {}\n' >"$proto"
proto_output=$(PATH="$stub_bin:$PATH" "$repo_root/tools/hooks/proto-check.sh" <<<"$proto_input")
jq -e '.hookSpecificOutput.additionalContext | contains("WidgetEvent") and (contains("WidgetState") | not)' \
  <<<"$proto_output" >/dev/null
printf 'syntax = "proto3";\nmessage WidgetConfig {}\n' >"$proto"
ok "proto hook stays silent about members the file-level comment names"

suppression_input=$(jq -n --arg cwd "$fixture" --arg path "$fixture/main.go" \
  --arg new $'func x() error { //nolint:errcheck\n\treturn nil\n}' \
  '{cwd:$cwd,tool_input:{file_path:$path,old_string:"",new_string:$new}}')
suppression_output=$("$repo_root/tools/hooks/suppression-warn.sh" <<<"$suppression_input")
jq -e '.hookSpecificOutput.additionalContext | contains("nolint")' <<<"$suppression_output" >/dev/null
plain_input=$(jq -n --arg cwd "$fixture" --arg path "$fixture/main.go" \
  --arg new $'func x() error {\n\treturn nil\n}' \
  '{cwd:$cwd,tool_input:{file_path:$path,old_string:"",new_string:$new}}')
[[ -z $("$repo_root/tools/hooks/suppression-warn.sh" <<<"$plain_input") ]]
ok "suppression hook reports new lint suppressions and nothing else"

guard_write=$(jq -n --arg cwd "$fixture" --arg path "$fixture/README.md" \
  '{cwd:$cwd,hook_event_name:"PreToolUse",tool_name:"Write",tool_input:{file_path:$path}}')
assert_deny "$repo_root/tools/hooks/worktree-guard.sh" "$guard_write"
guard_bash=$(jq -n --arg cwd "$fixture" --arg command "printf x > README.md" \
  '{cwd:$cwd,hook_event_name:"PreToolUse",tool_name:"Bash",tool_input:{command:$command}}')
assert_deny "$repo_root/tools/hooks/worktree-guard.sh" "$guard_bash"
guard_commit=$(jq -n --arg cwd "$fixture" --arg command "git add -A && git commit -m land" \
  '{cwd:$cwd,hook_event_name:"PreToolUse",tool_name:"Bash",tool_input:{command:$command}}')
assert_allow "$repo_root/tools/hooks/worktree-guard.sh" "$guard_commit"
guard_linked=$(jq -n --arg cwd "$linked_worktree" --arg path "$linked_worktree/README.md" \
  '{cwd:$cwd,hook_event_name:"PreToolUse",tool_name:"Write",tool_input:{file_path:$path}}')
assert_allow "$repo_root/tools/hooks/worktree-guard.sh" "$guard_linked"
guard_escape=$(jq -n --arg cwd "$linked_worktree" --arg path "$fixture/README.md" \
  '{cwd:$cwd,hook_event_name:"PreToolUse",tool_name:"Write",tool_input:{file_path:$path}}')
assert_deny "$repo_root/tools/hooks/worktree-guard.sh" "$guard_escape"
linked_git_dir=$(git -C "$linked_worktree" rev-parse --absolute-git-dir)
guard_ledger=$(jq -n --arg cwd "$linked_worktree" --arg path "$linked_git_dir/flowseer-plan-status.json" \
  '{cwd:$cwd,hook_event_name:"PreToolUse",tool_name:"Write",tool_input:{file_path:$path}}')
assert_allow "$repo_root/tools/hooks/worktree-guard.sh" "$guard_ledger"
guard_parent_git=$(jq -n --arg cwd "$linked_worktree" --arg path "$fixture/.git/config" \
  '{cwd:$cwd,hook_event_name:"PreToolUse",tool_name:"Write",tool_input:{file_path:$path}}')
assert_deny "$repo_root/tools/hooks/worktree-guard.sh" "$guard_parent_git"
guard_start=$(jq -n --arg cwd "$fixture" '{cwd:$cwd,hook_event_name:"SessionStart"}')
jq -e '.hookSpecificOutput.additionalContext | contains("EnterWorktree")' \
  <<<"$("$repo_root/tools/hooks/worktree-guard.sh" <<<"$guard_start")" >/dev/null
ok "worktree guard denies primary-checkout writes, allows commits and worktree edits"

no_buf=$fixture/no-buf
mkdir -p "$no_buf"
for command_name in bash cat jq git dirname basename grep awk sort sed rg; do
  ln -s "$(command -v "$command_name")" "$no_buf/$command_name"
done
proto_output=$(PATH="$no_buf" "$repo_root/tools/hooks/proto-check.sh" <<<"$proto_input")
jq -e '.hookSpecificOutput.additionalContext | contains("buf is not on PATH")' \
  <<<"$proto_output" >/dev/null
ok "proto hook reports missing buf"

set +e
lint_output=$(PATH="$stub_bin:$PATH" BUF_LINT_RC=9 \
  "$repo_root/tools/hooks/proto-check.sh" <<<"$proto_input" 2>&1)
lint_rc=$?
set -e
[[ $lint_rc -eq 2 && $lint_output == *"fixture lint failure"* ]]
ok "proto hook blocks failing lint"

touch "$fixture/main.go"
mark_input=$(jq -n --arg cwd "$fixture" --arg path "$fixture/main.go" \
  '{cwd:$cwd,tool_input:{file_path:$path}}')
assert_allow "$repo_root/tools/hooks/mark-verification-dirty.sh" "$mark_input"
grep -qx 'main.go' "$fixture/.git/flowseer-verification-dirty"
ok "edits mark the worktree dirty for the verifier"

rm -f "$fixture/.git/flowseer-verification-dirty"
bash_mark_input=$(jq -n --arg cwd "$fixture" --arg command "sed -i '' -e s/a/b/ main.go" \
  '{cwd:$cwd,tool_input:{command:$command}}')
assert_allow "$repo_root/tools/hooks/mark-verification-dirty.sh" "$bash_mark_input"
grep -qx '<Bash mutation; verify with --full>' "$fixture/.git/flowseer-verification-dirty"
ok "Bash source mutations mark a full-scope verification"

stop_input=$(jq -n --arg cwd "$fixture" '{cwd:$cwd,hook_event_name:"Stop"}')
stop_output=$("$repo_root/tools/hooks/stop-check.sh" <<<"$stop_input")
jq -e '.decision == null and (.systemMessage | contains("Edited but not verified"))' <<<"$stop_output" >/dev/null
rm -f "$fixture/.git/flowseer-verification-dirty"
stop_output=$("$repo_root/tools/hooks/stop-check.sh" <<<"$stop_input")
[[ $stop_output == '{}' ]]
ok "Stop reports unverified edits without blocking and passes a clean tree"

# A fixture with one gate package under test/conformance/. The fixture
# above has no such directory, so it proves only that Stop tolerates the
# directory's absence; this one proves the gate inside it runs.
gate_fixture="$fixture_parent/gate fixture"
mkdir -p "$gate_fixture/test/conformance/panic"
git -C "$gate_fixture" init -q
printf 'module example.invalid/gate\n\ngo 1.27\n' >"$gate_fixture/go.mod"
cat >"$gate_fixture/test/conformance/panic/panic_policy_test.go" <<'GATE'
package conformance

import "testing"

func TestPanicPolicy(t *testing.T) {
	t.Error("fixture violation")
}
GATE
gate_input=$(jq -n --arg cwd "$gate_fixture" '{cwd:$cwd,hook_event_name:"Stop"}')
gate_output=$("$repo_root/tools/hooks/stop-check.sh" <<<"$gate_input")
jq -e '.decision == "block" and (.reason | contains("fixture violation"))' <<<"$gate_output" >/dev/null
cat >"$gate_fixture/test/conformance/panic/panic_policy_test.go" <<'GATE'
package conformance

import "testing"

func TestPanicPolicy(t *testing.T) {}
GATE
gate_output=$("$repo_root/tools/hooks/stop-check.sh" <<<"$gate_input")
[[ $gate_output == '{}' ]]
ok "Stop blocks on a failing panic gate and passes when it holds"

# A gate package whose test is not called what the fixture above calls it.
# `go test -run` with a pattern that matches nothing exits 0, so a gate run
# by test name would pass here without running anything; the gate has to
# run the package.
cat >"$gate_fixture/test/conformance/panic/panic_policy_test.go" <<'GATE'
package conformance

import "testing"

func TestRenamedGate(t *testing.T) {
	t.Error("renamed fixture violation")
}
GATE
gate_output=$("$repo_root/tools/hooks/stop-check.sh" <<<"$gate_input")
jq -e '.decision == "block" and (.reason | contains("renamed fixture violation"))' <<<"$gate_output" >/dev/null
ok "Stop runs a gate package whatever its tests are named"

# A gate package whose directory is not called what the fixture above calls
# it. A hook that names its gate packages and skips an absent one would
# pass here with nothing run, the same silent skip the test above closes
# on the test-name axis; the hook has to run whatever test/conformance/
# holds.
mv "$gate_fixture/test/conformance/panic" "$gate_fixture/test/conformance/panics"
gate_output=$("$repo_root/tools/hooks/stop-check.sh" <<<"$gate_input")
jq -e '.decision == "block" and (.reason | contains("renamed fixture violation"))' <<<"$gate_output" >/dev/null
ok "Stop runs a gate package whatever its directory is named"

selection_fixture="$fixture_parent/selection fixture"
mkdir -p "$selection_fixture"
git -C "$selection_fixture" init -q
printf '# selection fixture\n' >"$selection_fixture/README.md"
printf 'module example.invalid/selection\n\ngo 1.27\n' >"$selection_fixture/go.mod"
git -C "$selection_fixture" add README.md go.mod
git -C "$selection_fixture" -c user.name=Hook -c user.email=hook@example.invalid commit -qm init

selection_script=$repo_root/.claude/skills/verify-change/scripts/verify-change.sh
selection_receipt=$selection_fixture/.git/flowseer-verification-receipt
selection_side_effect=$selection_fixture/docker-called
selection_go_side_effect=$selection_fixture/go-called
selection_bin=$selection_fixture/selection-bin
selection_tmp=$selection_fixture/selection-tmp
mkdir -p "$selection_bin" "$selection_tmp"
printf '#!/usr/bin/env bash\n: >%q\nexit 97\n' "$selection_side_effect" >"$selection_bin/docker"
printf '#!/usr/bin/env bash\n: >%q\nexit 98\n' "$selection_go_side_effect" >"$selection_bin/go"
chmod +x "$selection_bin/docker" "$selection_bin/go"

select_verifier() {
  (cd "$selection_fixture" && PATH="$selection_bin:$PATH" TMPDIR="$selection_tmp" \
    "$selection_script" --print-selection "$@")
}

selection_output=$(select_verifier --full)
[[ $selection_output == 'service_otel_integration=true' ]]
[[ ! -e $selection_receipt ]]
ok "verifier selection includes the Collector tier for full verification"

telemetry_paths=(
  go.mod
  src/common/service/telemetry_config.go
  src/common/service/test/integration/otel_test.go
  src/common/service/test/integration/testdata/otel-collector.yaml
  tools/test/service-otel-integration.sh
)
for telemetry_path in "${telemetry_paths[@]}"; do
  selection_output=$(select_verifier -- "$telemetry_path")
  [[ $selection_output == 'service_otel_integration=true' ]]
done
[[ ! -e $selection_receipt ]]
ok "verifier selection includes every telemetry-sensitive path category"

for unrelated_path in docs/notes.md src/common/errs/example.go 'docs/path with spaces.md'; do
  selection_output=$(select_verifier -- "$unrelated_path")
  [[ $selection_output == 'service_otel_integration=false' ]]
done
[[ ! -e $selection_receipt ]]
ok "verifier selection skips unrelated paths and preserves spaces"

selection_output=$(select_verifier --)
[[ $selection_output == 'service_otel_integration=false' ]]
[[ ! -e $selection_receipt ]]
ok "verifier selection handles an empty changed-path set without a receipt"

selection_output=$(select_verifier -- tools/hooks/tests/run.sh)
[[ $selection_output == 'service_otel_integration=false' ]]
[[ ! -e $selection_receipt ]]
[[ ! -e $selection_side_effect ]]
[[ ! -e $selection_go_side_effect ]]
selection_build_dirs=$(find "$selection_tmp" -maxdepth 1 -name 'flowseer-build.*' -print -quit)
[[ -z $selection_build_dirs ]]
ok "verifier selection exits before recursively running hook tests"

ledger_script=$repo_root/.claude/skills/verify-change/scripts/check-plan-status.py
ledger_fixture=$fixture_parent/ledger-fixture
mkdir -p "$ledger_fixture/docs/plans"
git -C "$ledger_fixture" init -q
printf '# ledger fixture plan\n' >"$ledger_fixture/docs/plans/example-plan.md"
ledger=$ledger_fixture/.git/flowseer-plan-status.json

check_ledger() {
  (cd "$ledger_fixture" && python3 "$ledger_script" "$@" 2>&1)
}

write_ledger() {
  cat >"$ledger" <<JSON
{
  "contract": "flowseer-plan-status/v1",
  "plan": "docs/plans/example-plan.md",
  "resume": ["U2"],
  "units": [
    {"id": "U1", "status": "passed", "commit": "abc1234",
     "verified_at": "2026-09-06T12:00:00Z", "note": null},
    {"id": "U2", "status": "$1", "commit": null, "verified_at": null, "note": null}
  ]
}
JSON
}

[[ -z $(check_ledger) ]]
[[ -z $(check_ledger "$ledger") ]]
ok "plan status check passes when no ledger exists"

write_ledger pending
[[ -z $(check_ledger) ]]
[[ -z $(check_ledger "$ledger") ]]
ok "plan status check accepts a well-formed ledger"

write_ledger 'done'
set +e
ledger_output=$(check_ledger)
ledger_rc=$?
set -e
[[ $ledger_rc -eq 1 ]]
[[ $ledger_output == *'units[1].status must be one of pending, in_progress, passed, blocked'* ]]
ok "plan status check names a status outside the allowed values"

write_ledger pending
sed -i.bak 's#docs/plans/example-plan.md#docs/plans/missing-plan.md#' "$ledger" && rm -f "$ledger.bak"
set +e
ledger_output=$(check_ledger)
ledger_rc=$?
set -e
[[ $ledger_rc -eq 1 ]]
[[ $ledger_output == *"plan 'docs/plans/missing-plan.md' does not exist"* ]]
ok "plan status check rejects a ledger whose plan does not exist"

write_ledger pending
mkdir -p "$ledger_fixture/src/deeper"
[[ -z $(cd "$ledger_fixture/src/deeper" && python3 "$ledger_script" 2>&1) ]]
sed -i.bak 's#"id": "U2"#"id": "U1"#' "$ledger" && rm -f "$ledger.bak"
set +e
ledger_output=$(check_ledger)
ledger_rc=$?
set -e
[[ $ledger_rc -eq 1 ]]
[[ $ledger_output == *'units must not repeat an id'* ]]
rm -f "$ledger"
ok "plan status check resolves the plan from a subdirectory and rejects repeated ids"

otel_wrapper=$repo_root/tools/test/service-otel-integration.sh
wrapper_tmp=$fixture_parent/wrapper-tmp
wrapper_bin=$fixture_parent/wrapper-bin
scan_error_bin=$fixture_parent/scan-error-bin
mkdir -p "$wrapper_tmp" "$wrapper_bin" "$scan_error_bin"

printf '%s\n' '#!/usr/bin/env bash' \
  "sleep \"\${FLOWSEER_FAKE_DOCKER_SLEEP:-0}\"" \
  "exit \"\${FLOWSEER_FAKE_DOCKER_RC:-0}\"" >"$wrapper_bin/docker"
printf '%s\n' '#!/usr/bin/env bash' \
  'while IFS="=" read -r name _; do' \
  "  if [[ \$name == OTEL_* ]]; then : >\"\$FLOWSEER_FAKE_OTEL_LEAK\"; exit 96; fi" \
  'done < <(env)' \
  "if [[ -n \${GOFLAGS:-} ]]; then : >\"\$FLOWSEER_FAKE_GOFLAGS_LEAK\"; exit 94; fi" \
  "if [[ \${FLOWSEER_WRAPPER_CONTROL:-} != preserved ]]; then : >\"\$FLOWSEER_FAKE_CONTROL_MISSING\"; exit 95; fi" \
  ": >\"\$FLOWSEER_FAKE_GO_CALLED\"" \
  "printf \"%s\\n\" \"\$FLOWSEER_OTEL_TEST_ARTIFACT_DIR\" >\"\$FLOWSEER_FAKE_GO_CAPTURE\"" \
  "printf \"%s\\n\" \"\$*\" >\"\$FLOWSEER_FAKE_GO_ARGS\"" \
  "case \"\$FLOWSEER_FAKE_GO_MODE\" in" \
  '  success) exit 0 ;;' \
  "  safe-failure) printf \"%s\\n\" \"bounded Collector diagnostic\" >\"\$FLOWSEER_OTEL_TEST_ARTIFACT_DIR/collector.log\"; exit 7 ;;" \
  "  sentinel-failure) printf \"%s\\n\" \"flowseer-otel-artifact-secret\" >\"\$FLOWSEER_OTEL_TEST_ARTIFACT_DIR/collector.log\"; exit 8 ;;" \
  "  scan-failure) printf \"%s\\n\" \"bounded Collector diagnostic\" >\"\$FLOWSEER_OTEL_TEST_ARTIFACT_DIR/collector.log\"; exit 9 ;;" \
  'esac' \
  'exit 99' >"$wrapper_bin/go"
printf '%s\n' '#!/usr/bin/env bash' 'exit 2' >"$scan_error_bin/grep"
chmod +x "$wrapper_bin/docker" "$wrapper_bin/go" "$scan_error_bin/grep"

wrapper_otel_leak=$fixture_parent/wrapper-otel-leaked
wrapper_control_missing=$fixture_parent/wrapper-control-missing
export OTEL_EXPORTER_OTLP_ENDPOINT=https://ambient.example
export OTEL_EXPORTER_OTLP_HEADERS=authorization=ambient-secret
export OTEL_SERVICE_NAME=ambient-service
export FLOWSEER_FAKE_OTEL_LEAK=$wrapper_otel_leak
export FLOWSEER_FAKE_CONTROL_MISSING=$wrapper_control_missing
export FLOWSEER_WRAPPER_CONTROL=preserved

set +e
wrapper_output=$(PATH="$no_buf" "$otel_wrapper" 2>&1)
wrapper_rc=$?
set -e
[[ $wrapper_rc -eq 1 ]]
[[ $wrapper_output == 'Docker is required for the service OpenTelemetry integration tier.' ]]
ok "service OpenTelemetry wrapper reports a missing Docker command"

daemon_go_marker=$fixture_parent/daemon-go-called
set +e
wrapper_output=$(PATH="$wrapper_bin:$PATH" FLOWSEER_FAKE_DOCKER_RC=23 \
  FLOWSEER_FAKE_GO_CALLED="$daemon_go_marker" "$otel_wrapper" 2>&1)
wrapper_rc=$?
set -e
[[ $wrapper_rc -eq 1 ]]
[[ $wrapper_output == 'Docker is installed but its daemon is unavailable.' ]]
[[ ! -e $daemon_go_marker ]]
ok "service OpenTelemetry wrapper stops before Go when the Docker daemon is unavailable"

hang_go_marker=$fixture_parent/hang-go-called
set +e
wrapper_output=$(PATH="$wrapper_bin:$PATH" FLOWSEER_FAKE_DOCKER_SLEEP=5 \
  FLOWSEER_DOCKER_PROBE_TIMEOUT=1 FLOWSEER_FAKE_GO_CALLED="$hang_go_marker" "$otel_wrapper" 2>&1)
wrapper_rc=$?
set -e
[[ $wrapper_rc -eq 1 ]]
[[ $wrapper_output == "Docker daemon did not answer 'docker info' within 1s." ]]
[[ ! -e $hang_go_marker ]]
ok "service OpenTelemetry wrapper gives up on a Docker daemon that does not answer"

success_capture=$fixture_parent/success-artifact-path
success_args=$fixture_parent/success-go-args
success_go_marker=$fixture_parent/success-go-called
success_goflags_leak=$fixture_parent/success-goflags-leaked
PATH="$wrapper_bin:$PATH" TMPDIR="$wrapper_tmp" GOFLAGS=-short FLOWSEER_FAKE_DOCKER_RC=0 \
  FLOWSEER_FAKE_GO_MODE=success FLOWSEER_FAKE_GO_CAPTURE="$success_capture" \
  FLOWSEER_FAKE_GO_ARGS="$success_args" FLOWSEER_FAKE_GO_CALLED="$success_go_marker" \
  FLOWSEER_FAKE_GOFLAGS_LEAK="$success_goflags_leak" \
  "$otel_wrapper"
success_artifact_dir=$(<"$success_capture")
[[ -e $success_go_marker ]]
[[ ! -e $success_artifact_dir ]]
[[ ! -e $wrapper_otel_leak && ! -e $wrapper_control_missing && ! -e $success_goflags_leak ]]
grep -qx -- 'test -race -count=1 -short=false -tags=service_otel_integration ./src/common/service/test/integration/...' \
  "$success_args"
ok "service OpenTelemetry wrapper removes success artifacts after the tagged race command"

safe_capture=$fixture_parent/safe-artifact-path
safe_args=$fixture_parent/safe-go-args
safe_go_marker=$fixture_parent/safe-go-called
set +e
wrapper_output=$(PATH="$wrapper_bin:$PATH" TMPDIR="$wrapper_tmp" FLOWSEER_FAKE_DOCKER_RC=0 \
  FLOWSEER_FAKE_GO_MODE=safe-failure FLOWSEER_FAKE_GO_CAPTURE="$safe_capture" \
  FLOWSEER_FAKE_GO_ARGS="$safe_args" FLOWSEER_FAKE_GO_CALLED="$safe_go_marker" \
  "$otel_wrapper" 2>&1)
wrapper_rc=$?
set -e
safe_artifact_dir=$(<"$safe_capture")
[[ $wrapper_rc -eq 7 ]]
[[ -e $safe_go_marker && -f $safe_artifact_dir/collector.log ]]
[[ $wrapper_output == "Collector failure artifacts retained at: $safe_artifact_dir" ]]
case "$safe_artifact_dir" in
  "$wrapper_tmp"/flowseer-service-otel.*) ;;
  *) echo "wrapper retained an artifact directory outside its fixture root" >&2; exit 1 ;;
esac
rm -rf "$safe_artifact_dir"
[[ ! -e $safe_artifact_dir ]]
ok "service OpenTelemetry wrapper retains and reports scrubbed failure artifacts"

sentinel_capture=$fixture_parent/sentinel-artifact-path
sentinel_args=$fixture_parent/sentinel-go-args
sentinel_go_marker=$fixture_parent/sentinel-go-called
set +e
wrapper_output=$(PATH="$wrapper_bin:$PATH" TMPDIR="$wrapper_tmp" FLOWSEER_FAKE_DOCKER_RC=0 \
  FLOWSEER_FAKE_GO_MODE=sentinel-failure FLOWSEER_FAKE_GO_CAPTURE="$sentinel_capture" \
  FLOWSEER_FAKE_GO_ARGS="$sentinel_args" FLOWSEER_FAKE_GO_CALLED="$sentinel_go_marker" \
  "$otel_wrapper" 2>&1)
wrapper_rc=$?
set -e
sentinel_artifact_dir=$(<"$sentinel_capture")
[[ $wrapper_rc -eq 8 ]]
[[ -e $sentinel_go_marker && ! -e $sentinel_artifact_dir ]]
[[ $wrapper_output == 'Collector artifacts contained the synthetic secret sentinel and were removed.' ]]
ok "service OpenTelemetry wrapper removes artifacts rejected by the sentinel scan"

scan_capture=$fixture_parent/scan-error-artifact-path
scan_args=$fixture_parent/scan-error-go-args
scan_go_marker=$fixture_parent/scan-error-go-called
set +e
wrapper_output=$(PATH="$scan_error_bin:$wrapper_bin:$PATH" TMPDIR="$wrapper_tmp" \
  FLOWSEER_FAKE_DOCKER_RC=0 FLOWSEER_FAKE_GO_MODE=scan-failure \
  FLOWSEER_FAKE_GO_CAPTURE="$scan_capture" FLOWSEER_FAKE_GO_ARGS="$scan_args" \
  FLOWSEER_FAKE_GO_CALLED="$scan_go_marker" "$otel_wrapper" 2>&1)
wrapper_rc=$?
set -e
scan_artifact_dir=$(<"$scan_capture")
[[ $wrapper_rc -eq 9 ]]
[[ -e $scan_go_marker && ! -e $scan_artifact_dir ]]
[[ $wrapper_output == 'Collector artifacts could not be scanned safely and were removed.' ]]
ok "service OpenTelemetry wrapper removes artifacts after a scan error"

printf '1..%d\n' "$passed"
