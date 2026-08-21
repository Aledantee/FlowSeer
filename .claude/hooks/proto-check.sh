#!/usr/bin/env bash
#
# proto-check.sh — PostToolUse on Edit|Write for spec/proto/**/*.proto.
#
# Three jobs, in order:
#   1. buf format -w on the edited file.
#   2. buf lint --path on the edited file; failures come back as blocking
#      feedback (exit 2) so the next turn fixes them instead of discovering
#      them at `buf generate` time.
#   3. Message-sync check — the repo's rule 1. Reports Config/State/Event
#      triad members and GlobalRef/LocalRef counterparts that the edit did
#      not bring along.
#
# The lint leg is skipped for layering-test fixtures, which are excluded from
# their buf module; the skip is always reported, never silent.

set -uo pipefail

input=$(cat)
file=$(jq -r '.tool_input.file_path // ""' <<<"$input")
cwd=$(jq -r '.cwd // ""' <<<"$input")

case "$file" in
  *.proto) ;;
  *) exit 0 ;;
esac

case "$file" in
  /*) abs="$file" ;;
  *)  abs="$cwd/$file" ;;
esac
[ -f "$abs" ] || exit 0

# Resolve to physical paths — /var vs /private/var style symlinks otherwise
# defeat the repo-prefix match below.
absdir=$(cd "$(dirname "$abs")" 2>/dev/null && pwd -P) || exit 0
abs="$absdir/$(basename "$abs")"

root=$(cd "$cwd" 2>/dev/null && git rev-parse --show-toplevel 2>/dev/null) || exit 0
root=$(cd "$root" 2>/dev/null && pwd -P) || exit 0
[ -d "$root/spec/proto" ] || exit 0

rel=${abs#"$root"/}
case "$rel" in
  spec/proto/*) ;;
  *) exit 0 ;;
esac

# The format and lint legs need buf; the message-sync leg does not, so a
# missing buf must not skip step 3.
lint_rc=0
lint_out=""
skip_msg=""
if command -v buf >/dev/null 2>&1; then
  # --- 1. format ---------------------------------------------------------
  buf format -w "$abs" >/dev/null 2>&1

  # --- 2. lint -----------------------------------------------------------
  # Layering-test fixtures are excluded from their buf module and deliberately
  # import packages that may not exist, so `buf lint --path` on one exits
  # non-zero ("no .proto files were targeted", or an unresolvable import) and
  # would block every save of a fixture. Format and sync still run.
  #
  # Only a directory buf.yaml actually excludes gets the skip: an unexcluded
  # one still lints, and its failure is the signal that the exclude is
  # missing. And the skip is always announced — a fixture edit that returned
  # silence would read exactly like a clean lint.
  case "$rel" in
    */_test_fixtures/*)
      fixture_dir="${rel%%/_test_fixtures/*}/_test_fixtures"
      if grep -qF -- "$fixture_dir" "$root/buf.yaml" 2>/dev/null; then
        skip_msg="buf lint skipped for $rel — $fixture_dir is excluded from its buf module, so lint cannot resolve it. This file was NOT checked; spec/proto/layering_test.go is what judges it."
      else
        lint_out=$(cd "$root" && buf lint --path "$rel" 2>&1)
        lint_rc=$?
      fi
      ;;
    *)
      lint_out=$(cd "$root" && buf lint --path "$rel" 2>&1)
      lint_rc=$?
      ;;
  esac
fi

# --- 3. message sync -----------------------------------------------------
# Mirrored artifacts this repo keeps in lockstep. Each edited message whose
# name ends in a triad/pair suffix implies siblings that must exist.
notes=""
names=$(grep -Eo '^[[:space:]]*message[[:space:]]+[A-Za-z0-9_]+' "$abs" \
        | awk '{print $NF}' | sort -u)

seen=" "

check_family() {
  local base="$1"; shift
  local family="$1"
  # Every member of a family names the same base, so check each base once.
  case "$seen" in *" ${family}:${base} "*) return ;; esac
  seen="$seen${family}:${base} "
  local missing=""
  for suffix in "$@"; do
    if ! grep -rqE "^[[:space:]]*message[[:space:]]+${base}${suffix}[[:space:]]*\{" "$root/spec/proto" 2>/dev/null; then
      missing="$missing ${base}${suffix}"
    fi
  done
  [ -n "$missing" ] && notes="$notes\n  - ${base}: no message found for$missing"
}

for n in $names; do
  case "$n" in
    *Config) check_family "${n%Config}" Config State Event ;;
    *State)  check_family "${n%State}"  Config State Event ;;
    *Event)  check_family "${n%Event}"  Config State Event ;;
    *GlobalRef) check_family "${n%GlobalRef}" GlobalRef LocalRef ;;
    *LocalRef)  check_family "${n%LocalRef}"  GlobalRef LocalRef ;;
  esac
done

if [ -n "$notes" ]; then
  sync_msg=$(printf 'Message sync (rule 1) — %s defines messages whose mirrored counterparts are missing from spec/proto:%b\nEither add them or confirm the family is deliberately partial (docs/conventions/protobuf.md — the conventions doc — says how to record that). Also verify the conventions doc and the origin brainstorm still match this change.' "$rel" "$notes")
else
  sync_msg=""
fi

# --- report --------------------------------------------------------------
if [ "$lint_rc" -ne 0 ]; then
  {
    printf 'buf lint failed on %s:\n%s\n' "$rel" "$lint_out"
    [ -n "$sync_msg" ] && printf '\n%s\n' "$sync_msg"
  } >&2
  exit 2
fi

context=$skip_msg
if [ -n "$sync_msg" ]; then
  [ -n "$context" ] && context="$context"$'\n\n'
  context="$context$sync_msg"
fi

if [ -n "$context" ]; then
  jq -n --arg m "$context" \
    '{hookSpecificOutput:{hookEventName:"PostToolUse",additionalContext:$m}}'
fi
exit 0
