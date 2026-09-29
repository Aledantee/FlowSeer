# Mechanical remedies

Load this when the verifier-receipt signal fails, a
`flowseer-verification-dirty` marker is present, or planless work has no
`implemented:` line.

| Failed signal | Remedy |
| --- | --- |
| receipt older than the last commit | a verifier run |
| marker names paths, receipt `full=false` | a targeted run naming those paths; a marker carrying the receipt's mtime holds the lines that run could not clear, listed in its output under `Unverified edits remain after this run:` |
| marker holds `<Bash mutation; verify with --full>`, receipt `full=false` | a `--base main` run, which clears the line when every file it stands for is identical to `main`; a `--full` run when it survives |
| marker beside a `full=true` receipt | a targeted run naming the marker's paths: they were edited after the full run |
| no `implemented:` line for planless work done in the main conversation or by `steer`, `main..HEAD` non-empty, receipt signal holds | ask whether the work is complete; on yes, write the line to the checkpoints file, and in Orca to the card, before merging |
