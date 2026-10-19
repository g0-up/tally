import { expect, test, type Page } from "@playwright/test";
import { HOME_PATH } from "../src/app/menu";
import { OWNER, OWNER_PASSWORD_MISSING, apiGet, apiPost, csrfToken, submitLogin } from "./support/real-api";
import { okToast, posTiles, scanIntoCart, uncoloredTile, type PosTile } from "./support/sales-data";

// Offline selling at the POS against the running API. The journey signs the seeded store's owner
// in on its own (it ends by signing out, which must not end the session other specs share), turns
// offline selling on, sells while the browser has no network and checks what reaches the server.
// The store policy and the product deactivated on the way are put back before signing out.

test.use({ storageState: { cookies: [], origins: [] } });

const pageErrors: string[] = [];

test.beforeEach(({ page }) => {
  test.skip(OWNER.password === "", OWNER_PASSWORD_MISSING);
  pageErrors.length = 0;
  page.on("pageerror", (err) => pageErrors.push(err.message));
});

test.afterEach(() => {
  expect(pageErrors, "uncaught errors in the page").toEqual([]);
});

const SYNC_PATH = "/api/v1/pos/offline-sync";
// Database and boot-snapshot key of src/features/pos/offline/offline-db.ts; that module needs the
// DOM typings the e2e project does not load.
const OFFLINE_DB_NAME = "tally-pos-offline";
const META_BOOT = "boot";
// A heartbeat runs every 15 s; reconnecting is noticed by the next one at the latest.
const RECONNECT_TIMEOUT = 30_000;

type StoreName = "bundles" | "outbox" | "meta";

// The e2e project has no DOM typings; code run in the page uses only these.
interface PageRequest<T> {
  readonly result: T;
  readonly error: { readonly name: string } | null;
  onsuccess: (() => void) | null;
  onerror: (() => void) | null;
}
interface PageDatabase {
  readonly objectStoreNames: { contains(name: string): boolean };
  transaction(store: string, mode: "readonly"): { objectStore(name: string): { getAll(): PageRequest<unknown[]> } };
  close(): void;
}
interface PageOpenRequest extends PageRequest<PageDatabase> {
  readonly transaction: { abort(): void } | null;
  onupgradeneeded: (() => void) | null;
}
interface PageGlobals {
  readonly indexedDB: { open(name: string): PageOpenRequest };
  readonly navigator: { readonly serviceWorker?: { readonly controller: unknown } };
}

interface PolicySettings {
  offline_pos_enabled: boolean;
  offline_pos_max_amount: string;
  offline_pos_max_hours: number;
}

interface SyncItem {
  offline_client_id: string;
  status: "posted" | "replayed" | "exception" | "failed";
  sales_document_id?: string;
}

interface SalesDocumentPage {
  data: { doc_no: string; source: string; offline_client_id: string | null }[];
  next_cursor: string | null;
}

/** Doc numbers of the POS-offline sales documents posted for each offline client id. */
async function offlineDocs(page: Page, clientIds: readonly string[]): Promise<Map<string, string[]>> {
  const docs = new Map<string, string[]>(clientIds.map((id) => [id, []]));
  let cursor: string | null = null;
  do {
    const query: string = `/sales-documents?source=pos_offline&limit=200${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`;
    const res: SalesDocumentPage = await apiGet<SalesDocumentPage>(page, query);
    for (const d of res.data) {
      if (d.offline_client_id !== null) docs.get(d.offline_client_id)?.push(d.doc_no);
    }
    cursor = res.next_cursor;
  } while (cursor !== null);
  return docs;
}

const banner = (page: Page) => page.getByRole("status").filter({ hasText: /^Đang ngoại tuyến\./ });
const savedToast = (page: Page) => okToast(page).filter({ hasText: /^Đã lưu OFF-\d+ trên máy\./ });

/**
 * Records of one store of the POS database, read in the page. A database the app has not created
 * yet reads as empty and is not created here (that would skip the app's own upgrade).
 */
async function readStore(page: Page, store: StoreName): Promise<unknown[]> {
  return page.evaluate(
    ([name, storeName]) =>
      new Promise<unknown[]>((resolve, reject) => {
        const open = (globalThis as unknown as PageGlobals).indexedDB.open(name);
        open.onupgradeneeded = () => open.transaction?.abort();
        open.onerror = () => (open.error?.name === "AbortError" ? resolve([]) : reject(open.error));
        open.onsuccess = () => {
          const db = open.result;
          if (!db.objectStoreNames.contains(storeName)) {
            db.close();
            resolve([]);
            return;
          }
          const req = db.transaction(storeName, "readonly").objectStore(storeName).getAll();
          req.onsuccess = () => {
            db.close();
            resolve(req.result as unknown[]);
          };
          req.onerror = () => {
            db.close();
            reject(req.error);
          };
        };
      }),
    [OFFLINE_DB_NAME, store] as const,
  );
}

const outboxCount = async (page: Page) => (await readStore(page, "outbox")).length;

/** GET then PATCH of a versioned resource of the page's host, under If-Match of the version read. */
async function patchVersioned<T>(page: Page, path: string, body: unknown): Promise<T> {
  const origin = new URL(page.url()).origin;
  const url = `${origin}/api/v1${path}`;
  const current = await page.request.get(url);
  expect(current.ok(), `GET ${path} answered ${current.status()}`).toBe(true);
  const etag = current.headers()["etag"];
  expect(etag, `GET ${path} carries an ETag`).toBeTruthy();
  const res = await page.request.patch(url, {
    data: body,
    headers: { "If-Match": etag ?? "", "X-CSRF-Token": await csrfToken(page.context(), origin) },
  });
  expect(res.ok(), `PATCH ${path} answered ${res.status()}: ${await res.text()}`).toBe(true);
  return (await res.json()) as T;
}

async function readSettings(page: Page): Promise<PolicySettings> {
  const res = await page.request.get(`${new URL(page.url()).origin}/api/v1/tenant/settings`);
  expect(res.ok(), `GET /tenant/settings answered ${res.status()}`).toBe(true);
  const s = (await res.json()) as PolicySettings;
  return { offline_pos_enabled: s.offline_pos_enabled, offline_pos_max_amount: s.offline_pos_max_amount, offline_pos_max_hours: s.offline_pos_max_hours };
}

/** Sells one unit to a walk-in buyer paying exactly in cash; returns the OFF number the device gave. */
async function sellOffline(page: Page, tile: PosTile): Promise<string> {
  const before = await savedToast(page).count();
  await scanIntoCart(page, tile, null);
  await page.getByRole("group", { name: "Tiền khách đưa nhanh" }).getByRole("button").first().click();
  await page.getByRole("button", { name: "Thanh toán · F9" }).click();
  await expect(savedToast(page)).toHaveCount(before + 1);
  const text = (await savedToast(page).last().textContent()) ?? "";
  const label = /OFF-\d+/.exec(text)?.[0];
  expect(label, "the saved toast names the OFF number").toBeTruthy();
  return label ?? "";
}

/** Two priced products without colors, each found by its own code: three of the first in stock. */
function pickTiles(tiles: readonly PosTile[]): { first: PosTile; second: PosTile } | null {
  const first = uncoloredTile(tiles, 3);
  if (first === null) return null;
  const rest = tiles.filter((t) => t !== first);
  const code = (t: PosTile) => t.sku_code.toLowerCase();
  const second = uncoloredTile(rest, 1);
  if (second === null || [first.sku_code, first.name, first.variant_label].some((s) => s.toLowerCase().includes(code(second)))) return null;
  return { first, second };
}

test("sells offline, keeps the sales across a reload, posts each once and settles a parked one", async ({ page }) => {
  const undo: { what: string; run: () => Promise<unknown> }[] = [];
  try {
    await page.goto("/login");
    await submitLogin(page, OWNER.email, OWNER.password);
    await expect(page).toHaveURL(HOME_PATH);

    const original = await readSettings(page);
    undo.push({ what: "store offline policy", run: () => patchVersioned(page, "/tenant/settings", original) });

    // The owner turns offline selling on, with room for the sales of the journey.
    await page.goto("/settings/account");
    const enable = page.getByRole("checkbox", { name: "Bán ngoại tuyến tại quầy" });
    await enable.check();
    await page.getByLabel(/^Hạn mức tiền mặt ngoại tuyến/).fill("50000000");
    await page.getByLabel(/^Số giờ tối đa không đồng bộ/).fill("12");
    await page.getByRole("button", { name: "Lưu", exact: true }).click();
    await expect(okToast(page).filter({ hasText: "Đã lưu chính sách bán ngoại tuyến." })).toBeVisible();

    // Opening the POS online downloads the catalog the device sells from.
    await page.goto("/sales/pos");
    const picked = pickTiles(await posTiles(page));
    test.skip(picked === null, "the seeded store needs two priced products without colors, three of one in stock");
    if (picked === null) throw new Error("unreachable");
    await expect.poll(async () => (await readStore(page, "bundles")).length).toBe(1);

    // Offline: three cash sales wait on the device.
    await page.context().setOffline(true);
    await expect(banner(page)).toBeVisible();
    for (let n = 1; n <= 3; n++) {
      await sellOffline(page, picked.first);
      await expect.poll(() => outboxCount(page)).toBe(n);
    }
    await expect(banner(page)).toContainText("Giao dịch chờ đồng bộ: 3.");

    // A reload during the outage opens the offline POS with the same three sales. A production build
    // serves the app shell from its service worker; the dev server cannot answer an offline browser,
    // so there the shell loads while every API call fails, which is what the app sees offline.
    const shellCached = await page.evaluate(() => (globalThis as unknown as PageGlobals).navigator.serviceWorker?.controller != null);
    if (!shellCached) {
      await page.route("**/api/v1/**", (route) => route.abort("internetdisconnected"));
      await page.context().setOffline(false);
    }
    await page.reload();
    await expect(banner(page)).toContainText("Giao dịch chờ đồng bộ: 3.");
    expect(await outboxCount(page)).toBe(3);

    // Back online: the three sales post, once each.
    const syncAnswer = page.waitForResponse((r) => r.request().method() === "POST" && new URL(r.url()).pathname === SYNC_PATH, { timeout: RECONNECT_TIMEOUT });
    if (!shellCached) await page.unroute("**/api/v1/**");
    await page.context().setOffline(false);
    const answered = await syncAnswer;
    await expect(okToast(page).filter({ hasText: /^Đồng bộ xong: 3 giao dịch ghi sổ\./ })).toBeVisible({ timeout: RECONNECT_TIMEOUT });
    const posted = ((await answered.json()) as { items: SyncItem[] }).items;
    expect(posted.map((i) => i.status)).toEqual(["posted", "posted", "posted"]);
    await expect.poll(() => outboxCount(page)).toBe(0);
    await expect(banner(page)).toBeHidden();

    // The same batch sent again posts nothing new: the server answers with the three sales it has.
    const replay = await apiPost<{ items: SyncItem[] }>(page, "/pos/offline-sync", answered.request().postDataJSON());
    expect(replay.items.map((i) => i.status)).toEqual(["replayed", "replayed", "replayed"]);
    expect(replay.items.map((i) => i.sales_document_id)).toEqual(posted.map((i) => i.sales_document_id));
    // Exactly one HD sales document per offline sale, marked as posted from the offline POS.
    const docs = await offlineDocs(page, posted.map((i) => i.offline_client_id));
    for (const [clientId, numbers] of docs) {
      expect(numbers, `sales documents of ${clientId}`).toHaveLength(1);
      expect(numbers[0]).toMatch(/^HD-/);
    }

    // Offline again, a sale of a product a manager deactivates before the device reconnects.
    await page.context().setOffline(true);
    await expect(banner(page)).toBeVisible();
    const parkedLabel = await sellOffline(page, picked.second);
    await expect.poll(() => outboxCount(page)).toBe(1);
    // API calls of the test run outside the browser, so they reach the server during the outage.
    await patchVersioned(page, `/skus/${picked.second.sku_id}`, { status: "inactive" });
    const reactivate = { what: `product ${picked.second.sku_code} status`, run: () => patchVersioned(page, `/skus/${picked.second.sku_id}`, { status: "active" }) };
    undo.push(reactivate);

    await page.context().setOffline(false);
    const callout = page.getByText(/^Hàng đợi ngoại lệ \(1\):/).locator("..");
    await expect(callout).toContainText(parkedLabel, { timeout: RECONNECT_TIMEOUT });
    await expect(callout).toContainText("Giao dịch không bị mất, quản lý cần xử lý.");

    // The manager puts the product back on sale, then posts the parked sale from the queue.
    await reactivate.run();
    undo.splice(undo.indexOf(reactivate), 1);
    await callout.getByRole("button", { name: "Xử lý" }).click();
    const row = page.getByRole("row").filter({ has: page.getByRole("cell", { name: parkedLabel, exact: true }) });
    await row.getByRole("button", { name: `Ghi sổ lại ${parkedLabel}`, exact: true }).click();
    await expect(okToast(page).filter({ hasText: `Đã ghi sổ ${parkedLabel} thành ` })).toBeVisible();
    await expect.poll(() => outboxCount(page)).toBe(0);
    await page.keyboard.press("Escape");
    await expect(page.getByText(/^Hàng đợi ngoại lệ \(/)).toHaveCount(0);

    // Put the store back before the session ends.
    for (const step of undo.splice(0).reverse()) await step.run();

    // Signing out drops the catalog and the saved start from the device.
    await page.goto(HOME_PATH);
    await page.getByRole("button", { name: "Đăng xuất" }).click();
    await expect(page).toHaveURL(/\/login$/);
    await expect.poll(async () => (await readStore(page, "bundles")).length).toBe(0);
    const meta = (await readStore(page, "meta")) as { key: string }[];
    expect(meta.map((m) => m.key)).not.toContain(META_BOOT);
    expect(await outboxCount(page)).toBe(0);
  } finally {
    // A journey that stopped half-way still puts the store back; a failure here is reported, not hidden.
    for (const step of undo.reverse()) {
      await step.run().catch((e: unknown) => test.info().annotations.push({ type: "cleanup failed", description: `${step.what}: ${String(e)}` }));
    }
  }
});
