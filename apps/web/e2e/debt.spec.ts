import { expect, test, type Page } from "@playwright/test";
import { OWNER, OWNER_PASSWORD_MISSING, OWNER_STATE_FILE, apiGet } from "./support/real-api";

// The debt journey on the seeded store's real session: a customer with an open balance pays part
// of it against the oldest document, a reconciliation statement of that customer is drawn up and
// sent, and the schedule and advances screens read the same postings.

interface Page_<T> {
  data: T[];
}
interface PartyRow {
  party_id: string;
  name: string;
  open: string;
  open_documents: number;
}

/** Opens a store screen and waits until the shell and the screen header are rendered. */
async function openScreen(page: Page, path: string): Promise<void> {
  await page.goto(path);
  await expect(page.locator("#view .ph h1")).toBeVisible();
}

/** A customer who still owes at least 10.000 ₫ on an open document. */
async function debtor(page: Page): Promise<PartyRow | undefined> {
  const r = await apiGet<Page_<PartyRow>>(page, "/debts/parties?direction=receivable&limit=200");
  return r.data.find((p) => p.open_documents > 0 && Number(p.open) >= 10_000);
}

test.describe("with the real API", () => {
  test.skip(OWNER.password === "", OWNER_PASSWORD_MISSING);
  test.use({ storageState: OWNER_STATE_FILE });

  test("collects part of a customer's debt against the oldest document", async ({ page }) => {
    await openScreen(page, "/debts/receivables");
    const party = await debtor(page);
    test.skip(party === undefined, "the seeded store has no customer with an open balance");
    if (party === undefined) return;

    await expect(page.getByRole("table", { name: "Sổ phải thu" })).toContainText(party.name);
    await openScreen(page, `/debts/receivables?party=${party.party_id}`);
    const drawer = page.getByRole("dialog", { name: party.name });
    await expect(drawer.getByText("Chứng từ còn mở")).toBeVisible();
    await drawer.getByRole("button", { name: "Thu tiền" }).click();

    const pay = page.getByRole("dialog", { name: "Thu tiền khách" });
    const amount = pay.getByRole("textbox", { name: "Số tiền" });
    await expect(amount).not.toHaveValue("");
    await amount.fill("10000");
    await expect(pay.getByRole("textbox", { name: /^Phân bổ / }).first()).toHaveValue("10.000");
    await pay.getByRole("button", { name: "Ghi phiếu thu" }).click();
    await expect(page.getByText(/^Đã ghi PT-/).first()).toBeVisible();
  });

  test("draws up a statement for a customer and sends it", async ({ page }) => {
    await openScreen(page, "/debts/statements");
    const party = await debtor(page);
    test.skip(party === undefined, "the seeded store has no customer with an open balance");
    if (party === undefined) return;

    await openScreen(page, `/debts/statements?party=${party.party_id}`);
    const create = page.getByRole("dialog", { name: "Lập bảng kê đối soát" });
    await create.getByRole("button", { name: "Lập bảng kê" }).click();
    const made = page.getByText(/^Đã lập DS-/).first();
    await expect(made).toBeVisible();
    const docNo = /DS-[\w-]+/.exec((await made.textContent()) ?? "")?.[0] ?? "";

    const drawer = page.getByRole("dialog", { name: `Bảng kê ${docNo} · v1` });
    await expect(drawer.getByText("Số dư đầu kỳ")).toBeVisible();
    await drawer.getByRole("button", { name: "Gửi khách" }).click();
    await expect(page.getByText("Đã gửi bảng kê. Bản đã gửi không bị thay đổi.").first()).toBeVisible();
  });

  test("reads the schedule and the advances of both ledgers", async ({ page }) => {
    await openScreen(page, "/debts/schedule");
    await expect(page.getByText("Dự kiến thu 7 ngày")).toBeVisible();
    await expect(page.getByRole("table", { name: "Lịch thu/trả 30 ngày tới" })).toBeVisible();

    await openScreen(page, "/debts/advances");
    await expect(page.getByRole("table", { name: "Ứng trước & cọc" })).toBeVisible();
  });
});
