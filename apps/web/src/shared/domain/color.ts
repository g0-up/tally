import type { components } from "../api/schema";

// The seven fixed product colors (CAT-09) plus "none" for stock that has no color assigned.
// Values come from the --product-* tokens; "none" renders the hatch pattern.

export type ColorCode = components["schemas"]["ColorCode"];
export type ColorKey = ColorCode | "none";

export interface ColorDef {
  key: ColorCode;
  label: string;
  /** CSS value for backgrounds, always a token reference. */
  css: string;
  /** Light swatches get a visible border on white. */
  light: boolean;
}

export const COLORS: readonly ColorDef[] = [
  { key: "xanh", label: "Xanh", css: "var(--product-xanh)", light: false },
  { key: "do", label: "Đỏ", css: "var(--product-do)", light: false },
  { key: "tim", label: "Tím", css: "var(--product-tim)", light: false },
  { key: "vang", label: "Vàng", css: "var(--product-vang)", light: false },
  { key: "nau", label: "Nâu", css: "var(--product-nau)", light: false },
  { key: "den", label: "Đen", css: "var(--product-den)", light: false },
  { key: "trang", label: "Trắng", css: "var(--product-trang)", light: true },
];

export const NO_COLOR_LABEL = "Chưa gán màu";
export const HATCH_CSS = "var(--hatch)";

const BY_KEY = new Map(COLORS.map((c) => [c.key, c]));

export function isColorCode(value: unknown): value is ColorCode {
  return typeof value === "string" && BY_KEY.has(value as ColorCode);
}

export function colorDef(key: ColorCode): ColorDef {
  const c = BY_KEY.get(key);
  if (!c) throw new RangeError(`Unknown color: ${key}`);
  return c;
}

/** "Đỏ", or "Chưa gán màu" for stock without a color. */
export function colorLabel(key: ColorKey): string {
  return key === "none" ? NO_COLOR_LABEL : colorDef(key).label;
}

/** Background for a dot or chip: the token color, or the hatch for "none". */
export function colorBackground(key: ColorKey): string {
  return key === "none" ? HATCH_CSS : colorDef(key).css;
}
