#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: verify-change.sh [--full] [--base REF] [-- PATH...]

Without explicit paths, verify files changed from REF (default: HEAD), including
untracked files. --full verifies every Go module plus protobuf and Claude tooling.
USAGE
}

full=false
base=HEAD
explicit=false
paths=()

while (($#)); do
  case "$1" in
    --full)
      full=true
      shift
      ;;
    --base)
      (($# >= 2)) || { echo "--base requires a ref" >&2; exit 2; }
      base=$2
      shift 2
      ;;
    --)
      explicit=true
      shift
      while (($#)); do
        paths+=("$1")
        shift
      done
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

root=$(git rev-parse --show-toplevel)
cd "$root"
for index in "${!paths[@]}"; do
  paths[index]=${paths[index]#./}
done
build_dir=$(mktemp -d "${TMPDIR:-/tmp}/flowseer-build.XXXXXX")
trap 'rm -rf "$build_dir"' EXIT

add_path() {
  local candidate=${1#./}
  local existing
  for existing in "${paths[@]:-}"; do
    [[ $existing == "$candidate" ]] && return
  done
  paths+=("$candidate")
}

if [[ $explicit == false && $full == false ]]; then
  git rev-parse --verify "$base^{commit}" >/dev/null
  while IFS= read -r -d '' path; do
    add_path "$path"
  done < <(git diff --name-only -z "$base" --)
  while IFS= read -r -d '' path; do
    add_path "$path"
  done < <(git ls-files --others --exclude-standard -z)
fi

if [[ $explicit == true && ${#paths[@]} -eq 0 ]]; then
  echo "no paths supplied after --" >&2
  exit 2
fi

if [[ $full == false && ${#paths[@]} -eq 0 ]]; then
  echo "No changed paths to verify."
  exit 0
fi

need_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "required tool is not on PATH: $1" >&2
    exit 1
  }
}

run() {
  printf '+ '
  printf '%q ' "$@"
  printf '\n'
  "$@"
}

contains_path() {
  local wanted=$1
  local path
  for path in "${paths[@]:-}"; do
    [[ $path == "$wanted" ]] && return 0
  done
  return 1
}

go_files=()
markdown_files=()
modules=()
proto_files=()
proto=false
claude=false

add_module() {
  local candidate=$1
  local existing
  for existing in "${modules[@]:-}"; do
    [[ $existing == "$candidate" ]] && return
  done
  modules+=("$candidate")
}

module_for_file() {
  local dir
  dir=$(dirname "$1")
  while :; do
    if [[ -f $dir/go.mod ]]; then
      printf '%s\n' "$dir"
      return 0
    fi
    [[ $dir == . || $dir == / ]] && break
    dir=$(dirname "$dir")
  done
  [[ -f go.mod ]] && printf '.\n'
}

if [[ $full == true ]]; then
  while IFS= read -r modfile; do
    add_module "$(dirname "$modfile")"
  done < <(find . -name go.mod -not -path './.git/*' -not -path './.claude/worktrees/*' -print | sort)
  while IFS= read -r gofile; do
    go_files+=("${gofile#./}")
  done < <(find . -name '*.go' -not -path './.git/*' -not -path './.claude/worktrees/*' -not -path './generated/*' -not -path './frontend/web/generated/*' -print | sort)
  proto=true
  claude=true
else
  for path in "${paths[@]}"; do
    case "$path" in
      *.md)
        [[ -f $path ]] && markdown_files+=("$path")
        case "$path" in
          .claude/*|CLAUDE.md|AGENTS.md|docs/agent-knowledge.md) claude=true ;;
        esac
        ;;
      *.go)
        if [[ -f $path && $path != generated/* && $path != frontend/web/generated/* ]]; then
          go_files+=("$path")
          module=$(module_for_file "$path")
          [[ -n $module ]] && add_module "$module"
        fi
        ;;
      go.mod|go.sum|*/go.mod|*/go.sum)
        module=$(module_for_file "$path")
        [[ -n $module ]] && add_module "$module"
        ;;
      *.proto|buf.yaml|buf.work.yaml|buf.gen.yaml)
        proto=true
        [[ $path == *.proto && -f $path ]] && proto_files+=("$path")
        ;;
      .claude/*)
        claude=true
        ;;
    esac
  done
fi

if ((${#markdown_files[@]})); then
  need_tool python3
  run python3 .claude/skills/verify-change/scripts/check-markdown-links.py "${markdown_files[@]}"
fi

if ((${#go_files[@]})); then
  need_tool gofumpt
  need_tool goimports
  format_failed=false
  if ! gofumpt_output=$(gofumpt -d "${go_files[@]}"); then
    printf '%s\n' "$gofumpt_output" >&2
    format_failed=true
  elif [[ -n $gofumpt_output ]]; then
    printf '%s\n' "$gofumpt_output" >&2
    format_failed=true
  fi
  if ! goimports_output=$(goimports -d "${go_files[@]}"); then
    printf '%s\n' "$goimports_output" >&2
    format_failed=true
  elif [[ -n $goimports_output ]]; then
    printf '%s\n' "$goimports_output" >&2
    format_failed=true
  fi
  [[ $format_failed == false ]] || {
    echo "Go formatting differs; run gofumpt and goimports." >&2
    exit 1
  }
fi

if ((${#modules[@]})); then
  need_tool go
  need_tool golangci-lint
  for module in "${modules[@]}"; do
    echo "== Go module: $module =="
    (
      cd "$module"
      # No -o: with an output directory, go refuses a module that builds no
      # main packages (e.g. a fully build-tag-gated bench module). Plain
      # go build compiles everything and discards the binaries.
      run go build ./...
      run go vet ./...
      run go test -race ./...
      # ./... would make the linters load and analyze the generated trees
      # even though their findings are excluded; enumerate packages and
      # drop generated output so lint stays bounded by hand-written code.
      lint_pkgs=()
      module_dir=$PWD
      while IFS= read -r pkg_dir; do
        # The module root strips to an empty string, not a relative path;
        # without the fallback it would be passed as an absolute path glued
        # to "./", which golangci-lint reports as a typechecking error.
        pkg_rel=${pkg_dir#"$module_dir"}
        pkg_rel=${pkg_rel#/}
        lint_pkgs+=("./${pkg_rel:-.}")
      done < <(go list -f '{{.Dir}}' ./... | grep -vE '/generated(/|$)')
      if ((${#lint_pkgs[@]})); then
        run golangci-lint run --config "$root/.golangci.yml" "${lint_pkgs[@]}"
      fi
    )
  done
fi

if [[ $proto == true ]]; then
  need_tool buf
  proto_path_args=()
  if [[ $full == false && ${#proto_files[@]} -gt 0 ]]; then
    for proto_file in "${proto_files[@]}"; do
      proto_path_args+=(--path "$proto_file")
    done
  fi
  run buf format -d --exit-code ${proto_path_args[@]+"${proto_path_args[@]}"}
  run buf lint ${proto_path_args[@]+"${proto_path_args[@]}"}
  if git show-ref --verify --quiet refs/heads/master; then
    run buf breaking --against '.git#branch=master' ${proto_path_args[@]+"${proto_path_args[@]}"}
  fi
  generated_dir=$build_dir/generated
  run buf generate -o "$generated_dir"
  run diff -qr generated/go/proto "$generated_dir/generated/go/proto"
  if [[ -d frontend/web/generated || -d $generated_dir/frontend/web/generated ]]; then
    run diff -qr frontend/web/generated "$generated_dir/frontend/web/generated"
  fi
fi

if [[ $claude == true ]]; then
  need_tool jq
  need_tool shellcheck
  run jq empty .claude/settings.json
  hook_scripts=(.claude/hooks/*.sh .claude/skills/verify-change/scripts/*.sh)
  run shellcheck "${hook_scripts[@]}"
  if [[ -x .claude/hooks/tests/run.sh ]]; then
    run .claude/hooks/tests/run.sh
  fi
fi

if [[ $full == true ]]; then
  run git diff --check
else
  run git diff --check "$base" -- "${paths[@]}"
fi

if contains_path .golangci.yml && [[ ${#modules[@]} -eq 0 ]]; then
  echo "Note: .golangci.yml changed; use --full to lint every Go module."
fi

git_dir=$(git rev-parse --git-dir)
case "$git_dir" in
  /*) ;;
  *) git_dir=$root/$git_dir ;;
esac
marker=$git_dir/flowseer-verification-dirty
receipt=$git_dir/flowseer-verification-receipt
if [[ -s $marker ]]; then
  remaining=$(mktemp "$build_dir/remaining.XXXXXX")
  while IFS= read -r marked; do
    verified=$full
    if [[ $verified == false ]]; then
      for path in "${paths[@]:-}"; do
        if [[ $path == "$marked" ]]; then
          verified=true
          break
        fi
      done
    fi
    [[ $verified == true ]] || printf '%s\n' "$marked" >>"$remaining"
  done <"$marker"
  if [[ -s $remaining ]]; then
    sort -u "$remaining" >"$marker"
  else
    rm -f "$marker"
  fi
fi
{
  printf 'verified_at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  printf 'base=%s\n' "$base"
  printf 'full=%s\n' "$full"
  printf 'paths='
  printf '%q ' "${paths[@]:-}"
  printf '\n'
} >"$receipt"

echo "FlowSeer verification passed."
