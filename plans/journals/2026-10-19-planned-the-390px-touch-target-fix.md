---
title: Planned the 390px touch-target fix
date: 2026-10-19
summary: Inventoried 59 failing states at 390px; six shared causes; plan in plans/261019-2225-touch-targets-390
---

# Planned the 390px touch-target fix

## What happened
Ran `touch targets at 390` across every parity state on a fresh throwaway store: 59 failed, 2 passed, 2 skipped (print states). Only onboarding.wizard passed.

## Causes found
1. The skip link is moved off screen with top:-200px but is still measured (156x42).
2. `.btn` is 37px tall at ≤520px. This comes from the ported prototype rule; the 44px block covers `.btn-sm` only.
3. `.auth-links a` are 20px tall.
4. Lone links and anchor `.lnk` have no minimum; the 44px block covers `button.lnk` only.
5. `.cp` has no min-width (42x44).
6. Native checkboxes are 13x13; the spec measures the input, not the label.

## Decision
All fixes go in the additions block of globals.css, inside the existing ≤860px/coarse block. The ported prototype rules stay as they are and the spec is not weakened.
- Skip link: visually hidden (1px clip) until it has focus.
- Checkboxes: appearance:none with a 44px box around a ~20px drawn square, plus a forced-colors fallback.

## Next steps
Phase 2 re-runs the inventory on the seeded cua-hang-a store, because table rows only render with data. It then compares parity at 390 before and after. Execute with /ak:cook plans/261019-2225-touch-targets-390/plan.md.

> Historical work record — not durable authority. Prefer docs/specs/ADRs for current decisions.
