import { expect, test, type Page } from "@playwright/test";
import { HOME_PATH } from "../src/app/menu";
import { waitForMailLink } from "./support/mailpit";
import { OWNER, OWNER_PASSWORD_MISSING, apiPost, submitLogin } from "./support/real-api";
import { msUntilNextStep, totpCode } from "./support/totp";

// Sign-in, sign-up, e-mail verification, password reset and MFA against the running API and the
// dev mail catcher (make dev). Store-host journeys use the seeded store; the @root journey creates
// a new store per run (stores accumulate in the dev database, each with a unique e2e-* address).
// The API limits credential requests per client address, so journeys keep sign-ins to a minimum.

const pageErrors: string[] = [];

test.beforeEach(({ page }) => {
  pageErrors.length = 0;
  page.on("pageerror", (err) => pageErrors.push(err.message));
});

test.afterEach(() => {
  expect(pageErrors, "uncaught errors in the page").toEqual([]);
});

const heading = (page: Page) => page.getByRole("heading", { level: 1 });
const formAlert = (page: Page) => page.locator(".callout[role=alert]");
const notice = (page: Page) => page.locator(".callout[role=status]");

async function signOut(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Đăng xuất" }).click();
  // Any host: the store host of the seeded store or of a store created by the journey.
  await expect(page).toHaveURL(/^[^?#]*\/login$/);
  await expect(notice(page)).toHaveText("Đã đăng xuất.");
}

test.describe("store host", () => {
  test("signs in after a wrong password, returns to the page asked for, and signs out", async ({ page }) => {
    test.skip(OWNER.password === "", OWNER_PASSWORD_MISSING);
    await page.goto("/inventory/stock?tab=a");
    await expect(page).toHaveURL(`/login?next=${encodeURIComponent("/inventory/stock?tab=a")}`);
    await expect(heading(page)).toHaveText("Đăng nhập");
    await expect(page.getByLabel(/^Email/)).toBeFocused();

    await submitLogin(page, OWNER.email, `${OWNER.password}-wrong`);
    await expect(formAlert(page)).toHaveText("Email hoặc mật khẩu không đúng.");
    await expect(page.getByLabel(/^Mật khẩu/)).toHaveValue("");
    await expect(page.getByLabel(/^Mật khẩu/)).toBeFocused();

    await page.getByLabel(/^Mật khẩu/).fill(OWNER.password);
    await page.getByRole("button", { name: "Đăng nhập", exact: true }).click();
    await expect(page).toHaveURL("/inventory/stock?tab=a");
    await expect(page.locator("#view .ph h1")).toHaveText("Tồn kho");

    await signOut(page);
    await page.goto("/inventory/stock");
    await expect(page).toHaveURL(`/login?next=${encodeURIComponent("/inventory/stock")}`);
  });

  test("sends the owner back to sign-in, keeping the page, when the session ends on the server", async ({ page, context }) => {
    test.skip(OWNER.password === "", OWNER_PASSWORD_MISSING);
    await page.goto("/inventory/stock?tab=a");
    await submitLogin(page, OWNER.email, OWNER.password);
    await expect(page.locator("#view .ph h1")).toHaveText("Tồn kho");

    // End the session on the server while the browser keeps its (now dead) session cookie.
    const cookies = await context.cookies();
    await apiPost(page, "/auth/logout", {});
    await context.addCookies(cookies);

    await page.reload();
    await expect(page).toHaveURL(`/login?next=${encodeURIComponent("/inventory/stock?tab=a")}`);
    await expect(heading(page)).toHaveText("Đăng nhập");
  });

  test("checks the form before calling the API", async ({ page }) => {
    await page.goto("/login");
    await page.getByLabel(/^Email/).fill("chu@");
    await page.getByRole("button", { name: "Đăng nhập", exact: true }).click();
    await expect(page.getByText("Email không hợp lệ.")).toBeVisible();
    await expect(page.getByText("Nhập mật khẩu.")).toBeVisible();
  });

  test("sends the MFA step without a challenge back to sign-in", async ({ page }) => {
    await page.goto("/mfa");
    await expect(page).toHaveURL("/login");
  });

  test("has neither the admin console nor the sign-up form", async ({ page }) => {
    for (const path of ["/admin", "/admin/login", "/signup"]) {
      await page.goto(path);
      await expect(heading(page)).toHaveText("Không tìm thấy trang");
    }
  });
});

test.describe("new store @root", () => {
  test.describe.configure({ mode: "serial" });

  const stamp = `${Date.now().toString(36)}${Math.floor(Math.random() * 1296).toString(36)}`;
  const slug = `e2e-${stamp}`;
  const email = `e2e-${stamp}@example.test`;
  const firstPassword = `mk-dau-${stamp}`;
  const newPassword = `mk-moi-${stamp}`;
  let storeOrigin = "";
  let totpSecret = "";
  let totpUsedAt = 0;

  test("has no store sign-in on the root host", async ({ page }) => {
    await page.goto("/login");
    await expect(heading(page)).toHaveText("Không tìm thấy trang");
  });

  test("signs up with a live address check", async ({ page }) => {
    await page.goto("/");
    await expect(page).toHaveURL("/signup");
    await expect(page.getByLabel(/^Họ tên/)).toBeFocused();

    await page.getByLabel(/^Họ tên/).fill("Kiểm Thử Tự Động");
    await page.getByLabel(/^Tên cửa hàng/).fill("Cửa hàng A");
    const address = page.getByLabel(/^Địa chỉ cửa hàng/);
    await expect(address).toHaveValue("cua-hang-a");
    const status = page.locator("#slug-status");
    await expect(status).toContainText("Địa chỉ này đã có cửa hàng dùng.");
    await expect(status.getByRole("button", { name: /^Dùng “cua-hang-a-/ })).toBeVisible();

    await address.fill("admin");
    await expect(status).toContainText("Địa chỉ này được giữ lại cho hệ thống.");
    await address.fill("ab--c");
    await expect(status).toContainText("Từ 3 đến 30 ký tự");
    await address.fill(slug);
    await expect(status).toHaveText("Địa chỉ này dùng được.");

    await page.getByLabel(/^Email/).fill(email);
    await page.getByLabel(/^Mật khẩu mới/).fill(firstPassword);
    await page.getByLabel(/^Nhập lại mật khẩu/).fill(firstPassword);
    await page.getByRole("button", { name: "Tạo cửa hàng" }).click();

    await expect(heading(page)).toHaveText("Kiểm tra email của bạn");
    await expect(page.locator(".auth-url")).toContainText(`${slug}.`);
    storeOrigin = new URL((await page.locator(".auth-url").textContent()) ?? "").origin;
  });

  test("verifies the e-mail from the mailed link, then signs in", async ({ page, request }) => {
    test.skip(storeOrigin === "", "sign-up did not finish");
    const link = await waitForMailLink(request, email, "/verify", new Date(Date.now() - 120_000));
    expect(link.origin).toBe(storeOrigin);
    await page.goto(link.toString());
    await expect(heading(page)).toHaveText("Xác minh email");
    await page.getByRole("button", { name: "Xác minh email" }).click();
    await expect(page).toHaveURL(`${storeOrigin}/login`);
    await expect(notice(page)).toHaveText("Email đã được xác minh. Đăng nhập để vào cửa hàng.");

    await submitLogin(page, email, firstPassword);
    await expect(page).toHaveURL(`${storeOrigin}${HOME_PATH}`);
    await expect(page.locator(".util")).toContainText("Cửa hàng A");
  });

  test("resets a forgotten password from the mailed link", async ({ page, request }) => {
    test.skip(storeOrigin === "", "sign-up did not finish");
    await page.goto(`${storeOrigin}/login`);
    await page.getByRole("link", { name: "Quên mật khẩu?" }).click();
    await expect(heading(page)).toHaveText("Quên mật khẩu");
    const since = new Date(Date.now() - 2_000);
    await page.getByLabel(/^Email/).fill(email);
    await page.getByRole("button", { name: "Gửi liên kết đặt lại" }).click();
    await expect(notice(page)).toContainText(`Nếu ${email} có tài khoản ở cửa hàng này`);

    const link = await waitForMailLink(request, email, "/reset-password", since);
    await page.goto(link.toString());
    await page.getByLabel(/^Mật khẩu mới/).fill(newPassword);
    await page.getByLabel(/^Nhập lại mật khẩu/).fill(newPassword);
    await page.getByRole("button", { name: "Lưu mật khẩu mới" }).click();
    await expect(page).toHaveURL(`${storeOrigin}/login`);
    await expect(notice(page)).toHaveText("Đã đặt mật khẩu mới. Đăng nhập bằng mật khẩu mới.");

    // The link is single-use.
    await page.goto(link.toString());
    await page.getByLabel(/^Mật khẩu mới/).fill(newPassword);
    await page.getByLabel(/^Nhập lại mật khẩu/).fill(newPassword);
    await page.getByRole("button", { name: "Lưu mật khẩu mới" }).click();
    await expect(formAlert(page)).toHaveText("Liên kết không hợp lệ, đã được dùng hoặc đã hết hạn.");
  });

  test("turns on TOTP and signs in with the second step", async ({ page }) => {
    test.skip(storeOrigin === "", "sign-up did not finish");
    await page.goto(`${storeOrigin}/login?next=${encodeURIComponent("/sales/invoices")}`);
    await submitLogin(page, email, newPassword);
    await expect(page).toHaveURL(`${storeOrigin}/sales/invoices`);

    // No enrolment screen yet: the account API is driven directly with the browser's session.
    await apiPost(page, "/auth/reauth", { password: newPassword });
    const enrol = await apiPost<{ secret: string; otpauth_uri: string }>(page, "/auth/mfa/totp", {});
    expect(enrol.otpauth_uri).toMatch(/^otpauth:\/\/totp\//);
    totpSecret = enrol.secret;
    totpUsedAt = Date.now();
    const confirmed = await apiPost<{ recovery_codes: string[] }>(page, "/auth/mfa/totp/verify", { code: totpCode(totpSecret, totpUsedAt) });
    expect(confirmed.recovery_codes.length).toBeGreaterThan(0);

    await signOut(page);
    await page.goto(`${storeOrigin}/login?next=${encodeURIComponent("/sales/invoices")}`);
    await submitLogin(page, email, newPassword);
    await expect(page).toHaveURL(`${storeOrigin}/mfa`);
    await expect(heading(page)).toHaveText("Xác thực hai lớp");
    await expect(page.getByLabel(/^Mã xác thực/)).toBeFocused();

    // A code is accepted once: wait for the step after the one used to confirm the enrolment.
    const nextStepAt = totpUsedAt + msUntilNextStep(totpUsedAt);
    await page.waitForTimeout(Math.max(0, nextStepAt - Date.now()) + 500);
    await page.getByLabel(/^Mã xác thực/).fill(totpCode(totpSecret));
    await page.getByRole("button", { name: "Xác thực" }).click();
    await expect(page).toHaveURL(`${storeOrigin}/sales/invoices`);
  });
});
