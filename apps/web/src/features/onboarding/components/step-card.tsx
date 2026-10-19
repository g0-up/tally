import type { ReactNode } from "react";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Card } from "../../../shared/ui/card";
import { StatusPill } from "../../../shared/ui/status-pill";
import { useStepCommand } from "../api/mutations";
import { STEP_STATUS_LABELS, STEP_STATUS_TONES, stepLabel, type ChecklistStep } from "../steps";

export const MARK_DONE = "Đánh dấu xong";
export const SKIP = "Bỏ qua";

/**
 * "Đánh dấu xong" on a step the owner completes by hand and "Bỏ qua" on a step the API lets them
 * skip, while the step is still pending.
 */
export function StepActions({ step, markDone = true }: { step: ChecklistStep; markDone?: boolean }) {
  const complete = useStepCommand(step.key, "complete");
  const skip = useStepCommand(step.key, "skip");
  if (step.status !== "pending") return null;
  const showDone = markDone && step.manual;
  if (!showDone && !step.skippable) return null;
  return (
    <ButtonRow>
      {showDone ? (
        <Button variant="primary" pending={complete.pending} disabled={skip.pending} onClick={() => void complete.run()}>
          {MARK_DONE}
        </Button>
      ) : null}
      {step.skippable ? (
        <Button pending={skip.pending} pendingLabel="Đang bỏ qua…" disabled={complete.pending} onClick={() => void skip.run()}>
          {SKIP}
        </Button>
      ) : null}
    </ButtonRow>
  );
}

/** One step's screen: its name and status above the body, the step commands below. */
export function StepCard({ step, note, children, markDone = true }: { step: ChecklistStep; note?: ReactNode; children: ReactNode; markDone?: boolean }) {
  return (
    <Card
      title={stepLabel(step.key)}
      note={note}
      actions={<StatusPill label={STEP_STATUS_LABELS[step.status]} tone={STEP_STATUS_TONES[step.status]} />}
      aria-label={stepLabel(step.key)}
      role="region"
    >
      {children}
      <StepActions step={step} markDone={markDone} />
    </Card>
  );
}
