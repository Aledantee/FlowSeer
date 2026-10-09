# Plan a Python script move

Load before writing units that move Python scripts or their command entry points.

In each unit's `Tests:` line, name the caller audit's search terms and scope:
the old filenames, module names without a `.py` suffix, and old directories.
Inspect imports and path injection in shell scripts and Markdown, including
inline interpreter programs passed with `-c` or a heredoc. A scan requiring
`.py` misses `import runlog`.

Name fixture tests that execute each documented replacement through the entry
point its caller uses. Compilation alone does not exercise imports or dispatch.
The [inline-import solution](../../../../docs/solutions/conventions/a-script-move-must-audit-inline-imports.md)
holds the caller scan and command fixtures for the `runlog` move.
