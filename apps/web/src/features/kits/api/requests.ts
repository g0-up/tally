import type { Money } from "../../../shared/domain/money";
import type { Qty } from "../../../shared/domain/qty";
import type { ComponentKind, KitType } from "../schemas";

// Request bodies of the kit and assembly commands (OpenAPI `KitRequest`, `KitRecipeRequest`,
// `AssemblyOrderRequest`, `AssemblyBatchRequest`, `DisassemblyRequest`).

export interface KitItemRequest {
  kind: ComponentKind;
  sku_id?: string;
  /** Defaults to the SKU's base unit; a mix pack names its part or pack unit. */
  unit_id?: string;
  qty: Qty;
  color_id?: string;
  mix_group_id?: string;
  flavors?: { sku_id: string; parts: Qty }[];
}

export interface RecipeRequest {
  packing_cost?: Money;
  note?: string;
  items: KitItemRequest[];
}

export interface KitCreateRequest {
  type: KitType;
  name: string;
  sku_code: string;
  base_unit_id: string;
  retail_price: Money;
  wholesale_price?: Money;
  /** A pre-packed kit only; the server refuses it on a combo. */
  min_stock?: Qty;
  recipe: RecipeRequest;
}

/** The color an assembly takes for a component whose color the recipe leaves open. */
export interface ComponentColor {
  sku_id: string;
  color_id: string;
}

export interface AssemblyRequest {
  kit_id: string;
  planned_qty: Qty;
  invoice_id?: string;
  colors?: ComponentColor[];
}

/** What a batch takes for one order item; an item without a line takes its share FEFO. */
export interface BatchLineRequest {
  order_item_id: string;
  qty: Qty;
  color_id?: string;
}

export interface DisassemblyRequest {
  kit_lot_id: string;
  qty: Qty;
  reason_id: string;
  damaged?: { sku_id: string; source_lot_id?: string; qty: Qty }[];
}
