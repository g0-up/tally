import { Button, ButtonRow } from "../../../shared/ui/button";
import { DocNo } from "../../../shared/ui/doc-no";
import type { QuoteStatus } from "../schemas";

export type QuoteAction = "send" | "edit" | "delete" | "accept" | "reject" | "revise" | "convert" | "copy";

export interface QuoteActionsProps {
  status: QuoteStatus;
  /** Number of the invoice a converted quote became, when known. */
  invoiceDocNo?: string | null | undefined;
  /** The action in flight, shown busy; the other buttons are disabled meanwhile. */
  pending?: QuoteAction | null;
  onAction: (action: QuoteAction) => void;
}

/** Prototype `qActs`: the transitions a quote allows in its status, then "Sao chép". */
export function QuoteActions({ status, invoiceDocNo, pending = null, onAction }: QuoteActionsProps) {
  const busy = pending !== null;
  const act = (action: QuoteAction, label: string, variant: "navy" | "line" | "danger" = "line") => (
    <Button key={action} size="sm" variant={variant} pending={pending === action} disabled={busy} onClick={() => onAction(action)}>
      {label}
    </Button>
  );
  const acts =
    status === "draft"
      ? [act("send", "Gửi khách", "navy"), act("edit", "Sửa"), act("delete", "Xóa", "danger")]
      : status === "sent"
        ? [act("accept", "Khách chấp nhận", "navy"), act("reject", "Khách từ chối", "danger"), act("revise", "Phiên bản mới")]
        : status === "accepted"
          ? [act("convert", "Chuyển thành hóa đơn", "navy")]
          : status === "rejected"
            ? [act("revise", "Phiên bản mới")]
            : status === "expired"
              ? [act("revise", "Gia hạn / phiên bản mới"), act("reject", "Khách từ chối")]
              : [];
  return (
    <ButtonRow>
      {status === "converted" ? <DocNo value={invoiceDocNo} /> : null}
      {acts}
      {act("copy", "Sao chép")}
    </ButtonRow>
  );
}
