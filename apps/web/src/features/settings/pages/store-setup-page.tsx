import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router";
import { ScreenError } from "../../../app/route-fallbacks";
import { ONBOARDING_PATH } from "../../../app/guards/onboarding-gate";
import { useApiClient } from "../../../shared/api/api-context";
import { onboardingQuery, useTenant } from "../../../shared/api/platform-queries";
import { Card } from "../../../shared/ui/card";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { StepStatusChip, StepTypeTag, orderedSteps, requiredProgress, type ChecklistStep } from "../../onboarding";

export const STORE_SETUP_LEAD = "Màn onboarding cho chủ cửa hàng mới. App tự chuyển tới màn này khi còn bước bắt buộc chưa xong; chỉ Danh mục được mở trong lúc đó.";

/** Khởi tạo cửa hàng (/settings/store-setup): the new-store checklist and the way back to the onboarding screen. */
export default function StoreSetupPage() {
  // OnboardingGate shares this query and lets the page through when it fails; refetching on mount
  // would turn it pending, swap the page for the gate's loading screen, and repeat.
  const onboarding = useQuery({ ...onboardingQuery(useApiClient()), retryOnMount: false });
  const tenant = useTenant();
  const steps = onboarding.data ? orderedSteps(onboarding.data.steps) : [];
  const confirmed = steps.find((s) => s.key === "confirm_opening_balances")?.status === "completed";
  const lock = { cutoff: tenant.data?.opening_cutoff_date ?? onboarding.data?.totals?.cutoff ?? null, confirmed };
  const progress = requiredProgress(steps);
  const columns: ReadonlyArray<Column<ChecklistStep>> = [
    { key: "step", header: "Bước", cell: (s) => `${steps.indexOf(s) + 1}. ${s.label}` },
    { key: "type", header: "Loại", cell: (s) => <StepTypeTag step={s} /> },
    { key: "status", header: "Trạng thái", cell: (s) => <StepStatusChip step={s} lock={lock} /> },
  ];
  return (
    <>
      <PageHeader
        lead={STORE_SETUP_LEAD}
        actions={
          <Link className="btn btn-primary" to={ONBOARDING_PATH}>
            Mở màn onboarding
          </Link>
        }
      />
      {onboarding.isPending ? (
        <ListLoading />
      ) : onboarding.isError ? (
        <ScreenError error={onboarding.error} onRetry={() => void onboarding.refetch()} />
      ) : (
        <Card title="Tiến độ cửa hàng mới" note={`${progress.done}/${progress.total} bước bắt buộc`}>
          <DataTable caption="Tiến độ cửa hàng mới" columns={columns} rows={steps} rowKey={(s) => s.key} flat />
        </Card>
      )}
    </>
  );
}
