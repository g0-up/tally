import { HttpResponse, http } from "msw";
import { apiUrl } from "../../../test/msw/handlers";
import type { SkuListItem } from "../../catalog";
import { UNIT_HOP, UNIT_THUNG } from "../../procurement/testing/fixtures";
import {
  assemblyDetailSchema,
  assemblyOrderSchema,
  costViewSchema,
  disassemblySchema,
  kitSchema,
  mixGroupSchema,
  postingSchema,
  type AssemblyDetail,
  type AssemblyOrder,
  type CostView,
  type Disassembly,
  type Kit,
  type MixGroup,
  type Posting,
} from "../schemas";

// API rows the kit tests build their MSW answers and props from. Shapes follow the response
// schemas; each builder takes overrides for the fields a test is about.

export const UNIT_GIO = "01J00000000000000000000UGIO";
export const UNIT_GOI = "01J00000000000000000000UGOI";

export const KIT_UNITS = [
  { id: UNIT_HOP, name: "hộp", active: true },
  { id: UNIT_THUNG, name: "thùng", active: true },
  { id: UNIT_GIO, name: "giỏ", active: true },
  { id: UNIT_GOI, name: "gói", active: true },
];

/** A mix group of two candy flavors, mixed by the gói (12 gói a thùng). */
export function testMixGroup(over: Record<string, unknown> = {}): MixGroup {
  return mixGroupSchema.parse({
    id: "mix-1",
    name: "Kẹo mix vị",
    pack_unit_id: UNIT_THUNG,
    units_per_pack: 12,
    part_unit_id: UNIT_GOI,
    units_per_part: 1,
    piece_unit_id: UNIT_GOI,
    status: "active",
    items: [
      { sku_id: "sku-dau", sku_name: "Kẹo dâu", flavor_label: "Dâu", active: true },
      { sku_id: "sku-cam", sku_name: "Kẹo cam", flavor_label: "Cam", active: true },
    ],
    ...over,
  });
}

/** Economics of a one-line recipe: 450.000 ₫ at retail, 450.000 ₫ of cost, 4 kits buildable. */
export function testCostView(over: Record<string, unknown> = {}): CostView {
  return costViewSchema.parse({
    components: [
      {
        line_no: 1,
        kind: "goods",
        sku_id: "sku-1",
        sku_code: "SP001",
        name: "Bánh quy bơ",
        unit_id: UNIT_HOP,
        qty: "3",
        base_qty: "3",
        unit_price: "150000",
        sale_amount: "450000",
        unit_cost: "143333",
        cost: "430000",
        sellable: "12",
        buildable: "4",
      },
    ],
    components_sale: "450000",
    components_cost: "430000",
    packing_cost: "20000",
    total_cost: "450000",
    kit_price: "500000",
    margin: "50000",
    buildable: "4",
    ...over,
  });
}

/** A draft kit "Giỏ An Khang" with a one-line recipe. */
export function testKit(over: Record<string, unknown> = {}): Kit {
  return kitSchema.parse({
    product_id: "kit-1",
    type: "kit",
    name: "Giỏ An Khang",
    status: "draft",
    sku_id: "sku-kit",
    sku_code: "GQAK",
    base_unit_id: UNIT_GIO,
    active_version: null,
    sold: false,
    latest: {
      id: "rcp-1",
      version_no: 1,
      status: "draft",
      packing_cost: "20000",
      items: [{ id: "ri-1", line_no: 1, kind: "goods", sku_id: "sku-1", unit_id: UNIT_HOP, qty: "3", base_qty: "3", flavors: [] }],
    },
    version: 1,
    ...over,
  });
}

/** A reserved order for 10 kits of "Giỏ An Khang", 4 of them already made. */
export function testAssemblyOrder(over: Record<string, unknown> = {}): AssemblyOrder {
  return assemblyOrderSchema.parse({
    id: "asm-1",
    doc_no: "LD-0001",
    product_id: "kit-1",
    kit_name: "Giỏ An Khang",
    kit_sku_id: "sku-kit",
    recipe_version: 1,
    planned_qty: "10",
    completed_qty: "4",
    disassembled_qty: "0",
    status: "partially_completed",
    planned_date: "2026-10-20",
    invoice_id: null,
    created_at: "2026-10-12T02:00:00Z",
    version: 3,
    ...over,
  });
}

/** One posting of 4 kits into lot GQ2601, consuming 12 hộp of lot L2601. */
export function testPosting(over: Record<string, unknown> = {}): Posting {
  return postingSchema.parse({
    id: "pst-1",
    order_id: "asm-1",
    produced_qty: "4",
    output_lot_id: "lot-kit-1",
    output_lot_no: "GQ2601",
    output_expiry: "2027-01-31",
    output_unit_cost: "450000",
    business_date: "2026-10-12",
    items: [{ id: "pi-1", order_item_id: "ai-1", sku_id: "sku-1", lot_id: "lot-1", lot_no: "L2601", consumed_qty: "12", scrap_qty: "0", unit_cost: "143333" }],
    ...over,
  });
}

/** The test order with its component line (30 hộp planned) and its posting. */
export function testAssemblyDetail(over: Record<string, unknown> = {}): AssemblyDetail {
  return assemblyDetailSchema.parse({
    ...testAssemblyOrder(),
    items: [{ id: "ai-1", sku_id: "sku-1", sku_code: "SP001", sku_name: "Bánh quy bơ", planned_qty: "30", consumed_qty: "12", remaining_qty: "18", reserved_qty: "18" }],
    postings: [testPosting()],
    components_cost: "4300000",
    packing_cost: "200000",
    expected_cost: "4500000",
    ...over,
  });
}

/** One kit of lot GQ2601 taken apart, its 3 hộp back to lot L2601. */
export function testDisassembly(over: Record<string, unknown> = {}): Disassembly {
  return disassemblySchema.parse({
    id: "dis-1",
    doc_no: "TG-0001",
    order_id: "asm-1",
    order_doc_no: "LD-0001",
    kit_lot_id: "lot-kit-1",
    kit_lot_no: "GQ2601",
    qty: "1",
    reason_id: "rsn-1",
    business_date: "2026-10-13",
    lines: [{ id: "dl-1", sku_id: "sku-1", source_lot_id: "lot-1", lot_no: "L2601", return_qty: "3", damaged_qty: "0", unit_cost: "143333" }],
    ...over,
  });
}

/** The lookups every recipe editor reads, with `skus` as the SKUs it resolves and `cost` as its preview. */
export function recipeEditorHandlers(skus: SkuListItem[], cost: CostView = testCostView()) {
  return [
    http.get(apiUrl("/units"), () => HttpResponse.json({ data: KIT_UNITS, next_cursor: null })),
    http.get(apiUrl("/colors"), () => HttpResponse.json({ data: [], next_cursor: null })),
    http.get(apiUrl("/mix-groups"), () => HttpResponse.json({ data: [testMixGroup()], next_cursor: null })),
    http.get(apiUrl("/skus"), () => HttpResponse.json({ data: skus, next_cursor: null })),
    http.post(/\/kits:preview$/, () => HttpResponse.json(cost)),
  ];
}
