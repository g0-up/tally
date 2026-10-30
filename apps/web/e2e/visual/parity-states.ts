import { expect, type Page } from "@playwright/test";
import { MENU_ROUTES, HOME_PATH } from "../../src/app/menu";

// Every state the visual and accessibility specs visit. A state is one screen of the app, optionally
// with one primary dialog or drawer open. States mapped to a prototype view are compared with a
// prototype capture taken at run time; states without a prototype counterpart ("additions") are
// compared with their committed snapshot after design review.

export type Host = "tenant" | "root" | "admin";

export interface ParityState {
  /** Stable id: the prototype view id ("kho.ton"), with a suffix for dialogs and additions. */
  id: string;
  kind: "route" | "dialog" | "addition";
  host: Host;
  /** App path on the host; ":id" is filled from the environment variable named by `requires`. */
  path: string;
  /**
   * Environment variable that supplies the id of a seeded record (a document id, or the code of a SKU
   * sold in several units); the state is skipped without it.
   */
  requires?: "E2E_SALES_DOC_ID" | "E2E_STATEMENT_ID" | "E2E_MULTI_UNIT_SKU_CODE";
  /**
   * The state exists only on a demo store (sandbox), the one store with "Đặt lại mẫu". The seeded
   * store `make seed` creates is a real one, so the state is skipped unless E2E_SANDBOX_STORE is set.
   */
  sandbox?: true;
  /**
   * Prototype view, the JavaScript that opens the same dialog there (prototype actions are global),
   * and a selector of prototype-only controls the app leaves out on purpose, masked in its capture.
   */
  prototype?: { viewId: string; open?: string; mask?: string };
  /** Runs before the first navigation (cookies, network state). */
  setup?: (page: Page) => Promise<void>;
  /** Runs after the screen has rendered (opens a dialog, goes offline). */
  open?: (page: Page) => Promise<void>;
}

/** Base URL of a host kind; the same environment variables as playwright.config.ts. */
export function hostURL(host: Host): string {
  if (host === "root") return process.env.E2E_ROOT_URL ?? "http://localhost:5173";
  if (host === "admin") return process.env.E2E_ADMIN_URL ?? "http://admin.localhost:5173";
  return process.env.E2E_TENANT_URL ?? "http://cua-hang-a.localhost:5173";
}

/** Absolute app URL of a state, or null when its document id is not configured. */
export function stateURL(state: ParityState): string | null {
  let path = state.path;
  if (state.requires) {
    const id = process.env[state.requires];
    if (!id) return null;
    path = path.replace(":id", encodeURIComponent(id));
  }
  return new URL(path, hostURL(state.host)).href;
}

/** Why a state cannot run in this environment, or undefined when it can. */
export function skipReason(state: ParityState): string | undefined {
  if (state.requires && !process.env[state.requires]) return `${state.requires} is not set: the state needs the id of a seeded record`;
  if (state.sandbox && !process.env.E2E_SANDBOX_STORE) return "E2E_SANDBOX_STORE is not set: the state needs a demo store";
  return undefined;
}

async function signOut(page: Page): Promise<void> {
  await page.context().clearCookies();
}

/** Opens a dialog by clicking the button with this exact name and waits for the dialog to appear. */
function clickOpens(button: string): (page: Page) => Promise<void> {
  return async (page) => {
    await page.getByRole("button", { name: button, exact: true }).click();
    await expect(page.getByRole("dialog").first()).toBeVisible();
  };
}

/** Name of the unit table in the product dialogs; the dialog has loaded once it is on screen. */
const UNIT_TABLE = "Đơn vị tính & giá bán";

/**
 * The prototype's product sold in several units (1 dây = 4 hộp, 1 thùng = 48 hộp). The app side opens
 * the SKU named by E2E_MULTI_UNIT_SKU_CODE; the captures line up when it names the same product, as in
 * a store holding the demo catalog.
 */
const PROTOTYPE_MULTI_UNIT_SKU = "TH-CD";

function multiUnitSkuCode(): string {
  const code = process.env.E2E_MULTI_UNIT_SKU_CODE;
  if (!code) throw new Error("E2E_MULTI_UNIT_SKU_CODE is not set");
  return code;
}

/** Opens the new-product dialog and waits for its unit table. */
async function openNewProduct(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Thêm hàng hóa", exact: true }).click();
  await expect(page.getByRole("dialog").getByRole("group", { name: UNIT_TABLE })).toBeVisible();
}

/** Narrows the product list to the multi-unit SKU and opens its "Đơn vị & giá" dialog. */
async function openUnitsAndPrices(page: Page): Promise<void> {
  const code = multiUnitSkuCode();
  await page.getByPlaceholder("Tìm mã, tên, mã vạch…").fill(code);
  const rows = page.getByRole("table", { name: "Hàng hóa" }).getByRole("row").filter({ has: page.getByRole("cell") });
  await expect(rows.filter({ hasNotText: code })).toHaveCount(0);
  await rows.filter({ hasText: code }).first().getByRole("button", { name: /^Đơn vị & giá / }).click();
  await expect(page.getByRole("dialog").getByRole("group", { name: UNIT_TABLE })).toBeVisible();
}

/** Prototype-only controls per view: the store-setup page has no "Đặt lại onboarding" reset. */
const PROTOTYPE_ONLY: Readonly<Record<string, string>> = { "thiet-lap.khoi-tao": '.btn-row:has-text("Đặt lại onboarding")' };

const routeStates: ParityState[] = MENU_ROUTES.map((r) => {
  const mask = PROTOTYPE_ONLY[r.viewId];
  return { id: r.viewId, kind: "route", host: "tenant", path: r.path, prototype: { viewId: r.viewId, ...(mask ? { mask } : {}) } };
});

const dialogStates: ParityState[] = [
  {
    id: "tong-quan.dashboard.reset-dialog",
    kind: "dialog",
    host: "tenant",
    path: HOME_PATH,
    sandbox: true,
    prototype: { viewId: "tong-quan.dashboard", open: "A.reset()" },
    open: clickOpens("Đặt lại mẫu"),
  },
  {
    id: "kho.ton.adjust-dialog",
    kind: "dialog",
    host: "tenant",
    path: "/inventory/stock",
    prototype: { viewId: "kho.ton", open: "A.newADJ()" },
    open: clickOpens("Điều chỉnh kho"),
  },
  {
    id: "kho.kiem-ke.new-count-dialog",
    kind: "dialog",
    host: "tenant",
    path: "/inventory/counts",
    prototype: { viewId: "kho.kiem-ke", open: "A.newCount()" },
    open: clickOpens("Bắt đầu phiên kiểm kê"),
  },
  {
    id: "cong-no.doi-soat.new-statement-dialog",
    kind: "dialog",
    host: "tenant",
    path: "/debts/statements",
    prototype: { viewId: "cong-no.doi-soat", open: "A.newSTM()" },
    open: clickOpens("Lập bảng kê"),
  },
  {
    id: "gio-qua.lenh-dong.assembly-dialog",
    kind: "dialog",
    host: "tenant",
    path: "/gift-baskets/assembly-orders",
    prototype: { viewId: "gio-qua.lenh-dong", open: "A.newASM()" },
    open: clickOpens("Tạo lệnh đóng"),
  },
  {
    id: "danh-muc.hang-hoa.new-product-dialog",
    kind: "dialog",
    host: "tenant",
    path: "/catalog/products",
    prototype: { viewId: "danh-muc.hang-hoa", open: "A.newSKU()" },
    open: openNewProduct,
  },
  {
    id: "danh-muc.hang-hoa.units-dialog",
    kind: "dialog",
    host: "tenant",
    path: "/catalog/products",
    requires: "E2E_MULTI_UNIT_SKU_CODE",
    prototype: {
      viewId: "danh-muc.hang-hoa",
      open: `UI.f.hq = '${PROTOTYPE_MULTI_UNIT_SKU}'; render(); A.editUnits('${PROTOTYPE_MULTI_UNIT_SKU}')`,
    },
    open: openUnitsAndPrices,
  },
];

// Screens narrowed to the multi-unit SKU: Tồn kho with its unit line and the "= 2 thùng 5 dây" breakdown.
const multiUnitStates: ParityState[] = [
  {
    id: "kho.ton.multi-unit",
    kind: "route",
    host: "tenant",
    path: "/inventory/stock?q=:id",
    requires: "E2E_MULTI_UNIT_SKU_CODE",
    prototype: { viewId: "kho.ton", open: `UI.f.tq = '${PROTOTYPE_MULTI_UNIT_SKU}'; render()` },
  },
];

// Additions: screens and dialogs the prototype does not draw. Their baselines are committed after review.
const additionStates: ParityState[] = [
  { id: "auth.login", kind: "addition", host: "tenant", path: "/login", setup: signOut },
  { id: "auth.verify", kind: "addition", host: "tenant", path: "/verify", setup: signOut },
  { id: "auth.forgot-password", kind: "addition", host: "tenant", path: "/forgot-password", setup: signOut },
  { id: "auth.reset-password", kind: "addition", host: "tenant", path: "/reset-password", setup: signOut },
  { id: "auth.mfa", kind: "addition", host: "tenant", path: "/mfa", setup: signOut },
  { id: "auth.signup", kind: "addition", host: "root", path: "/signup", setup: signOut },
  { id: "auth.find-store", kind: "addition", host: "root", path: "/find-store", setup: signOut },
  { id: "admin.login", kind: "addition", host: "admin", path: "/admin/login", setup: signOut },
  { id: "onboarding.wizard", kind: "addition", host: "tenant", path: "/onboarding" },
  { id: "settings.account-closure", kind: "addition", host: "tenant", path: "/settings/account-closure" },
  { id: "error.not-found", kind: "addition", host: "tenant", path: "/no-such-screen" },
  {
    id: "sales.pos.offline",
    kind: "addition",
    host: "tenant",
    path: "/sales/pos",
    open: async (page) => {
      await page.context().setOffline(true);
      await expect(page.getByRole("status").first()).toBeVisible();
    },
  },
  {
    id: "sales.returns.exchange-dialog",
    kind: "addition",
    host: "tenant",
    path: "/sales/returns",
    open: clickOpens("Đổi hàng"),
  },
  {
    id: "debts.receivables.payment-dialog",
    kind: "addition",
    host: "tenant",
    path: "/debts/receivables",
    open: clickOpens("Thu tiền khách"),
  },
  {
    id: "print.sales-document",
    kind: "addition",
    host: "tenant",
    path: "/print/sales-documents/:id",
    requires: "E2E_SALES_DOC_ID",
  },
  {
    id: "print.statement",
    kind: "addition",
    host: "tenant",
    path: "/print/statements/:id",
    requires: "E2E_STATEMENT_ID",
  },
];

/**
 * Every state: 41 menu screens, the primary dialogs the prototype draws (with the product dialogs and
 * Tồn kho narrowed to a multi-unit SKU), and the additions.
 */
export const PARITY_STATES: readonly ParityState[] = [...routeStates, ...dialogStates, ...multiUnitStates, ...additionStates];

/** Prototype-mapped states, compared with a prototype capture at run time. */
export const PROTOTYPE_STATES: readonly ParityState[] = PARITY_STATES.filter((s) => s.prototype);

/** Opens a state in the app: runs its setup, navigates, waits for the screen heading, then runs its open step. */
export async function openAppState(page: Page, state: ParityState): Promise<void> {
  const url = stateURL(state);
  if (!url) throw new Error(`${state.id}: ${skipReason(state) ?? "no URL"}`);
  await state.setup?.(page);
  await page.goto(url);
  await page.waitForLoadState("networkidle");
  await expect(page.locator("h1").first()).toBeVisible();
  await state.open?.(page);
}
