import { useQueries } from "@tanstack/react-query";
import { useApiClient } from "../../../shared/api/api-context";
import { colorLabel, type ColorKey } from "../../../shared/domain/color";
import type { Qty } from "../../../shared/domain/qty";
import { dec, toDecimalString, type Decimal } from "../../../shared/lib/decimal";
import { fd, num, vnd } from "../../../shared/lib/format";
import type { ComboItem } from "../../../shared/ui/combobox";
import { inLineUnits, lineName } from "../api/adapters";
import { skuStockQuery, useCustomerDirectory, useSalesDocumentList, type ColorMaps } from "../api/queries";
import type { ReturnLineInput } from "../api/return-mutations";
import type { ReturnableLine } from "../schemas";

// What a return or an exchange takes back from a source document: the lines that still have
// something returnable, split by color as the server sold them. A line an older document sold
// without a color asks which color the customer brings back when the product is stocked by color.

/** Parent lines of a mix pack or combo carry no stock; their children are returned. */
const STOCKLESS_KINDS = new Set<ReturnableLine["line_kind"]>(["mix_pack", "combo"]);

/** "Chứng từ bán gốc" candidates: the latest sale documents that are not fully returned yet. */
export function useSourceDocumentItems(): ComboItem[] {
  const list = useSalesDocumentList();
  const customers = useCustomerDirectory();
  return list.items
    .filter((d) => d.status !== "reversed")
    .map((d) => {
      const customer = customers.name(d.customer_party_id);
      return { id: d.id, name: d.doc_no, inputLabel: `${d.doc_no} · ${customer}`, detail: `${customer} · ${fd(d.business_date)} · ${vnd(d.total_amount)}`, keywords: [customer] };
    });
}

export interface ReturnRow {
  line: ReturnableLine;
  /** Still returnable, in the unit of the sold line. */
  max: string;
  /** Price the customer paid per line unit (after discounts). */
  price: Decimal;
  /** The color the document recorded; null when it recorded none. */
  color: ColorKey | null;
}

export function returnRows(lines: readonly ReturnableLine[], colors: ColorMaps): ReturnRow[] {
  return lines.flatMap((l) => {
    if (STOCKLESS_KINDS.has(l.line_kind) || dec(l.returnable_base_qty).lte(0)) return [];
    const sold = dec(l.sold_base_qty);
    const price = sold.gt(0) ? dec(l.line_amount).times(l.unit_factor).div(sold) : dec(l.unit_amount);
    const code = colors.codeOf(l.color_id);
    return [{ line: l, max: inLineUnits(l.returnable_base_qty, l.unit_factor), price, color: code === "none" ? null : code }];
  });
}

/**
 * Colors each SKU is stocked in (any quantity), for the "Chọn màu khách trả" picker of the lines a
 * document sold without a color. A SKU without colors gets an empty list and needs no pick.
 */
export function useReturnColorChoices(skuIds: readonly string[], colors: ColorMaps): (skuId: string) => ColorKey[] {
  const client = useApiClient();
  const unique = [...new Set(skuIds)];
  const stocks = useQueries({ queries: unique.map((id) => skuStockQuery(client, id)) });
  const bySku = new Map<string, ColorKey[]>();
  for (const s of stocks) {
    if (!s.data) continue;
    const codes: ColorKey[] = [];
    for (const c of s.data.colors) {
      const code = colors.codeOf(c.color_id);
      if (code !== "none" && !codes.includes(code)) codes.push(code);
    }
    bySku.set(s.data.sku_id, codes);
  }
  return (skuId: string) => bySku.get(skuId) ?? [];
}

export interface ReturnPick {
  qty: Qty | null;
  /** Color picked for a line sold without one. */
  color: ColorKey | null;
}

/**
 * Checks the quantities and colors typed against what is still returnable and builds the lines of
 * the command; a problem comes back as the toast text.
 */
export function returnLinesInput(
  rows: readonly ReturnRow[],
  pickOf: (row: ReturnRow) => ReturnPick,
  choicesOf: (skuId: string) => readonly ColorKey[],
  colors: ColorMaps,
  restockTo: ReturnLineInput["restock_to"],
): { lines: ReturnLineInput[] } | { problem: string } {
  const lines: ReturnLineInput[] = [];
  for (const r of rows) {
    const pick = pickOf(r);
    if (pick.qty === null || dec(pick.qty).lte(0)) continue;
    const color = r.color ?? pick.color;
    const name = lineName(r.line);
    if (dec(pick.qty).gt(r.max)) return { problem: `${name}${color && color !== "none" ? ` ${colorLabel(color)}` : ""}: chỉ còn được trả ${num(r.max)}.` };
    if (r.color === null && pick.color === null && choicesOf(r.line.sku_id).length > 0) return { problem: `Chọn màu khách trả cho ${name}.` };
    const colorId = r.line.color_id ?? colors.idOf(pick.color);
    lines.push({
      sales_document_line_id: r.line.sales_document_line_id,
      unit_id: r.line.unit_id,
      ...(colorId ? { color_id: colorId } : {}),
      qty: toDecimalString(pick.qty),
      restock_to: restockTo,
    });
  }
  return lines.length > 0 ? { lines } : { problem: "Chưa chọn số lượng trả." };
}
