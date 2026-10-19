import { expect, test, type Locator, type Page } from "@playwright/test";
import { HOME_PATH } from "../src/app/menu";
import { addDays, todayBusinessDate } from "../src/shared/lib/dates";
import { fd } from "../src/shared/lib/format";
import { waitForMailLink } from "./support/mailpit";
import { submitLogin } from "./support/real-api";

// A new store set up from scratch against the running API and the dev mail catcher (make dev):
// sign-up, e-mail verification, then every onboarding step until the opening balances are
// confirmed and the dashboard opens. Each run creates its own store (unique e2e-* address).

const pageErrors: string[] = [];

test.beforeEach(({ page }) => {
  pageErrors.length = 0;
  page.on("pageerror", (err) => pageErrors.push(err.message));
});

test.afterEach(() => {
  expect(pageErrors, "uncaught errors in the page").toEqual([]);
});

const heading = (page: Page) => page.getByRole("heading", { level: 1 });
const step = (page: Page, name: string) => page.getByRole("region", { name });
const rail = (page: Page) => page.getByRole("navigation", { name: "Các bước thiết lập" });

/** The step the screen opened on its own; each finished step hands over to the next. */
async function expectStep(page: Page, name: string): Promise<Locator> {
  const region = step(page, name);
  await expect(region).toBeVisible();
  return region;
}

/** Completes a balance step that has nothing to enter through its "Không có …" confirmation. */
async function confirmNone(page: Page, region: Locator, what: string): Promise<void> {
  await region.getByRole("button", { name: `Không có ${what}` }).click();
  const dialog = page.getByRole("dialog", { name: `Xác nhận không có ${what}?` });
  await dialog.getByRole("button", { name: `Đúng, không có ${what}` }).click();
  await expect(page.getByText(`Đã ghi nhận không có ${what}.`)).toBeVisible();
}

test.describe("new store onboarding @root", () => {
  test.describe.configure({ mode: "serial" });

  const stamp = `${Date.now().toString(36)}${Math.floor(Math.random() * 1296).toString(36)}`;
  const slug = `e2e-ob-${stamp}`;
  const email = `e2e-ob-${stamp}@example.test`;
  const password = `mk-ob-${stamp}`;
  let storeOrigin = "";

  test("signs up a new store and verifies its e-mail", async ({ page, request }) => {
    const since = new Date(Date.now() - 2_000);
    await page.goto("/signup");
    await page.getByLabel(/^Họ tên/).fill("Kiểm Thử Thiết Lập");
    await page.getByLabel(/^Tên cửa hàng/).fill("Cửa hàng Thiết lập");
    const address = page.getByLabel(/^Địa chỉ cửa hàng/);
    await address.fill(slug);
    await expect(page.locator("#slug-status")).toHaveText("Địa chỉ này dùng được.");
    await page.getByLabel(/^Email/).fill(email);
    await page.getByLabel(/^Mật khẩu mới/).fill(password);
    await page.getByLabel(/^Nhập lại mật khẩu/).fill(password);
    await page.getByRole("button", { name: "Tạo cửa hàng" }).click();
    await expect(heading(page)).toHaveText("Kiểm tra email của bạn");
    storeOrigin = new URL((await page.locator(".auth-url").textContent()) ?? "").origin;

    const link = await waitForMailLink(request, email, "/verify", since);
    await page.goto(link.toString());
    await page.getByRole("button", { name: "Xác minh email" }).click();
    await expect(page).toHaveURL(`${storeOrigin}/login`);
  });

  test("completes the required steps, confirms the opening balances and reaches the dashboard", async ({ page }) => {
    test.skip(storeOrigin === "", "sign-up did not finish");
    await page.goto(`${storeOrigin}/login?next=${encodeURIComponent("/onboarding")}`);
    await submitLogin(page, email, password);
    await expect(page).toHaveURL(`${storeOrigin}/onboarding`);
    await expect(heading(page)).toHaveText("Thiết lập cửa hàng");

    // Store profile, with the cutoff on yesterday's business date.
    const cutoff = addDays(todayBusinessDate(), -1);
    const profile = await expectStep(page, "Thông tin cửa hàng");
    await profile.getByLabel(/^Tên pháp lý/).fill("Công ty TNHH Kiểm Thử Thiết Lập");
    await profile.getByLabel(/^Mã số thuế/).fill("0101234567");
    await profile.getByRole("textbox", { name: /^Ngày chốt/ }).fill(fd(cutoff));
    await profile.getByRole("button", { name: "Lưu và tiếp tục" }).click();
    await expect(page.getByText(`Đã lưu thông tin cửa hàng. Số dư đầu kỳ tính đến hết ngày ${fd(cutoff)}.`)).toBeVisible();

    // The catalog is entered later: skipped.
    const catalog = await expectStep(page, "Danh mục hàng hóa");
    await catalog.getByRole("button", { name: "Bỏ qua bước này" }).click();
    await expect(rail(page).getByRole("button", { name: /^Bước \d+:\s*Danh mục hàng hóa/ })).toContainText("Đã bỏ qua");

    // A new store has no stock and no debts yet.
    await confirmNone(page, await expectStep(page, "Tồn kho đầu kỳ"), "tồn kho đầu kỳ");
    await confirmNone(page, await expectStep(page, "Công nợ đầu kỳ"), "công nợ đầu kỳ");

    // The cash fund opens with 1.000.000 ₫; every fund is saved at once and the step completes.
    const cash = await expectStep(page, "Tiền đầu kỳ");
    await cash.getByRole("textbox", { name: "Tiền mặt tại cửa hàng" }).fill("1000000");
    await cash.getByRole("button", { name: "Lưu số dư các quỹ" }).click();
    await expect(page.getByText(/^Đã lưu số dư \d+ quỹ\.$/)).toBeVisible();

    // The confirmation shows the totals at the cutoff; the API asks for the password again only when
    // the sign-in is not recent.
    const confirm = await expectStep(page, "Xác nhận và khóa số dư");
    await expect(confirm.getByText(fd(cutoff), { exact: true })).toBeVisible();
    await expect(confirm.getByText("Tiền mặt tại cửa hàng", { exact: true }).locator("xpath=following-sibling::*[1]")).toHaveText(/^1\.000\.000\s₫$/);
    await confirm.getByRole("checkbox").check();
    await confirm.getByRole("button", { name: "Xác nhận số dư đầu kỳ" }).click();
    const reauth = page.getByRole("dialog", { name: "Nhập lại mật khẩu để khóa sổ" });
    const locked = page.getByText("Đã khóa số dư đầu kỳ. POS đã mở.");
    await expect(reauth.or(locked)).toBeVisible();
    if (await reauth.isVisible()) {
      await reauth.getByLabel("Mật khẩu").fill(password);
      await reauth.getByRole("button", { name: "Khóa sổ và xác nhận" }).click();
    }
    await expect(locked).toBeVisible();

    // The first sale is optional; the store is ready and opens on the dashboard.
    await expectStep(page, "Bán đơn đầu tiên");
    await expect(page.getByRole("heading", { name: "Cửa hàng đã sẵn sàng bán" })).toBeVisible();
    await page.getByRole("button", { name: "Vào cửa hàng" }).click();
    await expect(page).toHaveURL(`${storeOrigin}${HOME_PATH}`);
  });
});
