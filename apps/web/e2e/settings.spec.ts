import { expect, test, type Locator, type Page } from "@playwright/test";
import { HOME_PATH } from "../src/app/menu";
import { OWNER, OWNER_PASSWORD_MISSING, OWNER_STATE_FILE, apiGet, submitLogin } from "./support/real-api";

// Settings journeys on the seeded store's real session: signing out another device, granting and
// revoking read-only support access on a ticket, and a posting refused inside a locked period
// (the lock is moved back to where it was afterwards).

interface Page_<T> {
  data: T[];
}
interface SessionRow {
  id: string;
  current: boolean;
}
interface TicketRow {
  id: string;
  number: string;
  status: string;
  assigned_admin_id: string | null;
}
interface PartyRow {
  party_id: string;
  open: string;
  open_documents: number;
}
interface Owner {
  user: { mfa_enabled: boolean };
}
interface PeriodLocks {
  locked_through: string | null;
}
interface Tenant {
  business_date?: string;
}

/** Opens a store screen and waits until the shell and the screen header are rendered. */
async function openScreen(page: Page, path: string): Promise<void> {
  await page.goto(path);
  await expect(page.locator("#view .ph h1")).toBeVisible();
}

const cardOf = (page: Page, title: string): Locator => page.locator(".card", { has: page.getByRole("heading", { name: title, exact: true }) });

/** Answers the shared "Xác nhận lại mật khẩu" dialog with the owner's password. */
async function enterPassword(page: Page): Promise<void> {
  const dialog = page.getByRole("dialog", { name: "Xác nhận lại mật khẩu" });
  await dialog.getByLabel(/^Mật khẩu/).fill(OWNER.password);
  await dialog.getByRole("button", { name: "Xác nhận", exact: true }).click();
  await expect(dialog).toBeHidden();
}

/** dd/mm/yyyy of an ISO date, as the date fields read it. */
const ddmmyyyy = (iso: string) => `${iso.slice(8, 10)}/${iso.slice(5, 7)}/${iso.slice(0, 4)}`;

test.describe("with the real API", () => {
  test.skip(OWNER.password === "", OWNER_PASSWORD_MISSING);
  test.use({ storageState: OWNER_STATE_FILE });

  test("signs out another device of the owner", async ({ page, browser }, testInfo) => {
    await openScreen(page, "/settings/account");
    const owner = await apiGet<Owner>(page, "/me");
    test.skip(owner.user.mfa_enabled, "the second sign-in would need the owner's authenticator code");
    const before = new Set((await apiGet<Page_<SessionRow>>(page, "/sessions")).data.map((s) => s.id));

    const other = await browser.newContext({ baseURL: testInfo.project.use.baseURL });
    try {
      const phone = await other.newPage();
      await phone.goto("/login");
      await submitLogin(phone, OWNER.email, OWNER.password);
      await expect(phone).toHaveURL(HOME_PATH);

      await openScreen(page, "/settings/account");
      const sessions = (await apiGet<Page_<SessionRow>>(page, "/sessions")).data;
      const index = sessions.findIndex((s) => !s.current && !before.has(s.id));
      expect(index, "the second sign-in is listed").toBeGreaterThanOrEqual(0);

      const table = page.getByRole("table", { name: "Thiết bị đang đăng nhập" });
      await table
        .getByRole("row")
        .nth(index + 1)
        .getByRole("button", { name: /^Đăng xuất / })
        .click();
      await expect(page.getByText("Đã đăng xuất thiết bị.").first()).toBeVisible();

      const origin = new URL(phone.url()).origin;
      const me = await other.request.get(`${origin}/api/v1/me`);
      expect(me.status()).toBe(401);
    } finally {
      await other.close();
    }
  });

  test("grants support read access on a ticket and revokes it", async ({ page }) => {
    await openScreen(page, "/settings/subscription");
    const tickets = (await apiGet<Page_<TicketRow>>(page, "/support-tickets")).data;
    const ticket = tickets.find((t) => t.status !== "resolved" && t.status !== "closed" && t.assigned_admin_id !== null);
    test.skip(ticket === undefined, "the seeded store has no open ticket assigned to a support agent");
    if (ticket === undefined) return;

    const access = cardOf(page, "Truy cập hỗ trợ");
    await access.getByRole("button", { name: "Cấp quyền hỗ trợ 2 giờ (chỉ đọc)" }).click();
    const dialog = page.getByRole("dialog", { name: "Cấp quyền hỗ trợ 2 giờ (chỉ đọc)" });
    await dialog.getByRole("combobox", { name: "Yêu cầu hỗ trợ" }).selectOption(ticket.id);
    await dialog.getByRole("button", { name: "Cấp quyền", exact: true }).click();

    const granted = page.getByText("Đã cấp quyền hỗ trợ có thời hạn.").first();
    const reauth = page.getByRole("dialog", { name: "Xác nhận lại mật khẩu" });
    await expect(granted.or(reauth)).toBeVisible();
    if (await reauth.isVisible()) await enterPassword(page);
    await expect(granted).toBeVisible();

    const live = access.getByText(/^Phiên hỗ trợ .* đang mở đến/);
    await expect(live).toBeVisible();
    await access.getByRole("button", { name: "Thu hồi", exact: true }).click();
    await expect(page.getByText("Đã thu hồi.").first()).toBeVisible();
    await expect(live).toBeHidden();
  });

  test("refuses a posting dated inside a locked period, then reopens the period", async ({ page }) => {
    await openScreen(page, "/settings/account");
    const tenant = await apiGet<Tenant>(page, "/tenant");
    const before = await apiGet<PeriodLocks>(page, "/period-locks?limit=1");
    const today = tenant.business_date;
    test.skip(today === undefined, "the store reports no business date");
    if (today === undefined) return;
    test.skip(before.locked_through !== null && before.locked_through >= today, "the books are already locked through today");
    const party = (await apiGet<Page_<PartyRow>>(page, "/debts/parties?direction=receivable&limit=200")).data.find(
      (p) => p.open_documents > 0 && Number(p.open) >= 10_000,
    );
    test.skip(party === undefined, "the seeded store has no customer with an open balance");
    if (party === undefined) return;

    // Lock the books through today: a receipt dated today now falls in the locked period.
    const lock = cardOf(page, "Khóa sổ kỳ");
    await lock.getByRole("textbox", { name: "Khóa sổ đến hết ngày" }).fill(ddmmyyyy(today));
    await lock.getByRole("button", { name: "Khóa sổ", exact: true }).click();
    const confirmLock = page.getByRole("dialog", { name: "Xác nhận khóa sổ" });
    await expect(confirmLock).toContainText(`Hết ngày ${ddmmyyyy(today)}`);
    await confirmLock.getByRole("button", { name: "Khóa sổ", exact: true }).click();
    await expect(page.getByText(`Đã khóa sổ đến hết ngày ${ddmmyyyy(today)}.`).first()).toBeVisible();
    await expect(confirmLock).toBeHidden();

    try {
      await openScreen(page, `/debts/receivables?party=${party.party_id}`);
      await page.getByRole("dialog").getByRole("button", { name: "Thu tiền" }).click();
      const pay = page.getByRole("dialog", { name: "Thu tiền khách" });
      const amount = pay.getByRole("textbox", { name: "Số tiền" });
      await expect(amount).not.toHaveValue("");
      await amount.fill("10000");
      await pay.getByRole("button", { name: "Ghi phiếu thu" }).click();

      const toast = page.getByRole("alert").filter({ hasText: "Kỳ kế toán của ngày này đã khóa." });
      await expect(toast).toBeVisible();
      await expect(toast).toContainText("Chưa ghi sổ.");
      await expect(pay).toBeVisible();
    } finally {
      // Move the lock back to where it was so later journeys can post today.
      await openScreen(page, "/settings/account");
      const card = cardOf(page, "Khóa sổ kỳ");
      await card.getByRole("button", { name: "Xác nhận mật khẩu" }).click();
      await enterPassword(page);
      await card.getByRole("button", { name: "Mở lại kỳ", exact: true }).click();
      const reopen = page.getByRole("dialog", { name: "Mở lại kỳ" });
      if (before.locked_through !== null) await reopen.getByRole("textbox", { name: "Khóa lại đến ngày" }).fill(ddmmyyyy(before.locked_through));
      await reopen.getByRole("textbox", { name: /^Lý do/ }).fill("Trả lại kỳ khóa sau kiểm thử");
      await reopen.getByRole("button", { name: "Mở lại kỳ", exact: true }).click();
      const confirm = page.getByRole("dialog", { name: "Xác nhận mở lại kỳ đã khóa" });
      await confirm.getByRole("checkbox", { name: "Tôi xác nhận mở lại kỳ" }).check();
      await confirm.getByRole("button", { name: "Mở lại kỳ", exact: true }).click();
      await expect(page.getByText(/^Đã mở lại/).first()).toBeVisible();
    }
  });
});
