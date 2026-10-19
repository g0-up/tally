import { useState } from "react";
import { toQty, type Qty } from "../../../shared/domain/qty";
import { dec } from "../../../shared/lib/decimal";
import { fd, num } from "../../../shared/lib/format";
import { Checkbox } from "../../../shared/ui/checkbox";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { Field } from "../../../shared/ui/field";
import { FormGrid } from "../../../shared/ui/form-grid";
import { ListFooter } from "../../../shared/ui/list-footer";
import { QtyInput } from "../../../shared/ui/qty-input";
import { toast } from "../../../shared/ui/toast-store";
import { EMPTY_REASON, ReasonField, useLoadAll, useReasons, type ReasonValue } from "../../inventory";
import { KIT_MESSAGES } from "../api/adapters";
import { useDisassemble } from "../api/mutations";
import { useKitLots, useKitLotSource, useRecipe } from "../api/queries";
import type { DisassemblyRequest } from "../api/requests";
import { isMaterial } from "../view-model";

/** Reason kinds that explain taking kits apart. */
const REASON_KINDS = new Set(["damage", "disposal", "internal_use", "write_off"]);

/**
 * Prototype `newDIS` "Tháo giỏ quà": takes kits of one finished lot apart; components return to the
 * lots they came from, damaged packing goes to quarantine instead of back to sellable stock.
 */
export function DisassemblyDialog({ onClose }: { onClose: () => void }) {
  const lots = useKitLots();
  // Every kit lot holding stock is offered, not only the first page.
  useLoadAll(lots);
  const reasons = (useReasons().data ?? []).filter((r) => r.active && REASON_KINDS.has(r.kind));

  const [lotId, setLotId] = useState<string | null>(null);
  const [qty, setQty] = useState<Qty | null>(toQty(1));
  const [damaged, setDamaged] = useState(true);
  const [reason, setReason] = useState<ReasonValue>(EMPTY_REASON);
  const picked = lots.items.find((l) => l.id === lotId) ?? lots.items[0];
  // A kit lot comes from one posting of one order, found through the lot's genealogy.
  const source = useKitLotSource(picked?.id ?? null);
  const recipe = useRecipe(source.order?.product_id ?? null, source.order?.recipe_version ?? null);
  const run = useDisassemble(onClose);

  const submit = () => {
    if (!picked) return toast.commitError(KIT_MESSAGES.lotRequired);
    if (source.pending) return toast.commitError(KIT_MESSAGES.lotSourceLoading);
    if (!source.order || !source.posting) return toast.commitError(KIT_MESSAGES.lotSourceMissing(picked.lot_no));
    const max = dec(picked.qty);
    if (qty === null || dec(qty).lt(1) || dec(qty).gt(max)) return toast.commitError(KIT_MESSAGES.disassembleRange(max.toString()));
    if (!reasons.some((r) => r.id === reason.reasonId)) return toast.commitError(KIT_MESSAGES.reasonRequired);
    if (damaged && recipe.isError) return toast.commitError(KIT_MESSAGES.recipeUnavailable);
    if (damaged && !recipe.data) return toast.commitError(KIT_MESSAGES.recipeLoading);
    const body: DisassemblyRequest = { kit_lot_id: picked.id, qty, reason_id: reason.reasonId };
    if (damaged && recipe.data) {
      const posting = source.posting;
      body.damaged = recipe.data.items.flatMap((i) => {
        if (!isMaterial(i.kind) || !i.sku_id) return [];
        const used = posting.items.find((u) => u.sku_id === i.sku_id);
        return [{ sku_id: i.sku_id, qty: toQty(dec(i.base_qty).times(qty)), ...(used ? { source_lot_id: used.lot_id } : {}) }];
      });
    }
    void run.run({ orderId: source.order.id, body });
  };

  return (
    <CommitDialog open onClose={onClose} title="Tháo giỏ quà" submitLabel="Ghi sổ tháo giỏ" onSubmit={submit} pending={run.pending}>
      <FormGrid>
        <Field label="Giỏ">
          <select className="in" value={picked?.id ?? ""} onChange={(e) => setLotId(e.target.value)}>
            {lots.items.length === 0 ? <option value="">{lots.isLoading ? "Đang tải…" : "Chưa có lô giỏ để tháo"}</option> : null}
            {lots.items.map((lot) => (
              <option key={lot.id} value={lot.id}>
                {lot.sku_name} · lô {lot.lot_no}
                {lot.expiry_date ? ` · hạn ${fd(lot.expiry_date)}` : ""}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Số giỏ tháo" hint={`Có thể tháo: ${num(picked?.qty ?? 0)}`}>
          <QtyInput value={qty} onChange={setQty} />
        </Field>
      </FormGrid>
      {lots.error ? <ListFooter list={lots} /> : null}
      {picked && !source.pending && !source.posting ? <p className="hint">{KIT_MESSAGES.lotSourceMissing(picked.lot_no)}</p> : null}
      <ReasonField label="Lý do tháo giỏ" reasons={reasons} value={reason} onChange={setReason} />
      <Checkbox label="Giỏ mây, nơ bị hỏng khi tháo (không nhập lại tồn)" checked={damaged} onChange={(e) => setDamaged(e.target.checked)} />
    </CommitDialog>
  );
}
