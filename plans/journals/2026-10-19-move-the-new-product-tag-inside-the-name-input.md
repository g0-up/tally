---
title: Move the new-product tag inside the name input
date: 2026-10-19
summary: "The Hàng mới chip now sits inside the name field so the new-product row's controls share one line"
---

# Move the new-product tag inside the name input

## What happened
In the buy-line table the solid crimson "Hàng mới" tag sat on its own line above the name input, pushing that input about 28px below the unit, quantity, price and amount controls of the same row.

## Decision
The tag moved inside the name input as a soft chip on the right (`.bp-name .tag`: bad-ink text on bad-bg with a bad-line ring, contrast about 7:1, `pointer-events: none` so a click focuses the input). The input reserves 84px of right padding so typed names never run under the chip. The `.tag` class and its text are unchanged, so existing tests still find it.

## Next steps
- Check visually and redeploy web when the change should reach production.

> Historical work record — not durable authority. Prefer docs/specs/ADRs for current decisions.
