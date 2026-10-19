import { isColorCode, type ColorCode } from "../../shared/domain/color";
import type { Money } from "../../shared/domain/money";
import { toQty, type Qty } from "../../shared/domain/qty";
import { dec, sumDec, type Decimal } from "../../shared/lib/decimal";
import type { ColorDirectory, SkuListItem } from "../catalog";
import { KIT_MESSAGES } from "./api/adapters";
import type { KitCreateRequest, KitItemRequest, RecipeRequest } from "./api/requests";
import type { ComponentCost, ComponentKind, Kit, KitType, MixGroup, RecipeItem } from "./schemas";

// The recipe editor's draft and its translation to and from the API's recipe items.

export interface ComponentDraft {
  key: string;
  mix: false;
  skuId: string | null;
  /** "SP001 · name" of the picked SKU, shown in the combobox. */
  label: string;
  /** Quantity in the SKU's base unit. */
  qty: Qty | null;
  colorId: string | null;
  kind: ComponentKind;
}

export interface MixDraft {
  key: string;
  mix: true;
  groupId: string;
  /** Room in the mix line, in `unit`. */
  qty: Qty | null;
  unit: "part" | "pack";
  /** Parts per flavor SKU. */
  parts: Record<string, Qty | null>;
}

export type LineDraft = ComponentDraft | MixDraft;

export interface KitDraft {
  type: KitType;
  code: string;
  name: string;
  retail: Money | null;
  wholesale: Money | null;
  minStock: Qty | null;
  packing: Money | null;
  note: string;
  lines: LineDraft[];
}

let seq = 0;
export const lineKey = () => `l${++seq}`;

export const emptyComponent = (): ComponentDraft => ({ key: lineKey(), mix: false, skuId: null, label: "", qty: toQty(1), colorId: null, kind: "goods" });

export const emptyMix = (group: MixGroup): MixDraft => ({
  key: lineKey(),
  mix: true,
  groupId: group.id,
  qty: toQty(3),
  unit: "part",
  parts: Object.fromEntries(group.items.filter((x) => x.active).map((x) => [x.sku_id, null])),
});

/** Recipe component kind of a picked SKU: materials go into the packaging. */
export const componentKind = (sku: Pick<SkuListItem, "product_type">): ComponentKind => (sku.product_type === "material" ? "packaging" : "goods");

/** Parts the mix line has room for, and the parts chosen. */
export function mixUsage(line: MixDraft, group: MixGroup): { cap: Decimal; used: Decimal } {
  const qty = dec(line.qty ?? "0");
  const cap = line.unit === "pack" ? qty.times(group.units_per_pack).div(group.units_per_part) : qty;
  return { cap, used: sumDec(Object.values(line.parts).map((p) => p ?? "0")) };
}

const positive = (q: Qty | null): q is Qty => q !== null && dec(q).gt(0);

function mixItem(line: MixDraft, group: MixGroup): KitItemRequest {
  const flavors = Object.entries(line.parts).flatMap(([sku_id, parts]) => (positive(parts) ? [{ sku_id, parts }] : []));
  // The API wants the parts to fill the line exactly, so the line carries what was chosen.
  return { kind: "mix_pack", mix_group_id: group.id, unit_id: group.part_unit_id, qty: toQty(mixUsage(line, group).used), flavors };
}

function componentItem(line: ComponentDraft & { skuId: string; qty: Qty }): KitItemRequest {
  return { kind: line.kind, sku_id: line.skuId, qty: line.qty, ...(line.colorId ? { color_id: line.colorId } : {}) };
}

export interface BuiltItems {
  items: KitItemRequest[];
  /** Draft line of each item, in order (the API numbers items from 1). */
  keys: string[];
}

/** Every complete line, for the live economics while the draft is being typed. */
export function completeItems(lines: readonly LineDraft[], groups: ReadonlyMap<string, MixGroup>): BuiltItems {
  const out: BuiltItems = { items: [], keys: [] };
  for (const line of lines) {
    if (line.mix) {
      const group = groups.get(line.groupId);
      if (!group) continue;
      const { cap, used } = mixUsage(line, group);
      if (used.lte(0) || used.gt(cap)) continue;
      out.items.push(mixItem(line, group));
    } else {
      if (line.skuId === null || !positive(line.qty)) continue;
      out.items.push(componentItem({ ...line, skuId: line.skuId, qty: line.qty }));
    }
    out.keys.push(line.key);
  }
  return out;
}

/** The recipe items to save, or the first prototype sentence that blocks saving. */
export function recipeItems(lines: readonly LineDraft[], groups: ReadonlyMap<string, MixGroup>, partName: (unitId: string) => string): KitItemRequest[] | string {
  if (lines.length === 0) return KIT_MESSAGES.noComponent;
  const items: KitItemRequest[] = [];
  for (const line of lines) {
    if (line.mix) {
      const group = groups.get(line.groupId);
      if (!group) return KIT_MESSAGES.componentIncomplete;
      if (group.status !== "active") return KIT_MESSAGES.mixGroupStopped(group.name);
      if (!positive(line.qty)) return KIT_MESSAGES.mixNoQty;
      const { cap, used } = mixUsage(line, group);
      if (used.lte(0)) return KIT_MESSAGES.mixNoFlavor(group.name);
      if (used.gt(cap)) return KIT_MESSAGES.mixOver(group.name, used.toString(), cap.toString(), partName(group.part_unit_id));
      items.push(mixItem(line, group));
    } else {
      if (line.skuId === null || !positive(line.qty)) return KIT_MESSAGES.componentIncomplete;
      items.push(componentItem({ ...line, skuId: line.skuId, qty: line.qty }));
    }
  }
  return items;
}

/** The recipe of a draft: kits carry their packing cost and note, combos only the components. */
export function recipeRequest(d: Pick<KitDraft, "type" | "packing" | "note">, items: KitItemRequest[]): RecipeRequest {
  if (d.type === "combo") return { items };
  const note = d.note.trim();
  return { items, ...(d.packing !== null ? { packing_cost: d.packing } : {}), ...(note ? { note } : {}) };
}

/** First prototype sentence blocking the header fields of a new kit or combo. */
export function headerError(d: KitDraft, creating: boolean): string | null {
  if (creating && !d.code.trim()) return KIT_MESSAGES.codeRequired;
  if (!d.name.trim()) return KIT_MESSAGES.nameRequired;
  if (d.retail === null || !dec(d.retail).gt(0)) return KIT_MESSAGES.pricePositive;
  if (d.wholesale !== null && dec(d.wholesale).gt(d.retail)) return KIT_MESSAGES.wholesaleAboveRetail;
  return null;
}

export const createRequest = (d: KitDraft, baseUnitId: string, items: KitItemRequest[]): KitCreateRequest => ({
  type: d.type,
  name: d.name.trim(),
  sku_code: d.code.trim(),
  base_unit_id: baseUnitId,
  retail_price: d.retail as Money,
  ...(d.wholesale !== null ? { wholesale_price: d.wholesale } : {}),
  ...(d.type === "kit" && d.minStock !== null ? { min_stock: d.minStock } : {}),
  recipe: recipeRequest(d, items),
});

/** Combobox label of a SKU already in a saved recipe ("SP001 · name"). */
export const componentLabel = (sku: Pick<SkuListItem, "code" | "display_name"> | undefined) => (sku ? `${sku.code} · ${sku.display_name}` : "");

/** Draft lines of a saved recipe; a mix line opens in parts. */
export function linesOf(items: readonly RecipeItem[], groups: ReadonlyMap<string, MixGroup>, label: (skuId: string) => string): LineDraft[] {
  return items.flatMap((it): LineDraft[] => {
    if (it.kind === "mix_pack") {
      const group = it.mix_group_id ? groups.get(it.mix_group_id) : undefined;
      if (!group) return [];
      const parts = Object.fromEntries(group.items.filter((x) => x.active).map((x) => [x.sku_id, null as Qty | null]));
      for (const f of it.flavors) parts[f.sku_id] = f.parts;
      const pack = it.unit_id === group.pack_unit_id;
      return [{ key: lineKey(), mix: true, groupId: group.id, qty: pack ? it.qty : toQty(sumDec(it.flavors.map((f) => f.parts))), unit: pack ? "pack" : "part", parts }];
    }
    if (!it.sku_id) return [];
    return [{ key: lineKey(), mix: false, skuId: it.sku_id, label: label(it.sku_id), qty: it.base_qty, colorId: it.color_id ?? null, kind: it.kind }];
  });
}

/** A saved recipe item as a request item, to price that version again through `POST /kits:preview`. */
export function itemRequestOf(it: RecipeItem): KitItemRequest {
  return {
    kind: it.kind,
    unit_id: it.unit_id,
    qty: it.qty,
    ...(it.sku_id ? { sku_id: it.sku_id } : {}),
    ...(it.color_id ? { color_id: it.color_id } : {}),
    ...(it.mix_group_id ? { mix_group_id: it.mix_group_id } : {}),
    ...(it.kind === "mix_pack" ? { flavors: it.flavors } : {}),
  };
}

/** A kit or combo on sale whose latest version is a draft not yet published. */
export const hasPendingDraft = (kit: Pick<Kit, "status" | "latest">) => kit.status !== "draft" && kit.latest.status === "draft";

/** The editor draft of a saved kit, with the prices and minimum stock of its SKU. */
export function draftOfKit(kit: Kit, sku: SkuListItem | undefined, lines: LineDraft[]): KitDraft {
  return {
    type: kit.type,
    code: kit.sku_code,
    name: kit.name,
    retail: sku?.retail_price ?? null,
    wholesale: sku?.wholesale_price ?? null,
    minStock: sku?.min_stock ?? null,
    packing: kit.latest.packing_cost,
    note: kit.latest.note ?? "",
    lines,
  };
}

/** Component economics of one draft line (a mix line has one row per flavor). */
export function costsByLine(components: readonly ComponentCost[], keys: readonly string[]): Map<string, ComponentCost[]> {
  const out = new Map<string, ComponentCost[]>();
  for (const c of components) {
    const key = keys[c.line_no - 1];
    if (key === undefined) continue;
    out.set(key, [...(out.get(key) ?? []), c]);
  }
  return out;
}

/** Components a customer sees on the kit; materials are listed as the packing. */
export const isMaterial = (kind: ComponentKind) => kind === "packaging" || kind === "basket" || kind === "accessory";

/** Fixed colors the SKU holds stock in. */
export function stockColors(sku: Pick<SkuListItem, "on_hand_by_color"> | undefined, colors: ColorDirectory): ColorCode[] {
  return (sku?.on_hand_by_color ?? []).flatMap((c) => {
    const code = colors.codeOf(c.color_id);
    return isColorCode(code) && dec(c.qty).gt(0) ? [code] : [];
  });
}

export interface OpenColor {
  skuId: string;
  name: string;
  /** Colors the SKU holds stock in. */
  palette: ColorCode[];
}

/**
 * Components whose color is left open although their SKU holds colored stock: the stock ledger
 * refuses to issue such a SKU without a color, so an assembly order has to name one.
 */
export function openColors(
  components: readonly { sku_id?: string | null | undefined; color_id?: string | null | undefined }[],
  skus: ReadonlyMap<string, SkuListItem> | undefined,
  colors: ColorDirectory,
): OpenColor[] {
  const seen = new Set<string>();
  return components.flatMap((c) => {
    if (!c.sku_id || c.color_id || seen.has(c.sku_id)) return [];
    seen.add(c.sku_id);
    const sku = skus?.get(c.sku_id);
    const palette = stockColors(sku, colors);
    return sku && palette.length > 0 ? [{ skuId: c.sku_id, name: sku.display_name, palette }] : [];
  });
}

/**
 * Which recipe line a control belongs to ("Thành phần 2"): its controls are named "Số lượng thành
 * phần 2", "Màu thành phần 2", "Xóa thành phần 2", so the rows of one recipe never share a name.
 */
export const componentPosition = (index: number): string => `Thành phần ${index + 1}`;
