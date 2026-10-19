---
title: Onboarding rebuilt to prototype v6.14
date: 2026-10-19
summary: "Rebuilt /onboarding and added Thiết lập › Khởi tạo cửa hàng per prototype v6.14; fixed touch targets, review findings and the reversal double count."
---

# Onboarding rebuilt to prototype v6.14

## What happened
- Delivered all five phases of plans/261019-1858-onboarding-prototype-rebuild:
  - the step model and styles
  - the frame, rail and step card
  - profile, catalog, confirm and first-sale steps
  - opening stock, debt and cash steps
  - the Thiết lập › Khởi tạo cửa hàng page, tests and user-guide docs
- a11y at 390px failed on onboarding controls: the date-picker button was 19px wide, the onboarding buttons and logout were 37–38px tall, the debt chips were 40px. Fixed in globals.css (`.dp-btn`/`button.lnk` min-width; 44px min-height inside `.onb` at ≤860px). The axe and touch-target runs for onboarding.wizard now pass.
- Code review found 8 issues. The Medium one: reversals of opening stock keep the `opening` kind and positive quantities, so the confirm step's stock-by-color table counted them. The fix reads only documents with `reversal_of === null`.
- Other review fixes:
  - re-read the checklist after a 409 `onboarding_steps_pending`
  - skip already-saved funds when retrying after the password prompt
  - show a lot or an expiry date even when only one is set
  - map `color_id` errors to the color select
  - wait for the debt reasons before posting
  - drop the redundant `:complete` after a posting, since the API completes the step in the same transaction
  - remove dead CSS

## Decision
- No "Đặt lại onboarding" button on the settings page; advances are dropped from the confirm totals (user decisions).
- The 20px confirm checkbox is kept as in the prototype; it sits inside a 44px label.

## Known gaps
- At 390px every app route fails the touch-target spec because of the skip link and the global `.btn` (37px at ≤520px). This predates the rebuild.
- There are no committed baselines for the visual additions, so onboarding.wizard has no baseline to re-review. The run wrote actuals into the untracked apps/web/e2e/visual/parity.spec.ts-snapshots/.

## Next steps
- Commit when the user asks.
- Decide on the shell-wide 390px touch-target fix and on committing addition baselines.

> Historical work record — not durable authority. Prefer docs/specs/ADRs for current decisions.
