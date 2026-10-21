import { HttpResponse, http } from "msw";
import { apiUrl } from "../../../test/msw/handlers";
import { testSku } from "../../procurement/testing/fixtures";
import { mixGroupSchema, type MixGroup, type SkuListItem } from "../schemas";

// API rows the catalog tests build their MSW answers from. Shapes follow the response schemas;
// each builder takes overrides for the fields a test is about.

export const UNIT_THUNG = "01J000000000000000000UTHUNG";
export const UNIT_GOI = "01J00000000000000000000UGOI";

export const MIX_UNITS = [
  { id: UNIT_THUNG, name: "thùng", active: true },
  { id: UNIT_GOI, name: "gói", active: true },
];

/** The two candy flavors of the test mix group, counted in gói. */
export const FLAVOR_SKUS: SkuListItem[] = [
  testSku({ id: "sku-dau", code: "KD01", display_name: "Kẹo dâu", base_unit_id: UNIT_GOI, units: [{ unit_id: UNIT_GOI, unit_name: "gói", factor: "1", is_base: true, active: true, is_default_sale: true, is_default_purchase: true, retail_price: null, wholesale_price: null, locked_by: null }] }),
  testSku({ id: "sku-cam", code: "KC01", display_name: "Kẹo cam", base_unit_id: UNIT_GOI, units: [{ unit_id: UNIT_GOI, unit_name: "gói", factor: "1", is_base: true, active: true, is_default_sale: true, is_default_purchase: true, retail_price: null, wholesale_price: null, locked_by: null }] }),
];

/** An active group of two flavors, 12 gói a thùng, already used on three invoices. */
export function testMixGroup(over: Partial<MixGroup> = {}): MixGroup {
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
      { sku_id: "sku-dau", sku_code: "KD01", sku_name: "Kẹo dâu", flavor_label: "Dâu", sort_order: 1, active: true },
      { sku_id: "sku-cam", sku_code: "KC01", sku_name: "Kẹo cam", flavor_label: "Cam", sort_order: 2, active: true },
    ],
    usage: [{ label: "hóa đơn", count: 3 }],
    used_sku_ids: ["sku-dau"],
    version: 1,
    ...over,
  });
}

/** The reads behind the mix group screen: the groups given, the units and the flavor SKUs. */
export function mixGroupHandlers(groups: readonly MixGroup[]) {
  return [
    http.get(apiUrl("/mix-groups"), () => HttpResponse.json({ data: [...groups], next_cursor: null })),
    http.get(apiUrl("/units"), () => HttpResponse.json({ data: MIX_UNITS, next_cursor: null })),
    http.get(apiUrl("/skus"), () => HttpResponse.json({ data: FLAVOR_SKUS, next_cursor: null })),
  ];
}
