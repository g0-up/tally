---
phase: 1
title: "API contract test for draft edit and delete"
status: completed
effort: 0.25d
---

# Phase 1: API contract test for draft edit and delete

## Context

`PATCH /purchase-orders/:id`, `PUT /purchase-orders/:id/lines` and `DELETE /purchase-orders/:id` exist (`apps/api/internal/procurement/http.go:52-54`) but no test calls them. The web will start relying on them, so their contract needs a test first.

## Requirements

Add a subtest to `TestProcurementOverHTTP` in `apps/api/internal/app/procurement_integration_test.go:381`, using its existing helpers (`send`, `dataOf`, `purchaseOrderOut`, the order body around line 273). It covers:

1. Create a draft. PATCH `expected_date` and `supplier_party_id` with `If-Match` set to the version. Expect 200, the new values, and a version one higher.
2. PUT lines on the new version: change a quantity, drop a line, and add a `new_product` line. Expect 200, the new lines and totals, and a new SKU created exactly once.
3. PUT again with the old version. Expect 412, and the lines unchanged.
4. Confirm the order, then try PATCH, PUT and DELETE on it. Expect 409 `order_not_draft` for each.
5. Create another draft and DELETE it at its version. Expect 204, and a GET that then returns 404.

## Files

- Modify: `apps/api/internal/app/procurement_integration_test.go`

## Validation

- `make test-integration` (or `go test ./internal/app -run TestProcurementOverHTTP` with the DB up).

## Risk and rollback

This phase only adds a test. If the API behaves differently from the list above, stop and report it. The web phases depend on this contract.
