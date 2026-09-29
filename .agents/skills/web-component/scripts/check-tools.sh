#!/usr/bin/env bash
# Reports which tools web-component work needs are present. It installs
# nothing: a missing tool is reported with its purpose and install command so
# the agent can ask the user before anything is installed.
#
# Exit 0 when every required tool is present, 1 otherwise. Optional tools
# only change which review steps can run.
set -uo pipefail

root=$(git rev-parse --show-toplevel 2>/dev/null) || {
  echo "not inside the FlowSeer repository" >&2
  exit 1
}
web="$root/frontend/web"
missing_required=0

report() { # status name purpose [install]
  printf '%-8s %-18s %s\n' "$1" "$2" "$3"
  [[ -n ${4:-} ]] && printf '%-8s %-18s install: %s\n' "" "" "$4"
}

# Required: the verifier and every gate run on these.
node_version=$(node --version 2>/dev/null || true)
if [[ -z $node_version ]]; then
  report missing node "runs every web gate (package.json engines: >=22.12.0)" \
    "a Node 22.12+ runtime (nvm install 22)"
  missing_required=1
elif [[ $(printf '%s\n' v22.12.0 "$node_version" | sort -V | head -1) != v22.12.0 ]]; then
  report old node "$node_version is below the engines floor 22.12.0" \
    "nvm install 22"
  missing_required=1
else
  report ok node "$node_version"
fi

if command -v pnpm >/dev/null 2>&1; then
  report ok pnpm "$(pnpm --version 2>/dev/null)"
else
  report missing pnpm "package manager pinned in package.json packageManager" \
    "corepack enable pnpm"
  missing_required=1
fi

if [[ -x $web/node_modules/.bin/vitest && -x $web/node_modules/.bin/vue-tsc ]]; then
  report ok node_modules "frontend/web dependencies installed"
else
  report missing node_modules "frontend/web dependencies from the lockfile" \
    "cd frontend/web && pnpm install --frozen-lockfile (needs the npm registry, unsandboxed)"
  missing_required=1
fi

# Optional: each enables one review step in the skill.
if command -v agent-browser >/dev/null 2>&1; then
  report ok agent-browser "$(agent-browser --version 2>/dev/null) (screenshot review of stories)"
else
  report optional agent-browser "screenshots stories in a real browser for layout, stacking, and motion checks" \
    "brew install agent-browser && agent-browser install"
fi

impeccable="$HOME/.claude/plugins/marketplaces/impeccable/.claude/skills/impeccable/scripts/impeccable"
if [[ -x $impeccable ]] && probe=$("$impeccable" engine-probe 2>/dev/null); then
  report ok impeccable "$probe (design anti-pattern detector at $impeccable)"
else
  report optional impeccable "design anti-pattern detector used in review mode" \
    "claude plugin marketplace add pbakaus/impeccable && claude plugin install impeccable@impeccable"
fi

if curl -sf -o /dev/null --max-time 2 http://127.0.0.1:6006/; then
  report ok storybook "running on http://127.0.0.1:6006"
else
  report optional storybook "not running; screenshot review needs it" \
    "cd frontend/web && pnpm storybook (keep it running in a background shell)"
fi

exit "$missing_required"
