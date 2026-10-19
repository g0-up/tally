import { expect, test, type Locator, type Page } from "@playwright/test";
import { OWNER, OWNER_PASSWORD_MISSING, OWNER_STATE_FILE, apiGet } from "./support/real-api";

// Catalog journeys on the seeded store's real session. A mix group is built from two goods of the
// store that share a base unit, renamed, deactivated behind its confirmation, used again and
// deleted, so every run leaves the catalog as it found it. (Stopping a supplier that still has an
// open order is part of procurement.spec.ts, which creates that order.)

interface SkuRow {
  id: string;
  code: string;
  display_name: string;
  base_unit_id: string;
  in_mix_group: boolean;
}

/** Opens a store screen and waits until the shell and the screen header are rendered. */
async function openScreen(page: Page, path: string): Promise<void> {
  await page.goto(path);
  await expect(page.locator("#view .ph h1")).toBeVisible();
}

/** Two active goods with the same base unit, preferring ones in no mix group yet. */
async function twoFlavors(page: Page): Promise<[SkuRow, SkuRow] | null> {
  const r = await apiGet<{ data: SkuRow[] }>(page, "/skus?type=goods&status=active&limit=200");
  const byUnit = new Map<string, SkuRow[]>();
  for (const s of [...r.data].sort((a, b) => Number(a.in_mix_group) - Number(b.in_mix_group))) {
    byUnit.set(s.base_unit_id, [...(byUnit.get(s.base_unit_id) ?? []), s]);
  }
  for (const skus of byUnit.values()) {
    const [a, b] = skus;
    if (a && b) return [a, b];
  }
  return null;
}

/** Picks a SKU on a flavor row through the remote goods search. */
async function pickFlavor(dialog: Locator, index: number, sku: SkuRow): Promise<void> {
  const box = dialog.getByRole("combobox", { name: `Mặt hàng vị ${index + 1}`, exact: true });
  await box.click();
  await box.fill(sku.code);
  await dialog.page().getByRole("option").filter({ hasText: sku.code }).first().click();
  await expect(box).toHaveValue(`${sku.code} · ${sku.display_name}`);
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

  test("creates, renames, deactivates, reuses and deletes a mix group", async ({ page }) => {
    const stamp = Date.now().toString(36);
    const name = `Mix e2e ${stamp}`;
    const renamed = `Mix e2e ${stamp} (sửa)`;

    await openScreen(page, "/catalog/mix-groups");
    const flavors = await twoFlavors(page);
    test.skip(flavors === null, "the seeded store has no two active goods with the same base unit");
    if (flavors === null) return;
    const [first, second] = flavors;

    await page.getByRole("button", { name: "Thêm nhóm mix" }).click();
    const create = page.getByRole("dialog", { name: "Thêm nhóm mix vị" });
    await expect(create.getByRole("combobox", { name: "Đơn vị thùng" })).toHaveValue("thùng");
    await expect(create.getByRole("combobox", { name: "Đơn vị chọn vị" })).toHaveValue("dây");
    await create.getByRole("textbox", { name: "Tên nhóm mix" }).fill(name);
    await pickFlavor(create, 0, first);
    await pickFlavor(create, 1, second);
    await create.getByRole("textbox", { name: `Tên vị ngắn ${first.code}`, exact: true }).fill("Vị A");
    await create.getByRole("textbox", { name: `Tên vị ngắn ${second.code}`, exact: true }).fill("Vị B");
    await create.getByRole("button", { name: "Tạo nhóm" }).click();
    await expect(page.getByText(/^Đã lưu nhóm mix/).first()).toContainText(name);
    await expect(create).toBeHidden();

    const row = (groupName: string) => page.getByRole("row").filter({ has: page.getByText(groupName, { exact: true }) });
    await expect(row(name)).toContainText("Vị A");
    await expect(row(name)).toContainText("Vị B");
    await expect(row(name)).toContainText("Đang dùng");

    await row(name).getByRole("button", { name: "Sửa" }).click();
    const edit = page.getByRole("dialog", { name: "Sửa nhóm mix vị" });
    await expect(edit.getByRole("textbox", { name: "Tên nhóm mix" })).toHaveValue(name);
    await edit.getByRole("textbox", { name: "Tên nhóm mix" }).fill(renamed);
    await edit.getByRole("button", { name: "Lưu", exact: true }).click();
    await expect(edit).toBeHidden();
    await expect(row(renamed)).toHaveCount(1);

    await row(renamed).getByRole("button", { name: "Ngừng dùng" }).click();
    const stop = page.getByRole("dialog", { name: `Ngừng dùng nhóm mix ${renamed}?` });
    await expect(stop).toContainText("Chứng từ cũ vẫn giữ nguyên.");
    await stop.getByRole("button", { name: "Ngừng dùng" }).click();
    await expect(page.getByText("Đã ngừng dùng nhóm mix. Chứng từ cũ vẫn giữ nguyên.").first()).toBeVisible();
    await expect(stop).toBeHidden();
    await expect(row(renamed)).toContainText("Ngừng dùng");
    await expect(row(renamed).getByRole("button", { name: "Dùng lại" })).toBeVisible();

    await row(renamed).getByRole("button", { name: "Dùng lại" }).click();
    await expect(page.getByText("Đã dùng lại nhóm mix.").first()).toBeVisible();
    await expect(row(renamed).getByRole("button", { name: "Ngừng dùng" })).toBeVisible();

    await row(renamed).getByRole("button", { name: "Xóa" }).click();
    const remove = page.getByRole("dialog", { name: `Xóa nhóm mix ${renamed}?` });
    await remove.getByRole("button", { name: "Xóa", exact: true }).click();
    await expect(page.getByText("Đã xóa nhóm mix.").first()).toBeVisible();
    await expect(row(renamed)).toHaveCount(0);
  });
});
