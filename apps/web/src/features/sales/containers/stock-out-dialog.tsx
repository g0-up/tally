import { useState } from "react";
import type { Qty } from "../../../shared/domain/qty";
import { dec, sumDec } from "../../../shared/lib/decimal";
import { num, vnd } from "../../../shared/lib/format";
import { Checkbox } from "../../../shared/ui/checkbox";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { ImpactSummary } from "../../../shared/ui/impact-summary";
import { ListLoading } from "../../../shared/ui/list-footer";
import { QtyInput } from "../../../shared/ui/qty-input";
import { toast } from "../../../shared/ui/toast-store";
import { lineName } from "../api/adapters";
import { usePostStockOut } from "../api/invoice-mutations";
import { useDebtLedger, useInvoice } from "../api/queries";
import { DrawerStatus } from "../components/drawer-status";
import type { Invoice, InvoiceLine } from "../schemas";
import { deliverBaseQty, deliverRows, type DeliverRow, type StockOutRow, type WholeRow } from "../stock-out-lines";

const WHOLE_TAGS: Partial<Record<InvoiceLine["line_kind"], string>> = { mix_pack: "Thùng mix", combo: "Combo" };

/** "Cola × 18 Lon" for each flavor or component of a mix pack or combo. */
const partsText = (r: WholeRow) => r.parts.map((p) => `${lineName(p.line)} × ${num(p.remaining)} ${p.line.unit_name}`).join(" · ");

/** Why a mix pack or combo cannot go out yet: the parts holding less than they still owe. */
const shortText = (r: WholeRow) => `Chưa giữ đủ hàng: ${r.short.map((p) => `${lineName(p.line)} giữ ${num(p.reserved)}/${num(p.remaining)} ${p.line.unit_name}`).join(" · ")}`;

export interface StockOutDialogProps {
  invoice: Invoice;
  onClose: () => void;
  onPosted: () => void;
}

/** Prototype `newDLV`: one stock-out of what the invoice has reserved. */
export function StockOutDialog({ invoice, onClose, onPosted }: StockOutDialogProps) {
  const detail = useInvoice(invoice.id);
  const title = `Xuất kho giao khách · ${invoice.doc_no}`;
  if (!detail.data) {
    return (
      <CommitDialog open onClose={onClose} title={title} wide>
        {detail.error ? <DrawerStatus error={detail.error} deletedText="Hóa đơn đã bị xóa." /> : <ListLoading />}
      </CommitDialog>
    );
  }
  return <StockOutForm key={`${detail.data.id}:${detail.data.version}`} invoice={detail.data} title={title} onClose={onClose} onPosted={onPosted} />;
}

function StockOutForm({ invoice, title, onClose, onPosted }: StockOutDialogProps & { title: string }) {
  const rows = deliverRows(invoice);
  const [qty, setQty] = useState<ReadonlyMap<string, Qty | null>>(() => new Map(rows.flatMap((r) => (r.kind === "line" ? [[r.line.id, r.max as Qty]] : []))));
  // Mix packs and combos picked to go out whole; those ready to go are picked to start with.
  const [whole, setWhole] = useState<ReadonlySet<string>>(() => new Set(rows.flatMap((r) => (r.kind === "whole" && r.short.length === 0 ? [r.line.id] : []))));
  const ledger = useDebtLedger(invoice.payer_party_id);
  const post = usePostStockOut(() => onPosted());

  const qtyOf = (r: DeliverRow) => qty.get(r.line.id) ?? null;
  /** Base quantity going out on a row as picked now, before it is checked against the cap. */
  const outBase = (r: StockOutRow) => {
    if (r.kind === "whole") return whole.has(r.line.id) ? r.cap : null;
    const q = qtyOf(r);
    return q === null ? null : dec(q).times(r.line.unit_factor);
  };
  const value = sumDec(
    rows.map((r) => {
      const out = outBase(r);
      const base = dec(r.line.base_qty);
      if (out === null || base.lte(0)) return "0";
      return dec(r.line.line_amount).times(out).div(base);
    }),
  );
  const prepaid = sumDec((ledger.data?.advances ?? []).filter((a) => a.earmarked_invoice_id === invoice.id).map((a) => a.amount));
  const applied = prepaid.lt(value) ? prepaid : value;

  const submit = () => {
    const lines: { invoice_line_id: string; base_qty: string }[] = [];
    for (const r of rows) {
      if (r.kind === "whole") {
        // Sent through the header for its remaining packs; the server delivers every part with it.
        if (whole.has(r.line.id)) lines.push({ invoice_line_id: r.line.id, base_qty: r.cap });
        continue;
      }
      const q = qtyOf(r);
      if (q === null || dec(q).lte(0)) continue;
      const baseQty = deliverBaseQty(r, q);
      if (baseQty === null) {
        toast.commitError(`${lineName(r.line)}: chỉ xuất được tối đa ${num(r.max)} ${r.line.unit_name}.`);
        return;
      }
      lines.push({ invoice_line_id: r.line.id, base_qty: baseQty });
    }
    if (lines.length === 0) {
      toast.commitError("Nhập số lượng xuất lần này.");
      return;
    }
    void post.run({ invoice, lines });
  };

  const whyId = (r: WholeRow) => `stock-out-short-${r.line.id}`;
  const columns: Column<StockOutRow>[] = [
    {
      key: "item",
      header: "Hàng",
      cell: (r) =>
        r.kind === "line" ? (
          lineName(r.line)
        ) : (
          <>
            {lineName(r.line)} <span className="tag">{WHOLE_TAGS[r.line.line_kind]}</span>
            <CellSub>{partsText(r)}</CellSub>
          </>
        ),
    },
    {
      key: "reserved",
      header: "Đã giữ, chưa xuất",
      numeric: true,
      cell: (r) => {
        if (r.kind === "line") return `${num(r.max)} ${r.line.unit_name}`;
        if (r.short.length === 0) return `${num(r.qty)} ${r.line.unit_name}`;
        return <span id={whyId(r)}>{shortText(r)}</span>;
      },
    },
    {
      key: "qty",
      header: "Xuất lần này",
      numeric: true,
      cell: (r) =>
        r.kind === "whole" ? (
          <Checkbox
            label={`Nguyên ${num(r.qty)} ${r.line.unit_name}`}
            aria-label={`Xuất lần này – ${lineName(r.line)}`}
            checked={whole.has(r.line.id)}
            disabled={r.short.length > 0}
            {...(r.short.length > 0 ? { "aria-describedby": whyId(r) } : {})}
            onChange={(e) => {
              const on = e.target.checked;
              setWhole((s) => {
                const next = new Set(s);
                if (on) next.add(r.line.id);
                else next.delete(r.line.id);
                return next;
              });
            }}
          />
        ) : (
          <QtyInput
            size="sm"
            style={{ width: 80 }}
            value={qtyOf(r)}
            disabled={dec(r.max).lte(0)}
            aria-label={`Xuất lần này – ${lineName(r.line)}`}
            onChange={(q) => setQty((m) => new Map(m).set(r.line.id, q))}
          />
        ),
    },
  ];

  return (
    <CommitDialog
      open
      onClose={onClose}
      title={title}
      sub="Một bước: trừ kho, ghi doanh thu và công nợ của khách. Tiền khách đã trả trước được tự trừ vào công nợ."
      wide
      pending={post.pending}
      onSubmit={submit}
      submitLabel="Ghi sổ xuất kho"
      live={
        <ImpactSummary
          items={[
            { label: "Giá trị xuất", value: vnd(value) },
            { label: "Kho", value: "Trừ ngay" },
            { label: "Công nợ khách", value: `+${vnd(value.minus(applied))}` },
            { label: "Trừ tiền trả trước", value: vnd(applied) },
          ]}
        />
      }
    >
      <DataTable caption={`Hàng xuất của ${invoice.doc_no}`} columns={columns} rows={rows} rowKey={(r) => r.line.id} flat empty="Hóa đơn không còn hàng chờ giao." />
      {rows.some((r) => r.kind === "line" && dec(r.max).lte(0)) ? <p className="hint">Dòng có “0” là hàng chưa giữ đủ; bấm “Giữ thêm hàng” ở hóa đơn khi có tồn.</p> : null}
    </CommitDialog>
  );
}
