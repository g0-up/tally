import { useState } from "react";
import { useBusinessDate } from "../../../shared/api/platform-queries";
import { addDays } from "../../../shared/lib/dates";
import { num, vnd } from "../../../shared/lib/format";
import { Button } from "../../../shared/ui/button";
import { Combobox } from "../../../shared/ui/combobox";
import { CLOSE_UNSETTLED_MESSAGE } from "../../../shared/hooks/create-once";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { DatePicker } from "../../../shared/ui/date-picker";
import { Field } from "../../../shared/ui/field";
import { ImpactSummary } from "../../../shared/ui/impact-summary";
import { Input } from "../../../shared/ui/input";
import { ListLoading } from "../../../shared/ui/list-footer";
import { toast } from "../../../shared/ui/toast-store";
import { useSaveQuote, type QuoteSaveMode } from "../api/quote-mutations";
import { customerSearchSource, useColorMaps, useCustomer, useMixGroups, useQuote, useUnitNames } from "../api/queries";
import { DrawerStatus } from "../components/drawer-status";
import { draftProblem, draftToInput, linesToDraft, newItemLine, type LineDraft } from "../components/line-items/draft";
import { LineItemsEditor } from "../components/line-items/line-items-editor";
import { useLinePricing } from "../components/line-items/use-line-pricing";
import type { Quote } from "../schemas";

export type QuoteDialogMode = "new" | "edit" | "revise";

export interface QuoteDialogProps {
  mode: QuoteDialogMode;
  /** The quote edited or revised; null for "new". */
  quote: Quote | null;
  onClose: () => void;
  onSaved: (quote: Quote) => void;
}

/** Validity of a new quote or a new version: ten days from today in the store's time zone. */
const QUOTE_VALID_DAYS = 10;

interface QuoteFormState {
  customer: { id: string; name: string } | null;
  validUntil: string | null;
  note: string;
  lines: LineDraft[];
}

const NO_GROUPS = [] as const;

/** Prototype `quoteModal`: create, edit a draft or issue a new version of a quote. */
export function QuoteDialog({ mode, quote, onClose, onSaved }: QuoteDialogProps) {
  const detail = useQuote(mode === "new" || quote === null ? null : quote.id);
  const colors = useColorMaps();
  const groups = useMixGroups();
  const customer = useCustomer(detail.data?.customer_party_id);
  const today = useBusinessDate();
  const title = mode === "new" ? "Tạo báo giá" : mode === "edit" ? `Sửa báo giá nháp ${quote?.doc_no ?? ""}` : `Phiên bản mới cho ${quote?.doc_no ?? ""}`;

  if (mode !== "new") {
    const loaded = detail.data;
    const error = detail.error ?? customer.error ?? groups.error;
    if (!loaded || groups.data === undefined || customer.data === undefined) {
      return (
        <CommitDialog open onClose={onClose} title={title} wide>
          {error ? <DrawerStatus error={error} deletedText="Báo giá đã bị xóa." /> : <ListLoading />}
        </CommitDialog>
      );
    }
    const initial: QuoteFormState = {
      customer: { id: loaded.customer_party_id, name: customer.data.name },
      validUntil: mode === "revise" ? addDays(today, QUOTE_VALID_DAYS) : loaded.valid_until,
      note: mode === "revise" ? "" : (loaded.note ?? ""),
      lines: linesToDraft(loaded.lines ?? [], colors, groups.data),
    };
    return <QuoteForm key={`${loaded.id}:${loaded.version}`} mode={mode} quote={loaded} title={title} initial={initial} onClose={onClose} onSaved={onSaved} />;
  }

  const initial: QuoteFormState = { customer: null, validUntil: addDays(today, QUOTE_VALID_DAYS), note: "", lines: [newItemLine()] };
  return <QuoteForm mode="new" quote={null} title={title} initial={initial} onClose={onClose} onSaved={onSaved} />;
}

interface QuoteFormProps {
  mode: QuoteDialogMode;
  quote: Quote | null;
  title: string;
  initial: QuoteFormState;
  onClose: () => void;
  onSaved: (quote: Quote) => void;
}

function QuoteForm({ mode, quote, title, initial, onClose, onSaved }: QuoteFormProps) {
  const [form, setForm] = useState<QuoteFormState>(initial);
  const today = useBusinessDate();
  const colors = useColorMaps();
  const groups = useMixGroups().data ?? NO_GROUPS;
  const unitName = useUnitNames();
  const partyId = form.customer?.id ?? null;
  const pricing = useLinePricing({ lines: form.lines, partyId, groups, colors });
  const retail = useLinePricing({ lines: form.lines, partyId: null, groups, colors, listOnly: true });
  const save = useSaveQuote((q) => onSaved(q));

  const problem = (): string | null => {
    if (form.customer === null) return "Chọn khách hàng.";
    if (form.validUntil === null) return "Nhập ngày hiệu lực đến.";
    if (form.validUntil < today) return "Ngày hiệu lực không được trước hôm nay.";
    if (mode === "revise" && !form.note.trim()) return "Ghi lý do thay đổi để lưu vào lịch sử phiên bản.";
    const lines = draftProblem(form.lines, groups, unitName);
    if (lines !== null) return lines;
    return pricing.pending ? "Đang tính giá theo khách. Thử lại sau giây lát." : null;
  };

  const submit = (saveMode: QuoteSaveMode) => {
    const message = problem();
    if (message !== null || form.customer === null || form.validUntil === null) {
      toast.commitError(message ?? "Chọn khách hàng.");
      return;
    }
    const lines = draftToInput(form.lines, { idOf: colors.idOf, listPrice: (k) => pricing.listPrice(k)?.amount ?? null, flavorPrice: pricing.flavorPrice, groups });
    void save.run({ mode: saveMode, quote, fields: { customerId: form.customer.id, validUntil: form.validUntil, note: form.note, lines } });
  };

  const priced = form.lines.filter((l) => (l.kind === "item" ? l.skuId !== null : true));
  return (
    <CommitDialog
      open
      onClose={onClose}
      {...(save.unsettled ? { closeWarning: CLOSE_UNSETTLED_MESSAGE } : {})}
      title={title}
      sub={mode === "revise" && quote ? `Hiện tại v${quote.revision_no}. Phiên bản cũ giữ trong lịch sử.` : "Tìm sản phẩm theo mã, tên hoặc mã vạch. Dùng “Thùng mix vị” để ghép nhiều vị trong một thùng."}
      wide
      pending={save.pending}
      onSubmit={() => submit(mode === "new" ? "new" : mode)}
      submitLabel={mode === "new" ? "Lưu & gửi khách" : mode === "edit" ? "Lưu nháp" : "Lưu & gửi phiên bản mới"}
      extra={
        mode === "new" ? (
          <Button variant="navy" disabled={save.pending} onClick={() => submit("draft")}>
            Lưu nháp
          </Button>
        ) : undefined
      }
      live={
        <ImpactSummary
          items={[
            { label: "Giá trị báo giá", value: vnd(pricing.totals?.total_amount ?? "0") },
            { label: "Số dòng", value: num(priced.length) },
            { label: "Giữ tồn", value: "Không" },
          ]}
        />
      }
    >
      <div className="fg">
        <Field label="Khách hàng">
          <Combobox
            kind="party"
            source={customerSearchSource}
            value={form.customer?.id ?? null}
            valueLabel={form.customer?.name ?? ""}
            aria-label="Khách hàng"
            onChange={(item) => setForm((f) => ({ ...f, customer: { id: item.id, name: item.name } }))}
          />
        </Field>
        <Field label="Hiệu lực đến">
          <DatePicker value={form.validUntil} onChange={(v) => setForm((f) => ({ ...f, validUntil: v }))} aria-label="Hiệu lực đến" />
        </Field>
        <Field label={mode === "revise" ? "Lý do thay đổi (bắt buộc)" : "Ghi chú"}>
          <Input value={form.note} maxLength={1000} onChange={(e) => setForm((f) => ({ ...f, note: e.target.value }))} />
        </Field>
      </div>
      <LineItemsEditor
        preset="quote"
        lines={form.lines}
        onChange={(lines) => setForm((f) => ({ ...f, lines }))}
        pricing={pricing}
        retail={retail}
        groups={groups}
        colors={colors}
        unitName={unitName}
      />
    </CommitDialog>
  );
}
