---
name: TypeScript & Web Style
last_updated: 2026-08-16
---

# FlowSeer — TypeScript & Web Style

Conventions for the web frontend (`frontend/web/`, pnpm workspace; consumes the
Connect-ES bindings that `buf generate` emits into `frontend/web/generated/proto/`).
The frontend has its own toolchain — Vite, Vitest, ESLint (flat config),
`typescript-eslint` — and this document, not the Go guide, governs it.

The comment discipline and the *Rules for coding agents* in
[`code-style.md`](code-style.md) apply language-independently — comments explain
*why*, no process narration, no planning identifiers, no TODOs.

## Typing

- **`strict: true` is non-negotiable**, plus `noUnusedLocals`, `noUnusedParameters`,
  `noFallthroughCasesInSwitch`, `verbatimModuleSyntax`, and `isolatedModules`. The
  type-check (`tsc --build` / framework equivalent) is a CI gate, separate from the
  bundler — Vite does not type-check.
- Avoid `any`. When an external surface forces it, contain it at the boundary and
  narrow immediately — `any` must not propagate into internal signatures. Prefer
  `unknown` plus a type guard.
- No non-null assertions (`!`) where a guard or optional chain expresses the same
  thing checkably. No `as` casts to silence the checker — a cast is a claim, and an
  unproven claim is a future runtime error.
- Union types and discriminated unions over enums; `interface` for object shapes,
  `type` for unions/compositions — pick one per shape and stay consistent.
- Wire data is typed by the generated Connect-ES bindings. Do not hand-write
  duplicate interfaces for protobuf messages; import the generated types.

## Structure & style

- ESLint flat config with `eslint.configs.recommended` +
  `tseslint.configs.recommended` as the floor; rules added there bind everyone —
  never disable a rule inline without a comment stating *why this line* is the
  exception.
- Formatting belongs to the formatter (Prettier or ESLint stylistic — one of them,
  configured once). Never hand-align or argue formatting in review.
- Modules stay small and single-purpose; `utils.ts` grab-bags are the TypeScript
  equivalent of a `common` package — find the domain the code belongs to.
- Naming: `camelCase` values, `PascalCase` types/components, `SCREAMING_SNAKE_CASE`
  only for true compile-time constants. File names follow the framework's
  convention and stay consistent.
- Never edit anything under `frontend/web/generated/` — it is `buf generate` output.

## Async & errors

- No floating promises: every promise is awaited, returned, or explicitly
  `void`-marked with a reason (`@typescript-eslint/no-floating-promises`).
- Errors caught at boundaries are narrowed before use (`catch (err: unknown)`); do
  not assume `Error`. Rethrow or surface — never swallow silently.

## Testing

- Vitest; test files co-located or under `tests/`, mirroring the source layout.
- Test the observable behavior of a module, not its internals; if a test needs to
  reach into private state, the module boundary is wrong.

## Sources

Researched 2026-08-16:

- TypeScript handbook, strictness — https://www.typescriptlang.org/tsconfig/#strict
- typescript-eslint recommended configs — https://typescript-eslint.io/users/configs/
- Connect-ES — https://connectrpc.com/docs/web/getting-started
- Google TypeScript Style Guide — https://google.github.io/styleguide/tsguide.html
