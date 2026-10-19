import { colorBackground, colorDef, type ColorKey } from "../../domain/color";
import type { DecimalLike } from "../../lib/decimal";
import { num } from "../../lib/format";

export interface ColorQtyPillProps {
  color: ColorKey;
  qty: DecimalLike;
  /** Base unit name for the tooltip ("hộp"). */
  base: string;
  /** Highlight the color currently picked on the screen. */
  active?: boolean;
}

/** Prototype stock `.cpill`: dot, color name and on-hand quantity, e.g. "Đỏ 12"; untagged stock reads "Chưa gán". */
export function ColorQtyPill({ color, qty, base, active = false }: ColorQtyPillProps) {
  const name = color === "none" ? "Chưa gán" : colorDef(color).label;
  return (
    <span className={active ? "cpill on" : "cpill"} title={`${name}: tồn ${num(qty)} ${base}`}>
      <i className="cdot" style={{ background: colorBackground(color) }} aria-hidden="true" />
      {name} <b>{num(qty)}</b>
    </span>
  );
}
