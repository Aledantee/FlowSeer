# Serena configuration

Serena normally keeps project configuration and runtime data in `.serena` at
the repository root. FlowSeer keeps the shared configuration here so agent
tooling does not add another hidden top-level directory. This alternate location
requires one user-level setting on each development machine.

Add this value to `~/.serena/serena_config.yml`:

```yaml
project_serena_folder_location: "$projectDir/tools/serena"
```

Keep any existing settings in that file. If Serena requires explicit trust, add
the primary checkout and external worktree roots to its existing
`trusted_project_path_patterns` list. Use absolute, machine-specific paths
there; do not commit them to this repository.

Codex also needs one user-level MCP registration in `~/.codex/config.toml`:

```toml
[mcp_servers.serena]
startup_timeout_sec = 15
command = "serena"
args = ["start-mcp-server", "--context=codex", "--project-from-cwd", "--open-web-dashboard=false"]
```

`--project-from-cwd` lets the same registration work in the primary checkout
and external worktrees. The ignored `cache/`, `logs/`, `.gitignore`, and
`project.local.yml` paths beside this file are machine-local runtime data.

Verify the setup from any FlowSeer checkout:

```bash
serena project is_ignored_path buf.lock "$(git rev-parse --show-toplevel)"
```

The command should report that `buf.lock` is ignored. That rule comes from the
tracked `tools/serena/project.yml`, so the result proves Serena loaded the
visible project configuration without running the comprehensive health check,
which writes diagnostic logs under a root-level `.serena` directory.
