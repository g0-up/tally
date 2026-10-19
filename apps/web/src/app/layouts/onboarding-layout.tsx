import { Outlet } from "react-router";
import { LogoutButton } from "../../features/auth";
import { useSession } from "../../shared/api/platform-queries";
import { useDocumentTitle } from "../../shared/hooks/use-document-title";
import { useRouteTitle } from "../../shared/hooks/use-route-meta";
import { BRAND_NAME, BRAND_TAGLINE } from "./brand";

/** Onboarding runs outside the app shell: no menu until the required steps are done. */
export function OnboardingLayout() {
  useDocumentTitle(useRouteTitle());
  const session = useSession();
  const store = session.data?.tenant?.display_name;
  return (
    <div className="onb">
      <header className="onb-top">
        <div className="onb-brand">
          <b>{BRAND_NAME}</b>
          <span>{BRAND_TAGLINE}</span>
        </div>
        <span className="sp" />
        <span className="who">
          {store ? `${store} · ` : ""}Chủ cửa hàng <b>{session.data?.user.full_name}</b>
        </span>
        <LogoutButton className="onb-out" />
      </header>
      <main className="view" id="view" tabIndex={-1}>
        <Outlet />
      </main>
    </div>
  );
}
