import { act, screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { renderApp } from "../test/render";
import { LEGACY_HASH_MAP, MOVED_PATHS } from "./legacy-redirects";
import { HOME_PATH, MENU, MENU_ROUTES, firstSubPath } from "./menu";
import { NOT_FOUND_TITLE } from "./route-fallbacks";
import { documentTitle } from "../shared/hooks/use-document-title";

async function landsOn(path: string, target: string, kind: "tenant" | "root" | "admin" = "tenant") {
  const { router } = renderApp(path, kind);
  // Pages are lazy chunks: the location moves once the target page has loaded.
  await waitFor(() => expect(router.state.location.pathname).toBe(target), { timeout: 10_000 });
  await screen.findByRole("heading", { level: 1 });
}

describe("store route tree", () => {
  it("has 41 menu routes with unique paths and view ids", () => {
    expect(MENU_ROUTES).toHaveLength(41);
    expect(new Set(MENU_ROUTES.map((r) => r.path)).size).toBe(41);
    expect(new Set(MENU_ROUTES.map((r) => r.viewId)).size).toBe(41);
  });

  it("renders every menu route inside the shell with its page header", async () => {
    const { router } = renderApp(HOME_PATH);
    for (const r of MENU_ROUTES) {
      await act(() => router.navigate(r.path));
      const h1 = await screen.findByRole("heading", { level: 1, name: r.sub.title });
      expect(h1).toBeInTheDocument();
      expect(router.state.location.pathname).toBe(r.path);
      expect(screen.getByRole("link", { name: r.sub.label, current: "page" })).toHaveAttribute("href", r.path);
      await waitFor(() => expect(document.title).toBe(documentTitle(r.sub.title)));
    }
  }, 30_000);

  it.each(MENU.map((g) => [`/${g.segment}`, firstSubPath(g)]))("redirects group path %s to %s", async (from, to) => {
    await landsOn(from, to);
  });

  it.each(MENU.map((g) => [`/${g.segment}/unknown-screen`, firstSubPath(g)]))("redirects unknown sub %s to %s", async (from, to) => {
    await landsOn(from, to);
  });

  it.each(Object.entries(MOVED_PATHS))("redirects moved path %s to %s", async (from, to) => {
    await landsOn(from, to);
  });

  it.each(Object.entries(LEGACY_HASH_MAP))("redirects legacy link /%s to %s", async (hash, to) => {
    await landsOn(`/${hash}`, to);
  });

  it("resolves a legacy view id through the menu", async () => {
    await landsOn("/#kho.the-kho", "/inventory/stock-card");
  });

  it("opens the dashboard at /", async () => {
    await landsOn("/", HOME_PATH);
  });

  it("has no admin routes on a store host", async () => {
    renderApp("/admin");
    expect(await screen.findByRole("heading", { name: NOT_FOUND_TITLE })).toBeInTheDocument();
  });

  it("renders 404 for unknown paths", async () => {
    renderApp("/no-such-page");
    expect(await screen.findByRole("heading", { name: NOT_FOUND_TITLE })).toBeInTheDocument();
  });
});

describe("root route tree", () => {
  it("sends / to sign-up", async () => {
    await landsOn("/", "/signup", "root");
  });

  it("has no store sign-in on the root host", async () => {
    renderApp("/login", "root");
    expect(await screen.findByRole("heading", { name: NOT_FOUND_TITLE })).toBeInTheDocument();
  });

  it("has no menu screens on the root host", async () => {
    renderApp("/inventory/stock", "root");
    expect(await screen.findByRole("heading", { name: NOT_FOUND_TITLE })).toBeInTheDocument();
  });
});

describe("admin route tree", () => {
  it("opens the admin console at /", async () => {
    const { router } = renderApp("/", "admin");
    await waitFor(() => expect(router.state.location.pathname).toBe("/admin/tenants"));
  });

  it("has no store screens on the admin host", async () => {
    renderApp("/overview/dashboard", "admin");
    expect(await screen.findByRole("heading", { name: NOT_FOUND_TITLE })).toBeInTheDocument();
  });
});

describe("browser tab names outside the shell", () => {
  it.each([
    ["/login", "tenant"],
    ["/forgot-password", "tenant"],
    ["/signup", "root"],
    ["/find-store", "root"],
    ["/admin/login", "admin"],
  ] as const)("names %s after its heading", async (path, kind) => {
    document.title = "";
    renderApp(path, kind);
    const h1 = await screen.findByRole("heading", { level: 1 }, { timeout: 10_000 });
    await waitFor(() => expect(document.title).toBe(documentTitle(h1.textContent)));
  });
});
