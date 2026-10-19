import { useState } from "react";
import { useNavigate } from "react-router";
import { HOME_PATH } from "../../../app/menu";
import { RouteLoading, ScreenError } from "../../../app/route-fallbacks";
import type { OnboardingStepKey } from "../../../shared/api/endpoints";
import { useOnboarding, useTenant } from "../../../shared/api/platform-queries";
import { Button } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { PageHeader } from "../../../shared/ui/page-header";
import { Stepper } from "../../../shared/ui/stepper";
import { OnboardingChecklist } from "../components/onboarding-checklist";
import { CatalogSetupStep } from "../steps/catalog-setup-step";
import { ConfirmOpeningStep } from "../steps/confirm-opening-step";
import { FirstSaleStep } from "../steps/first-sale-step";
import { OpeningCashStep } from "../steps/opening-cash-step";
import { OpeningDebtStep } from "../steps/opening-debt-step";
import { OpeningStockStep } from "../steps/opening-stock-step";
import { StoreProfileStep } from "../steps/store-profile-step";
import { firstOpenStep, leadingFinished, orderedSteps, requiredStepsDone, stepLabel, type ChecklistStep } from "../steps";

export const READY_TEXT = "Các bước bắt buộc đã xong. Cửa hàng sẵn sàng sử dụng.";
export const ENTER_STORE = "Vào cửa hàng";

/** Onboarding: the seven steps as a stepper, the checklist, and the screen of the step opened. */
export default function OnboardingPage() {
  const navigate = useNavigate();
  const onboarding = useOnboarding();
  const tenant = useTenant();
  const [opened, setOpened] = useState<OnboardingStepKey | null>(null);
  const steps = orderedSteps(onboarding.data?.steps ?? []);
  const open = (key: OnboardingStepKey) => setOpened(key);
  // The first open step is chosen once: a posting that completes the step on the server keeps the
  // owner on it (several debts in a row) until they pick another one.
  if (opened === null && onboarding.data) setOpened(firstOpenStep(steps));

  const body = () => {
    if (onboarding.isPending) return <RouteLoading />;
    if (onboarding.isError) return <ScreenError error={onboarding.error} onRetry={() => void onboarding.refetch()} />;
    const active = steps.find((s) => s.key === (opened ?? firstOpenStep(steps)));
    const confirmed = steps.some((s) => s.key === "confirm_opening_balances" && s.status === "completed");
    return (
      <>
        {requiredStepsDone(steps) ? (
          <Callout tone="ok" role="status">
            {READY_TEXT}{" "}
            <Button variant="navy" size="sm" onClick={() => navigate(HOME_PATH)}>
              {ENTER_STORE}
            </Button>
          </Callout>
        ) : null}
        <OnboardingChecklist steps={steps} onOpen={open} />
        {active ? (
          <StepBody
            step={active}
            steps={steps}
            cutoff={tenant.data?.opening_cutoff_date ?? onboarding.data.totals?.cutoff ?? null}
            confirmed={confirmed}
            totals={onboarding.data.totals}
            lockedThrough={onboarding.data.locked_through}
            onOpen={open}
          />
        ) : null}
      </>
    );
  };

  return (
    <>
      <PageHeader eyebrow="Bắt đầu" title="Thiết lập cửa hàng" lead="Hoàn tất các bước bắt buộc để mở cửa hàng. Bước có thể bỏ qua không chặn việc sử dụng." />
      <Stepper steps={steps.map((s) => stepLabel(s.key))} done={leadingFinished(steps)} label="Các bước thiết lập" />
      {body()}
    </>
  );
}

interface StepBodyProps {
  step: ChecklistStep;
  steps: readonly ChecklistStep[];
  cutoff: string | null;
  confirmed: boolean;
  totals: Parameters<typeof ConfirmOpeningStep>[0]["totals"];
  lockedThrough: string | null;
  onOpen: (key: OnboardingStepKey) => void;
}

function StepBody({ step, steps, cutoff, confirmed, totals, lockedThrough, onOpen }: StepBodyProps) {
  const openProfile = () => onOpen("store_profile");
  switch (step.key) {
    case "store_profile":
      return <StoreProfileStep step={step} confirmed={confirmed} />;
    case "catalog_setup":
      return <CatalogSetupStep step={step} />;
    case "opening_stock":
      return <OpeningStockStep step={step} cutoff={cutoff} confirmed={confirmed} onOpenProfile={openProfile} />;
    case "opening_debt":
      return <OpeningDebtStep step={step} cutoff={cutoff} confirmed={confirmed} onOpenProfile={openProfile} />;
    case "opening_cash":
      return <OpeningCashStep step={step} cutoff={cutoff} confirmed={confirmed} onOpenProfile={openProfile} />;
    case "first_sale":
      return <FirstSaleStep step={step} />;
    case "confirm_opening_balances":
      return <ConfirmOpeningStep step={step} steps={steps} totals={totals} lockedThrough={lockedThrough} onOpen={onOpen} />;
  }
}
