import { useEffect, useRef, useState, type ComponentType } from "react";
import { useNavigate } from "react-router";
import { HOME_PATH } from "../../../app/menu";
import { RouteLoading, ScreenError } from "../../../app/route-fallbacks";
import type { OnboardingStepKey } from "../../../shared/api/endpoints";
import { useOnboarding, useTenant } from "../../../shared/api/platform-queries";
import { useEscapeLayer } from "../../../shared/hooks/use-hotkeys";
import { fd } from "../../../shared/lib/format";
import { Button } from "../../../shared/ui/button";
import { PageHeader } from "../../../shared/ui/page-header";
import { StepFrame } from "../components/step-card";
import { StepRail } from "../components/step-rail";
import { OnboardingContext, type OnboardingScreen } from "../onboarding-context";
import { CatalogSetupStep } from "../steps/catalog-setup-step";
import { ConfirmOpeningStep } from "../steps/confirm-opening-step";
import { FirstSaleStep } from "../steps/first-sale-step";
import { OpeningCashStep } from "../steps/opening-cash-step";
import { OpeningDebtStep } from "../steps/opening-debt-step";
import { OpeningStockStep } from "../steps/opening-stock-step";
import { StoreProfileStep } from "../steps/store-profile-step";
import { firstOpenStep, orderedSteps, requiredProgress, type ChecklistStep } from "../steps";

const STEP_BODIES: Record<OnboardingStepKey, ComponentType> = {
  store_profile: StoreProfileStep,
  catalog_setup: CatalogSetupStep,
  opening_stock: OpeningStockStep,
  opening_debt: OpeningDebtStep,
  opening_cash: OpeningCashStep,
  confirm_opening_balances: ConfirmOpeningStep,
  first_sale: FirstSaleStep,
};

/** Prototype `#onboarding`: the required-step meter, the ready banner, the step rail and the open step. */
export default function OnboardingPage() {
  const onboarding = useOnboarding();
  const steps = orderedSteps(onboarding.data?.steps ?? []);
  const { done, total } = requiredProgress(steps);
  const pct = total ? Math.round((done / total) * 100) : 0;
  const progress = (
    <div className="onb-prog">
      <span className="k" id="onb-prog-k">
        Bước bắt buộc
      </span>
      <b className="tab-n">
        {done}
        <small> / {total} đã xong</small>
      </b>
      <div className="meter acc" role="progressbar" aria-labelledby="onb-prog-k" aria-valuemin={0} aria-valuemax={total} aria-valuenow={done} aria-valuetext={`${done} trên ${total} bước bắt buộc đã xong`}>
        <i style={{ width: `${pct}%` }} />
      </div>
    </div>
  );

  return (
    <>
      <PageHeader eyebrow="Bắt đầu" title="Thiết lập cửa hàng" lead={`Làm ${total} bước bắt buộc để mở bán. Bước tùy chọn có thể bỏ qua và làm sau.`} actions={progress} />
      {onboarding.isPending ? (
        <RouteLoading />
      ) : onboarding.isError ? (
        <ScreenError error={onboarding.error} onRetry={() => void onboarding.refetch()} />
      ) : (
        <OnboardingSteps steps={steps} ready={done === total} totals={onboarding.data.totals} />
      )}
    </>
  );
}

function OnboardingSteps({ steps, ready, totals }: { steps: readonly ChecklistStep[]; ready: boolean; totals: OnboardingScreen["totals"] }) {
  const navigate = useNavigate();
  const tenant = useTenant();
  // The first open step is chosen once: a posting that completes the step on the server keeps the
  // owner on it (several debts in a row) until they move on.
  const [opened, setOpened] = useState<OnboardingStepKey>(() => firstOpenStep(steps));
  const [railOpen, setRailOpen] = useState(false);
  const [moves, setMoves] = useState(0);
  const toggleRef = useRef<HTMLButtonElement>(null);

  const confirmStep = steps.find((s) => s.key === "confirm_opening_balances");
  const confirmed = confirmStep?.status === "completed";
  const cutoff = tenant.data?.opening_cutoff_date ?? totals?.cutoff ?? null;
  const lock = { cutoff, confirmed };

  const goTo = (key: OnboardingStepKey) => {
    setOpened(key);
    setRailOpen(false);
    setMoves((n) => n + 1);
  };

  // Prototype `onbGoTo`: the step title takes focus, and the card scrolls into view when it is off screen.
  useEffect(() => {
    if (moves === 0) return;
    const h = document.getElementById("onb-h");
    if (!h) return;
    h.focus({ preventScroll: true });
    const top = h.closest(".onb-card")?.getBoundingClientRect().top ?? 0;
    if (top < 70 || top > window.innerHeight * 0.6) window.scrollTo(0, Math.max(0, window.scrollY + top - 80));
  }, [moves]);

  useEscapeLayer(
    railOpen,
    () => {
      setRailOpen(false);
      toggleRef.current?.focus();
    },
    { blocking: false },
  );

  const active = steps.find((s) => s.key === opened) ?? steps[0];
  if (!active) return null;
  const Body = STEP_BODIES[active.key];
  const screen: OnboardingScreen = {
    ...lock,
    steps,
    cta: ready ? "navy" : "primary",
    totals,
    confirmedAt: confirmStep?.completed_at ?? null,
    goTo,
  };

  return (
    <OnboardingContext.Provider value={screen}>
      {ready ? (
        <section className="card onb-ready" aria-labelledby="onb-ready-h">
          <div>
            <h2 id="onb-ready-h">Cửa hàng đã sẵn sàng bán</h2>
            <p>Các bước bắt buộc đã xong và số dư đầu kỳ đã khóa đến hết ngày {fd(cutoff)}. Bán đơn đầu tiên ngay hoặc vào màn chính.</p>
          </div>
          <Button variant="primary" onClick={() => navigate(HOME_PATH)}>
            Vào cửa hàng
          </Button>
        </section>
      ) : null}
      <div className="onb-grid">
        <StepRail steps={steps} current={active.key} lock={lock} open={railOpen} onToggle={() => setRailOpen((o) => !o)} onGo={goTo} toggleRef={toggleRef} />
        <StepFrame key={active.key} step={active} steps={steps} lock={lock} onGo={goTo}>
          <Body />
        </StepFrame>
      </div>
    </OnboardingContext.Provider>
  );
}
