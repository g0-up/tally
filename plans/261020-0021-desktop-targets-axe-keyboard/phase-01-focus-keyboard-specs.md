---
phase: 1
title: "Repair the focus and keyboard specs, then fix any real failure they reveal"
status: completed
---

# Phase 1: focus and keyboard specs

## Context

Causes #1 and #2 in [plan.md](plan.md). Five checks never compared anything, because
`locator.evaluate("(el) => …")` returns `undefined`. One check times out, because `getByRole`
cannot see the opener behind the open modal. Once the checks work, they may expose real app
failures. Those are in scope.

## Files

- Modify `apps/web/e2e/a11y/focus.spec.ts`
- Modify `apps/web/e2e/a11y/keyboard.spec.ts`
- Only if a repaired check fails against the app: `apps/web/src/shared/ui/*` dialog or
  return-focus code, or the focus-ring rules in the `globals.css` additions block

## Steps

1. **`keyboard.spec.ts` `tabTo`.** Replace the string evaluate with a check that runs:
   `(await target.and(page.locator(":focus")).count()) > 0`. This needs no DOM types.
   Alternatively, use a real function with a typed globals interface.
2. **Focus trap check (`focus.spec.ts:25`).** Assert that the focused element is inside the
   dialog with a real check, for example `await dialog.locator(":focus").count()` equals 1 after
   every Tab. Keep the 25 iterations.
3. **Opener removed (`focus.spec.ts:36`).**
   - Remove the opener with an element handle taken before the dialog opens, or with
     `getByRole(…, { includeHidden: true })`.
   - Call `remove()` from a real function.
   - The focus check that follows already uses `page.evaluate` with an IIFE string, which works.
     Keep it.
4. **Focus ring (`focus.spec.ts:54`).** Replace the string with a real function. Read
   `getComputedStyle` through a typed globals interface, following `PageGlobals` in
   `e2e/pos-offline.spec.ts`. Keep the outline-or-box-shadow condition.
5. **Prove the checks can fail.** In a scratch run, which is not committed:
   - `tabTo` on a locator that never gets focus throws.
   - The trap check reports `false` when focus is moved outside the dialog.
   - The ring check reports `false` when `outline: none; box-shadow: none` is injected.
6. **Run the two specs** on the demo store. For any assertion that now fails:
   - Diagnose the app cause. Known parts: Radix focus trap, the `returnFocus` hook used by
     `commit-dialog.tsx` and `detail-drawer.tsx`, and `:focus-visible` at `globals.css:9`.
   - Fix it in the app, then rerun.
   - A programmatic `.focus()` may not match `:focus-visible` in Chromium. If the ring check fails
     for that reason only, give the focused control a visible indicator under `:focus` for
     keyboard-reachable buttons. Do not move the test to a click.

## Validation

```
E2E_TENANT_URL=<demo store> E2E_OWNER_EMAIL=<owner> E2E_OWNER_PASSWORD=<shell only> \
  corepack pnpm exec playwright test --project a11y e2e/a11y/focus.spec.ts e2e/a11y/keyboard.spec.ts --reporter=line
```

All 7 tests pass. `corepack pnpm typecheck` and `corepack pnpm lint` are clean for `e2e/`.

## Risks and rollback

- If a repaired check exposes an app bug, the phase grows. That growth is in scope, so record the
  cause in the report.
- Rollback: revert the two spec files and any app file touched.
