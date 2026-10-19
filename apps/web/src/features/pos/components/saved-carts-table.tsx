import { useId, type ReactNode } from "react";
import { businessDateOf } from "../../../shared/lib/dates";
import { fd, num, vnd } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { ColorChip } from "../../../shared/ui/color/color-chip";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DocNo } from "../../../shared/ui/doc-no";
import { StatusPill } from "../../../shared/ui/status-pill";
import { useSalesDocument, type ColorMaps } from "../../sales/api/queries";
import type { PosCart, SalesLine } from "../../sales/schemas";
import { CART_STATUS_LABELS } from "../api/adapters";

export interface SavedCartsTableProps {
  carts: readonly PosCart[];
  /** Lines of a listed cart once its detail arrived (the list carries none). */
  linesOf: (cartId: string) => readonly SalesLine[] | undefined;
  customerName: (partyId: string | null) => string;
  colors: ColorMaps;
  onEdit: (cart: PosCart) => void;
  onCopy: (cart: PosCart) => void;
  onDelete: (cart: PosCart) => void;
  /** A copy is being written. */
  copying: boolean;
  /** Why the commands are unavailable (no connection to the API); null when they are. */
  lockedReason?: string | null;
}

/** Number of the sale a sold cart became, as a link to its document. */
function SaleDocLink({ id }: { id: string }) {
  const doc = useSalesDocument(id);
  return <DocNo value={doc.data?.doc_no} />;
}

function CartGoods({ lines, colors }: { lines: readonly SalesLine[] | undefined; colors: ColorMaps }): ReactNode {
  if (lines === undefined)
    return (
      <>
        <span aria-hidden="true">…</span>
        <span className="sr-only">Đang tải</span>
      </>
    );
  return lines
    .filter((l) => l.line_kind !== "combo_component" && l.line_kind !== "mix_flavor")
    .map((l, i) => {
      const color = l.color_id === null ? null : colors.codeOf(l.color_id);
      return (
        <span key={l.id}>
          {i > 0 ? <br /> : null}
          {num(l.qty)} {l.unit_name} {l.name}
          {l.variant_label ? ` ${l.variant_label}` : ""}
          {color !== null ? <> {" "}<ColorChip color={color} /></> : null}
        </span>
      );
    });
}

/**
 * "Giỏ hàng đã lưu": open carts can be edited, copied or deleted; a sold cart is locked and only
 * copied. Each command names its cart. While the commands are locked the reason is shown above the
 * table and describes every disabled button, since a disabled button takes no focus.
 */
export function SavedCartsTable({ carts, linesOf, customerName, colors, onEdit, onCopy, onDelete, copying, lockedReason = null }: SavedCartsTableProps) {
  const locked = lockedReason !== null;
  const title = lockedReason ?? undefined;
  const reasonId = useId();
  const described = locked ? reasonId : undefined;
  const docNoOf = (id: string | null) => (id === null ? null : (carts.find((c) => c.id === id)?.doc_no ?? null));
  const columns: Column<PosCart>[] = [
    {
      key: "cart",
      header: "Giỏ",
      cell: (c) => {
        const from = docNoOf(c.copied_from_id);
        return (
          <>
            <span className="doc">{c.doc_no}</span>
            <CellSub>
              {fd(businessDateOf(c.updated_at))}
              {from ? ` · sao chép từ ${from}` : ""}
            </CellSub>
          </>
        );
      },
    },
    { key: "customer", header: "Khách hàng", cell: (c) => <b style={{ color: "var(--navy-900)" }}>{customerName(c.customer_party_id)}</b> },
    { key: "goods", header: "Hàng", cell: (c) => <CartGoods lines={linesOf(c.id)} colors={colors} /> },
    { key: "total", header: "Tổng tiền", numeric: true, cell: (c) => vnd(c.total_amount) },
    {
      key: "status",
      header: "Trạng thái",
      cell: (c) =>
        c.status === "sold" ? (
          <>
            <StatusPill label={CART_STATUS_LABELS.sold} />{" "}
            {c.sales_document_id !== null ? (
              <CellSub>
                <SaleDocLink id={c.sales_document_id} />
              </CellSub>
            ) : null}
          </>
        ) : (
          <StatusPill label={CART_STATUS_LABELS.open} />
        ),
    },
    {
      key: "acts",
      header: "",
      className: "acts",
      cell: (c) => (
        <ButtonRow style={{ flexWrap: "nowrap", whiteSpace: "nowrap" }}>
          {c.status === "open" ? (
            <Button variant="navy" size="sm" disabled={locked} title={title} aria-label={`Sửa giỏ ${c.doc_no}`} aria-describedby={described} onClick={() => onEdit(c)}>
              Sửa
            </Button>
          ) : null}
          <Button size="sm" disabled={copying || locked} title={title} aria-label={`Sao chép giỏ ${c.doc_no}`} aria-describedby={described} onClick={() => onCopy(c)}>
            Sao chép
          </Button>
          {c.status === "open" ? (
            <Button variant="danger" size="sm" disabled={locked} title={title} aria-label={`Xóa giỏ ${c.doc_no}`} aria-describedby={described} onClick={() => onDelete(c)}>
              Xóa
            </Button>
          ) : null}
        </ButtonRow>
      ),
    },
  ];
  return (
    <>
      {locked && carts.length > 0 ? (
        <p className="hint" id={reasonId}>
          {lockedReason}
        </p>
      ) : null}
      <DataTable caption="Giỏ hàng đã lưu" columns={columns} rows={carts} rowKey={(c) => c.id} empty="Không có giỏ hàng." />
    </>
  );
}
