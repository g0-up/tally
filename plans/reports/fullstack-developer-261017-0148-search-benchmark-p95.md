# SKU search benchmark: p95 at 20 000 SKUs

Date: 2026-10-17. This is a stateful measurement record that feeds the release performance gate (AT/NFR-03: p95 ≤ 1 s with 20 000 SKUs).

## Result

| Run | Samples | p50 | p95 | max | mean |
|-----|---------|-----|-----|-----|------|
| `-benchtime 1000x` | 1000 | 30.6 ms | **66.2 ms** | 114.8 ms | 35.8 ms |
| `-benchtime 400x` | 400 | 51.2 ms | 94.0 ms | 135.6 ms | 55.8 ms |

The gate passes with a wide margin: the worst p95 measured was 94 ms against a 1 s limit. The `LIKE '%term%'` design on `skus.search_norm` behind `(tenant_id, status)` is sufficient for now, so the fallback token table is not needed.

## How it was measured

- Command, run from `apps/api` with the dev `.env` sourced:
  ```
  go test -count=1 -tags integration -run '^$' -bench BenchmarkSearch -benchtime 1000x ./internal/catalog/
  ```
  The `-bench` flag is required because `-run` alone does not execute benchmarks.
- Benchmark source: `apps/api/internal/catalog/search_benchmark_integration_test.go` (`BenchmarkSearch`).
- Database: a testcontainers `postgres:16-alpine` instance (the same migrations, RLS and app role as production), with `ANALYZE` run after seeding.
- Host: Intel Xeon E5-2696 v4 @ 2.20 GHz with 88 threads, Linux.
- Data, in one tenant: 20 000 active SKUs, made of 12 000 single-SKU products plus 2 000 products with 4 flavour variants each. Every SKU has a base unit row and a barcode, and its `search_norm` is built by `catalog.SearchIndexer`. Product names combine 16 kinds, 16 brands and 12 sizes, all in Vietnamese with diacritics.
- Measured operation: `catalog.Service.Search` with `type=sku`. This covers the tenant transaction (RLS), the repository query (200 candidates), ranking in Go and the stock lookup hook. It excludes HTTP framing.
- Query mix, round-robin:
  - multi-word names: `banh dau xanh`, `keo deo dau`, `nuoc ngot cosy 330ml`, `tra xanh tan tan`, `hai ha`
  - a broad single word: `sua`
  - a no-match query that forces a full scan: `zzzz khong co`
  - 21 SKU codes and 21 barcodes looked up exactly
- Before measuring, every query is warmed once. The benchmark also asserts that each sampled code and barcode returns its own SKU first.

## Caveats

- The test container runs on a fast developer host. The production VPS should be re-measured in the release gate phase with the same command against a seeded staging tenant.
- `in_stock=true` was not measured because inventory tables are still empty in this phase.
