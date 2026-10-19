import { Navigate, Outlet, useLocation } from "react-router";
import { Toaster } from "../shared/ui/toaster";
import { RequireEntitlement } from "./guards/require-entitlement";
import { legacyRedirectTarget } from "./legacy-redirects";
import { HOME_PATH, type EntitlementModule } from "./menu";
import { HotkeysProvider } from "./providers/hotkeys-provider";
import { SessionProvider } from "./providers/session-provider";

// Small route elements of the router trees.

/** Layout route of a module sold separately: its pages render only when the plan includes it. */
export function EntitlementOutlet({ module }: { module: EntitlementModule }) {
  return (
    <RequireEntitlement module={module}>
      <Outlet />
    </RequireEntitlement>
  );
}

/** "/" of a store: an old "#mod.sub" link goes to its screen, anything else to the dashboard. */
export function TenantHome() {
  const { pathname, hash } = useLocation();
  return <Navigate to={legacyRedirectTarget(pathname, hash) ?? HOME_PATH} replace />;
}

/** Root route of every tree: session-wide dialogs, the global hotkey listener and the toaster. */
export function RootRoute() {
  return (
    <SessionProvider>
      <HotkeysProvider>
        <Outlet />
        <Toaster />
      </HotkeysProvider>
    </SessionProvider>
  );
}
