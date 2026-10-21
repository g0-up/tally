import { useMemo, useState } from "react";
import { errorMessage } from "../../../shared/api/api-error";
import { useFocusFirstInvalid } from "../../../shared/hooks/use-focus-first-invalid";
import { Button } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { ListLoading } from "../../../shared/ui/list-footer";
import { toast } from "../../../shared/ui/toast-store";
import { productFormErrors, unitsInUse, useReplaceSkuUnits, type ReplaceSkuUnits } from "../api/mutations";
import { useSkuDetail, useUnits } from "../api/queries";
import {
  UNIT_EDIT_MESSAGES,
  UNIT_LOCK_LABELS,
  editUnitTableErrors,
  firstProductError,
  sameUnitTable,
  savedUnitRow,
  savedUnitTable,
  savedUnitsOf,
  type ProductErrors,
  type UnitLock,
  type UnitTable,
} from "../components/product-draft";
import { UnitPricePreview, UnitPriceTable } from "../components/unit-price-table";
import type { SkuDetail } from "../schemas";

const SUB = "Áp dụng cho giao dịch mới. Chứng từ đã lập giữ đơn vị, hệ số và giá đã chụp.";
const titleOf = (name: string) => `Đơn vị & giá · ${name}`;

export interface EditUnitsDialogProps {
  sku: { id: string; display_name: string };
  onClose: () => void;
}

/**
 * Prototype `editUnits` (Danh mục › Hàng hóa › "Đơn vị & giá"): the whole unit table of a saved
 * SKU with its unit prices, saved in one request. The base unit is fixed; a unit that documents,
 * price history or barcodes use can only be set to "Ngừng dùng". Mount per opening.
 */
export function EditUnitsDialog({ sku, onClose }: EditUnitsDialogProps) {
  const detail = useSkuDetail(sku.id);
  const units = useUnits();
  if (detail.data === undefined || units.isLoading) {
    return (
      <CommitDialog open onClose={onClose} title={titleOf(sku.display_name)} sub={SUB} wide>
        {detail.error ? (
          <Callout tone="bad" role="alert">
            Không tải được hàng hóa: {errorMessage(detail.error)}{" "}
            <Button size="sm" onClick={() => void detail.refetch()}>
              Thử lại
            </Button>
          </Callout>
        ) : (
          <ListLoading />
        )}
      </CommitDialog>
    );
  }
  return <EditUnitsForm sku={detail.data} onClose={onClose} />;
}

/**
 * `sku` is the latest read of the SKU: after a stale save (412) everything refetches, so the next
 * save carries the new version and the hints the new locks, while the draft keeps what was typed.
 */
function EditUnitsForm({ sku, onClose }: { sku: SkuDetail; onClose: () => void }) {
  const [started] = useState<UnitTable>(() => savedUnitTable(sku));
  const [draft, setDraft] = useState<UnitTable>(started);
  const [errors, setErrors] = useState<ProductErrors>({});
  /** Locks a refused save reported for units the loaded SKU still showed as deletable. */
  const [locks, setLocks] = useState<Readonly<Record<string, UnitLock>>>({});
  const focusFirstInvalid = useFocusFirstInvalid();
  const current = useMemo(() => savedUnitTable(sku), [sku]);
  const saved = useMemo(() => savedUnitsOf(sku, locks), [sku, locks]);
  const baseName = sku.units.find((u) => u.is_base)?.unit_name ?? "";

  /** A unit the server would not delete goes back into the table as "Ngừng dùng", marked on its row. */
  const keepInUse = (inUse: ReadonlyMap<string, UnitLock>, { table }: ReplaceSkuUnits) => {
    const units = [...table.units];
    const rowErrors: Record<string, string> = {};
    for (const [unitId, lock] of inUse) {
      let i = units.findIndex((u) => u.unitId === unitId);
      const row = units[i];
      if (row !== undefined) {
        units[i] = { ...row, active: false };
      } else {
        const unit = sku.units.find((u) => u.unit_id === unitId);
        if (unit === undefined) continue;
        i = units.push(savedUnitRow(unit, { active: false })) - 1;
      }
      rowErrors[`units.${i}.unit`] = UNIT_EDIT_MESSAGES.inUse(UNIT_LOCK_LABELS[lock]);
    }
    setLocks((l) => ({ ...l, ...Object.fromEntries(inUse) }));
    setDraft({ ...table, units });
    setErrors(rowErrors);
  };

  const commit = useReplaceSkuUnits(sku.id, {
    onRefused: (e, sent) => {
      const inUse = unitsInUse(e);
      if (inUse.size > 0) keepInUse(inUse, sent);
      else setErrors(productFormErrors(e));
    },
    after: onClose,
  });

  const submit = () => {
    const found = editUnitTableErrors(draft, current);
    setErrors(found);
    const first = firstProductError(found);
    if (first !== undefined) {
      toast.commitError(first);
      focusFirstInvalid();
      return;
    }
    if (sameUnitTable(draft, current)) {
      toast.commitError(UNIT_EDIT_MESSAGES.unchanged);
      return;
    }
    void commit.run({ table: draft, started, version: sku.version });
  };

  return (
    <CommitDialog
      open
      onClose={onClose}
      title={titleOf(sku.display_name)}
      sub={SUB}
      submitLabel="Lưu đơn vị & giá"
      onSubmit={submit}
      pending={commit.pending}
      wide
      live={<UnitPricePreview value={draft} baseUnitName={baseName} />}
    >
      <UnitPriceTable value={draft} onChange={(patch) => setDraft((d) => ({ ...d, ...patch }))} errors={errors} baseUnitName={baseName} saved={saved} />
    </CommitDialog>
  );
}
