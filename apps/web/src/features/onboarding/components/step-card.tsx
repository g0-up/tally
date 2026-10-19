import { Component, type ReactNode } from "react";
import type { OnboardingStepKey } from "../../../shared/api/endpoints";
import { cx } from "../../../shared/lib/cx";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { LinkButton } from "../../../shared/ui/link-button";
import { useStepCommand } from "../api/mutations";
import { nextStep, ONBOARDING_PHASES, stepStatusChip, type ChecklistStep, type LockState } from "../steps";

/** A step body that throws shows its error inside the card instead of blanking the screen. */
class StepBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  override state: { error: Error | null } = { error: null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  override render() {
    if (this.state.error) return <Callout tone="bad">Không hiển thị được bước này: {this.state.error.message}</Callout>;
    return this.props.children;
  }
}

/** Prototype `obStatusChip` as a `.st` pill. */
export function StepStatusChip({ step, lock }: { step: ChecklistStep; lock: LockState }) {
  const chip = stepStatusChip(step, lock);
  return <span className={cx("st", chip.tone)}>{chip.label}</span>;
}

export function StepTypeTag({ step }: { step: ChecklistStep }) {
  return step.skippable ? <span className="tag alt">Tùy chọn</span> : <span className="tag">Bắt buộc</span>;
}

export interface StepFrameProps {
  step: ChecklistStep;
  steps: readonly ChecklistStep[];
  lock: LockState;
  onGo: (key: OnboardingStepKey) => void;
  children: ReactNode;
}

/** Prototype `obCard`: eyebrow, title, note, type and status, the step body, then the step navigation. */
export function StepFrame({ step, steps, lock, onGo, children }: StepFrameProps) {
  const i = steps.findIndex((s) => s.key === step.key);
  const prev = steps[i - 1];
  const next = steps[i + 1];
  const skip = useStepCommand(step.key, "skip", { after: () => onGo(nextStep(step.key)) });
  return (
    <section className="card onb-card" aria-labelledby="onb-h">
      <div className="onb-sh">
        <div>
          <p className="eyebrow">
            Bước {i + 1}/{steps.length} · {ONBOARDING_PHASES[step.phase]}
          </p>
          <h2 id="onb-h" tabIndex={-1}>
            {step.label}
          </h2>
          <p>{step.note}</p>
        </div>
        <ButtonRow>
          <StepTypeTag step={step} />
          <StepStatusChip step={step} lock={lock} />
        </ButtonRow>
      </div>
      <div className="onb-body">
        <StepBoundary key={step.key}>{children}</StepBoundary>
      </div>
      <div className="onb-foot">
        <div>
          {prev ? (
            <Button size="sm" onClick={() => onGo(prev.key)}>
              ← Bước trước
            </Button>
          ) : null}
        </div>
        <ButtonRow className="onb-nav">
          {step.skippable && step.status === "pending" ? (
            <LinkButton disabled={skip.pending} aria-busy={skip.pending || undefined} onClick={() => void skip.run()}>
              Bỏ qua bước này
            </LinkButton>
          ) : null}
          {next ? (
            <Button size="sm" onClick={() => onGo(next.key)}>
              Bước tiếp →
            </Button>
          ) : null}
        </ButtonRow>
      </div>
    </section>
  );
}
