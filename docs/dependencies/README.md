# Dependency statements

Each direct dependency has one statement under `statements/go/`,
`statements/npm/`, or `statements/pypi/`. The frontmatter records the lockfile
ecosystem, the manifests that require it, the inventory criterion, the
proposed verdict, and the date of a human ruling. An empty `approved` value means that the verdict
still needs that ruling.

The body answers three questions:

- why the dependency is required, starting with its importing package or
  package graph;
- why the pinned version is safe according to the local inventory and lookup
  commands;
- why the repository does not replace the external package with owned code.

Run the statement gate from the repository root:

```text
go test ./test/conformance/dependencies
```

The gate also rejects an empty direct-dependency set. It reads Go manifests and
`frontend/web/pnpm-lock.yaml`, then checks that statements and direct
requirements match in both directions.

The gate does not read `statements/pypi/`. A Python statement covers a direct
dependency in a script's metadata block, and nothing checks that the two
match until the gate reads those blocks.
