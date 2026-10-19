import { expect, test as setup } from "@playwright/test";
import { HOME_PATH } from "../src/app/menu";
import { OWNER, OWNER_PASSWORD_MISSING, OWNER_STATE_FILE, submitLogin } from "./support/real-api";

// Signs the seeded store's owner in once through the real API and saves the browser state, so
// journeys that only need a session do not spend the API's sign-in rate limit.

setup("signs in as the seeded store's owner", async ({ page }) => {
  setup.skip(OWNER.password === "", OWNER_PASSWORD_MISSING);
  await page.goto("/login");
  await submitLogin(page, OWNER.email, OWNER.password);
  await expect(page).toHaveURL(HOME_PATH);
  await page.context().storageState({ path: OWNER_STATE_FILE });
});
