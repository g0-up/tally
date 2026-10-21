import { toMoney } from "../../../shared/domain/money";
import { toQty } from "../../../shared/domain/qty";
import type { SkuListItem } from "../../catalog";
import { orderLineSchema, orderSchema, receiptListItemSchema, type Order, type OrderLine, type ReceiptListItem } from "../schemas";

// API rows the procurement tests build their MSW answers and props from. Shapes follow the
// response schemas; each builder takes overrides for the fields a test is about.

export const UNIT_HOP = "01J0000000000000000000UHOP";
export const UNIT_THUNG = "01J000000000000000000UTHUNG";
export const SUPPLIER_ID = "01J0000000000000000000SUPP";

export const TEST_UNITS = [
  { id: UNIT_HOP, name: "hộp", active: true },
  { id: UNIT_THUNG, name: "thùng", active: true },
];

/** A goods SKU counted in hộp and bought by the thùng of 12. */
export function testSku(over: Partial<SkuListItem> = {}): SkuListItem {
  return {
    id: "sku-1",
    product_id: "prod-1",
    code: "SP001",
    display_name: "Bánh quy bơ",
    variant_label: "",
    base_unit_id: UNIT_HOP,
    track_lot: false,
    track_expiry: false,
    ref_cost: toMoney(9000),
    min_price: null,
    min_stock: toQty(0),
    spec_text: null,
    status: "active",
    product_type: "goods",
    category_id: null,
    category_name: null,
    units: [
      { unit_id: UNIT_HOP, unit_name: "hộp", factor: "1", is_base: true, active: true, is_default_sale: true, is_default_purchase: false, retail_price: null, wholesale_price: null, locked_by: null },
      { unit_id: UNIT_THUNG, unit_name: "thùng", factor: "12", is_base: false, active: true, is_default_sale: false, is_default_purchase: true, retail_price: null, wholesale_price: null, locked_by: null },
    ],
    barcode: null,
    retail_price: toMoney(15000),
    wholesale_price: null,
    has_color_prices: false,
    on_hand: toQty(0),
    sellable: toQty(0),
    avg_cost: null,
    last_purchase_price: toMoney(10000),
    in_mix_group: false,
    on_hand_by_color: [],
    ...over,
  };
}

/** An ordered line of 2 thùng (24 hộp) of the test SKU at 120.000 ₫ a thùng. */
export function testOrderLine(over: Record<string, unknown> = {}): OrderLine {
  return orderLineSchema.parse({
    id: "pol-1",
    line_no: 1,
    sku_id: "sku-1",
    sku_code: "SP001",
    name: "Bánh quy bơ",
    unit_id: UNIT_THUNG,
    unit_name: "thùng",
    unit_factor: "12",
    qty: "2",
    base_qty: "24",
    unit_price: "120000",
    line_amount: "240000",
    received_base_qty: "0",
    pending_base_qty: "24",
    ...over,
  });
}

export function testOrder(over: Record<string, unknown> = {}): Order {
  return orderSchema.parse({
    id: "po-1",
    doc_no: "PO-0001",
    supplier_party_id: SUPPLIER_ID,
    supplier_name: "Công ty Bánh kẹo Hải Hà",
    order_date: "2026-10-10",
    expected_date: "2026-10-15",
    status: "confirmed",
    total_amount: "240000",
    created_at: "2026-10-10T02:00:00Z",
    version: 3,
    ordered_base_qty: "24",
    received_base_qty: "0",
    pending_base_qty: "24",
    ...over,
  });
}

export function testReceipt(over: Record<string, unknown> = {}): ReceiptListItem {
  return receiptListItemSchema.parse({
    id: "rcv-1",
    doc_no: "PN-0001",
    supplier_party_id: SUPPLIER_ID,
    supplier_name: "Công ty Bánh kẹo Hải Hà",
    business_date: "2026-10-11",
    goods_amount: "240000",
    tax_amount: "0",
    landed_cost_amount: "0",
    total_amount: "240000",
    status: "posted",
    open_amount: "240000",
    ...over,
  });
}
