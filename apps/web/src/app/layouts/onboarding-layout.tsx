import { Outlet } from "react-router";
import { useDocumentTitle } from "../../shared/hooks/use-document-title";
import { useRouteTitle } from "../../shared/hooks/use-route-meta";
import { BRAND_NAME, BRAND_TAGLINE } from "./brand";

/** Onboarding runs outside the app shell: no menu until the required steps are done. */
export function OnboardingLayout() {
  useDocumentTitle(useRouteTitle());
  return (
    <div className="onb">
      <header className="onb-top">
        <b>{BRAND_NAME}</b>
        <span>{BRAND_TAGLINE}</span>
      </header>
      <main className="view" id="view" tabIndex={-1}>
        <Outlet />
      </main>
    </div>
  );
}
