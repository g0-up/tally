import { useMemo, useState } from "react";
import type { ColorKey } from "../../../shared/domain/color";
import { useDebounce } from "../../../shared/hooks/use-debounce";
import { dec, sumDec } from "../../../shared/lib/decimal";
import { num, vnd } from "../../../shared/lib/format";
import { Button } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { Checkbox } from "../../../shared/ui/checkbox";
import { Combobox } from "../../../shared/ui/combobox";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { Field } from "../../../shared/ui/field";
import { ImpactSummary } from "../../../shared/ui/impact-summary";
import { ListLoading } from "../../../shared/ui/list-footer";
import { QtyInput } from "../../../shared/ui/qty-input";
import { toast } from "../../../shared/ui/toast-store";
import { lineName } from "../api/adapters";
import { translateSalesError } from "../api/mutations";
import { useColorMaps, usePaymentMethods, useReasons, useReturnableLines, useSalesDocument, useUnitNames } from "../api/queries";
import { useExchangePreview, usePostExchange, type ExchangeBody } from "../api/return-mutations";
import { draftProblem, draftToInput, newItemLine, type LineDraft } from "../components/line-items/draft";
import { LineItemsEditor } from "../components/line-items/line-items-editor";
import { PRICING_DEBOUNCE_MS, useLinePricing } from "../components/line-items/use-line-pricing";
import { ReturnColorCell } from "../components/return-color-cell";
import { returnLinesInput, returnRows, useReturnColorChoices, useSourceDocumentItems, type ReturnPick, type ReturnRow } from "../components/return-rows";
import type { Exchange, MixGroup } from "../schemas";

export interface ExchangeDialogProps {
  /** Source document to prefill (from the sale-document drawer); null picks the latest one. */
  documentId: string | null;
  onClose: () => void;
  onPosted: (posted: Exchange) => void;
}

const EMPTY_PICK: ReturnPick = { qty: null, color: null };
const NO_GROUPS: readonly MixGroup[] = [];
/** Reason preselected when the tenant keeps it in its return reasons. */
const DEFAULT_REASON = "Đổi màu / đổi mẫu";

/** Text of a refused preview (the server's sentence in Vietnamese). */
function previewErrorText(error: unknown): string {
  const e = translateSalesError(error);
  return e instanceof Error && e.message ? e.message : "Không tính được chênh lệch đổi hàng.";
}

/**
 * Prototype `newExchange` "Đổi hàng": the customer brings goods back against a sale document and
 * takes others (another color or another product). One command posts the return and the new sale;
 * only the difference is collected or paid back. The amounts come from the server preview and travel
 * with the command so a change in between is refused.
 */
export function ExchangeDialog({ documentId, onClose, onPosted }: ExchangeDialogProps) {
  const colors = useColorMaps();
  const unitName = useUnitNames();
  const docItems = useSourceDocumentItems();
  const [docChoice, setDocChoice] = useState<string | null>(documentId);
  const docId = docChoice ?? docItems[0]?.id ?? null;
  const doc = useSalesDocument(docId);
  const returnable = useReturnableLines(docId);
  const [picks, setPicks] = useState<ReadonlyMap<string, ReturnPick>>(new Map());
  const [quarantine, setQuarantine] = useState(false);
  const [reasonChoice, setReason] = useState<string | null>(null);
  const [methodChoice, setMethod] = useState<string | null>(null);
  const [lines, setLines] = useState<LineDraft[]>([]);
  const reasons = useReasons("sales_return").data ?? [];
  const methods = usePaymentMethods().data ?? [];
  const post = usePostExchange(onPosted);

  const rows = useMemo(() => returnRows(returnable.data ?? [], colors), [returnable.data, colors]);
  const pickKey = (r: ReturnRow) => `${docId ?? ""}:${r.line.sales_document_line_id}`;
  const pickOf = (r: ReturnRow) => picks.get(pickKey(r)) ?? EMPTY_PICK;
  const setPick = (r: ReturnRow, patch: Partial<ReturnPick>) => setPicks((m) => new Map(m).set(pickKey(r), { ...pickOf(r), ...patch }));
  const choicesOf = useReturnColorChoices(
    rows.map((r) => r.line.sku_id),
    colors,
  );

  const partyId = doc.data?.customer_party_id ?? null;
  const pricing = useLinePricing({ lines, partyId, groups: NO_GROUPS, colors });
  const reasonId = reasons.some((r) => r.id === reasonChoice) ? reasonChoice : (reasons.find((r) => r.name === DEFAULT_REASON)?.id ?? reasons[0]?.id ?? null);
  const methodId = methodChoice ?? methods[0]?.id ?? null;
  const restockTo = quarantine ? "quarantine" : "sellable";

  const returned = docId !== null ? returnLinesInput(rows, pickOf, choicesOf, colors, restockTo) : null;
  const taken = draftToInput(lines, { idOf: colors.idOf, listPrice: (k) => pricing.listPrice(k)?.amount ?? null, flavorPrice: pricing.flavorPrice, groups: NO_GROUPS });
  const body: ExchangeBody | null =
    docId !== null && reasonId !== null && returned !== null && "lines" in returned && taken.length > 0
      ? { original_sales_document_id: docId, reason_id: reasonId, return_lines: returned.lines, lines: taken, ...(methodId ? { payment_method_id: methodId } : {}) }
      : null;
  const bodyKey = body === null ? null : JSON.stringify(body);
  const settledKey = useDebounce(bodyKey, PRICING_DEBOUNCE_MS);
  const settled = bodyKey !== null && settledKey === bodyKey;
  const preview = useExchangePreview(settled ? body : null);
  const shown = body !== null ? preview.data : undefined;
  const current = settled && preview.data !== undefined && !preview.isPlaceholderData && !preview.isFetching ? preview.data : undefined;

  /** Adds the returned SKU to the lines taken, in the one other color when there is exactly one. */
  const swapColor = (r: ReturnRow) => {
    const pick = pickOf(r);
    if (pick.qty === null) return;
    const sold = r.color ?? pick.color;
    const others = choicesOf(r.line.sku_id).filter((c) => c !== sold);
    const color: ColorKey | null = others.length === 1 ? (others[0] ?? null) : null;
    setLines((ls) => [...ls, newItemLine({ skuId: r.line.sku_id, skuLabel: `${r.line.sku_code} · ${lineName(r.line)}`, unitId: r.line.unit_id, qty: pick.qty, color })]);
  };

  const problem = (): string | null => {
    if (docId === null) return "Chọn chứng từ bán gốc.";
    if (reasonId === null) return "Cần chọn lý do trả.";
    if (returned !== null && "problem" in returned) return returned.problem;
    if (lines.length === 0) return "Chọn hàng khách lấy đổi.";
    const taking = draftProblem(lines, NO_GROUPS, unitName);
    if (taking !== null) return taking;
    if (pricing.pending) return "Đang tính giá theo khách. Thử lại sau giây lát.";
    if (current !== undefined && dec(current.customer_pays_amount).gt(0) && methodId === null) return "Chọn quỹ thu phần khách trả thêm.";
    return null;
  };

  const submit = () => {
    const message = problem();
    if (message !== null || body === null) {
      toast.commitError(message ?? "Chọn hàng khách lấy đổi.");
      return;
    }
    if (preview.error && settled) {
      toast.commitError(previewErrorText(preview.error));
      return;
    }
    if (current === undefined) {
      toast.commitError("Đang tính chênh lệch đổi hàng. Thử lại sau giây lát.");
      return;
    }
    void post.run({ body, preview: current });
  };

  const columns: Column<ReturnRow>[] = [
    { key: "item", header: "Hàng", cell: (r) => lineName(r.line) },
    {
      key: "color",
      header: "Màu",
      cell: (r) => (
        <ReturnColorCell
          fixed={r.color}
          choices={r.color === null ? choicesOf(r.line.sku_id) : []}
          value={pickOf(r).color}
          onChange={(c) => setPick(r, { color: c })}
          base={r.line.unit_name}
          name={lineName(r.line)}
        />
      ),
    },
    { key: "max", header: "Còn được trả", numeric: true, cell: (r) => `${num(r.max)} ${r.line.unit_name}` },
    { key: "price", header: "Giá đã bán", numeric: true, cell: (r) => vnd(r.price) },
    {
      key: "qty",
      header: "SL trả",
      numeric: true,
      cell: (r) => (
        <QtyInput size="sm" style={{ width: 80 }} value={pickOf(r).qty} aria-label={`Số lượng trả – ${lineName(r.line)}`} onChange={(q) => setPick(r, { qty: q })} />
      ),
    },
    {
      key: "swap",
      header: "",
      cell: (r) => {
        const q = pickOf(r).qty;
        return q !== null && dec(q).gt(0) ? (
          <Button size="sm" variant="line" aria-label={`Đổi sang màu khác – ${lineName(r.line)}`} onClick={() => swapColor(r)}>
            Đổi sang màu khác
          </Button>
        ) : null;
      },
    },
  ];

  const returnedValue = sumDec(
    rows.map((r) => {
      const q = pickOf(r).qty;
      return q === null ? "0" : r.price.times(q);
    }),
  );
  const pays = shown !== undefined && dec(shown.customer_pays_amount).gt(0);
  const dash = "—";

  return (
    <CommitDialog
      open
      onClose={onClose}
      title="Đổi hàng"
      sub="Khách trả hàng theo chứng từ gốc và lấy hàng khác (khác màu hoặc khác mặt hàng). Phần mềm ghi phiếu trả và hóa đơn bán mới, chỉ thu hoặc trả khách phần chênh lệch."
      wide
      pending={post.pending}
      onSubmit={submit}
      submitLabel="Ghi sổ đổi hàng"
      live={
        <>
          <ImpactSummary
            items={[
              { label: "Giá trị hàng trả", value: shown ? vnd(shown.returned_amount) : vnd(returnedValue) },
              { label: "Giá trị hàng lấy", value: shown ? vnd(shown.taken_amount) : vnd(pricing.totals?.total_amount ?? "0") },
              pays && shown
                ? { label: "Khách trả thêm", value: vnd(shown.customer_pays_amount) }
                : { label: "Cửa hàng trả lại khách", value: shown ? vnd(shown.store_refunds_amount) : dash },
              { label: "Giảm nợ cũ của khách", value: shown ? vnd(shown.debt_reduction_amount) : dash },
            ]}
          />
          {preview.error && settled ? <Callout tone="bad">{previewErrorText(preview.error)}</Callout> : null}
        </>
      }
    >
      <div className="fg">
        <Field label="Chứng từ bán gốc">
          <Combobox
            items={docItems}
            value={docId}
            valueLabel={doc.data?.doc_no ?? ""}
            placeholder="Gõ số chứng từ hoặc tên khách…"
            aria-label="Chứng từ bán gốc"
            onChange={(item) => setDocChoice(item.id)}
          />
        </Field>
        <Field label="Lý do">
          <select className="in" value={reasonId ?? ""} onChange={(e) => setReason(e.target.value)}>
            {reasons.map((r) => (
              <option key={r.id} value={r.id}>
                {r.name}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Thu / trả chênh lệch qua">
          <select className="in" value={methodId ?? ""} onChange={(e) => setMethod(e.target.value)}>
            {methods.map((m) => (
              <option key={m.id} value={m.id}>
                {m.name}
              </option>
            ))}
          </select>
        </Field>
      </div>

      <h3>1. Hàng khách trả lại</h3>
      {returnable.isLoading ? (
        <ListLoading />
      ) : (
        <DataTable caption="Hàng khách trả lại" columns={columns} rows={rows} rowKey={(r) => r.line.sales_document_line_id} flat empty="Chứng từ này không còn hàng được trả." />
      )}
      <Checkbox
        label="Hàng trả bị lỗi, đưa vào khu cách ly (bỏ tích nếu hàng còn nguyên, nhập lại bán được)"
        checked={quarantine}
        onChange={(e) => setQuarantine(e.target.checked)}
      />

      <h3>2. Hàng khách lấy đổi</h3>
      <LineItemsEditor
        preset="sell"
        lines={lines}
        onChange={setLines}
        pricing={pricing}
        groups={NO_GROUPS}
        colors={colors}
        unitName={unitName}
        addLabel="+ Thêm hàng khác"
        allowMix={false}
        empty={<p className="hint">Bấm “Đổi sang màu khác” ở dòng hàng trả, hoặc thêm mặt hàng khác.</p>}
      />
    </CommitDialog>
  );
}
