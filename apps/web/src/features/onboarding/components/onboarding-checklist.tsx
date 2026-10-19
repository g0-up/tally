import type { OnboardingStepKey } from "../../../shared/api/endpoints";
import { Card } from "../../../shared/ui/card";
import { StatusPill } from "../../../shared/ui/status-pill";
import { TodoList } from "../../../shared/ui/todo-list";
import { STEP_STATUS_LABELS, STEP_STATUS_TONES, stepLabel, type ChecklistStep } from "../steps";

export const CHECKLIST_TITLE = "Việc cần làm để bắt đầu";

/**
 * Onboarding checklist card. Each row says whether the step is required (the API's `skippable`
 * flag) and opens that step's screen.
 */
export function OnboardingChecklist({ steps, onOpen }: { steps: readonly ChecklistStep[]; onOpen: (key: OnboardingStepKey) => void }) {
  const left = steps.filter((s) => s.status === "pending").length;
  return (
    <Card title={CHECKLIST_TITLE} note={left === 0 ? "Đã hoàn tất" : `Còn ${left} bước`}>
      <TodoList
        items={steps.map((s) => ({
          key: s.key,
          lead: <StatusPill label={STEP_STATUS_LABELS[s.status]} tone={STEP_STATUS_TONES[s.status]} />,
          title: stepLabel(s.key),
          sub: s.skippable ? "Có thể bỏ qua" : "Bắt buộc trước khi dùng cửa hàng",
          onClick: () => onOpen(s.key),
        }))}
      />
    </Card>
  );
}
