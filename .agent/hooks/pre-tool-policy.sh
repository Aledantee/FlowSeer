#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR

set -uo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
# shellcheck source=common.sh
source "$script_dir/common.sh"

if ! hook_init; then
  hook_deny "The edit hook received malformed input and failed closed."
fi

found_path=false
while IFS= read -r candidate_file; do
  [ -n "$candidate_file" ] || continue
  found_path=true

  relative_file=$(hook_relative_path "$candidate_file")
  resolve_rc=$?
  if [ "$resolve_rc" -eq 2 ]; then
    # Absolute path outside the repository: repository policy does not apply.
    continue
  fi
  if [ "$resolve_rc" -ne 0 ]; then
    hook_deny "$candidate_file cannot be resolved safely inside the repository. Use a normalized repository-relative path."
  fi
  case "$relative_file" in
    generated/*|frontend/web/generated/*)
      hook_deny "$relative_file is generated output. Change spec/proto or the generator configuration, then run 'buf generate'."
      ;;
    buf.lock)
      hook_deny "buf.lock is managed by buf. Change dependencies in buf.yaml, then run 'buf dep update'."
      ;;
    spec/proto/*)
      case "/$relative_file/" in
        */_test_fixtures/*|*/fixtures/*|*/testdata/*)
          hook_deny "$relative_file is a test artifact inside the Buf source tree. Put schema tests and fixtures under src/common/protoconformance instead."
          ;;
      esac
      case "${relative_file##*/}" in
        README.md|.*) ;;
        *)
          case "$relative_file" in
            *.proto) ;;
            *)
              hook_deny "$relative_file is not a production schema, package README, or dotfile placeholder. spec/proto is source-only; put executable tests under src/common/protoconformance."
              ;;
          esac
          ;;
      esac
      ;;
  esac
done < <(hook_paths)

if [ "$found_path" = false ]; then
  hook_deny "The edit hook could not determine the target path. Use a supported file edit whose destination can be checked against repository policy."
fi
