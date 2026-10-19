import type { ReactNode } from "react";
import { Link } from "react-router";
import { hasModule } from "../../shared/api/endpoints";
import { useEntitlements } from "../../shared/api/platform-queries";
import { Callout } from "../../shared/ui/callout";
import { PageHeader } from "../../shared/ui/page-header";
import { SUBSCRIPTION_PATH, type EntitlementModule } from "../menu";
import { RouteLoading } from "../route-fallbacks";

export const MODULE_NOT_IN_PLAN = "Gói thuê bao hiện tại chưa có tính năng này.";

/**
 * Screens of a module sold separately render only when the plan includes it; otherwise the page
 * explains how to get it. If entitlements cannot be read the page renders and the API decides.
 */
export function RequireEntitlement({ module, children }: { module: EntitlementModule; children: ReactNode }) {
  const entitlements = useEntitlements();
  if (entitlements.isPending) return <RouteLoading />;
  if (entitlements.data && !hasModule(entitlements.data, module)) {
    return (
      <>
        <PageHeader />
        <Callout tone="warn" role="status">
          <b>{MODULE_NOT_IN_PLAN}</b> Nâng cấp gói để dùng màn hình này; dữ liệu đã có vẫn được giữ nguyên. <Link to={SUBSCRIPTION_PATH}>Xem gói thuê bao</Link>
        </Callout>
      </>
    );
  }
  return <>{children}</>;
}
