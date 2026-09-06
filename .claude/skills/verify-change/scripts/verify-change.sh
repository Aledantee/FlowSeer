#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: verify-change.sh [--full] [--print-selection] [--base REF] [-- PATH...]

Without explicit paths, verify files changed from REF (default: HEAD), including
untracked files. --full verifies every Go module plus protobuf and agent hook tooling.
--print-selection reports the selected gates without running them or writing a receipt.
USAGE
}

full=false
print_selection=false
base=HEAD
explicit=false
paths=()

while (($#)); do
  case "$1" in
    --full)
      full=true
      shift
      ;;
    --print-selection)
      print_selection=true
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

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
root=$(git rev-parse --show-toplevel)
cd "$root"
for index in "${!paths[@]}"; do
  paths[index]=${paths[index]#./}
done
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
  if [[ $print_selection == true ]]; then
    echo "service_otel_integration=false"
    exit 0
  fi
  echo "no paths supplied after --" >&2
  exit 2
fi

if [[ $full == false && ${#paths[@]} -eq 0 ]]; then
  if [[ $print_selection == true ]]; then
    echo "service_otel_integration=false"
    exit 0
  fi
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
hook_tooling=false
mib=false
serena=false
service_otel_integration=false

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
  done < <(find . -name go.mod -not -path './.git/*' -not -path './.claude/worktrees/*' -not -path './.codex/worktrees/*' -print | sort)
  while IFS= read -r gofile; do
    go_files+=("${gofile#./}")
  done < <(find . -name '*.go' -not -path './.git/*' -not -path './.claude/worktrees/*' -not -path './.codex/worktrees/*' -not -path './generated/*' -not -path './frontend/web/generated/*' -print | sort)
  proto=true
  hook_tooling=true
  mib=true
  serena=true
  service_otel_integration=true
else
  for path in "${paths[@]}"; do
    case "$path" in
      *.md)
        [[ -f $path ]] && markdown_files+=("$path")
        case "$path" in
          .claude/*|.codex/*|CLAUDE.md|AGENTS.md|docs/agent-knowledge.md|tools/hooks/*) hook_tooling=true ;;
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
      .claude/*|.codex/*|tools/hooks/*)
        hook_tooling=true
        ;;
    esac
    # Independent of the classification above: a change to the mibgen
    # generator, its config, or a MIB source can silently drift the
    # committed bindings under generated/go/mib.
    case "$path" in
      mibgen.yaml|spec/mib/*|src/protocol/snmp/cmd/mibgen/*|src/protocol/smi/*) mib=true ;;
    esac
    case "$path" in
      tools/serena/*) serena=true ;;
    esac
    case "$path" in
      go.mod|go.sum|src/common/service/*.go|src/common/service/test/integration/otel*|src/common/service/test/integration/testdata/otel-collector.yaml|tools/test/service-otel-integration.sh)
        service_otel_integration=true
        ;;
    esac
  done
fi

if [[ $print_selection == true ]]; then
  printf 'service_otel_integration=%s\n' "$service_otel_integration"
  exit 0
fi

need_tool python3
run python3 "$script_dir/check-plan-status.py"

build_dir=$(mktemp -d "${TMPDIR:-/tmp}/flowseer-build.XXXXXX")
trap 'rm -rf "$build_dir"' EXIT

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
  need_tool python3
  for module in "${modules[@]}"; do
    echo "== Go module: $module =="
    (
      cd "$module"
      # No -o: with an output directory, go refuses a module that builds no
      # main packages (e.g. a fully build-tag-gated bench module). Plain
      # go build compiles everything and discards the binaries.
      run go build ./...
      # A targeted run vets, tests, and lints only the packages that can
      # observe the change: those holding a changed file plus every
      # package that imports one of them, directly or through its test
      # imports. A module-wide race run of this repository takes over ten
      # minutes and was being repeated per worker and per integration
      # for changes a single package proves in seconds. --full and a
      # module selected without a changed Go file (a go.mod edit) keep
      # the module-wide scope.
      targets=(./...)
      changed_pkgs=()
      for go_file in "${go_files[@]}"; do
        if [[ $module == . ]]; then
          rel=$go_file
        elif [[ $go_file == "$module"/* ]]; then
          rel=${go_file#"$module"/}
        else
          continue
        fi
        pkg_dir=$(dirname "$rel")
        [[ -d $pkg_dir ]] || continue
        pkg=$(go list -e -f '{{.ImportPath}}' "./$pkg_dir" 2>/dev/null) || continue
        [[ -n $pkg ]] && changed_pkgs+=("$pkg")
      done
      if [[ $full == false && ${#changed_pkgs[@]} -gt 0 ]]; then
        go list -e -f '{{.ImportPath}}|{{join .Deps " "}} {{join .TestImports " "}} {{join .XTestImports " "}}' ./... > "$build_dir/packages.txt"
        targets=()
        # macOS ships bash 3.2, which has no mapfile.
        while IFS= read -r target; do
          targets+=("$target")
        done < <(python3 - "$build_dir/packages.txt" "${changed_pkgs[@]}" <<'PY'
import sys

rows = []
with open(sys.argv[1], encoding="utf-8") as handle:
    for line in handle:
        import_path, _, deps = line.rstrip("\n").partition("|")
        rows.append((import_path, set(deps.split())))
affected = set(sys.argv[2:])
grown = True
while grown:
    grown = False
    for import_path, deps in rows:
        if import_path not in affected and deps & affected:
            affected.add(import_path)
            grown = True
print("\n".join(sorted(affected)))
PY
)
        echo "Targeted packages: ${#targets[@]} (changed: ${changed_pkgs[*]})"
      fi
      run go vet "${targets[@]}"
      run go test -race "${targets[@]}"
      # Enumerate package directories and drop generated output so lint
      # stays bounded by hand-written code even though generated findings
      # are excluded by configuration.
      lint_pkgs=()
      module_dir=$PWD
      while IFS= read -r pkg_dir; do
        # The module root itself can hold a package (the repo-root
        # generate.go). Stripping "$module_dir/" leaves that dir
        # untouched — no trailing slash to match — which would emit
        # ".//<abspath>" and fail golangci-lint with a typecheck error.
        if [[ $pkg_dir == "$module_dir" ]]; then
          lint_pkgs+=(".")
        else
          lint_pkgs+=("./${pkg_dir#"$module_dir"/}")
        fi
      done < <(go list -f '{{.Dir}}' "${targets[@]}" | grep -vE '/generated(/|$)')
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

if [[ $mib == true ]]; then
  # Regenerates into a tmpdir and diffs against the committed bindings;
  # exits non-zero on drift. Pairs with the buf-generate diff above so
  # both code generators are gated the same way.
  run go run ./src/protocol/snmp/cmd/mibgen -check
  # The baseline's second tier forbids a pending group. Without it the
  # refresh flag's own output passes: refreshing writes every new group as
  # pending, and the always-on gate tolerates pending by design, so a
  # branch could record no reason for anything and still come out green.
  run go test -tags=mibgen_baseline_complete -run TestBaselineComplete ./src/protocol/snmp/cmd/mibgen/
  if [[ $full == true ]]; then
    # The parser's default corpus tier reads a deduplicated corpus so the
    # developer loop stays cheap. The whole corpus sits behind a build tag
    # and is only worth its cost here, where nothing else is in a hurry.
    run go test -tags=smi_corpus_full -run TestCorpus ./src/protocol/smi/
    # The differential suite compares the parser against the dependency it
    # replaced. It is its own module, so it needs -C to resolve; and its
    # corpus pass is single-goroutine and skips under -race, which is the
    # only way this script runs Go tests elsewhere, so a plain invocation
    # is the only thing that runs it at all. It walks the same corpus, so
    # it belongs in the same tier as the run above.
    run go test -C src/protocol/smi/differential ./...
  fi
fi

if [[ $hook_tooling == true ]]; then
  need_tool jq
  need_tool shellcheck
  run jq empty .claude/settings.json
  run jq empty .codex/hooks.json
  hook_scripts=(tools/hooks/*.sh tools/hooks/tests/*.sh .claude/skills/verify-change/scripts/*.sh)
  run shellcheck "${hook_scripts[@]}"
  if [[ -x tools/hooks/tests/run.sh ]]; then
    run tools/hooks/tests/run.sh
  fi
fi

if [[ $serena == true ]]; then
  if command -v serena >/dev/null 2>&1; then
    if ! serena_output=$(run serena project is_ignored_path buf.lock "$root"); then
      printf '%s\n' "$serena_output" >&2
      exit 1
    fi
    printf '%s\n' "$serena_output"
    [[ $serena_output == *"Path 'buf.lock' IS ignored by the project configuration."* ]] || {
      echo "Serena did not load tools/serena/project.yml." >&2
      exit 1
    }
  else
    echo "serena is not on PATH; skipping the optional project-config smoke check."
  fi
fi

if [[ $service_otel_integration == true ]]; then
  run tools/test/service-otel-integration.sh
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
