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

/** Opens a step from the checklist and returns its screen. */
async function openStep(page: Page, name: string): Promise<Locator> {
  await page.getByRole("button", { name: new RegExp(`^(Chưa làm|Đã xong|Bỏ qua)\\s*${name}`) }).click();
  const region = step(page, name);
  await expect(region).toBeVisible();
  return region;
}

async function expectStatus(region: Locator, status: "Đã xong" | "Bỏ qua"): Promise<void> {
  // The status pill in the step's header; the step's own buttons are gone once it is not pending.
  await expect(region.getByText(status, { exact: true })).toBeVisible();
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
    const profile = await openStep(page, "Thông tin cửa hàng");
    await profile.getByLabel(/^Tên pháp lý/).fill("Công ty TNHH Kiểm Thử Thiết Lập");
    await profile.getByLabel(/^Mã số thuế/).fill("0101234567");
    await profile.getByRole("textbox", { name: "Ngày chốt số dư đầu kỳ" }).fill(fd(cutoff));
    await profile.getByRole("button", { name: "Lưu và đánh dấu xong" }).click();
    await expect(page.getByText("Đã lưu thông tin cửa hàng.")).toBeVisible();
    await expectStatus(profile, "Đã xong");

    // The catalog is entered later: skipped.
    const catalog = await openStep(page, "Danh mục hàng hóa");
    await catalog.getByRole("button", { name: "Bỏ qua" }).click();
    await expectStatus(catalog, "Bỏ qua");

    // A new store has no stock and no debts yet.
    for (const name of ["Tồn kho đầu kỳ", "Công nợ đầu kỳ"]) {
      const region = await openStep(page, name);
      await region.getByRole("button", { name: "Đánh dấu xong" }).click();
      await expectStatus(region, "Đã xong");
    }

    // The cash fund opens with 1.000.000 ₫; saving it completes the step.
    const cash = await openStep(page, "Tiền đầu kỳ");
    const amount = cash.getByRole("textbox", { name: "Số dư đầu kỳ Tiền mặt tại cửa hàng" });
    await amount.fill("1000000");
    await cash.getByRole("button", { name: "Lưu số dư Tiền mặt tại cửa hàng" }).click();
    await expect(page.getByText("Đã lưu số dư đầu kỳ của Tiền mặt tại cửa hàng.")).toBeVisible();
    await expectStatus(cash, "Đã xong");

    const sale = await openStep(page, "Bán đơn đầu tiên");
    await expect(sale.getByRole("button", { name: "Đánh dấu xong" })).toHaveCount(0);
    await sale.getByRole("button", { name: "Bỏ qua" }).click();
    await expectStatus(sale, "Bỏ qua");

    // The confirmation shows the totals at the cutoff, then asks for the password.
    const confirm = await openStep(page, "Xác nhận số dư đầu kỳ");
    await expect(confirm.getByText(fd(cutoff), { exact: true })).toBeVisible();
    await expect(confirm.getByText("Tiền mặt tại cửa hàng", { exact: true }).locator("xpath=following-sibling::*[1]")).toHaveText(/^1\.000\.000\s₫$/);
    await confirm.getByRole("checkbox").check();
    await confirm.getByRole("button", { name: "Xác nhận số dư đầu kỳ" }).click();
    const reauth = page.getByRole("dialog", { name: "Xác nhận lại mật khẩu" });
    await reauth.getByLabel("Mật khẩu").fill(password);
    await reauth.getByRole("button", { name: "Xác nhận" }).click();

    await expect(page).toHaveURL(`${storeOrigin}${HOME_PATH}`);
    await expect(page.getByText(`Đã xác nhận số dư đầu kỳ. Sổ sách đến hết ngày ${fd(cutoff)} đã khóa.`)).toBeVisible();
  });
});
