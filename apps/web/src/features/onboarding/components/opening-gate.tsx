import { useState, type ReactNode } from "react";
import { fd } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { LinkButton } from "../../../shared/ui/link-button";
import { toast } from "../../../shared/ui/toast-store";
import { useStepCommand } from "../api/mutations";
import { useOnboardingScreen } from "../onboarding-context";
import { nextStep, stepLabel } from "../steps";

/** Prototype `obGate`: an opening balance is entered as of the cutoff date, so it waits for one. */
export function CutoffGate({ onOpenProfile }: { onOpenProfile: () => void }) {
  return (
    <Callout tone="warn" role="note">
      <b>Cần ngày chốt số dư trước.</b> Chọn ngày chốt ở bước Thông tin cửa hàng, số dư bước này tính đến hết ngày đó.{" "}
      <LinkButton onClick={onOpenProfile}>Đến Thông tin cửa hàng</LinkButton>
    </Callout>
  );
}

/** Prototype `obLocked`: the opening balances stop changing once confirmed. */
export function LockedCallout({ cutoff }: { cutoff: string | null }) {
  return (
    <Callout tone="ok" role="note">
      <b>Đã khóa sổ đến hết ngày {fd(cutoff)}.</b> Số dư đầu kỳ không sửa được nữa. Sai lệch sau ngày chốt được xử lý bằng chứng từ điều chỉnh.
    </Callout>
  );
}

/** Prototype `obEmpty`: dashed empty state with optional centered actions. */
export function OnbEmpty({ title, text, actions }: { title: string; text: string; actions?: ReactNode }) {
  return (
    <div className="onb-empty">
      <b>{title}</b>
      <span>{text}</span>
      {actions ? <ButtonRow style={{ justifyContent: "center", marginTop: 10 }}>{actions}</ButtonRow> : null}
    </div>
  );
}

const NONE_WHAT = { opening_stock: "tồn kho đầu kỳ", opening_debt: "công nợ đầu kỳ" } as const;

/**
 * Prototype `A.onbNone`: "Không có …" completes a balance step with nothing posted, after a
 * confirmation, and moves on to the next step.
 */
export function NoneButton({ step }: { step: keyof typeof NONE_WHAT }) {
  const { goTo } = useOnboardingScreen();
  const [open, setOpen] = useState(false);
  const what = `không có ${NONE_WHAT[step]}`;
  const complete = useStepCommand(step, "complete", {
    quiet: true,
    after: () => {
      toast.success(`Đã ghi nhận ${what}.`);
      setOpen(false);
      goTo(nextStep(step));
    },
  });
  return (
    <>
      <Button onClick={() => setOpen(true)}>Không có {NONE_WHAT[step]}</Button>
      <CommitDialog
        open={open}
        onClose={() => setOpen(false)}
        title={`Xác nhận ${what}?`}
        sub="Số dư đầu kỳ của mục này sẽ là 0 khi khóa sổ. Trước khi xác nhận vẫn có thể ghi thêm."
        onSubmit={() => void complete.run()}
        submitLabel={`Đúng, ${what}`}
        pending={complete.pending}
      >
        <Callout>
          Đánh dấu bước <b>{stepLabel(step)}</b> là đã xong với số dư 0.
        </Callout>
      </CommitDialog>
    </>
  );
}
