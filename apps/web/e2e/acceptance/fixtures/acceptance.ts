import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { expect, test, type Browser, type Page, type TestInfo } from "@playwright/test";
import { HOME_PATH } from "../../../src/app/menu";
import { dec, sumDec } from "../../../src/shared/lib/decimal";
import { OWNER, OWNER_PASSWORD_MISSING, apiGet, apiPost, csrfToken, submitLogin } from "../../support/real-api";

// Shared by the acceptance journeys (PRD §13): the run gates, the evidence folder, the API readers
// each journey asserts with, stock set-up through the API, and the ledger verification that closes
// every journey.

const run = promisify(execFile);

/**
 * A short upper-case token that makes the names and codes one journey creates unique, so a rerun
 * on the same stack never collides with data an earlier run left behind. Letters and digits only,
 * so it also fits a SKU code.
 */
export function uniqueSuffix(now: number = Date.now()): string {
  return `${now.toString(36)}${Math.random().toString(36).slice(2, 6)}`.toUpperCase();
}

/** Calendar day of a run in Asia/Saigon; it names the evidence folder. */
export function evidenceDate(now: Date = new Date()): string {
  return new Intl.DateTimeFormat("en-CA", { timeZone: "Asia/Ho_Chi_Minh" }).format(now);
}

/** Traces and screenshots of one day's run. Git-ignored; CI keeps it as the acceptance-evidence artifact. */
export const EVIDENCE_DIR = `e2e/acceptance/evidence/${evidenceDate()}`;

/** The seeded store's owner signs in through the saved session, which needs the password to exist. */
export function requireOwner(): void {
  test.skip(OWNER.password === "", OWNER_PASSWORD_MISSING);
}

/** The journey needs the test-only fault hook, which the API honours only with APP_ENV=test. */
export function requireTestStack(): void {
  test.skip(process.env.APP_ENV !== "test", "the fault hook TALLY_FAULT_INJECT is honoured only when the stack runs with APP_ENV=test");
}

/** The journey needs a second tenant or a prepared environment; the skip names the variable to set. */
export function requireEnv(name: string, why: string): string {
  const value = process.env[name];
  test.skip(!value, `${name} is not set: ${why}`);
  return value ?? "";
}

/** Collects uncaught page errors; the journey fails on any of them when it finishes. */
export function watchPageErrors(page: Page): string[] {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  return errors;
}

/** Ends a journey: no uncaught page errors, then the ledger verification of the tenant. */
export async function finishJourney(page: Page, testInfo: TestInfo, errors: readonly string[]): Promise<void> {
  expect(errors, "uncaught errors in the page").toEqual([]);
  await verifyLedger(page, testInfo);
}

/**
 * Runs `tally ledger verify --tenant <id>` with the command in E2E_LEDGER_VERIFY_CMD. When the
 * variable is unset the step is skipped and the skip is recorded on the test, not hidden.
 */
export async function verifyLedger(page: Page, testInfo: TestInfo): Promise<void> {
  const cmd = process.env.E2E_LEDGER_VERIFY_CMD;
  if (!cmd) {
    testInfo.annotations.push({ type: "skipped-step", description: "tally ledger verify --tenant: E2E_LEDGER_VERIFY_CMD is not set" });
    return;
  }
  const tenantId = await tenantIdOf(page);
  // The tenant id is passed as an argument, never interpolated into the shell string.
  const { stdout } = await run("sh", ["-c", `${cmd} "$@"`, "sh", "--tenant", tenantId], { timeout: 300_000 });
  expect(stdout, "ledger verify output").toMatch(/\b0 mismatches\b/);
}

interface MeBody {
  tenant: { id: string };
}

interface StockBody {
  sku_id: string;
  colors: { color_id: string | null; on_hand: string }[];
}

interface PartyBody {
  receivable: string;
  payable: string;
  advances: { unallocated: string }[];
}

interface FundsBody {
  data: { kind: "cash" | "bank"; active: boolean; balance: string }[];
}

export async function tenantIdOf(page: Page): Promise<string> {
  return (await apiGet<MeBody>(page, "/me")).tenant.id;
}

/** On-hand quantity by color id of one SKU; uncolored stock is keyed "uncolored". */
export async function onHandByColor(page: Page, skuId: string): Promise<Record<string, string>> {
  const body = await apiGet<StockBody>(page, `/stock/${skuId}`);
  return Object.fromEntries(body.colors.map((c) => [c.color_id ?? "uncolored", c.on_hand]));
}

/** Asserts the on-hand quantity of each named color id (or "uncolored"); other colors are not checked. */
export async function expectOnHandByColor(page: Page, skuId: string, expected: Record<string, string>): Promise<void> {
  const actual = await onHandByColor(page, skuId);
  for (const [color, qty] of Object.entries(expected)) {
    expect(dec(actual[color] ?? "0").eq(qty), `on hand of ${color} of ${skuId}`).toBe(true);
  }
}

/** Receivable, payable and unallocated advances of one party. */
export async function partyBalance(page: Page, partyId: string): Promise<{ receivable: string; payable: string; advances: string }> {
  const body = await apiGet<PartyBody>(page, `/debts/parties/${partyId}`);
  return { receivable: body.receivable, payable: body.payable, advances: sumDec(body.advances.map((a) => a.unallocated)).toFixed() };
}

/** Balance of the store's active cash fund (opening balance plus its cash entries). */
export async function cashFundBalance(page: Page): Promise<string> {
  const body = await apiGet<FundsBody>(page, "/funds");
  const fund = body.data.find((f) => f.kind === "cash" && f.active);
  expect(fund, "the store has an active cash fund").toBeDefined();
  return fund?.balance ?? "0";
}

/** Signs an owner in on another store, in a browser context of its own. The caller closes `page.context()`. */
export async function openStoreAs(browser: Browser, baseURL: string, email: string, password: string): Promise<Page> {
  const context = await browser.newContext({ baseURL });
  const page = await context.newPage();
  await page.goto("/login");
  await submitLogin(page, email, password);
  await expect(page).toHaveURL(HOME_PATH);
  return page;
}

/** POST to the API without asserting the answer, for journeys that expect a refusal or a retry. */
export async function apiPostRaw(page: Page, path: string, body: unknown, headers: Record<string, string> = {}): Promise<{ status: number; text: string }> {
  const origin = new URL(page.url()).origin;
  const res = await page.request.post(`${origin}/api/v1${path}`, {
    data: body,
    headers: { "X-CSRF-Token": await csrfToken(page.context(), origin), ...headers },
  });
  return { status: res.status(), text: await res.text() };
}

/** Adds stock of one SKU (one color, or uncolored) through a stock increase, prepared by the journey. */
export async function addStockApi(page: Page, line: { sku_id: string; qty: string; color_id?: string }, note: string): Promise<void> {
  await apiPost(page, "/stock-adjustments", {
    kind: "increase",
    note,
    lines: [{ sku_id: line.sku_id, qty: line.qty, ...(line.color_id ? { color_id: line.color_id } : { uncolored: true }) }],
  });
}
