import { colorLabel } from "../../../../shared/domain/color";
import type { ItemDraft } from "./draft";

/** Product name of a line without its SKU code ("SP001 · Bánh quy bơ" → "Bánh quy bơ"). */
export function shortName(line: ItemDraft): string {
  const i = line.skuLabel.indexOf(" · ");
  return i >= 0 ? line.skuLabel.slice(i + 3) : line.skuLabel;
}

/** What tells a line apart from the others: its product and picked color (same product and color merge). */
export function itemLineName(line: ItemDraft): string {
  const name = line.skuId === null ? "" : shortName(line);
  return name !== "" && line.color !== null ? `${name} ${colorLabel(line.color)}` : name;
}

/**
 * Accessible name of a control in a line row, so a reader can tell the rows apart:
 * "Số lượng – Bánh quy bơ Đỏ", or "Số lượng – dòng 3" while the line has no product yet.
 */
export function rowLabel(label: string, name: string, index: number): string {
  return `${label} – ${name === "" ? `dòng ${index + 1}` : name}`;
}
