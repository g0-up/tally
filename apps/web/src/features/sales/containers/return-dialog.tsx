import { useMemo, useState } from "react";
import { useQueries } from "@tanstack/react-query";
import { useApiClient } from "../../../shared/api/api-context";
import type { ColorKey } from "../../../shared/domain/color";
import type { Money } from "../../../shared/domain/money";
import type { Qty } from "../../../shared/domain/qty";
import { dec, sumDec, toDecimalString, type Decimal } from "../../../shared/lib/decimal";
import { num, vnd } from "../../../shared/lib/format";
import { Button } from "../../../shared/ui/button";
import { Checkbox } from "../../../shared/ui/checkbox";
import { Combobox, type ComboItem } from "../../../shared/ui/combobox";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { Field } from "../../../shared/ui/field";
import { ImpactSummary } from "../../../shared/ui/impact-summary";
import { ListLoading } from "../../../shared/ui/list-footer";
import { MoneyInput } from "../../../shared/ui/money-input";
import { QtyInput } from "../../../shared/ui/qty-input";
import { toast } from "../../../shared/ui/toast-store";
import { lineName } from "../api/adapters";
import {
  customerSearchSource,
  skuDetailQuery,
  skuSearchSource,
  useColorMaps,
  useDebtLedger,
  usePaymentMethods,
  useReasons,
  useReturnableLines,
  useSalesDocument,
} from "../api/queries";
import { usePostReturn, type FreeReturnLineInput, type ReturnLineInput } from "../api/return-mutations";
import { rowLabel } from "../components/line-items/row-label";
import { ReturnColorCell } from "../components/return-color-cell";
import { returnLinesInput, returnRows, useReturnColorChoices, useSourceDocumentItems, type ReturnPick, type ReturnRow } from "../components/return-rows";
import type { DebtLedger, SalesReturn } from "../schemas";

export interface ReturnDialogProps {
  /** Source document to prefill (from the sale-document drawer); null picks the latest one. */
  documentId: string | null;
  onClose: () => void;
  onPosted: (posted: SalesReturn) => void;
}

/** A line of a return without a source document. */
interface FreeRow {
  key: number;
  sku: ComboItem | null;
  color: ColorKey | null;
  qty: Qty | null;
  price: Money | null;
}

const EMPTY_PICK: ReturnPick = { qty: null, color: null };

/** Open receivable of one sale document in the customer's ledger (what a return reduces first). */
function documentOpen(ledger: DebtLedger | undefined, documentId: string): Decimal {
  const ob = ledger?.documents.find((d) => d.direction === "receivable" && d.source_id === documentId);
  return dec(ob?.open ?? "0");
}

const newFreeRow = (key: number): FreeRow => ({ key, sku: null, color: null, qty: null, price: null });

/**
 * Prototype `newReturn` "Tạo phiếu trả hàng": takes goods back against a sale document, per color,
 * never beyond what is still returnable. The value reduces the customer's debt first; the rest is a
 * refund due (a walk-in customer is paid back at once from the chosen fund). Without a source
 * document the owner names the goods and the price taken back.
 */
export function ReturnDialog({ documentId, onClose, onPosted }: ReturnDialogProps) {
  const client = useApiClient();
  const colors = useColorMaps();
  const docItems = useSourceDocumentItems();
  const [noSource, setNoSource] = useState(false);
  const [docChoice, setDocChoice] = useState<string | null>(documentId);
  const docId = noSource ? null : (docChoice ?? docItems[0]?.id ?? null);
  const doc = useSalesDocument(docId);
  const returnable = useReturnableLines(docId);
  const [picks, setPicks] = useState<ReadonlyMap<string, ReturnPick>>(new Map());
  const [quarantine, setQuarantine] = useState(true);
  const [reasonChoice, setReason] = useState<string | null>(null);
  const [methodChoice, setMethod] = useState<string | null>(null);
  const [customer, setCustomer] = useState<ComboItem | null>(null);
  const [freeRows, setFreeRows] = useState<readonly FreeRow[]>([newFreeRow(0)]);
  const reasons = useReasons(noSource ? "return_without_source" : "sales_return").data ?? [];
  const methods = usePaymentMethods().data ?? [];
  const post = usePostReturn(onPosted);

  const rows = useMemo(() => returnRows(returnable.data ?? [], colors), [returnable.data, colors]);
  const pickKey = (r: ReturnRow) => `${docId ?? ""}:${r.line.sales_document_line_id}`;
  const pickOf = (r: ReturnRow) => picks.get(pickKey(r)) ?? EMPTY_PICK;
  const setPick = (r: ReturnRow, patch: Partial<ReturnPick>) => setPicks((m) => new Map(m).set(pickKey(r), { ...pickOf(r), ...patch }));

  const choicesOf = useReturnColorChoices(
    noSource ? freeRows.flatMap((f) => (f.sku ? [f.sku.id] : [])) : rows.filter((r) => r.color === null).map((r) => r.line.sku_id),
    colors,
  );
  const freeSkuIds = [...new Set(freeRows.flatMap((f) => (f.sku ? [f.sku.id] : [])))];
  const details = useQueries({ queries: freeSkuIds.map((id) => skuDetailQuery(client, id)) });
  const baseUnitOf = (skuId: string) => {
    const units = details.find((d) => d.data?.id === skuId)?.data?.units ?? [];
    return units.find((u) => u.is_base) ?? units[0];
  };

  const customerId = noSource ? (customer?.id ?? null) : (doc.data?.customer_party_id ?? null);
  const walkIn = noSource ? customer === null : doc.data !== undefined && doc.data.customer_party_id === null;
  const ledger = useDebtLedger(customerId);
  const reasonId = reasons.some((r) => r.id === reasonChoice) ? reasonChoice : (reasons[0]?.id ?? null);
  const methodId = methodChoice ?? methods[0]?.id ?? null;

  const value = noSource
    ? sumDec(freeRows.map((f) => (f.qty !== null && f.price !== null ? dec(f.qty).times(f.price) : "0")))
    : sumDec(
        rows.map((r) => {
          const q = pickOf(r).qty;
          return q === null ? "0" : r.price.times(q);
        }),
      );
  const debtOpen = noSource ? dec(ledger.data?.receivable ?? "0") : docId !== null ? documentOpen(ledger.data, docId) : dec(0);
  const open = debtOpen.gt(0) ? debtOpen : dec(0);
  const credit = walkIn ? dec(0) : open.lt(value) ? open : value;
  const refund = value.minus(credit);
  const restockTo: ReturnLineInput["restock_to"] = quarantine ? "quarantine" : "sellable";

  const freeLines = (): { lines: FreeReturnLineInput[] } | { problem: string } => {
    const lines: FreeReturnLineInput[] = [];
    for (const f of freeRows) {
      if (f.sku === null || f.qty === null || dec(f.qty).lte(0)) continue;
      const unit = baseUnitOf(f.sku.id);
      if (!unit) return { problem: `Đang tải đơn vị của ${f.sku.name}. Thử lại sau giây lát.` };
      if (f.color === null && choicesOf(f.sku.id).length > 0) return { problem: `Chọn màu khách trả cho ${f.sku.name}.` };
      if (f.price === null) return { problem: `Nhập đơn giá trả cho ${f.sku.name}.` };
      const colorId = colors.idOf(f.color);
      lines.push({ sku_id: f.sku.id, unit_id: unit.unit_id, ...(colorId ? { color_id: colorId } : {}), qty: toDecimalString(f.qty), unit_price: toDecimalString(f.price), restock_to: restockTo });
    }
    return lines.length > 0 ? { lines } : { problem: "Chưa chọn số lượng trả." };
  };

  /** The shared checks; the toast text of the first problem, or null. */
  const headerProblem = (): string | null => {
    if (!noSource && docId === null) return "Chọn chứng từ bán gốc.";
    if (reasonId === null) return "Cần chọn lý do trả.";
    if (walkIn && refund.gt(0) && methodId === null) return "Chọn quỹ chi để hoàn tiền cho khách.";
    return null;
  };

  const submit = () => {
    const problem = headerProblem();
    if (problem !== null || reasonId === null) {
      toast.commitError(problem ?? "Cần chọn lý do trả.");
      return;
    }
    const refundPaymentMethodId = walkIn ? methodId : null;
    if (noSource || docId === null) {
      const built = freeLines();
      if ("problem" in built) toast.commitError(built.problem);
      else void post.run({ salesDocumentId: null, customerPartyId: customerId, reasonId, lines: built.lines, refundPaymentMethodId });
      return;
    }
    const built = returnLinesInput(rows, pickOf, choicesOf, colors, restockTo);
    if ("problem" in built) toast.commitError(built.problem);
    else void post.run({ salesDocumentId: docId, reasonId, lines: built.lines, refundPaymentMethodId });
  };

  const sourceColumns: Column<ReturnRow>[] = [
    { key: "item", header: "Hàng", cell: (r) => lineName(r.line) },
    {
      key: "color",
      header: "Màu",
      cell: (r) => <ReturnColorCell fixed={r.color} choices={choicesOf(r.line.sku_id)} value={pickOf(r).color} onChange={(c) => setPick(r, { color: c })} base={r.line.unit_name} name={lineName(r.line)} />,
    },
    { key: "max", header: "Còn được trả", numeric: true, cell: (r) => `${num(r.max)} ${r.line.unit_name}` },
    {
      key: "qty",
      header: "SL trả",
      numeric: true,
      cell: (r) => (
        <QtyInput size="sm" style={{ width: 80 }} value={pickOf(r).qty} aria-label={`Số lượng trả – ${lineName(r.line)}`} onChange={(q) => setPick(r, { qty: q })} />
      ),
    },
  ];

  const patchFree = (key: number, patch: Partial<FreeRow>) => setFreeRows((rs) => rs.map((f) => (f.key === key ? { ...f, ...patch } : f)));
  const skuSource = useMemo(() => skuSearchSource(colors), [colors]);
  const freeColumns: Column<FreeRow>[] = [
    {
      key: "item",
      header: "Hàng",
      cell: (f, i) => (
        <Combobox source={skuSource} value={f.sku?.id ?? null} valueLabel={f.sku?.name ?? ""} aria-label={rowLabel("Hàng trả", "", i)} onChange={(item) => patchFree(f.key, { sku: item, color: null })} />
      ),
    },
    {
      key: "color",
      header: "Màu",
      cell: (f) =>
        f.sku ? <ReturnColorCell fixed={null} choices={choicesOf(f.sku.id)} value={f.color} onChange={(c) => patchFree(f.key, { color: c })} base={baseUnitOf(f.sku.id)?.unit_name ?? ""} name={f.sku.name} /> : null,
    },
    {
      key: "qty",
      header: "SL trả",
      numeric: true,
      cell: (f, i) => (
        <>
          <QtyInput size="sm" style={{ width: 80 }} value={f.qty} aria-label={rowLabel("Số lượng trả", f.sku?.name ?? "", i)} onChange={(q) => patchFree(f.key, { qty: q })} />{" "}
          {f.sku ? (baseUnitOf(f.sku.id)?.unit_name ?? "") : ""}
        </>
      ),
    },
    {
      key: "price",
      header: "Đơn giá trả",
      numeric: true,
      cell: (f, i) => <MoneyInput size="sm" style={{ width: 110 }} value={f.price} aria-label={rowLabel("Đơn giá trả", f.sku?.name ?? "", i)} onChange={(p) => patchFree(f.key, { price: p })} />,
    },
    { key: "amount", header: "Thành tiền", numeric: true, cell: (f) => vnd(f.qty !== null && f.price !== null ? dec(f.qty).times(f.price) : 0) },
    {
      key: "del",
      header: "",
      cell: (f, i) =>
        freeRows.length > 1 ? (
          <Button size="sm" variant="line" aria-label={rowLabel("Xóa", f.sku?.name ?? "", i)} onClick={() => setFreeRows((rs) => rs.filter((x) => x.key !== f.key))}>
            Xóa
          </Button>
        ) : null,
    },
  ];

  return (
    <CommitDialog
      open
      onClose={onClose}
      title="Tạo phiếu trả hàng"
      wide
      pending={post.pending}
      onSubmit={submit}
      submitLabel="Ghi sổ trả hàng"
      live={
        <ImpactSummary
          items={[
            { label: "Giá trị trả (theo giá gốc)", value: vnd(value) },
            { label: "Giảm nợ khách", value: vnd(credit) },
            { label: "Khoản cần hoàn", value: vnd(refund) },
            { label: "Kho", value: quarantine ? "Vào khu cách ly" : "Nhập lại, bán được ngay" },
          ]}
        />
      }
    >
      <div className="fg">
        {noSource ? (
          <Field label="Khách hàng" hint="Bỏ trống nếu là khách lẻ.">
            <Combobox kind="party" source={customerSearchSource} value={customer?.id ?? null} valueLabel={customer?.name ?? ""} aria-label="Khách trả hàng" onChange={setCustomer} />
          </Field>
        ) : (
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
        )}
        <Field label="Lý do">
          <select className="in" value={reasonId ?? ""} onChange={(e) => setReason(e.target.value)}>
            {reasons.map((r) => (
              <option key={r.id} value={r.id}>
                {r.name}
              </option>
            ))}
          </select>
        </Field>
        {walkIn ? (
          <Field label="Hoàn tiền từ quỹ" hint="Khách lẻ được hoàn tiền ngay khi ghi sổ.">
            <select className="in" value={methodId ?? ""} onChange={(e) => setMethod(e.target.value)}>
              {methods.map((m) => (
                <option key={m.id} value={m.id}>
                  {m.name}
                </option>
              ))}
            </select>
          </Field>
        ) : null}
      </div>
      <Checkbox label="Không có chứng từ gốc" checked={noSource} onChange={(e) => setNoSource(e.target.checked)} />

      {noSource ? (
        <>
          <DataTable caption="Hàng khách trả" columns={freeColumns} rows={freeRows} rowKey={(f) => String(f.key)} flat />
          <div className="btn-row">
            <Button size="sm" variant="line" onClick={() => setFreeRows((rs) => [...rs, newFreeRow(rs.reduce((k, x) => Math.max(k, x.key), 0) + 1)])}>
              + Thêm hàng
            </Button>
          </div>
        </>
      ) : returnable.isLoading ? (
        <ListLoading />
      ) : (
        <DataTable caption="Hàng còn được trả" columns={sourceColumns} rows={rows} rowKey={(r) => r.line.sales_document_line_id} flat empty="Chứng từ này không còn hàng được trả." />
      )}

      <Checkbox
        label="Đưa hàng trả vào khu cách ly chờ kiểm tra (bỏ tích nếu hàng còn nguyên, bán lại được ngay)"
        checked={quarantine}
        onChange={(e) => setQuarantine(e.target.checked)}
      />
    </CommitDialog>
  );
}
