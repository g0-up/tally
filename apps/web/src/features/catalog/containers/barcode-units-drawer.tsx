import { useId, useRef, useState } from "react";
import { errorMessage } from "../../../shared/api/api-error";
import { dec } from "../../../shared/lib/decimal";
import { num } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DetailDrawer, DrawerError } from "../../../shared/ui/detail-drawer";
import { ListLoading } from "../../../shared/ui/list-footer";
import { toast } from "../../../shared/ui/toast-store";
import { baseUnitName } from "../api/adapters";
import { barcodeUnitErrorMessage, useUpdateBarcodeUnit } from "../api/mutations";
import { useSkuBarcodes } from "../api/queries";
import type { Barcode, SkuListItem, SkuUnit } from "../schemas";

export const NO_BARCODES_TEXT = "Hàng này chưa có mã vạch.";

/** Looks like a button with a write in flight; the `.btn[aria-disabled="true"]` rule does not reach a select. */
const PENDING_STYLE = { opacity: 0.55, cursor: "progress" } as const;

export interface BarcodeUnitsDrawerProps {
  /** The SKU as the list holds it now, so units added meanwhile show up. */
  sku: SkuListItem;
  onClose: () => void;
}

/**
 * "Mã vạch" of one SKU: each barcode with the unit a scan of it answers. Picking another unit only
 * edits the row; "Lưu" sends it. Once saved, Kiểm kê, Nhập hàng and POS items outside the tiles
 * read the new unit on the next scan through `/sku-lookup`. POS matches tile barcodes from its
 * bootstrap first: this tab's bootstrap is invalidated, so POS opened here fetches it again, while
 * a POS open in another tab or device keeps the old unit for tile items until its bootstrap reloads:
 * after it posts a sale or saves, copies or deletes a cart, and, once its bootstrap is over 15 s old,
 * on window refocus, on reconnecting or on reopening POS. The offline pack follows on its own
 * refresh (opening POS, reconnecting, every 30 minutes). Adding or removing barcodes is not done here.
 */
export function BarcodeUnitsDrawer({ sku, onClose }: BarcodeUnitsDrawerProps) {
  const barcodes = useSkuBarcodes(sku.id);
  const columns: Column<Barcode>[] = [
    { key: "code", header: "Mã vạch", cell: (b) => <span className="doc">{b.code}</span> },
    { key: "unit", header: "Đơn vị khi quét", cell: (b) => <BarcodeUnitPicker sku={sku} barcode={b} /> },
  ];
  return (
    <DetailDrawer
      open
      onClose={onClose}
      title={`Mã vạch · ${sku.display_name}`}
      sub={`${sku.code} · Chọn đơn vị rồi bấm Lưu. POS đang mở ở nơi khác có thể tính theo đơn vị cũ tới khi tải lại hàng bán.`}
      docKey={sku.id}
    >
      {barcodes.isError ? (
        <DrawerError message={errorMessage(barcodes.error)} />
      ) : barcodes.data === undefined ? (
        <ListLoading />
      ) : (
        <DataTable caption={`Mã vạch của ${sku.display_name}`} flat columns={columns} rows={barcodes.data} rowKey={(b) => b.id} empty={NO_BARCODES_TEXT} />
      )}
    </DetailDrawer>
  );
}

/** "thùng (12 hộp)" for a conversion unit, the bare name for the base unit. */
function unitLabel(u: SkuUnit, base: string): string {
  return u.is_base ? u.unit_name : `${u.unit_name} (${num(u.factor)} ${base})`;
}

/** Active units of the SKU, smallest first, plus the barcode's own unit even when it is stopped. */
function unitChoices(sku: SkuListItem, current: string): SkuUnit[] {
  return sku.units.filter((u) => u.active || u.unit_id === current).sort((a, b) => dec(a.factor).comparedTo(dec(b.factor)));
}

function BarcodeUnitPicker({ sku, barcode }: { sku: SkuListItem; barcode: Barcode }) {
  const update = useUpdateBarcodeUnit(sku.id);
  // The unit picked but not saved yet; null shows the unit the server holds.
  const [draft, setDraft] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const errorId = useId();
  const pickerRef = useRef<HTMLSelectElement>(null);
  const saveRef = useRef<HTMLButtonElement>(null);
  const base = baseUnitName(sku);
  const choices = unitChoices(sku, barcode.unit_id);
  const value = draft ?? barcode.unit_id;
  const changed = value !== barcode.unit_id;

  const pick = (unitId: string) => {
    if (update.isPending) return;
    setError(null);
    setDraft(unitId === barcode.unit_id ? null : unitId);
  };

  // "Lưu" goes away once the change settles; the keyboard user who pressed it lands back on the picker.
  const settle = () => {
    if (document.activeElement === saveRef.current) pickerRef.current?.focus();
    setDraft(null);
  };

  const save = () => {
    if (update.isPending || !changed) return;
    setError(null);
    update.mutate(
      { barcodeId: barcode.id, unitId: value },
      {
        onSuccess: (saved) => {
          settle();
          const name = sku.units.find((u) => u.unit_id === saved.unit_id)?.unit_name;
          toast.success(
            name ? (
              <>
                Mã <b>{saved.code}</b> nay là mã của {name}.
              </>
            ) : (
              <>
                Đã đổi đơn vị của mã <b>{saved.code}</b>.
              </>
            ),
          );
        },
        onError: (e) => {
          settle();
          setError(barcodeUnitErrorMessage(e));
        },
      },
    );
  };

  return (
    <>
      <ButtonRow>
        <select
          ref={pickerRef}
          className="in"
          aria-label={`Đơn vị của mã ${barcode.code}`}
          value={value}
          // Locked while saving, yet focusable (as buttons with a write in flight), so the keyboard
          // user keeps their place; a pick made meanwhile is ignored and the shown unit stays.
          aria-disabled={update.isPending || undefined}
          aria-busy={update.isPending || undefined}
          style={update.isPending ? PENDING_STYLE : undefined}
          aria-invalid={error === null ? undefined : true}
          aria-describedby={error === null ? undefined : errorId}
          onChange={(e) => pick(e.target.value)}
        >
          {choices.some((u) => u.unit_id === barcode.unit_id) ? null : (
            // The list of units is older than the barcode (a unit added elsewhere): say so rather
            // than show the first unit as the barcode's.
            <option value={barcode.unit_id}>Đơn vị khác · tải lại để xem</option>
          )}
          {choices.map((u) => (
            <option key={u.unit_id} value={u.unit_id}>
              {unitLabel(u, base)}
            </option>
          ))}
        </select>
        {changed || update.isPending ? (
          <Button ref={saveRef} size="sm" variant="primary" aria-label={`Lưu đơn vị của mã ${barcode.code}`} pending={update.isPending} pendingLabel="Đang lưu…" onClick={save}>
            Lưu
          </Button>
        ) : null}
      </ButtonRow>
      {error === null ? null : (
        <div className="err-text" role="alert" id={errorId}>
          {error}
        </div>
      )}
    </>
  );
}
