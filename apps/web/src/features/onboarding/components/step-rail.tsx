import type { Ref } from "react";
import type { OnboardingStepKey } from "../../../shared/api/endpoints";
import { fd } from "../../../shared/lib/format";
import { cx } from "../../../shared/lib/cx";
import { ONBOARDING_PHASES, stepLock, type ChecklistStep, type LockState } from "../steps";

/** Prototype `OB_CHECK`. */
export function CheckMark() {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M5 12l5 5L20 7" />
    </svg>
  );
}

function Chevron() {
  return (
    <svg className="onb-chev" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M6 9l6 6 6-6" />
    </svg>
  );
}

function phaseNote(phase: number, cutoff: string | null): string {
  if (phase === 1) return cutoff ? `Tính đến hết ngày ${fd(cutoff)}` : "Cần chọn ngày chốt trước";
  return phase === 2 ? "Sau khi khóa số dư" : "";
}

function stepNote(step: ChecklistStep, lock: string): string {
  if (step.status === "completed") return lock ? `Đã xong · ${lock.toLowerCase()}` : "Đã xong";
  if (step.status === "skipped") return "Đã bỏ qua";
  return lock || (step.skippable ? "Tùy chọn" : "Bắt buộc");
}

export interface StepRailProps {
  steps: readonly ChecklistStep[];
  current: OnboardingStepKey;
  lock: LockState;
  /** The step list is shown below the toggle (phone width). */
  open: boolean;
  onToggle: () => void;
  onGo: (key: OnboardingStepKey) => void;
  toggleRef: Ref<HTMLButtonElement>;
}

/** Prototype `obRail`: the seven steps in their three groups, collapsible on a phone. */
export function StepRail({ steps, current, lock, open, onToggle, onGo, toggleRef }: StepRailProps) {
  const n = steps.findIndex((s) => s.key === current) + 1;
  const cur = steps[n - 1];
  return (
    <nav className={cx("onb-rail", open && "open")} aria-label="Các bước thiết lập">
      <button type="button" className="onb-rail-tg" aria-expanded={open} aria-controls="onb-groups" onClick={onToggle} ref={toggleRef}>
        <span>
          Bước{" "}
          <b className="tab-n">
            {n}/{steps.length}
          </b>{" "}
          · {cur?.label}
        </span>
        <Chevron />
      </button>
      <div className="onb-groups" id="onb-groups">
        {ONBOARDING_PHASES.map((phase, pi) => {
          const note = phaseNote(pi, lock.cutoff);
          return (
            <div key={phase}>
              <div className="onb-ph" id={`onb-ph-${pi}`}>
                {phase}
                {note ? <small>{note}</small> : null}
              </div>
              <ol className="onb-steps" aria-labelledby={`onb-ph-${pi}`}>
                {steps.map((s, i) => {
                  if (s.phase !== pi) return null;
                  const lk = stepLock(s.key, lock);
                  const cls = s.status === "completed" ? "done" : s.status === "skipped" ? "skip" : lk ? "locked" : null;
                  return (
                    <li key={s.key}>
                      <button type="button" className={cx("onb-step", cls)} aria-current={s.key === current ? "step" : undefined} onClick={() => onGo(s.key)}>
                        <span className="onb-mk" aria-hidden="true">
                          {s.status === "completed" ? <CheckMark /> : i + 1}
                        </span>
                        <span>
                          <span className="sr-only">Bước {i + 1}: </span>
                          <b>{s.label}</b>
                          <small>{stepNote(s, lk)}</small>
                        </span>
                      </button>
                    </li>
                  );
                })}
              </ol>
            </div>
          );
        })}
      </div>
    </nav>
  );
}
