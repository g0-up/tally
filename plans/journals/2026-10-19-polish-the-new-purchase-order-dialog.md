---
title: Polish the new purchase order dialog
date: 2026-10-19
summary: "Fixed shared Field label-style leak and alignment, compacted the buy-line table with a pinned delete column"
---

# Polish the new purchase order dialog

## What happened
The "Tạo đơn nhập" dialog had two shared form defects and a crowded line table:
- `.f > span` (meant for the Field label) also matched the hint and error spans, so the supplier hint rendered uppercase.
- `.f` stretched its grid rows when a sibling field in `.fg` was taller, which pushed the date picker below the supplier combobox.
- The buy-line table overflowed the 1040px modal, so "Giá bán lẻ" and "Xóa" sat behind a horizontal scroll; control heights differed per column (qty 35px, others 41px) and the line amount floated above the inputs.
- The seventh (white) color swatch wrapped onto its own line next to "Chưa chọn" and read as an empty checkbox.

## Decision
- `.f` gets `align-content: start`; `.f > .hint, .f > .err` reset the label typography. Both are global, covered by the full web suite.
- The buy table is scoped with a `.bp` class: one 38px control height, 8px cell padding, narrower qty/money/spec inputs, a sticky delete column, `td.amt` on the controls' line, and the swatch status forced onto its own line.
- The new-product row moved its background to the `bp-new` class so the sticky cell can share it.
- Both add buttons are now equal-size secondary buttons, the unit conversion line reads "Quy đổi 1 [ĐV lớn] = [hệ số] [ĐV cơ sở]", and the in-table SKU placeholder is shortened.

## Next steps
- Check visually in a running app (not done this session), including the "Nhận hàng" variant with the lot column.

> Historical work record — not durable authority. Prefer docs/specs/ADRs for current decisions.
