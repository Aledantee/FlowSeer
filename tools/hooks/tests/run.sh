#!/usr/bin/env bash

set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
fixture_parent=$(mktemp -d "${TMPDIR:-/tmp}/flowseer-hooks.XXXXXX")
fixture="$fixture_parent/repository with spaces"
project_dir_with_spaces="$fixture_parent/project root with spaces"
linked_worktree=$(mktemp -d "${TMPDIR:-/tmp}/flowseer-hooks-worktree.XXXXXX")
external_worktree_root=$(mktemp -d "${TMPDIR:-/tmp}/flowseer-external-worktrees.XXXXXX")
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

jq -e '.hooks | keys | sort == ["PostToolUse", "PreToolUse", "Stop", "TaskCompleted", "WorktreeCreate"]' \
  "$claude_config" >/dev/null
jq -e '.hooks | keys | sort == ["PostToolUse", "PreToolUse", "Stop"]' \
  "$codex_config" >/dev/null

assert_hook_mapping "$claude_config" "WorktreeCreate" "<none>" \
  "\"\$CLAUDE_PROJECT_DIR/tools/hooks/create-worktree.sh\""
assert_hook_mapping "$claude_config" "PreToolUse" "Edit|Write|MultiEdit|NotebookEdit" \
  "\"\$CLAUDE_PROJECT_DIR/tools/hooks/pre-tool-policy.sh\""
assert_hook_mapping "$claude_config" "PreToolUse" "Bash" \
  "\$CLAUDE_PROJECT_DIR/tools/hooks/protect-generated-bash.sh"
assert_hook_mapping "$claude_config" "PostToolUse" "Edit|Write|MultiEdit|NotebookEdit" \
  "\"\$CLAUDE_PROJECT_DIR/tools/hooks/go-format.sh\"" \
  "\"\$CLAUDE_PROJECT_DIR/tools/hooks/proto-check.sh\"" \
  "\$CLAUDE_PROJECT_DIR/tools/hooks/mark-verification-dirty.sh"
assert_hook_mapping "$claude_config" "PostToolUse" "Bash" \
  "\$CLAUDE_PROJECT_DIR/tools/hooks/mark-verification-dirty.sh"
assert_hook_mapping "$claude_config" "Stop" "<none>" \
  "\"\$CLAUDE_PROJECT_DIR/tools/hooks/stop-check.sh\"" \
  "\$CLAUDE_PROJECT_DIR/tools/hooks/require-verification-receipt.sh"
assert_hook_mapping "$claude_config" "TaskCompleted" "<none>" \
  "\$CLAUDE_PROJECT_DIR/tools/hooks/require-verification-receipt.sh"

assert_hook_mapping "$codex_config" "PreToolUse" "Edit|Write" \
  "\"\$(git rev-parse --show-toplevel)/tools/hooks/pre-tool-policy.sh\""
assert_hook_mapping "$codex_config" "PostToolUse" "Edit|Write" \
  "\"\$(git rev-parse --show-toplevel)/tools/hooks/go-format.sh\"" \
  "\"\$(git rev-parse --show-toplevel)/tools/hooks/proto-check.sh\""
assert_hook_mapping "$codex_config" "Stop" "<none>" \
  "\"\$(git rev-parse --show-toplevel)/tools/hooks/stop-check.sh\""

for configured_hook in \
  tools/hooks/create-worktree.sh \
  tools/hooks/pre-tool-policy.sh \
  tools/hooks/protect-generated-bash.sh \
  tools/hooks/go-format.sh \
  tools/hooks/proto-check.sh \
  tools/hooks/mark-verification-dirty.sh \
  tools/hooks/stop-check.sh \
  tools/hooks/require-verification-receipt.sh; do
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
  .hooks[0].command
' "$claude_config")
claude_edit_output=$(CLAUDE_PROJECT_DIR="$repo_root" bash -c "$claude_edit_command" <<<"$edit_input")
[[ $(decision <<<"$claude_edit_output") == deny ]]
codex_edit_command=$(jq -r '
  .hooks.PreToolUse[] |
  select(.matcher == "Edit|Write") |
  .hooks[0].command
' "$codex_config")
codex_edit_output=$(cd "$repo_root" && bash -c "$codex_edit_command" <<<"$edit_input")
[[ $(decision <<<"$codex_edit_output") == deny ]]
ok "configured edit guards deny generated output"

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
  .hooks[0].command
' "$claude_config")
claude_bash_output=$(CLAUDE_PROJECT_DIR="$repo_root" bash -c "$claude_bash_command" <<<"$bash_edit")
[[ $(decision <<<"$claude_bash_output") == deny ]]
ok "configured Bash guard denies generated-file mutation"

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
ok "proto hook reports deliberate partial families"

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
stop_input=$(jq -n --arg cwd "$fixture" \
  '{cwd:$cwd,hook_event_name:"Stop",stop_hook_active:false}')
stop_output=$("$repo_root/tools/hooks/require-verification-receipt.sh" <<<"$stop_input")
jq -e '.decision == "block" and (.reason | contains("main.go"))' <<<"$stop_output" >/dev/null
ok "Stop blocks when verification is stale"

active_stop_input=$(jq -n --arg cwd "$fixture" \
  '{cwd:$cwd,hook_event_name:"Stop",stop_hook_active:true}')
assert_allow "$repo_root/tools/hooks/require-verification-receipt.sh" "$active_stop_input"
ok "Stop allows a second pass to prevent a hook loop"

task_input=$(jq -n --arg cwd "$fixture" '{cwd:$cwd,hook_event_name:"TaskCompleted"}')
set +e
task_output=$("$repo_root/tools/hooks/require-verification-receipt.sh" <<<"$task_input" 2>&1)
task_rc=$?
set -e
[[ $task_rc -eq 2 && $task_output == *"main.go"* ]]
ok "TaskCompleted blocks when verification is stale"

rm -f "$fixture/.git/flowseer-verification-dirty"
bash_mark_input=$(jq -n --arg cwd "$fixture" --arg command "sed -i '' -e s/a/b/ main.go" \
  '{cwd:$cwd,tool_input:{command:$command}}')
assert_allow "$repo_root/tools/hooks/mark-verification-dirty.sh" "$bash_mark_input"
grep -qx '<Bash mutation; verify with --full>' "$fixture/.git/flowseer-verification-dirty"
ok "Bash source mutations require a full receipt"

printf '1..%d\n' "$passed"
