import { useMemo, useState } from "react";
import { toQty, type Qty } from "../../../shared/domain/qty";
import { dec } from "../../../shared/lib/decimal";
import { num, vnd } from "../../../shared/lib/format";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { Field } from "../../../shared/ui/field";
import { ImpactSummary } from "../../../shared/ui/impact-summary";
import { QtyInput } from "../../../shared/ui/qty-input";
import { toast } from "../../../shared/ui/toast-store";
import { baseUnitName, useColorDirectory, useSkusByIds } from "../../catalog";
import { assemblyRemaining, KIT_MESSAGES } from "../api/adapters";
import { usePostAssembly } from "../api/mutations";
import { useAssembly } from "../api/queries";
import type { BatchLineRequest } from "../api/requests";
import { OpenColorFields } from "../components/open-color-fields";
import type { AssemblyOrder } from "../schemas";
import { openColors } from "../view-model";

export interface AssemblyCompleteDialogProps {
  order: AssemblyOrder;
  onClose: () => void;
}

/**
 * Prototype `asmDone` "Ghi nhận hoàn thành": issues the components FEFO and receives the finished
 * kits in one posting, at most the kits the order still has to make. A component planned without a
 * color that is now held in colors takes the color picked here.
 */
export function AssemblyCompleteDialog({ order, onClose }: AssemblyCompleteDialogProps) {
  const remaining = assemblyRemaining(order);
  const [qty, setQty] = useState<Qty | null>(toQty(remaining));
  const detail = useAssembly(order.id).data;
  const ids = useMemo(() => detail?.items.map((i) => i.sku_id) ?? [], [detail]);
  const skus = useSkusByIds(ids).data;
  const colors = useColorDirectory();
  const [picks, setPicks] = useState<Record<string, string>>({});
  const open = useMemo(() => openColors(detail?.items ?? [], skus, colors), [detail, skus, colors]);
  const post = usePostAssembly(onClose);

  const n = qty === null ? dec(0) : dec(qty);
  const issued = detail
    ? detail.items
        .map((i) => {
          const sku = skus?.get(i.sku_id);
          const perKit = dec(i.planned_qty).div(detail.planned_qty);
          return `${num(perKit.times(n))} ${sku ? baseUnitName(sku) : ""}`.trim();
        })
        .join(" · ")
    : "—";

  const submit = () => {
    if (qty === null || n.lt(1) || n.gt(remaining)) return toast.commitError(KIT_MESSAGES.doneRange(remaining.toString()));
    if (detail === undefined || (ids.length > 0 && skus === undefined)) return toast.commitError(KIT_MESSAGES.recipeLoading);
    const missing = open.filter((o) => !picks[o.skuId]);
    if (missing.length > 0) return toast.commitError(KIT_MESSAGES.colorPick(missing.map((o) => o.name)));
    // A picked color goes on a line carrying the item's share of the batch, as the API would take it.
    const lines: BatchLineRequest[] = detail.items.flatMap((i) => {
      const color = i.color_id ? undefined : picks[i.sku_id];
      if (color === undefined || !dec(i.remaining_qty).gt(0)) return [];
      return [{ order_item_id: i.id, qty: toQty(dec(i.remaining_qty).times(n).div(remaining)), color_id: color }];
    });
    void post.run({ id: order.id, producedQty: qty, ...(lines.length > 0 ? { lines } : {}) });
  };

  return (
    <CommitDialog
      open
      onClose={onClose}
      title={`Ghi nhận hoàn thành ${order.doc_no}`}
      sub="Xuất nguyên liệu theo FEFO và nhập giỏ thành phẩm trong cùng một lần ghi sổ. Hạn bán của giỏ = hạn sớm nhất của thành phần."
      submitLabel="Ghi sổ hoàn thành"
      onSubmit={submit}
      pending={post.pending}
      live={
        <ImpactSummary
          items={[
            { label: "Nguyên liệu xuất", value: issued },
            { label: "Giỏ nhập kho", value: `${num(n)} giỏ` },
            // The order's expected cost covers every planned kit.
            { label: "Giá vốn / giỏ (dự kiến)", value: detail && dec(detail.planned_qty).gt(0) ? vnd(dec(detail.expected_cost).div(detail.planned_qty)) : "—" },
          ]}
        />
      }
    >
      <Field label={`Số giỏ hoàn thành (còn ${num(remaining)})`}>
        <QtyInput value={qty} onChange={setQty} autoFocus />
      </Field>
      {open.length > 0 ? (
        <>
          <p className="hint">Lệnh chưa cố định màu cho các thành phần dưới đây; chọn màu sẽ xuất cho đợt này.</p>
          <OpenColorFields open={open} picks={picks} onPick={(skuId, colorId) => setPicks((p) => ({ ...p, [skuId]: colorId }))} colors={colors} />
        </>
      ) : null}
    </CommitDialog>
  );
}
