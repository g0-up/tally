import { useState } from "react";
import { toQty, type Qty } from "../../../shared/domain/qty";
import { dec, sumDec, type Decimal } from "../../../shared/lib/decimal";
import { fd, num, vnd } from "../../../shared/lib/format";
import { ColorChip } from "../../../shared/ui/color/color-chip";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { Field } from "../../../shared/ui/field";
import { FormGrid } from "../../../shared/ui/form-grid";
import { ImpactSummary } from "../../../shared/ui/impact-summary";
import { LIST_LOADING_TEXT } from "../../../shared/ui/list-footer";
import { QtyInput } from "../../../shared/ui/qty-input";
import { toast } from "../../../shared/ui/toast-store";
import { baseUnitOf, useColorDirectory, useSkusByIds, type SkuListItem } from "../../catalog";
import { PROCUREMENT_MESSAGES } from "../api/adapters";
import { usePostReturn, type PostReturnVars } from "../api/mutations";
import { useReasons, useReceipt, useReceiptList, useSkuLots } from "../api/queries";
import type { Lot, Receipt, ReceiptLine } from "../schemas";

export interface NewPurchaseReturnDialogProps {
  /** Receipt chosen beforehand ("Trả NCC từ phiếu này"). */
  receiptId?: string | null;
  onClose: () => void;
}

/** Prototype default reason of newPRT. */
const DEFAULT_REASON = "Hàng lỗi bao bì";

/** Price of one base unit of a receipt line. */
const basePrice = (l: ReceiptLine): Decimal => (dec(l.base_qty).gt(0) ? dec(l.line_amount).div(l.base_qty) : dec(l.unit_price).div(l.unit_factor));

/** "Tồn lô tại kho": the sellable stock of the line's lot (quarantined stock is not returned from here). */
function lotStock(l: ReceiptLine, lots: readonly Lot[]): Decimal {
  return sumDec(lots.filter((z) => !z.is_quarantine && (l.lot_id ? z.id === l.lot_id : z.sku_id === l.sku_id && z.lot_no === l.lot_no && (z.color_id ?? null) === (l.color_id ?? null))).map((z) => z.qty));
}

type ReturnQty = Readonly<Record<string, Qty | null>>;

/** Request lines in the SKU's base unit; a line whose unit is the base one needs no unit. */
function returnLines(receipt: Receipt, qty: ReturnQty, skus: ReadonlyMap<string, SkuListItem> | undefined): { ok: true; lines: PostReturnVars["lines"] } | { ok: false; message: string } {
  const lines: PostReturnVars["lines"] = [];
  for (const l of receipt.lines ?? []) {
    const q = qty[l.id] ?? null;
    if (q === null || !dec(q).gt(0)) continue;
    if (dec(q).gt(l.returnable_base_qty)) return { ok: false, message: PROCUREMENT_MESSAGES.returnOverLot(l.name) };
    const sku = skus?.get(l.sku_id);
    const unitId = sku ? baseUnitOf(sku)?.unit_id : undefined;
    if (unitId !== undefined) {
      lines.push({ receipt_line_id: l.id, unit_id: unitId, qty: q });
      continue;
    }
    // Without the SKU row the quantity goes in the receipt line's own unit, when it divides exactly.
    const inLineUnit = dec(q).div(l.unit_factor);
    if (!inLineUnit.toDecimalPlaces(4).eq(inLineUnit)) return { ok: false, message: PROCUREMENT_MESSAGES.returnNotInUnit(l.name, l.unit_name) };
    lines.push({ receipt_line_id: l.id, qty: toQty(inLineUnit) });
  }
  if (lines.length === 0) return { ok: false, message: PROCUREMENT_MESSAGES.returnEmpty };
  return { ok: true, lines };
}

/**
 * Prototype newPRT: goods back to the supplier out of the lots a receipt brought in, quantities in
 * base units. The payable is reduced; what exceeds it becomes "NCC cần hoàn".
 */
export function NewPurchaseReturnDialog({ receiptId: preset = null, onClose }: NewPurchaseReturnDialogProps) {
  const receipts = useReceiptList("");
  const reasons = useReasons("purchase_return");
  const colors = useColorDirectory();
  const [picked, setPicked] = useState<string | null>(null);
  const receiptId = picked ?? preset ?? receipts.items[0]?.id ?? null;
  const receipt = useReceipt(receiptId);
  const r = receipt.data?.id === receiptId ? receipt.data : undefined;
  const skuIds = (r?.lines ?? []).map((l) => l.sku_id);
  const lots = useSkuLots(skuIds);
  const skus = useSkusByIds(skuIds);
  const [qty, setQty] = useState<ReturnQty>({});
  const [reasonPicked, setReasonPicked] = useState<string | null>(null);
  const reasonId = reasonPicked ?? reasons.data?.find((x) => x.name === DEFAULT_REASON)?.id ?? reasons.data?.[0]?.id ?? "";
  const post = usePostReturn(onClose);

  const lines = r?.lines ?? [];
  const amount = sumDec(lines.map((l) => dec(qty[l.id] ?? 0).times(basePrice(l))));
  const open = dec(r?.payable?.open_amount ?? 0);
  const credit = amount.gt(open) ? open : amount;

  const submit = () => {
    if (!r) return;
    if (reasonId === "") {
      toast.commitError(PROCUREMENT_MESSAGES.returnReasonRequired);
      return;
    }
    const built = returnLines(r, qty, skus.data);
    if (!built.ok) {
      toast.commitError(built.message);
      return;
    }
    void post.run({ receiptId: r.id, reasonId, lines: built.lines });
  };

  const options = receipts.items.map((x) => ({ id: x.id, label: `${x.doc_no} · ${x.supplier_name ?? "—"} · ${fd(x.business_date)}` }));
  if (r && !options.some((o) => o.id === r.id)) options.unshift({ id: r.id, label: `${r.doc_no} · ${r.supplier_name ?? "—"} · ${fd(r.business_date)}` });

  const columns: Column<ReceiptLine>[] = [
    {
      key: "name",
      header: "Hàng",
      cell: (l) => (
        <>
          {l.name}
          {l.color_id ? (
            <>
              {" "}
              <ColorChip color={colors.codeOf(l.color_id)} />
            </>
          ) : null}
        </>
      ),
    },
    { key: "lot", header: "Lô", cell: (l) => l.lot_no || "—" },
    { key: "stock", header: "Tồn lô tại kho", numeric: true, cell: (l) => (l.lot_no ? num(lotStock(l, lots.data ?? [])) : "—") },
    {
      key: "qty",
      header: "SL trả",
      numeric: true,
      cell: (l) => <QtyInput size="sm" value={qty[l.id] ?? null} onChange={(v) => setQty((cur) => ({ ...cur, [l.id]: v }))} aria-label={`SL trả – ${l.name}`} />,
    },
  ];

  return (
    <CommitDialog
      open
      onClose={onClose}
      wide
      title="Trả hàng nhà cung cấp"
      submitLabel="Ghi sổ xuất trả"
      onSubmit={submit}
      submitDisabled={!r}
      pending={post.pending}
      live={
        <ImpactSummary
          items={[
            { label: "Giá trị trả", value: vnd(amount) },
            { label: "Giảm nợ NCC", value: vnd(credit) },
            { label: "NCC cần hoàn", value: vnd(amount.minus(credit)) },
          ]}
        />
      }
    >
      <FormGrid>
        <Field label="Phiếu nhận gốc">
          <select
            className="in"
            value={receiptId ?? ""}
            onChange={(e) => {
              setPicked(e.target.value);
              setQty({});
            }}
          >
            {options.map((o) => (
              <option key={o.id} value={o.id}>
                {o.label}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Lý do">
          <select className="in" value={reasonId} onChange={(e) => setReasonPicked(e.target.value)}>
            {reasonId === "" ? <option value="">Chọn lý do</option> : null}
            {(reasons.data ?? []).map((x) => (
              <option key={x.id} value={x.id}>
                {x.name}
              </option>
            ))}
          </select>
        </Field>
      </FormGrid>
      {r ? (
        <DataTable caption={`Dòng hàng ${r.doc_no}`} columns={columns} rows={lines} rowKey={(l) => l.id} flat />
      ) : (
        <p className="hint">{receiptId === null && !receipts.isLoading ? "Chưa có phiếu nhận để trả." : LIST_LOADING_TEXT}</p>
      )}
    </CommitDialog>
  );
}
