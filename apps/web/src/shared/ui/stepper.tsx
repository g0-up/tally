import type { ReactNode } from "react";

export type StepState = "done" | "on" | "todo";

export interface StepperProps {
  steps: readonly string[];
  /** Number of finished steps; the step right after them is the current one (`.on`). */
  done: number;
  /** Mark the step at index `done` as current. Off when the flow ended or has no "in progress" step. */
  showCurrent?: boolean;
  label?: string;
  /** Trailing content such as a status pill for a failed delivery. */
  children?: ReactNode;
}

function stepState(index: number, done: number, showCurrent: boolean): StepState {
  if (index < done) return "done";
  if (showCurrent && index === done) return "on";
  return "todo";
}

/** Prototype `.steps`: horizontal step labels, finished ones filled navy, the current one outlined. */
export function Stepper({ steps, done, showCurrent = true, label = "Các bước", children }: StepperProps) {
  return (
    <div className="steps" role="list" aria-label={label}>
      {steps.map((s, i) => {
        const state = stepState(i, done, showCurrent);
        return (
          <span key={s} role="listitem" className={state === "todo" ? undefined : state} aria-current={state === "on" ? "step" : undefined}>
            {s}
          </span>
        );
      })}
      {children}
    </div>
  );
}
