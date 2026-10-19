import { Navigate, Outlet, useLocation } from "react-router";
import { useOnboarding } from "../../shared/api/platform-queries";
import { RouteLoading } from "../route-fallbacks";
import { hasBlockingOnboardingStep } from "./onboarding-steps";

export const ONBOARDING_PATH = "/onboarding";

/**
 * The screens the onboarding steps send the owner to (products, suppliers, price lists live under
 * Danh mục), reachable while a required step is still pending. Suppliers and their customer role
 * are edited at /catalog/suppliers; there is no separate partners route.
 */
const ONBOARDING_OPEN_PATHS: readonly string[] = ["/catalog"];

/** Whether the path is one of ONBOARDING_OPEN_PATHS or below it. */
function isOnboardingOpenPath(pathname: string): boolean {
  return ONBOARDING_OPEN_PATHS.some((p) => pathname === p || pathname.startsWith(`${p}/`));
}

/**
 * A new store finishes its required onboarding steps (opening balances and their confirmation)
 * before using the app; skippable steps never block, and the screens the steps link to stay open.
 * When the checklist cannot be read the app stays usable: the API still refuses what the store is
 * not ready for.
 */
export function OnboardingGate() {
  const onboarding = useOnboarding();
  const { pathname } = useLocation();
  if (isOnboardingOpenPath(pathname)) return <Outlet />;
  if (onboarding.isPending) return <RouteLoading />;
  if (onboarding.data && hasBlockingOnboardingStep(onboarding.data)) return <Navigate to={ONBOARDING_PATH} replace />;
  return <Outlet />;
}
