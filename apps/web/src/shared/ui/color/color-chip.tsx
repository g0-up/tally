import type { ReactNode } from "react";
import { colorBackground, colorLabel, type ColorKey } from "../../domain/color";

/** Prototype `colorChip()`: small square plus the color name ("Chưa gán màu" with the hatch for none). */
export function ColorChip({ color, children, className }: { color: ColorKey; children?: ReactNode; className?: string }) {
  return (
    <span className={className ? `sw-chip ${className}` : "sw-chip"}>
      <i style={{ background: colorBackground(color) }} aria-hidden="true" />
      {colorLabel(color)}
      {children !== undefined ? <> {children}</> : null}
    </span>
  );
}
