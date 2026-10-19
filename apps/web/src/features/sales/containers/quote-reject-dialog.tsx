import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { useQuoteTransition } from "../api/quote-mutations";
import type { Quote } from "../schemas";

export interface QuoteRejectDialogProps {
  quote: Quote;
  customerName: string;
  onClose: () => void;
}

/**
 * Prototype `qReject`: records that the customer declined the version shown. The API keeps no
 * rejection reason, so the dialog only confirms the transition.
 */
export function QuoteRejectDialog({ quote, customerName, onClose }: QuoteRejectDialogProps) {
  const reject = useQuoteTransition(() => onClose());
  return (
    <CommitDialog
      open
      onClose={onClose}
      title={`Khách từ chối ${quote.doc_no}`}
      sub={`${customerName} · phiên bản ${quote.revision_no}. Báo giá chuyển sang “Khách từ chối”; vẫn tạo được phiên bản mới.`}
      pending={reject.pending}
      submitLabel="Ghi nhận"
      submitVariant="danger"
      onSubmit={() => void reject.run({ quote, action: "reject" })}
    >
      <p className="hint">Thao tác được ghi nhật ký.</p>
    </CommitDialog>
  );
}
