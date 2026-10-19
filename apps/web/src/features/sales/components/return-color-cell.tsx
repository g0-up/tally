import type { ColorKey } from "../../../shared/domain/color";
import { ColorChip } from "../../../shared/ui/color/color-chip";
import { StockColorPicker } from "../../../shared/ui/color/stock-color-picker";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";

export interface ReturnColorCellProps {
  /** Color the source document recorded for the line. */
  fixed: ColorKey | null;
  /** Colors the product is stocked in; empty for a product without colors. */
  choices: readonly ColorKey[];
  value: ColorKey | null;
  onChange: (color: ColorKey) => void;
  /** Product unit for the picker tooltip. */
  base: string;
  /** Row's product, so each row's picker has its own name. */
  name: string;
}

/**
 * Prototype `retColorCell`: the color sold, or, for an older document that recorded none on a
 * product stocked by color, the picker "Chọn màu khách trả" (with "Chưa gán" for unknown).
 */
export function ReturnColorCell({ fixed, choices, value, onChange, base, name }: ReturnColorCellProps) {
  if (fixed !== null) return <ColorChip color={fixed} />;
  if (choices.length === 0) return <span className="hint">—</span>;
  return (
    <>
      <StockColorPicker
        options={[...choices, "none" as const].map((color) => ({ color, qty: "0" }))}
        value={value}
        onChange={onChange}
        base={base}
        label={`Màu khách trả – ${name}`}
        needText="Chọn màu khách trả"
        showQty={false}
      />
      <CellSub>Chứng từ cũ chưa ghi màu</CellSub>
    </>
  );
}
