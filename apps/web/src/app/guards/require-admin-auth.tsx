import { Navigate, Outlet, useLocation } from "react-router";
import { isSessionExpired } from "../../shared/api/api-error";
import { useAdminSession } from "../../shared/api/platform-queries";
import { RouteLoading, ScreenError } from "../route-fallbacks";
import { ADMIN_LOGIN_PATH, loginPathFor } from "./login-redirect";

/** Admin console routes need the separate platform-admin session (password + TOTP). */
export function RequireAdminAuth() {
  const session = useAdminSession();
  const location = useLocation();
  if (isSessionExpired(session.error)) return <Navigate to={loginPathFor(location, ADMIN_LOGIN_PATH)} replace />;
  if (session.isPending) return <RouteLoading />;
  if (session.isError) return <ScreenError error={session.error} onRetry={() => void session.refetch()} />;
  return <Outlet />;
}
