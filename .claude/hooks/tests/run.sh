#!/usr/bin/env bash

set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
fixture=$(mktemp -d "${TMPDIR:-/tmp}/flowseer-hooks.XXXXXX")
linked_worktree=$(mktemp -d "${TMPDIR:-/tmp}/flowseer-hooks-worktree.XXXXXX")
rmdir "$linked_worktree"
trap 'rm -rf "$fixture" "$linked_worktree"' EXIT

git -C "$fixture" init -q
mkdir -p "$fixture/generated" "$fixture/spec/proto"
touch "$fixture/generated/device.pb.go"
printf '# hook fixture\n' >"$fixture/README.md"
git -C "$fixture" add README.md
git -C "$fixture" -c user.name=Hook -c user.email=hook@example.invalid commit -qm init
git -C "$fixture" worktree add -qb hook-test "$linked_worktree"
mkdir -p "$linked_worktree/generated"

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

edit_input=$(jq -n --arg cwd "$fixture" --arg path "$fixture/generated/device.pb.go" \
  '{cwd:$cwd,tool_input:{file_path:$path}}')
assert_deny "$repo_root/.claude/hooks/protect-generated.sh" "$edit_input"
ok "Edit denies generated output"

missing_input=$(jq -n --arg cwd "$fixture" --arg path "$fixture/generated/missing.pb.go" \
  '{cwd:$cwd,tool_input:{file_path:$path}}')
assert_deny "$repo_root/.claude/hooks/protect-generated.sh" "$missing_input"
ok "Edit denies a missing generated output path"

worktree_input=$(jq -n --arg cwd "$linked_worktree" --arg path "$linked_worktree/generated/device.pb.go" \
  '{cwd:$cwd,tool_input:{file_path:$path}}')
assert_deny "$repo_root/.claude/hooks/protect-generated.sh" "$worktree_input"
ok "Edit denies generated output in a linked worktree"

assert_deny "$repo_root/.claude/hooks/protect-generated.sh" '{malformed'
assert_deny "$repo_root/.claude/hooks/protect-generated-bash.sh" '{malformed'
ok "generated guards fail closed on malformed JSON"

source_input=$(jq -n --arg cwd "$fixture" --arg path "$fixture/spec/proto/device.proto" \
  '{cwd:$cwd,tool_input:{file_path:$path}}')
assert_allow "$repo_root/.claude/hooks/protect-generated.sh" "$source_input"
ok "Edit allows protobuf source"

bash_edit=$(jq -n --arg cwd "$fixture" \
  --arg command "sed -i '' -e s/old/new/ generated/device.pb.go" \
  '{cwd:$cwd,tool_input:{command:$command}}')
assert_deny "$repo_root/.claude/hooks/protect-generated-bash.sh" "$bash_edit"
ok "Bash denies direct generated-file mutation"

bash_redirect=$(jq -n --arg cwd "$fixture" \
  --arg command "printf x > generated/device.pb.go" \
  '{cwd:$cwd,tool_input:{command:$command}}')
assert_deny "$repo_root/.claude/hooks/protect-generated-bash.sh" "$bash_redirect"
ok "Bash denies redirection into generated output"

bash_patch=$(jq -n --arg cwd "$fixture" \
  --arg command "apply_patch <<'PATCH'
*** Update File: generated/device.pb.go
PATCH" \
  '{cwd:$cwd,tool_input:{command:$command}}')
assert_deny "$repo_root/.claude/hooks/protect-generated-bash.sh" "$bash_patch"
ok "Bash denies patching generated output"

bash_read=$(jq -n --arg cwd "$fixture" --arg command "git diff -- generated/device.pb.go" \
  '{cwd:$cwd,tool_input:{command:$command}}')
assert_allow "$repo_root/.claude/hooks/protect-generated-bash.sh" "$bash_read"
ok "Bash allows generated-file reads"

bash_generate=$(jq -n --arg cwd "$fixture" --arg command "buf generate" \
  '{cwd:$cwd,tool_input:{command:$command}}')
assert_allow "$repo_root/.claude/hooks/protect-generated-bash.sh" "$bash_generate"
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
proto_output=$(PATH="$stub_bin:$PATH" "$repo_root/.claude/hooks/proto-check.sh" <<<"$proto_input")
jq -e '.hookSpecificOutput.additionalContext | contains("WidgetState") and contains("WidgetEvent")' \
  <<<"$proto_output" >/dev/null
ok "proto hook reports deliberate partial families"

no_buf=$fixture/no-buf
mkdir -p "$no_buf"
for command_name in bash cat jq git dirname basename grep awk sort; do
  ln -s "$(command -v "$command_name")" "$no_buf/$command_name"
done
proto_output=$(PATH="$no_buf" "$repo_root/.claude/hooks/proto-check.sh" <<<"$proto_input")
jq -e '.hookSpecificOutput.additionalContext | contains("buf is not on PATH")' \
  <<<"$proto_output" >/dev/null
ok "proto hook reports missing buf"

set +e
lint_output=$(PATH="$stub_bin:$PATH" BUF_LINT_RC=9 \
  "$repo_root/.claude/hooks/proto-check.sh" <<<"$proto_input" 2>&1)
lint_rc=$?
set -e
[[ $lint_rc -eq 2 && $lint_output == *"fixture lint failure"* ]]
ok "proto hook blocks failing lint"

touch "$fixture/main.go"
mark_input=$(jq -n --arg cwd "$fixture" --arg path "$fixture/main.go" \
  '{cwd:$cwd,tool_input:{file_path:$path}}')
assert_allow "$repo_root/.claude/hooks/mark-verification-dirty.sh" "$mark_input"
grep -qx 'main.go' "$fixture/.git/flowseer-verification-dirty"
stop_input=$(jq -n --arg cwd "$fixture" \
  '{cwd:$cwd,hook_event_name:"Stop",stop_hook_active:false}')
stop_output=$("$repo_root/.claude/hooks/require-verification-receipt.sh" <<<"$stop_input")
jq -e '.decision == "block" and (.reason | contains("main.go"))' <<<"$stop_output" >/dev/null
ok "Stop blocks when verification is stale"

active_stop_input=$(jq -n --arg cwd "$fixture" \
  '{cwd:$cwd,hook_event_name:"Stop",stop_hook_active:true}')
assert_allow "$repo_root/.claude/hooks/require-verification-receipt.sh" "$active_stop_input"
ok "Stop allows a second pass to prevent a hook loop"

task_input=$(jq -n --arg cwd "$fixture" '{cwd:$cwd,hook_event_name:"TaskCompleted"}')
set +e
task_output=$("$repo_root/.claude/hooks/require-verification-receipt.sh" <<<"$task_input" 2>&1)
task_rc=$?
set -e
[[ $task_rc -eq 2 && $task_output == *"main.go"* ]]
ok "TaskCompleted blocks when verification is stale"

rm -f "$fixture/.git/flowseer-verification-dirty"
bash_mark_input=$(jq -n --arg cwd "$fixture" --arg command "sed -i '' -e s/a/b/ main.go" \
  '{cwd:$cwd,tool_input:{command:$command}}')
assert_allow "$repo_root/.claude/hooks/mark-verification-dirty.sh" "$bash_mark_input"
grep -qx '<Bash mutation; verify with --full>' "$fixture/.git/flowseer-verification-dirty"
ok "Bash source mutations require a full receipt"

jq -e '.hooks.PreToolUse[] | select(.matcher == "Edit|Write|MultiEdit|NotebookEdit")' \
  "$repo_root/.claude/settings.json" >/dev/null
jq -e '.hooks.PreToolUse[] | select(.matcher == "Bash")' \
  "$repo_root/.claude/settings.json" >/dev/null
jq -e '.hooks.Stop[]' "$repo_root/.claude/settings.json" >/dev/null
jq -e '.hooks.TaskCompleted[]' "$repo_root/.claude/settings.json" >/dev/null
jq -e '.hooks.PostToolUse[] | select(.matcher == "Bash")' \
  "$repo_root/.claude/settings.json" >/dev/null
ok "settings wire guards and completion receipts"

printf '1..%d\n' "$passed"
