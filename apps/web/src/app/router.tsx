import type { ComponentType } from "react";
import { Navigate, createBrowserRouter, type RouteObject } from "react-router";
import type { HostKind } from "../shared/api/endpoints";
import type { RouteMeta, RouteTitle } from "../shared/hooks/use-route-meta";
import { ONBOARDING_PATH, OnboardingGate } from "./guards/onboarding-gate";
import { RequireAdminAuth } from "./guards/require-admin-auth";
import { RequireAuth } from "./guards/require-auth";
import { AdminShell } from "./layouts/admin-shell";
import { AppShell } from "./layouts/app-shell";
import { AuthLayout } from "./layouts/auth-layout";
import { OnboardingLayout } from "./layouts/onboarding-layout";
import { MOVED_PATHS } from "./legacy-redirects";
import { MENU, MENU_ROUTES, firstSubPath, type MenuRoute } from "./menu";
import { EntitlementOutlet, RootRoute, TenantHome } from "./route-elements";
import { NotFoundPage, RouteErrorBoundary, RouteLoading } from "./route-fallbacks";

// One bundle, three route trees: the API tells main.tsx which kind of host this is and only that
// tree is mounted, so "/admin" on a store host or "/login" on the root host is a plain 404.

type PageModule = { default: ComponentType };

// Every page is its own chunk, loaded when its route is first opened.
const PAGE_MODULES = import.meta.glob<PageModule>("../features/*/pages/*-page.tsx");

/** Route `lazy` for `features/<feature>/pages/<page>-page.tsx`; a missing file fails loudly. */
function lazyPage(feature: string, page: string): () => Promise<{ Component: ComponentType }> {
  const file = `../features/${feature}/pages/${page}-page.tsx`;
  const load = PAGE_MODULES[file];
  if (!load) throw new Error(`Missing page module ${file}`);
  return async () => ({ Component: (await load()).default });
}

function routeMeta(r: MenuRoute): RouteMeta {
  return {
    viewId: r.viewId,
    eyebrow: r.group.label,
    title: r.sub.title,
    groupLabel: r.group.label,
    tabs: r.group.subs.map((s) => ({ to: `/${r.group.segment}/${s.segment}`, label: s.label })),
  };
}

/** The 40 menu screens plus, per group, "/group" and "/group/<unknown>" → first screen. */
function menuRoutes(): RouteObject[] {
  const pages: RouteObject[] = MENU_ROUTES.map((r) => {
    const page: RouteObject = {
      path: r.path,
      handle: routeMeta(r),
      lazy: lazyPage(r.sub.feature, r.sub.segment),
      errorElement: <RouteErrorBoundary />,
    };
    return r.sub.module ? { element: <EntitlementOutlet module={r.sub.module} />, children: [page] } : page;
  });
  const groupRedirects: RouteObject[] = MENU.flatMap((g) => [
    { path: `/${g.segment}`, element: <Navigate to={firstSubPath(g)} replace /> },
    { path: `/${g.segment}/*`, element: <Navigate to={firstSubPath(g)} replace /> },
  ]);
  return [...pages, ...groupRedirects];
}

/** Paths that moved keep working (query string and hash are dropped on purpose: old screens had none). */
function movedRoutes(): RouteObject[] {
  return Object.entries(MOVED_PATHS).map(([from, to]) => ({ path: from, element: <Navigate to={to} replace /> }));
}

/** A sign-in, sign-up or password screen inside the auth card, named after its heading for the tab. */
function authPage(path: string, page: string, title: string): RouteObject {
  const handle: RouteTitle = { title };
  return { path, handle, lazy: lazyPage("auth", page), errorElement: <RouteErrorBoundary /> };
}

function tenantRoutes(): RouteObject[] {
  return [
    {
      element: <AuthLayout />,
      children: [
        authPage("/login", "login", "Đăng nhập"),
        authPage("/verify", "verify", "Xác minh email"),
        authPage("/forgot-password", "forgot-password", "Quên mật khẩu"),
        authPage("/reset-password", "reset-password", "Đặt lại mật khẩu"),
        authPage("/mfa", "mfa", "Xác thực hai lớp"),
      ],
    },
    { index: true, element: <TenantHome /> },
    ...movedRoutes(),
    {
      element: <RequireAuth />,
      children: [
        {
          element: <OnboardingGate />,
          children: [
            {
              element: <AppShell />,
              children: [
                ...menuRoutes(),
                // Opened from the e-mailed link that confirms an account closure.
                { path: "/settings/account-closure", handle: { title: "Xác nhận đóng tài khoản" } satisfies RouteTitle, lazy: lazyPage("settings", "account-closure"), errorElement: <RouteErrorBoundary /> },
              ],
            },
            // Printable documents render without the shell.
            { path: "/print/sales-documents/:id", lazy: lazyPage("sales", "print-sales-document"), errorElement: <RouteErrorBoundary /> },
            { path: "/print/statements/:id", lazy: lazyPage("debt", "print-statement"), errorElement: <RouteErrorBoundary /> },
          ],
        },
        {
          element: <OnboardingLayout />,
          children: [{ path: ONBOARDING_PATH, handle: { title: "Thiết lập cửa hàng" } satisfies RouteTitle, lazy: lazyPage("onboarding", "onboarding"), errorElement: <RouteErrorBoundary /> }],
        },
      ],
    },
  ];
}

function rootRoutes(): RouteObject[] {
  return [
    { element: <AuthLayout />, children: [authPage("/signup", "signup", "Đăng ký cửa hàng"), authPage("/find-store", "find-store", "Tìm cửa hàng của bạn")] },
    { index: true, element: <Navigate to="/signup" replace /> },
  ];
}

/** Admin console pages under /admin: path relative to it and page module of features/admin. */
const ADMIN_PAGES: ReadonlyArray<readonly [path: string, page: string]> = [
  ["tenants", "tenants"],
  ["tenants/:id", "tenant"],
  ["usage", "usage"],
  ["invoices", "invoices"],
  ["incidents", "incidents"],
  ["feature-flags", "feature-flags"],
  ["support", "support"],
  ["support/tickets/:id", "support-ticket"],
  ["support/tenants/:tenantId/:module", "support-view"],
  ["partners", "partners"],
];

const ADMIN_HOME_PATH = "/admin/tenants";

function adminPage([path, page]: readonly [string, string]): RouteObject {
  return { path, lazy: lazyPage("admin", page), errorElement: <RouteErrorBoundary /> };
}

function adminRoutes(): RouteObject[] {
  return [
    { element: <AuthLayout />, children: [authPage("/admin/login", "admin-login", "Đăng nhập quản trị")] },
    { index: true, element: <Navigate to="/admin" replace /> },
    {
      path: "/admin",
      element: <RequireAdminAuth />,
      children: [{ element: <AdminShell />, children: [{ index: true, element: <Navigate to={ADMIN_HOME_PATH} replace /> }, ...ADMIN_PAGES.map(adminPage)] }],
    },
  ];
}

const TREES: Readonly<Record<HostKind, () => RouteObject[]>> = {
  root: rootRoutes,
  tenant: tenantRoutes,
  admin: adminRoutes,
};

/** Route tree of a host kind; any path outside it renders the 404 page. */
export function routesFor(kind: HostKind): RouteObject[] {
  return [
    {
      path: "/",
      element: <RootRoute />,
      errorElement: <RouteErrorBoundary />,
      HydrateFallback: RouteLoading,
      children: [...TREES[kind](), { path: "*", element: <NotFoundPage /> }],
    },
  ];
}

export function createAppRouter(kind: HostKind) {
  return createBrowserRouter(routesFor(kind));
}
