import { Outlet } from "react-router";
import { useDocumentTitle } from "../../shared/hooks/use-document-title";
import { useRouteTitle } from "../../shared/hooks/use-route-meta";
import { BRAND_NAME, BRAND_TAGLINE } from "./brand";

/** Centered card for sign-in, sign-up, verification and password screens. */
export function AuthLayout() {
  useDocumentTitle(useRouteTitle());
  return (
    <div className="auth">
      <div className="auth-card">
        <div className="auth-brand">
          <b>{BRAND_NAME}</b>
          <span>{BRAND_TAGLINE}</span>
        </div>
        <main className="auth-body">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
