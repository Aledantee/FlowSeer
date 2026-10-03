#!/usr/bin/env bash
# Reproduction loop for a step only a person can take: a lab device, a
# browser, a power cycle. Copy this file to "$TMPDIR", edit the steps
# between the markers, and ask the user to run the copy in their terminal:
#
#   bash "$TMPDIR/hitl-loop.sh"
#
#   step "<instruction>"        show the instruction, wait for Enter
#   capture VAR "<question>"    show the question, read the answer into VAR
#
# The captured values print at the end as KEY=VALUE lines. capture echoes
# what the person types, so ask for observations and leave passwords and
# keys to a step.

set -euo pipefail

CAPTURED=()

step() {
  printf '\n>>> %s\n' "$1"
  read -r -p "    [Enter when done] " _
}

capture() {
  # Underscored locals, so a caller's variable name cannot collide with one.
  local _var="$1" _question="$2" _answer
  printf '\n>>> %s\n' "$_question"
  read -r -p "    > " _answer
  printf -v "$_var" '%s' "$_answer"
  CAPTURED+=("$_var")
}

# --- edit below ---------------------------------------------------------

step "Power on the switch and wait for the console prompt."

capture FAILED "Run the command under test. Did it fail? (y/n)"

capture OUTPUT "Paste the last line of output (or 'none'):"

# --- edit above ---------------------------------------------------------

printf '\n--- Captured ---\n'
# The guarded expansion keeps bash 3.2 from failing on an empty array.
for var in ${CAPTURED[@]+"${CAPTURED[@]}"}; do
  printf '%s=%s\n' "$var" "${!var}"
done
