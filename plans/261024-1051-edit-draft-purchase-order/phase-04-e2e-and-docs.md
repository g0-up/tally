---
phase: 4
title: "E2E and user guide"
status: in-progress
effort: 0.25d
---

# Phase 4: E2E and user guide

## Requirements

1. In `apps/web/e2e/procurement.spec.ts`, add a scenario:
   - Create a draft and reopen it with Sửa.
   - Change a quantity and add a line, then save it as a draft. Reopen it and check the values.
   - Then use Lưu & chốt đơn and check the status "Đã chốt".
   - Create another draft and delete it.
2. In `docs/user-guide/nhap-hang.md`, Đơn nhập step 2, add that while an order is a draft, **Sửa** changes the supplier, expected date and lines, and **Xóa** removes it. Once confirmed, it can only be closed short.

## Files

- Modify: `apps/web/e2e/procurement.spec.ts`
- Modify: `docs/user-guide/nhap-hang.md`

## Validation

- Run the e2e procurement spec: `cd apps/web && pnpm e2e procurement`, with the stack up.
- `make test` and `make lint` for the whole repo.
- Check the plan's acceptance criteria one by one.
