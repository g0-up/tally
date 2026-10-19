import { defineConfig, devices, type Project } from "@playwright/test";
import { hostURL } from "./e2e/visual/parity-states";
import { OWNER_STATE_FILE } from "./e2e/support/real-api";
import { EVIDENCE_DIR } from "./e2e/acceptance/fixtures/acceptance";

// One SPA bundle serves three host kinds (root, tenant, admin); each project points its baseURL at
// one of them. Dev hosts use *.localhost, which resolves to loopback without DNS.
//   E2E_ROOT_URL    root host            (default http://localhost:5173)
//   E2E_TENANT_URL  a seeded tenant host (default http://cua-hang-a.localhost:5173)
//   E2E_ADMIN_URL   admin host           (default http://admin.localhost:5173)
// Specs tag root-host and admin-host journeys with @root / @admin; untagged specs run on the tenant host.
// Journeys against the real API need `make dev` running; the seeded store's owner signs in with
// E2E_OWNER_EMAIL / E2E_OWNER_PASSWORD (see e2e/support/real-api.ts), once, in "owner-session".
const rootURL = hostURL("root");
const tenantURL = hostURL("tenant");
const adminURL = hostURL("admin");

const HOST_TAGS = /@root|@admin/;
// Parity and accessibility specs run in their own projects below, on the seeded store's saved session.
const OWNER_SESSION_SPECS = ["visual/**", "a11y/**"];
// Acceptance journeys run in their own project (make acceptance), with evidence written per day.
const ACCEPTANCE_SPECS = ["acceptance/**"];
const mobile390 = {
  ...devices["Desktop Chrome"],
  viewport: { width: 390, height: 844 },
  deviceScaleFactor: 3,
  isMobile: true,
  hasTouch: true,
};

const tenantProjects: Project[] = [
  { name: "chromium", use: { ...devices["Desktop Chrome"] } },
  { name: "firefox", use: { ...devices["Desktop Firefox"] } },
  { name: "webkit", use: { ...devices["Desktop Safari"] } },
  { name: "mobile-390", use: mobile390 },
].map((p) => ({
  ...p,
  testIgnore: [...OWNER_SESSION_SPECS, ...ACCEPTANCE_SPECS],
  grepInvert: HOST_TAGS,
  dependencies: ["owner-session"],
  use: { ...p.use, baseURL: tenantURL },
}));

export default defineConfig({
  testDir: "e2e",
  fullyParallel: false,
  workers: 1,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [["github"], ["html", { open: "never" }]] : [["list"]],
  use: {
    trace: "retain-on-failure",
    locale: "vi-VN",
    timezoneId: "Asia/Ho_Chi_Minh",
  },
  projects: [
    { name: "owner-session", testMatch: /auth\.setup\.ts$/, use: { ...devices["Desktop Chrome"], baseURL: tenantURL } },
    ...tenantProjects,
    {
      name: "root-chromium",
      testIgnore: ["visual/**"],
      grep: /@root/,
      use: { ...devices["Desktop Chrome"], baseURL: rootURL },
    },
    {
      name: "admin-chromium",
      testIgnore: ["visual/**"],
      grep: /@admin/,
      use: { ...devices["Desktop Chrome"], baseURL: adminURL },
    },
    {
      // Prototype parity screenshots. Prototype captures are rewritten on every run; additions are
      // compared with snapshots committed after design review. Thresholds live in visual/parity.spec.ts.
      name: "visual",
      testMatch: ["visual/**/*.spec.ts"],
      dependencies: ["owner-session"],
      use: { ...devices["Desktop Chrome"], baseURL: tenantURL, storageState: OWNER_STATE_FILE },
    },
    {
      // PRD §13 acceptance journeys on the seeded store's saved session. Traces and screenshots of each run
      // go to a dated folder under e2e/acceptance/evidence (git-ignored).
      name: "acceptance",
      testMatch: ["acceptance/**/*.spec.ts"],
      dependencies: ["owner-session"],
      outputDir: EVIDENCE_DIR,
      use: { ...devices["Desktop Chrome"], baseURL: tenantURL, storageState: OWNER_STATE_FILE, trace: "on", screenshot: "on" },
    },
    {
      // Axe, keyboard, focus and target-size checks. Firefox and WebKit run the functional projects only.
      name: "a11y",
      testMatch: ["a11y/**/*.spec.ts"],
      dependencies: ["owner-session"],
      use: { ...devices["Desktop Chrome"], baseURL: tenantURL, storageState: OWNER_STATE_FILE },
    },
    // Edge is the Chromium build Microsoft ships; it runs only on CI, where the msedge channel is installed.
    ...(process.env.CI
      ? [
          {
            name: "a11y-msedge",
            testMatch: ["a11y/**/*.spec.ts"],
            dependencies: ["owner-session"],
            use: { ...devices["Desktop Chrome"], channel: "msedge", baseURL: tenantURL, storageState: OWNER_STATE_FILE },
          },
        ]
      : []),
  ],
});
