# Merging a worker that ran without hooks

Load this after merging the branch of a worker on `agy` or `omp`,
before running the verifier. Those CLIs load none of the repository hooks
(no format-on-edit, no guard on `generated/`, no Stop gate), so the
coordinator's checks after the merge are the only gate the branch gets.

1. Check that `git diff --name-only <base>..<branch> -- generated buf.lock`
   prints nothing, with `<base>` the commit the lane was started from.
2. Format what the branch changed and commit the result, since the
   verifier's format gate fails on what the hook would have fixed:

```bash
git diff --name-only --diff-filter=d <base>..<branch> -- '*.go' ':!generated' | xargs -r sh -c 'gofumpt -w "$@" && goimports -w "$@"' sh
git diff --name-only --diff-filter=d <base>..<branch> -- 'spec/proto/*.proto' | xargs -r -n1 buf format -w
```

3. Run the message-sync hook on each changed schema, which the verifier
   does not cover, and the suppression hook on the lines the branch adds
   (it prints the first five matches):

```bash
git diff --name-only --diff-filter=d <base>..<branch> -- 'spec/proto/*.proto' | while read -r f; do
  jq -n --arg cwd "$PWD" --arg f "$PWD/$f" '{cwd:$cwd,tool_input:{file_path:$f}}' | tools/hooks/proto-check.sh; done
git diff -U0 <base>..<branch> | sed -n 's/^+\([^+].*\)/\1/p' | jq -Rs '{tool_input:{file_path:"<branch>",content:.}}' | tools/hooks/suppression-warn.sh
```

4. Fix a reported message-sync gap before the verifier runs. Remove a
   suppression the worker's report does not justify and fix its finding;
   one it does justify goes into the report for the user, since `AGENTS.md`
   makes a suppression a policy change.
5. Run the verifier.
