import type { CSSProperties } from "react";
import { RadioGroup } from "radix-ui";
import { COLORS, colorDef, isColorCode, type ColorCode } from "../../domain/color";
import { cx } from "../../lib/cx";

export interface ColorSwatchGroupProps {
  value: ColorCode | null;
  onChange: (color: ColorCode) => void;
  /** Allowed colors; all seven by default. */
  colors?: readonly ColorCode[];
  label?: string;
  disabled?: boolean;
  className?: string;
}

/**
 * Prototype `swatches()` (CAT-09): 22×22 swatches as a radio group with arrow-key roving focus,
 * followed by the chosen name or "Chưa chọn". Color is always picked, never typed.
 */
export function ColorSwatchGroup({ value, onChange, colors, label = "Màu sắc", disabled, className }: ColorSwatchGroupProps) {
  const list = colors ? COLORS.filter((c) => colors.includes(c.key)) : COLORS;
  return (
    <RadioGroup.Root
      className={cx("swatches", className)}
      aria-label={label}
      value={value ?? ""}
      onValueChange={(v) => {
        if (isColorCode(v)) onChange(v);
      }}
      disabled={disabled}
      orientation="horizontal"
      loop
    >
      <span className="sw-l" aria-hidden="true">
        Màu
      </span>
      {list.map((c) => (
        <RadioGroup.Item key={c.key} value={c.key} className={cx("sw", c.light && "light")} aria-label={c.label} title={c.label} style={{ "--sw": c.css } as CSSProperties} />
      ))}
      <span className="sw-n">{value ? colorDef(value).label : "Chưa chọn"}</span>
    </RadioGroup.Root>
  );
}
