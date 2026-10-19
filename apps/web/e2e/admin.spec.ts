import { expect, test, type Page } from "@playwright/test";
import { msUntilNextStep, totpCode } from "./support/totp";

// Platform-admin console journeys against the running API on the admin host. The admin signs in
// with E2E_ADMIN_EMAIL / E2E_ADMIN_PASSWORD and answers the TOTP step from E2E_ADMIN_TOTP_SECRET
// (base32, the secret of its authenticator enrolment); none of them is stored in the repo. The
// journeys act on the seeded store E2E_ADMIN_TENANT_SLUG and put it back as they found it.

const ADMIN = {
  email: process.env.E2E_ADMIN_EMAIL ?? "",
  password: process.env.E2E_ADMIN_PASSWORD ?? "",
  totpSecret: process.env.E2E_ADMIN_TOTP_SECRET ?? "",
};
const SLUG = process.env.E2E_ADMIN_TENANT_SLUG ?? "cua-hang-a";
const FLAG_KEY = "e2e.admin-console";

const CREDENTIALS_MISSING = "E2E_ADMIN_EMAIL, E2E_ADMIN_PASSWORD and E2E_ADMIN_TOTP_SECRET must be set for the admin console journeys";

test.use({ baseURL: "http://admin.localhost:5173" });

/** Password then TOTP; waits for a fresh time step when the current one is about to end. */
async function signIn(page: Page): Promise<void> {
  await page.goto("/admin/login");
  await page.getByLabel(/^Email/).fill(ADMIN.email);
  await page.getByLabel(/^Mật khẩu/).fill(ADMIN.password);
  await page.getByRole("button", { name: "Tiếp tục" }).click();
  const code = page.getByLabel(/^Mã xác thực/);
  await expect(code).toBeVisible();
  if (msUntilNextStep() < 3000) await page.waitForTimeout(msUntilNextStep() + 200);
  await code.fill(totpCode(ADMIN.totpSecret));
  await page.getByRole("button", { name: "Xác thực" }).click();
  await expect(page).toHaveURL(/\/admin\/tenants$/);
}

/** The seeded store's detail page, found through the slug search. */
async function openStore(page: Page): Promise<void> {
  await page.getByRole("link", { name: "Cửa hàng", exact: true }).click();
  await page.getByRole("searchbox", { name: "Tìm theo tên hoặc địa chỉ cửa hàng" }).fill(SLUG);
  const row = page.getByRole("table", { name: "Danh sách cửa hàng" }).getByRole("row").filter({ hasText: `${SLUG}.` });
  await expect(row).toHaveCount(1);
  await row.getByRole("link").first().click();
  await expect(page).toHaveURL(/\/admin\/tenants\/[0-9a-f-]+$/);
}

test.describe("platform admin console @admin", () => {
  test.describe.configure({ mode: "serial" });
  test.skip(ADMIN.email === "" || ADMIN.password === "" || ADMIN.totpSecret === "", CREDENTIALS_MISSING);

  let page: Page;

  test.beforeAll(async ({ browser }) => {
    page = await browser.newPage({ baseURL: "http://admin.localhost:5173" });
    await signIn(page);
  });

  test.afterAll(async () => {
    await page.close();
  });

  test("finds a store by its slug", async () => {
    await page.getByRole("searchbox", { name: "Tìm theo tên hoặc địa chỉ cửa hàng" }).fill(SLUG);
    const table = page.getByRole("table", { name: "Danh sách cửa hàng" });
    await expect(table.getByRole("link", { name: new RegExp(`^https?://${SLUG}\\.`) })).toBeVisible();
  });

  test("suspends the store after typing its slug, then reactivates it", async () => {
    await openStore(page);
    await page.getByRole("button", { name: "Tạm ngưng" }).click();
    const dialog = page.getByRole("dialog");
    const submit = dialog.getByRole("button", { name: "Tạm ngưng" });
    await expect(submit).toBeDisabled();
    await dialog.getByLabel(/Gõ địa chỉ cửa hàng/).fill(SLUG);
    await submit.click();
    await expect(page.getByText(`Đã tạm ngưng ${SLUG}: cửa hàng chỉ còn xem.`)).toBeVisible();
    await expect(page.getByText("Chỉ xem").first()).toBeVisible();

    await page.getByRole("button", { name: "Kích hoạt lại" }).click();
    await page.getByRole("dialog").getByRole("button", { name: "Kích hoạt lại" }).click();
    await expect(page.getByText(`Đã kích hoạt lại ${SLUG}.`)).toBeVisible();
  });

  test("sets a flag for the store and switches it back off", async () => {
    await openStore(page);
    await page.getByRole("button", { name: "Đặt cờ khác" }).click();
    const dialog = page.getByRole("dialog");
    await dialog.getByLabel(/^Mã cờ/).fill(FLAG_KEY);
    await dialog.getByRole("checkbox", { name: "Bật" }).check();
    await dialog.getByRole("button", { name: "Lưu cờ" }).click();
    await expect(page.getByText(`Đã bật cờ ${FLAG_KEY} cho ${SLUG}.`)).toBeVisible();

    const row = page.getByRole("table", { name: "Cờ tính năng của cửa hàng" }).getByRole("row").filter({ hasText: FLAG_KEY });
    await expect(row).toContainText("Bật (riêng)");
    await row.getByRole("button", { name: `Tắt ${FLAG_KEY} cho ${SLUG}` }).click();
    await expect(page.getByText(`Đã tắt cờ ${FLAG_KEY} cho ${SLUG}.`)).toBeVisible();
    await expect(row).toContainText("Tắt (riêng)");
  });
});
