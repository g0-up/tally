import { HttpResponse, http } from "msw";
import { beforeEach, describe, expect, it } from "vitest";
import { createPlatform } from "../../../app/platform";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { bundleFresh, loadBundle, refreshBundle, saveBundle } from "./offline-bundle-repository";
import { resetOfflineDb } from "./testing/fake-offline-db";
import { OFFLINE_SESSION_ID, OFFLINE_TENANT_ID, PRICED_AT, bootstrapHandlers, offlineSessionHandler, testBundle } from "./testing/handlers";

const OWNER = { tenantId: OFFLINE_TENANT_ID, sessionId: OFFLINE_SESSION_ID };
const NOW = new Date("2026-10-18T09:00:00.000Z");

beforeEach(resetOfflineDb);

describe("refreshBundle", () => {
  it("stores the catalog, payment methods, settings and colors of this tenant and session", async () => {
    server.use(...bootstrapHandlers(), offlineSessionHandler("2026-10-19T08:00:00.000Z"));
    const bundle = await refreshBundle(createPlatform("tenant").client, OWNER, NOW);
    expect(bundle).toMatchObject({
      scope: `${OFFLINE_TENANT_ID}:${OFFLINE_SESSION_ID}`,
      version: '"boot-1"',
      cached_at: PRICED_AT,
      fetched_at: NOW.toISOString(),
      // 12 hours after the download comes before the server's window.
      expires_at: "2026-10-18T21:00:00.000Z",
    });
    expect(bundle?.tiles.map((t) => t.sku_id)).toEqual(["sku-keo", "sku-mut"]);
    expect(bundle?.colors).toHaveLength(1);
    expect(await loadBundle(OWNER)).toEqual(bundle);
  });

  it("expires with the server's offline window when it ends first", async () => {
    server.use(...bootstrapHandlers(), offlineSessionHandler("2026-10-18T11:00:00.000Z"));
    const bundle = await refreshBundle(createPlatform("tenant").client, OWNER, NOW);
    expect(bundle?.expires_at).toBe("2026-10-18T11:00:00.000Z");
  });

  it("drops the saved catalog when the store turned offline selling off", async () => {
    await saveBundle(testBundle());
    server.use(...bootstrapHandlers({ offline_pos_enabled: false }));
    expect(await refreshBundle(createPlatform("tenant").client, OWNER, NOW)).toBeNull();
    expect(await loadBundle(OWNER)).toBeNull();
  });

  it("drops it too when the offline window is refused as disabled", async () => {
    await saveBundle(testBundle());
    server.use(...bootstrapHandlers(), http.post(apiUrl("/pos/offline-session"), () => apiError(422, "offline_pos_disabled")));
    expect(await refreshBundle(createPlatform("tenant").client, OWNER, NOW)).toBeNull();
    expect(await loadBundle(OWNER)).toBeNull();
  });

  it("writes nothing when the owner signed out while the catalog was downloading", async () => {
    server.use(...bootstrapHandlers(), offlineSessionHandler("2026-10-19T08:00:00.000Z"));
    expect(await refreshBundle(createPlatform("tenant").client, OWNER, NOW, () => true)).toBeNull();
    expect(await loadBundle(OWNER)).toBeNull();

    // A store that turned offline selling off does not lose the catalog of a cancelled refresh either.
    const saved = testBundle();
    await saveBundle(saved);
    server.use(...bootstrapHandlers({ offline_pos_enabled: false }));
    await refreshBundle(createPlatform("tenant").client, OWNER, NOW, () => true);
    expect(await loadBundle(OWNER)).toEqual(saved);
  });

  it("keeps the saved catalog when the download fails", async () => {
    const saved = testBundle();
    await saveBundle(saved);
    server.use(http.get(apiUrl("/pos/bootstrap"), () => HttpResponse.error()));
    await expect(refreshBundle(createPlatform("tenant").client, OWNER, NOW)).rejects.toBeTruthy();
    expect(await loadBundle(OWNER)).toEqual(saved);
  });
});

describe("bundleFresh", () => {
  it("is sellable until its expiry instant", () => {
    const bundle = { expires_at: "2026-10-18T20:00:00.000Z" };
    expect(bundleFresh(bundle, new Date("2026-10-18T20:00:00.000Z"))).toBe(true);
    expect(bundleFresh(bundle, new Date("2026-10-18T20:00:00.001Z"))).toBe(false);
  });
});
