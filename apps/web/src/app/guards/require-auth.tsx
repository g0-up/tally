import { Navigate, Outlet, useLocation } from "react-router";
import { isSessionExpired } from "../../shared/api/api-error";
import { useSession } from "../../shared/api/platform-queries";
import { RouteLoading, ScreenError } from "../route-fallbacks";
import { loginPathFor } from "./login-redirect";

/** Store routes need a session on this host; a 401 sends the visitor to /login?next=<path>. */
export function RequireAuth() {
  const session = useSession();
  const location = useLocation();
  if (isSessionExpired(session.error)) return <Navigate to={loginPathFor(location)} replace />;
  if (session.isPending) return <RouteLoading />;
  if (session.isError) return <ScreenError error={session.error} onRetry={() => void session.refetch()} />;
  return <Outlet />;
}
