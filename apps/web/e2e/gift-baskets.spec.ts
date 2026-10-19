import { expect, test, type Locator, type Page } from "@playwright/test";
import { OWNER, OWNER_PASSWORD_MISSING, OWNER_STATE_FILE, apiGet } from "./support/real-api";

// The gift-basket journey on the seeded store's real session: a kit of one product and one
// packaging material is created and published, an assembly order shows its shortage when planned
// past the stock, is planned for two kits, reserved and completed in part, its finished lot is
// traced back on the progress screen, and one kit is taken apart with its packaging written off.
// Every run uses a fresh kit code, so runs never collide.

interface Page_<T> {
  data: T[];
}
interface SkuRow {
  id: string;
  code: string;
  display_name: string;
  product_type: string;
  sellable: string;
  in_mix_group: boolean;
  on_hand_by_color: { color_id: string | null; qty: string }[];
}

/** Opens a store screen and waits until the shell and the screen header are rendered. */
async function openScreen(page: Page, path: string): Promise<void> {
  await page.goto(path);
  await expect(page.locator("#view .ph h1")).toBeVisible();
}

/**
 * An active SKU of the type with at least two units sellable, enough for two kits. It holds no
 * colored stock, so the order needs no color pick and the posting issues it first expiry first.
 */
async function stocked(page: Page, type: "goods" | "material"): Promise<SkuRow | undefined> {
  const r = await apiGet<Page_<SkuRow>>(page, `/skus?type=${type}&status=active&limit=200`);
  const colored = (s: SkuRow) => s.on_hand_by_color.some((c) => c.color_id !== null && Number(c.qty) > 0);
  return r.data.find((s) => Number(s.sellable) >= 2 && !s.in_mix_group && !colored(s));
}

/** Picks a SKU on a recipe line through the remote goods search and sets its quantity to 1. */
async function pickComponent(dialog: Locator, index: number, sku: SkuRow): Promise<void> {
  const box = dialog.getByRole("combobox", { name: `Thành phần ${index + 1}`, exact: true });
  await box.click();
  await box.fill(sku.code);
  await dialog.page().getByRole("option").filter({ hasText: sku.code }).first().click();
  await dialog.getByRole("textbox", { name: `Số lượng thành phần ${index + 1}`, exact: true }).fill("1");
}

const pageErrors: string[] = [];

test.beforeEach(({ page }) => {
  pageErrors.length = 0;
  page.on("pageerror", (err) => pageErrors.push(err.message));
});

test.afterEach(() => {
  expect(pageErrors, "uncaught errors in the page").toEqual([]);
});

test.describe("with the real API", () => {
  test.skip(OWNER.password === "", OWNER_PASSWORD_MISSING);
  test.use({ storageState: OWNER_STATE_FILE });

  test("creates and publishes a kit, assembles part of an order, traces the lot and takes a kit apart", async ({ page }) => {
    const stamp = Date.now().toString(36).toUpperCase();
    const code = `GQE2E${stamp}`;
    const name = `Giỏ e2e ${stamp}`;

    await openScreen(page, "/gift-baskets/recipes");
    const goods = await stocked(page, "goods");
    const material = await stocked(page, "material");
    test.skip(goods === undefined || material === undefined, "the seeded store has no uncolored goods and packaging material with two units sellable");
    if (goods === undefined || material === undefined) return;

    // Create and publish: one product and one packaging material per kit.
    await page.getByRole("button", { name: "Tạo giỏ / combo" }).click();
    const create = page.getByRole("dialog", { name: "Tạo giỏ quà / combo" });
    await create.getByRole("textbox", { name: "Mã" }).fill(code);
    await create.getByRole("textbox", { name: "Tên" }).fill(name);
    await create.getByRole("textbox", { name: "Giá bán lẻ" }).fill("500000");
    await pickComponent(create, 0, goods);
    await create.getByRole("button", { name: "+ Thêm thành phần" }).click();
    await pickComponent(create, 1, material);
    await expect(create).toContainText("Có thể đóng ngay");
    await create.getByRole("button", { name: "Lưu & phát hành" }).click();
    await expect(page.getByText(/^Đã tạo và phát hành/).first()).toContainText(name);
    await expect(create).toBeHidden();

    // Plan past the stock to see the shortage, then plan two kits.
    const card = page.locator(".card").filter({ has: page.getByRole("table", { name: `Thành phần ${name}` }) });
    await card.getByRole("button", { name: "Tạo lệnh đóng" }).click();
    const plan = page.getByRole("dialog", { name: "Tạo lệnh đóng giỏ" });
    await expect(plan.getByRole("combobox", { name: "Công thức" })).toContainText(name);
    const planned = plan.getByRole("textbox", { name: "Số giỏ kế hoạch" });
    await planned.fill(String(Math.min(Number(goods.sellable), Number(material.sellable)) + 1));
    const needs = plan.getByRole("table", { name: "Nguyên liệu cần cho lệnh đóng" });
    await expect(needs).toContainText("Thiếu");
    await planned.fill("2");
    await expect(needs).not.toContainText("Thiếu");
    await plan.getByRole("button", { name: "Tạo lệnh" }).click();
    const created = page.getByText(/^Đã tạo LD-/).first();
    await expect(created).toBeVisible();
    const docNo = /LD-[\w-]+/.exec((await created.textContent()) ?? "")?.[0] ?? "";

    // Reserve, then complete one of the two kits.
    await openScreen(page, "/gift-baskets/assembly-orders");
    const row = page.getByRole("row").filter({ has: page.getByRole("button", { name: docNo, exact: true }) });
    await row.getByRole("button", { name: "Chốt & giữ NL" }).click();
    await expect(page.getByText("Đã chốt lệnh và giữ nguyên liệu.").first()).toBeVisible();
    await row.getByRole("button", { name: "Ghi nhận hoàn thành" }).click();
    const done = page.getByRole("dialog", { name: `Ghi nhận hoàn thành ${docNo}` });
    const qty = done.getByRole("textbox", { name: /^Số giỏ hoàn thành/ });
    await expect(qty).toHaveValue("2");
    await qty.fill("1");
    await done.getByRole("button", { name: "Ghi sổ hoàn thành" }).click();
    await expect(page.getByText("Đã nhập giỏ thành phẩm.").first()).toBeVisible();
    await expect(row).toContainText("Đang đóng");

    // The finished lot traces back to the order and its component lots.
    await openScreen(page, "/gift-baskets/progress");
    const trace = page.getByRole("table", { name: "Truy xuất lô thành phần" });
    await expect(trace.getByRole("button", { name: docNo, exact: true })).toBeVisible();

    // Take the kit apart; its packaging is damaged and written off.
    await openScreen(page, "/gift-baskets/disassembly");
    await page.getByRole("button", { name: "Tháo giỏ" }).click();
    const apart = page.getByRole("dialog", { name: "Tháo giỏ quà" });
    const lot = apart.getByRole("combobox", { name: "Giỏ" });
    const value = await lot.locator("option").filter({ hasText: name }).first().getAttribute("value");
    await lot.selectOption(value ?? "");
    await apart.getByRole("textbox", { name: /^Số giỏ tháo/ }).fill("1");
    const reason = apart.getByRole("combobox", { name: /^Lý do tháo giỏ/ });
    const first = await reason.locator("option:not([value=''])").first().getAttribute("value");
    await reason.selectOption(first ?? "");
    await expect(apart.getByRole("checkbox", { name: "Giỏ mây, nơ bị hỏng khi tháo (không nhập lại tồn)" })).toBeChecked();
    await apart.getByRole("button", { name: "Ghi sổ tháo giỏ" }).click();
    await expect(page.getByText("Đã tháo giỏ, thành phần về kho theo lô gốc.").first()).toBeVisible();
    const slips = page.getByRole("table", { name: "Phiếu tháo giỏ" });
    await expect(slips.getByRole("row").filter({ hasText: name }).first()).toContainText("Hỏng – không nhập lại");
  });
});
