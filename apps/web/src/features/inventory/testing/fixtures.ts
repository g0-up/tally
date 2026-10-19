import {
  colorStockSchema,
  countListItemSchema,
  countLineSchema,
  countSchema,
  lotSchema,
  stockRowSchema,
  type ColorStock,
  type Count,
  type CountLine,
  type CountListItem,
  type Lot,
  type StockRow,
} from "../schemas";

// API rows the inventory tests build their MSW answers and props from. Shapes follow the response
// schemas; each builder takes overrides for the fields a test is about.

export const COLOR_RED = "01J0000000000000000000CLRDO";
export const COLOR_BLUE = "01J000000000000000000CLRXANH";

/** One colored stock bucket of a stock row. */
export function testColorStock(over: Record<string, unknown> = {}): ColorStock {
  return colorStockSchema.parse({ color_id: COLOR_RED, color_code: "do", color_name: "Đỏ", on_hand: "10", sellable: "10", quarantine: "0", expired: "0", value: "100000", ...over });
}

/** 24 hộp of the test SKU, untagged. */
export function testStockRow(over: Record<string, unknown> = {}): StockRow {
  return stockRowSchema.parse({
    sku_id: "sku-1",
    code: "SP001",
    name: "Bánh quy bơ",
    variant_label: null,
    unit_name: "hộp",
    category_id: null,
    status: "active",
    track_lot: false,
    track_expiry: false,
    min_stock: "0",
    max_stock: null,
    on_hand: "24",
    sellable: "24",
    reserved: "0",
    available: "24",
    quarantine: "0",
    expired: "0",
    avg_cost: "9000",
    stock_value: "216000",
    price: "15000",
    low: false,
    over: false,
    colors: [],
    ...over,
  });
}

export function testLot(over: Record<string, unknown> = {}): Lot {
  return lotSchema.parse({
    id: "lot-1",
    sku_id: "sku-1",
    sku_code: "SP001",
    sku_name: "Bánh quy bơ",
    variant_label: null,
    unit_name: "hộp",
    product_type: "goods",
    lot_no: "L2601",
    expiry_date: "2027-03-01",
    days_to_expiry: 134,
    is_quarantine: false,
    received_on: "2026-10-01",
    qty: "24",
    state: "normal",
    color_id: null,
    color_code: null,
    color_name: null,
    ...over,
  });
}

export function testCountLine(over: Record<string, unknown> = {}): CountLine {
  return countLineSchema.parse({
    id: "cl-1",
    sku_id: "sku-1",
    sku_code: "SP001",
    sku_name: "Bánh quy bơ",
    variant_label: null,
    unit_name: "hộp",
    track_lot: false,
    lot_id: null,
    lot_no: null,
    system_qty: null,
    counted_qty: null,
    variance_qty: null,
    color_id: null,
    color_code: null,
    color_name: null,
    ...over,
  });
}

/** A blind whole-store count in progress. */
export function testCount(over: Record<string, unknown> = {}): Count {
  return countSchema.parse({
    id: "cnt-1",
    doc_no: "KK-0001",
    scope: "all",
    category_id: null,
    blind: true,
    status: "counting",
    business_date: "2026-10-12",
    started_at: "2026-10-12T02:00:00Z",
    note: null,
    version: 2,
    adjustment_no: null,
    lines: [testCountLine()],
    ...over,
  });
}

/** A row of `GET /stock-counts`: no lines, their totals, and no variances while a blind count is open. */
export function testCountListItem(over: Record<string, unknown> = {}): CountListItem {
  return countListItemSchema.parse({ ...testCount({ lines: null }), line_count: 1, counted_lines: 0, ...over });
}
