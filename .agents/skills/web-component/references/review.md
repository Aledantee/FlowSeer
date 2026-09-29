# Review and the browser loop

Load this to review or polish a component, and before calling one done.
Report only findings you verified in code or in a screenshot.

## 1. Mechanical scans

Run from the worktree root, over the changed files. The verifier covers
lint, stylelint, types, tests, and axe. The impeccable detector adds
design anti-patterns, when `check-tools.sh` reports it:

```bash
~/.claude/plugins/marketplaces/impeccable/.claude/skills/impeccable/scripts/impeccable detect --quiet frontend/web/src/ui
```

- **Exit 0** is clean.
- **Exit 2** means findings. Rerun with `--json` and fix them, or report
  them.
- Never silence a finding with its `ignore-*` hooks.

## 2. Browser loop

This loop needs Storybook on port 6006 and agent-browser. Story ids come
from `curl -s http://127.0.0.1:6006/index.json`. The theme is set through
the `globals` URL parameter, because the preview uses a `data-theme`
attribute and `prefers-color-scheme` does not reach it.

Take one round of four screenshots (390 × 844 and 1280 × 800, light and
dark), fix everything in one batch, confirm with at most one more round,
then stop.

```bash
agent-browser open "http://127.0.0.1:6006/iframe.html?id=ui-dropdownmenu--default&globals=theme:dark&viewMode=story"
agent-browser set viewport 390 844
agent-browser wait "#storybook-root button"
agent-browser click "#storybook-root button"
agent-browser wait '[role="menu"]'
agent-browser screenshot "$TMPDIR/ui-dropdownmenu-dark-390.png"
agent-browser errors
agent-browser close
```

- Open an overlay by clicking its trigger. A story that renders an
  overlay open with no rendered trigger shows nothing in a real browser.
- Screenshots go to `$TMPDIR`, never into the repository.
- Read each screenshot, and check it against `overlays-and-motion.md`
  ("Checks happy-dom cannot make") and `states-and-a11y.md`.

When agent-browser or Storybook is missing, name the stories to look at
and ask the user.

## 3. Checklist

Score each area pass or fail, with `file:line` evidence.

- **Contract:**
  - no literal text
  - `ai` prop where the component represents something
  - tokens only
  - a `Ui*` wrapper for every Reka use
  - nothing imported from Reka outside `src/ui/`
- **States:** every applicable state has a story, including the long,
  empty, and huge values.
- **Accessibility:**
  - keyboard path
  - focus return
  - live-region announcements
  - 24 px targets
  - 3:1 non-text contrast
  - color never carries meaning alone
- **Overlays:**
  - portalled
  - collision padding
  - `z-(--z-overlay)`
  - `closeAutoFocus` exposed
  - open-state audit story anchored to a trigger
- **Motion:**
  - one mechanism per property
  - keyframe exits on Reka parts
  - token durations
  - transform and opacity only
  - reduced motion becomes a fade
- **Data:** tabular figures in columns, locale formatting, `translate="no"`
  on identifiers.
- **System drift:** classify each drift, then fix it at that level.
  - a missing token → add the token
  - a one-off that should reuse a `Ui*` → reuse it
  - a conceptual mismatch → raise it with the user
  - a local defect → fix it in place

Fix in this order:
1. broken or inaccessible paths
2. missing states
3. hierarchy and system drift
4. visual and motion polish
5. cleanup

## 4. Design critique

For a deeper visual critique, the impeccable plugin's Operate-mode
guidance applies. Load `impeccable:impeccable` when it is installed.
Ignore its pushes toward bolder palettes, new typefaces, or generated
imagery: the token system is fixed, and those changes belong in
`docs/architecture/`, not in a component.
