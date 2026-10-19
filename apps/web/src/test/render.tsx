import type { ReactElement } from "react";
import { render, type RenderResult } from "@testing-library/react";
import { RouterProvider, createMemoryRouter, type RouteObject } from "react-router";
import type { HostKind } from "../shared/api/endpoints";
import { createPlatform, type Platform } from "../app/platform";
import { QueryProvider } from "../app/providers/query-provider";
import { RootRoute } from "../app/route-elements";
import { routesFor } from "../app/router";

export interface RenderAppResult extends RenderResult {
  router: ReturnType<typeof createMemoryRouter>;
  platform: Platform;
}

/**
 * The app's platform with failed reads not retried: a test that expects an error sees it at once
 * instead of after the retry delay. A test of the retry itself turns it back on for its query.
 */
function testPlatform(kind: HostKind): Platform {
  const platform = createPlatform(kind);
  const defaults = platform.queryClient.getDefaultOptions();
  platform.queryClient.setDefaultOptions({ ...defaults, queries: { ...defaults.queries, retry: false } });
  return platform;
}

function renderRouter(routes: RouteObject[], path: string, kind: HostKind): RenderAppResult {
  const platform = testPlatform(kind);
  const router = createMemoryRouter(routes, { initialEntries: [path] });
  const result = render(
    <QueryProvider queryClient={platform.queryClient} client={platform.client}>
      <RouterProvider router={router} />
    </QueryProvider>,
  );
  return { ...result, router, platform };
}

/** The real route tree of a host kind at `path` (hash included), against the MSW handlers. */
export function renderApp(path: string, kind: HostKind = "tenant"): RenderAppResult {
  return renderRouter(routesFor(kind), path, kind);
}

/**
 * One component inside the same providers the app mounts (API client, query cache, router,
 * session dialogs, hotkeys and toaster), at `path`.
 */
export function renderWithProviders(ui: ReactElement, { path = "/", kind = "tenant" }: { path?: string; kind?: HostKind } = {}): RenderAppResult {
  return renderRouter([{ element: <RootRoute />, children: [{ path: "*", element: ui }] }], path, kind);
}
