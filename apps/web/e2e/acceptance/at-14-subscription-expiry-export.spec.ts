import { randomUUID } from "node:crypto";
import { expect, test } from "@playwright/test";
import { apiGet } from "../support/real-api";
import { apiPostRaw, finishJourney, openStoreAs, requireEnv, watchPageErrors } from "./fixtures/acceptance";

// AT-14: a store whose subscription has expired is read-only: a sale is refused, but its owner can still
// take a full export of its data (OPEN-15 default). It needs an expired store with its own owner.

interface SkuListBody {
  data: { id: string }[];
}

interface ExportJobBody {
  id: string;
  status: "queued" | "running" | "succeeded" | "failed" | "expired" | "cancelled";
}

test("refuses a sale on an expired subscription and still lets its owner export every record", async ({ browser }, testInfo) => {
  const baseURL = requireEnv("E2E_EXPIRED_TENANT_URL", "the web address of a store whose subscription has expired");
  const email = requireEnv("E2E_EXPIRED_OWNER_EMAIL", "the owner email of the expired store");
  const password = requireEnv("E2E_EXPIRED_OWNER_PASSWORD", "the owner password of the expired store");

  const page = await openStoreAs(browser, baseURL, email, password);
  const errors = watchPageErrors(page);

  const sku = (await apiGet<SkuListBody>(page, "/skus?limit=200")).data[0];
  test.skip(sku === undefined, "the expired store has no product");
  if (sku === undefined) throw new Error("unreachable");

  const sale = await apiPostRaw(page, "/pos/checkout", { lines: [{ sku_id: sku.id, qty: "1" }] }, { "Idempotency-Key": randomUUID() });
  expect(sale.status, `a sale on an expired store answered ${sale.status}: ${sale.text}`).toBe(403);
  expect(sale.text).toContain("subscription_read_only");

  const reauth = await apiPostRaw(page, "/auth/reauth", { password });
  expect(reauth.status, `re-authentication answered ${reauth.status}: ${reauth.text}`).toBe(200);

  const queued = await apiPostRaw(page, "/data-exports", {}, { "Idempotency-Key": randomUUID() });
  expect(queued.status, `the export request answered ${queued.status}: ${queued.text}`).toBe(202);
  const job = JSON.parse(queued.text) as ExportJobBody;

  await expect
    .poll(async () => (await apiGet<ExportJobBody>(page, `/export-jobs/${job.id}`)).status, { timeout: 120_000 })
    .toBe("succeeded");

  await finishJourney(page, testInfo, errors);
  await page.context().close();
});
