import { useEffect, useId } from "react";
import { RadioGroup } from "radix-ui";
import { colorBackground, colorDef, type ColorKey } from "../../domain/color";
import { dec, type DecimalLike } from "../../lib/decimal";
import { num } from "../../lib/format";

export interface StockColorOption {
  color: ColorKey;
  /** Quantity available in this color (base unit). */
  qty: DecimalLike;
}

export interface StockColorPickerProps {
  options: readonly StockColorOption[];
  value: ColorKey | null;
  onChange: (color: ColorKey) => void;
  /** Base unit name ("hộp") for the "Còn N hộp" tooltip. */
  base: string;
  label?: string;
  /** Hint when nothing is picked; returns use "Chọn màu khách trả". */
  needText?: string;
  /** Hide quantities (return lines pick the color the customer brings back, not stock). */
  showQty?: boolean;
}

const isKey = (v: string, options: readonly StockColorOption[]): v is ColorKey => options.some((o) => o.color === v);

/**
 * Prototype `colorPick()` (CAT-09): which color the customer takes. Each option shows its name and
 * available quantity; an empty color is disabled unless already chosen; a single option is picked
 * automatically; "Chọn màu" / "Màu này đã hết" explain what is still needed and describe the group.
 */
export function StockColorPicker({ options, value, onChange, base, label = "Màu khách lấy", needText = "Chọn màu", showQty = true }: StockColorPickerProps) {
  const only = options.length === 1 ? options[0] : undefined;
  useEffect(() => {
    if (value === null && only && (!showQty || dec(only.qty).gt(0))) onChange(only.color);
  }, [value, only, showQty, onChange]);

  const current = value === null ? undefined : options.find((o) => o.color === value);
  const soldOut = showQty && current !== undefined && dec(current.qty).lte(0);
  const need = value === null ? needText : soldOut ? "Màu này đã hết" : null;
  const needId = useId();

  return (
    <RadioGroup.Root
      className="cpick"
      aria-label={label}
      aria-describedby={need === null ? undefined : needId}
      value={value ?? ""}
      onValueChange={(v) => {
        if (isKey(v, options)) onChange(v);
      }}
      orientation="horizontal"
      loop
    >
      {options.map((o) => {
        const name = o.color === "none" ? "Chưa gán" : colorDef(o.color).label;
        const empty = showQty && dec(o.qty).lte(0);
        return (
          <RadioGroup.Item key={o.color} value={o.color} className="cp" disabled={empty && o.color !== value} title={showQty ? `Còn ${num(o.qty)} ${base}` : undefined}>
            <i style={{ background: colorBackground(o.color) }} aria-hidden="true" />
            {name}
            {showQty ? (
              <>
                {" "}
                <small>{num(o.qty)}</small>
              </>
            ) : null}
          </RadioGroup.Item>
        );
      })}
      {need === null ? null : (
        <span className="cp-need" id={needId}>
          {need}
        </span>
      )}
    </RadioGroup.Root>
  );
}
