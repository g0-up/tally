import { useMemo, useState } from "react";
import { dec } from "../../../shared/lib/decimal";
import { num, vnd } from "../../../shared/lib/format";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { ImpactSummary } from "../../../shared/ui/impact-summary";
import { toast } from "../../../shared/ui/toast-store";
import { useSkusByIds } from "../../catalog";
import { INVENTORY_MESSAGES } from "../api/adapters";
import { lotLine, useCreateAdjustment } from "../api/mutations";
import { useReasons } from "../api/queries";
import { ReasonField } from "../components/reason-field";
import { reasonGiven, type ReasonValue } from "../view-model";
import type { Lot } from "../schemas";

export interface DisposeLotDialogProps {
  lot: Lot;
  onClose: () => void;
}

const DEFAULT_REASON = "Hết hạn sử dụng";

/** Prototype `destroyLot` "Tiêu hủy lô hết hạn": writes the whole lot off at average cost. */
export function DisposeLotDialog({ lot, onClose }: DisposeLotDialogProps) {
  const reasons = (useReasons().data ?? []).filter((r) => r.kind === "disposal");
  const preset = reasons.find((r) => r.name === DEFAULT_REASON)?.id ?? "";
  const [reason, setReason] = useState<ReasonValue | null>(null);
  const value: ReasonValue = reason ?? { reasonId: preset, text: DEFAULT_REASON };
  const skus = useSkusByIds(useMemo(() => [lot.sku_id], [lot.sku_id]));
  const cost = skus.data?.get(lot.sku_id)?.avg_cost;
  const dispose = useCreateAdjustment(() => "Đã ghi tiêu hủy.", onClose);

  const submit = () => {
    if (!reasonGiven(value, reasons)) return toast.commitError(INVENTORY_MESSAGES.reasonRequired);
    void dispose.run({
      kind: "disposal",
      reasonId: reasons.length > 0 ? value.reasonId : null,
      ...(reasons.length > 0 ? {} : { note: value.text.trim() }),
      lines: [lotLine(lot)],
      name: lot.sku_name,
    });
  };

  return (
    <CommitDialog
      open
      onClose={onClose}
      title="Tiêu hủy lô hết hạn"
      sub={`${lot.sku_name} · lô ${lot.lot_no} · ${num(lot.qty)} ${lot.unit_name}`}
      submitLabel="Ghi sổ tiêu hủy"
      onSubmit={submit}
      pending={dispose.pending}
      live={
        <ImpactSummary
          items={[
            { label: "Giá trị ghi giảm", value: cost === null || cost === undefined ? "—" : vnd(dec(lot.qty).times(cost)) },
            { label: "Doanh thu", value: "Không phát sinh" },
          ]}
        />
      }
    >
      <ReasonField label="Lý do" reasons={reasons} value={value} onChange={setReason} autoFocus />
    </CommitDialog>
  );
}
