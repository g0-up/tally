import { useState } from "react";
import type { Money } from "../../../shared/domain/money";
import { EXCEPTION_CHECKBOX, exceptionUncheckedMessage } from "../../../shared/hooks/use-confirm-exception";
import { diffDays } from "../../../shared/lib/dates";
import { dec, sumDec } from "../../../shared/lib/decimal";
import { vnd } from "../../../shared/lib/format";
import { Button } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { Checkbox } from "../../../shared/ui/checkbox";
import { Combobox } from "../../../shared/ui/combobox";
import { CLOSE_UNSETTLED_MESSAGE } from "../../../shared/hooks/create-once";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { Field } from "../../../shared/ui/field";
import { PayBox } from "../../../shared/ui/impact-summary";
import { Input } from "../../../shared/ui/input";
import { ListLoading } from "../../../shared/ui/list-footer";
import { MoneyInput } from "../../../shared/ui/money-input";
import { StatusPill } from "../../../shared/ui/status-pill";
import { toast } from "../../../shared/ui/toast-store";
import { PAYMENT_LABELS, invoicePayment, paymentCodeOf } from "../api/adapters";
import { useSaveInvoice } from "../api/invoice-mutations";
import { customerSearchSource, useColorMaps, useCustomer, useDebtLedger, useInvoice, useMixGroups, usePaymentMethods, useUnitNames } from "../api/queries";
import { DrawerStatus } from "../components/drawer-status";
import { draftProblem, draftToInput, linesToDraft, newItemLine, type LineDraft } from "../components/line-items/draft";
import { LineItemsEditor } from "../components/line-items/line-items-editor";
import { useLinePricing } from "../components/line-items/use-line-pricing";
import type { Invoice } from "../schemas";

export interface InvoiceDialogProps {
  /** The draft being edited; null creates a new invoice. */
  invoice: Invoice | null;
  onClose: () => void;
  onSaved: (invoice: Invoice) => void;
}

const OVERRIDE_LABEL = "Tôi xác nhận vẫn bán nếu vượt hạn mức nợ hoặc dưới giá tối thiểu (ghi vào nhật ký)";

interface InvoiceFormState {
  customer: { id: string; name: string } | null;
  /** Days of credit as typed; null follows the customer's payment terms. */
  terms: string | null;
  note: string;
  lines: LineDraft[];
  paid: Money | null;
  paymentMethodId: string | null;
  override: boolean;
}

const NO_GROUPS = [] as const;

/** Prototype `soModal`: create an invoice or edit a draft, optionally recording a payment and confirming it. */
export function InvoiceDialog({ invoice, onClose, onSaved }: InvoiceDialogProps) {
  const detail = useInvoice(invoice?.id ?? null);
  const colors = useColorMaps();
  const groups = useMixGroups();
  const customer = useCustomer(detail.data?.customer_party_id);

  if (invoice) {
    const title = `Sửa hóa đơn ${invoice.doc_no}`;
    const loaded = detail.data;
    const error = detail.error ?? customer.error ?? groups.error;
    if (!loaded || groups.data === undefined || customer.data === undefined) {
      return (
        <CommitDialog open onClose={onClose} title={title} wide>
          {error ? <DrawerStatus error={error} deletedText="Hóa đơn đã bị xóa." /> : <ListLoading />}
        </CommitDialog>
      );
    }
    const initial: InvoiceFormState = {
      customer: { id: loaded.customer_party_id, name: customer.data.name },
      terms: String(loaded.due_date ? Math.max(0, diffDays(loaded.due_date, loaded.invoice_date)) : 0),
      note: loaded.note ?? "",
      lines: linesToDraft(loaded.lines ?? [], colors, groups.data),
      paid: null,
      paymentMethodId: null,
      override: false,
    };
    return <InvoiceForm key={`${loaded.id}:${loaded.version}`} invoice={loaded} title={title} initial={initial} onClose={onClose} onSaved={onSaved} />;
  }

  const initial: InvoiceFormState = { customer: null, terms: null, note: "", lines: [newItemLine()], paid: null, paymentMethodId: null, override: false };
  return <InvoiceForm invoice={null} title="Tạo hóa đơn" initial={initial} onClose={onClose} onSaved={onSaved} />;
}

interface InvoiceFormProps {
  invoice: Invoice | null;
  title: string;
  initial: InvoiceFormState;
  onClose: () => void;
  onSaved: (invoice: Invoice) => void;
}

const TERMS = /^\d{1,3}$/;

function InvoiceForm({ invoice, title, initial, onClose, onSaved }: InvoiceFormProps) {
  const [form, setForm] = useState<InvoiceFormState>(initial);
  const colors = useColorMaps();
  const groups = useMixGroups().data ?? NO_GROUPS;
  const unitName = useUnitNames();
  const methods = usePaymentMethods().data ?? [];
  const partyId = form.customer?.id ?? null;
  const customer = useCustomer(partyId);
  const ledger = useDebtLedger(partyId);
  const pricing = useLinePricing({ lines: form.lines, partyId, groups, colors });
  const save = useSaveInvoice((s) => onSaved(s.invoice));

  const terms = form.terms ?? String(customer.data?.payment_terms_days ?? 0);
  const termsDays = TERMS.test(terms) ? Number(terms) : null;
  const methodId = form.paymentMethodId ?? methods[0]?.id ?? null;
  const total = pricing.totals?.total_amount ?? "0";
  const before = invoice ? invoicePayment(invoice).paid_amount : "0";
  const paid = sumDec([before, form.paid ?? "0"]);
  const remain = dec(total).minus(paid);
  const code = paymentCodeOf(total, paid.toFixed());

  const limit = customer.data?.credit_limit ?? null;
  const receivable = ledger.data?.receivable ?? "0";
  const exposure = dec(receivable).plus(remain.gt(0) ? remain : 0);
  const overLimit = limit !== null && (termsDays ?? 0) > 0 && remain.gt(0) && exposure.gt(limit);
  const belowMin = form.lines.filter((l) => pricing.belowMin(l.key));
  const breach = overLimit || belowMin.length > 0;

  const issues: string[] = [];
  if (overLimit && limit !== null) issues.push(`Vượt hạn mức nợ: ${vnd(exposure)} > ${vnd(limit)}.`);
  if (termsDays === 0 && remain.gt(0) && dec(total).gt(0)) issues.push("Khách thanh toán ngay nhưng còn thiếu tiền. Thu đủ trước khi giao hoặc đặt số ngày nợ.");
  if (belowMin.length > 0) issues.push(`${belowMin.length} dòng có giá sau chiết khấu dưới mức tối thiểu.`);
  if (remain.lt(0)) issues.push(`Khách trả thừa ${vnd(remain.negated())}. Phần thừa ghi thành tiền khách ứng trước.`);

  const problem = (): string | null => {
    if (form.customer === null) return "Chọn khách hàng.";
    if (termsDays === null) return "Số ngày nợ là số nguyên từ 0 đến 999.";
    if (form.paid !== null && dec(form.paid).gt(0) && methodId === null) return "Chọn quỹ nhận tiền khách trả.";
    const lines = draftProblem(form.lines, groups, unitName);
    if (lines !== null) return lines;
    return pricing.pending ? "Đang tính giá theo khách. Thử lại sau giây lát." : null;
  };

  const submit = (confirm: boolean) => {
    const message = problem();
    if (message !== null || form.customer === null || termsDays === null) {
      toast.commitError(message ?? "Chọn khách hàng.");
      return;
    }
    if (confirm && breach && !form.override) {
      toast.commitError(exceptionUncheckedMessage(EXCEPTION_CHECKBOX));
      return;
    }
    const lines = draftToInput(form.lines, { idOf: colors.idOf, listPrice: (k) => pricing.listPrice(k)?.amount ?? null, flavorPrice: pricing.flavorPrice, groups });
    void save.run({
      invoice,
      fields: { customerId: form.customer.id, termsDays, note: form.note, lines, paidNow: form.paid ?? "0", paymentMethodId: methodId },
      confirm,
      override: confirm && breach && form.override,
    });
  };

  const advances = sumDec((ledger.data?.advances ?? []).map((a) => a.amount));
  const customerHint = form.customer
    ? `Hạn mức ${limit !== null ? vnd(limit) : "không giới hạn"} · đang nợ ${vnd(receivable)} · ứng trước ${vnd(advances)}`
    : undefined;

  return (
    <CommitDialog
      open
      onClose={onClose}
      {...(save.unsettled ? { closeWarning: CLOSE_UNSETTLED_MESSAGE } : {})}
      title={title}
      {...(invoice ? { sub: `Phiên bản ${invoice.current_version} → ${invoice.current_version + 1}. Sửa xong có thể xác nhận bán ngay.` } : {})}
      wide
      pending={save.pending}
      onSubmit={() => submit(true)}
      submitLabel="Lưu & xác nhận bán"
      extra={
        <Button variant="navy" disabled={save.pending} onClick={() => submit(false)}>
          Lưu nháp
        </Button>
      }
      live={
        <>
          <PayBox
            items={[
              { label: "Tổng tiền", value: vnd(total) },
              { label: "Khách đã thanh toán", value: vnd(paid) },
              { label: "Còn lại", value: vnd(remain.gt(0) ? remain : 0) },
              {
                label: "Trạng thái thanh toán",
                // a new invoice of nothing has no payment to speak of yet
                value: !invoice && dec(total).isZero() && paid.isZero() ? "—" : <StatusPill label={PAYMENT_LABELS[code]} />,
              },
            ]}
          />
          {issues.length > 0 ? (
            <Callout tone="warn">
              {issues.map((i) => (
                <div key={i}>{i}</div>
              ))}
              {breach ? <b>Tích “{EXCEPTION_CHECKBOX}” bên trên nếu vẫn muốn xác nhận.</b> : null}
            </Callout>
          ) : null}
          <p className="hint">Xác nhận hóa đơn chỉ giữ hàng. Kho chỉ trừ và công nợ chỉ phát sinh khi xuất kho giao. Tiền khách trả trước được ghi nhận ngay và tự trừ vào công nợ khi giao.</p>
        </>
      }
    >
      <div className="fg">
        <Field label="Khách hàng" hint={customerHint}>
          <Combobox
            kind="party"
            source={customerSearchSource}
            value={form.customer?.id ?? null}
            valueLabel={form.customer?.name ?? ""}
            aria-label="Khách hàng"
            onChange={(item) => setForm((f) => ({ ...f, customer: { id: item.id, name: item.name }, terms: null, override: false }))}
          />
        </Field>
        <Field label="Số ngày nợ" hint="Mặc định theo điều khoản khách">
          <Input value={terms} inputMode="numeric" maxLength={3} onChange={(e) => setForm((f) => ({ ...f, terms: e.target.value.trim() }))} />
        </Field>
        <Field label="Ghi chú / lịch giao">
          <Input value={form.note} maxLength={1000} onChange={(e) => setForm((f) => ({ ...f, note: e.target.value }))} />
        </Field>
      </div>
      <LineItemsEditor preset="sell" lines={form.lines} onChange={(lines) => setForm((f) => ({ ...f, lines }))} pricing={pricing} groups={groups} colors={colors} unitName={unitName} />
      <div className="fg">
        <Field
          label={invoice ? "Khách trả thêm" : "Khách đã thanh toán"}
          hint={invoice && dec(before).gt(0) ? `Đã ghi nhận trước đó: ${vnd(before)}` : "Để 0 nếu khách chưa trả"}
        >
          <MoneyInput value={form.paid} placeholder="0" onChange={(paidNow) => setForm((f) => ({ ...f, paid: paidNow }))} />
        </Field>
        <Field label="Vào quỹ">
          <select className="in" value={methodId ?? ""} onChange={(e) => setForm((f) => ({ ...f, paymentMethodId: e.target.value }))}>
            {methods.map((m) => (
              <option key={m.id} value={m.id}>
                {m.name}
              </option>
            ))}
          </select>
        </Field>
      </div>
      {breach ? <Checkbox label={OVERRIDE_LABEL} checked={form.override} onChange={(e) => setForm((f) => ({ ...f, override: e.target.checked }))} /> : null}
    </CommitDialog>
  );
}
