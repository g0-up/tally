import { expect, type BrowserContext, type Page } from "@playwright/test";

// Journeys against the running API (make dev: API on :8080 behind the Vite proxy on :5173), as
// opposed to mock-api.ts. The seeded store "cua-hang-a" is the tenant host by default; its owner
// signs in with E2E_OWNER_EMAIL / E2E_OWNER_PASSWORD. The password is never stored in the repo:
// set it once (for example through "Quên mật khẩu" and the dev mail catcher) and pass it in.

export const OWNER = {
  email: process.env.E2E_OWNER_EMAIL ?? "chu-cua-hang-a@example.test",
  password: process.env.E2E_OWNER_PASSWORD ?? "",
};

export const OWNER_PASSWORD_MISSING = "E2E_OWNER_PASSWORD is not set: real sign-in journeys of the seeded store need the owner's password";

/** Saved signed-in browser state of the seeded store's owner, written by auth.setup.ts. */
export const OWNER_STATE_FILE = "test-results/.auth/owner.json";

/** Fills the store sign-in form and submits it. */
export async function submitLogin(page: Page, email: string, password: string): Promise<void> {
  await page.getByLabel(/^Email/).fill(email);
  await page.getByLabel(/^Mật khẩu/).fill(password);
  await page.getByRole("button", { name: "Đăng nhập", exact: true }).click();
}

/** CSRF token the API set for this host (dev cookie name; production uses the __Host- one). */
export async function csrfToken(context: BrowserContext, origin: string): Promise<string> {
  const cookies = await context.cookies(origin);
  const cookie = cookies.find((c) => c.name === "tally_csrf" || c.name === "__Host-tally_csrf");
  if (!cookie) throw new Error(`no CSRF cookie for ${origin}`);
  return decodeURIComponent(cookie.value);
}

/** GET from the API of the page's host with the session cookies. */
export async function apiGet<T>(page: Page, path: string): Promise<T> {
  const origin = new URL(page.url()).origin;
  const res = await page.request.get(`${origin}/api/v1${path}`);
  expect(res.ok(), `GET ${path} answered ${res.status()}: ${await res.text()}`).toBe(true);
  return (await res.json()) as T;
}

/** POST to the API of the page's host with the session cookies, the CSRF header and any extra headers (Idempotency-Key). */
export async function apiPost<T>(page: Page, path: string, body: unknown, headers: Record<string, string> = {}): Promise<T> {
  const origin = new URL(page.url()).origin;
  const res = await page.request.post(`${origin}/api/v1${path}`, {
    data: body,
    headers: { "X-CSRF-Token": await csrfToken(page.context(), origin), ...headers },
  });
  expect(res.ok(), `POST ${path} answered ${res.status()}: ${await res.text()}`).toBe(true);
  const text = await res.text();
  return (text ? JSON.parse(text) : undefined) as T;
}
