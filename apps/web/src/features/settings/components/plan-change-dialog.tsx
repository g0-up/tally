import { useRef, useState } from "react";
import { useApiClient } from "../../../shared/api/api-context";
import { platformKeys } from "../../../shared/api/query-keys";
import { useCommit } from "../../../shared/hooks/use-commit";
import { fd, vnd } from "../../../shared/lib/format";
import { Callout } from "../../../shared/ui/callout";
import { Checkbox } from "../../../shared/ui/checkbox";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { KeyValueGrid } from "../../../shared/ui/key-value-grid";
import { ListLoading } from "../../../shared/ui/list-footer";
import type { Plan } from "../../../shared/api/endpoints";
import { saasInvoiceSchema, usePlanChangeQuote, type SaasInvoice } from "../api/billing";
import { settingsKeys } from "../api/tenant-settings";
import { INVOICE_PURPOSE } from "../labels";
import { LoadError } from "./load-error";

export const ACCEPT_TERMS_LABEL = "Tôi đồng ý điều khoản gói";
export const PLAN_CHANGE_SUBMIT = "Đổi gói";
export const ADDON_BUY_SUBMIT = "Mua add-on";

/** What the issued invoice means: a free one is paid at once, so the change is already made. */
function changedText(inv: SaasInvoice, addon: boolean) {
  if (inv.status === "paid") {
    return (
      <>
        {addon ? "Đã bật add-on." : "Đã đổi gói."} Hóa đơn <b>{inv.number}</b> miễn phí nên đã được thanh toán.
      </>
    );
  }
  return (
    <>
      Đã tạo hóa đơn <b>{inv.number}</b>. {addon ? "Thanh toán để bật add-on." : "Thanh toán để đổi gói."}
    </>
  );
}

/**
 * Moving to another plan, or buying an add-on (a plan of kind `addon`): the server's quote
 * (period, amount, proration), the terms to accept, then `POST /subscription:change`, which issues
 * the invoice to pay. The request carries the intent's Idempotency-Key, so the retry after the
 * password prompt (`reauth_required`) or a lost answer issues no second invoice. Closing and opening
 * the same plan again is still that intent; another plan starts a new one.
 */
export function PlanChangeDialog({ plan, onClose }: { plan: Plan | null; onClose: () => void }) {
  const client = useApiClient();
  const quote = usePlanChangeQuote(plan?.id ?? null);
  const [accepted, setAccepted] = useState(false);
  const addon = plan?.kind === "addon";

  const close = () => {
    setAccepted(false);
    onClose();
  };

  const lastPlan = useRef<string | null>(null);
  const change = useCommit({
    mutationFn: (planId: string, { idempotencyKey }) => client.post("/subscription:change", { plan_id: planId, accept_terms: true }, { schema: saasInvoiceSchema, idempotencyKey }),
    success: (inv) => changedText(inv, addon),
    invalidate: [settingsKeys.invoices(), platformKeys.subscription(), platformKeys.entitlements()],
    after: close,
  });

  return (
    <CommitDialog
      open={plan !== null}
      onClose={close}
      title={plan ? (addon ? `${ADDON_BUY_SUBMIT} ${plan.name}` : `Đổi sang gói ${plan.name}`) : PLAN_CHANGE_SUBMIT}
      sub={addon ? "Giá và kỳ do máy chủ tính; add-on được bật khi hóa đơn được thanh toán." : "Giá và kỳ do máy chủ tính; gói mới có hiệu lực khi hóa đơn được thanh toán."}
      onSubmit={() => {
        if (!plan) return;
        if (lastPlan.current !== plan.id) change.resetKey();
        lastPlan.current = plan.id;
        void change.run(plan.id);
      }}
      submitLabel={addon ? ADDON_BUY_SUBMIT : PLAN_CHANGE_SUBMIT}
      submitDisabled={!accepted || !quote.data}
      pending={change.pending}
    >
      {quote.isPending ? (
        <ListLoading />
      ) : quote.error ? (
        <LoadError what="báo giá đổi gói" error={quote.error} />
      ) : (
        <>
          <KeyValueGrid
            items={[
              { label: "Gói", value: quote.data.plan.name },
              { label: "Loại hóa đơn", value: INVOICE_PURPOSE[quote.data.purpose] },
              { label: "Kỳ", value: `${fd(quote.data.period_start)} – ${fd(quote.data.period_end)}` },
              { label: "Số tiền", value: vnd(quote.data.amount) },
            ]}
          />
          {quote.data.proration_note ? <p className="hint">{quote.data.proration_note}</p> : null}
          <Callout tone="info" style={{ margin: "10px 0", whiteSpace: "pre-line" }}>
            {quote.data.terms}
          </Callout>
          <Checkbox label={ACCEPT_TERMS_LABEL} checked={accepted} onChange={(e) => setAccepted(e.target.checked)} />
        </>
      )}
    </CommitDialog>
  );
}
