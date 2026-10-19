import { useMemo, useState } from "react";
import type { Money } from "../../../shared/domain/money";
import { useDebounce } from "../../../shared/hooks/use-debounce";
import { useColorDirectory, useSkuPicker, useSkusByIds, useUnits, type SkuListItem } from "../../catalog";
import { useDraftCost, useMixGroups } from "../api/queries";
import type { RecipeRequest } from "../api/requests";
import type { ComponentCost, Kit, MixGroup } from "../schemas";
import { completeItems, costsByLine, recipeItems, recipeRequest, type KitDraft, type LineDraft } from "../view-model";

const DEBOUNCE_MS = 300;

/**
 * State shared by every recipe editor (new kit, draft edit, new version, combo edit): the SKUs the
 * lines refer to, the mix groups, and the live economics of the complete lines priced at
 * the kit's retail price.
 */
export function useRecipeEditor(lines: readonly LineDraft[], recipe: Pick<KitDraft, "type" | "packing" | "note">, retail: Money | null) {
  const picker = useSkuPicker(null);
  const units = useUnits();
  const colors = useColorDirectory();
  const groupsQuery = useMixGroups();
  const groups = useMemo(() => groupsQuery.data ?? [], [groupsQuery.data]);
  const groupMap = useMemo(() => new Map<string, MixGroup>(groups.map((g) => [g.id, g])), [groups]);
  const [picked, setPicked] = useState<ReadonlyMap<string, SkuListItem>>(new Map());

  // Saved components and every flavor of the mix groups in use, for prices and availability.
  const ids = useMemo(
    () => lines.flatMap((l) => (l.mix ? (groupMap.get(l.groupId)?.items.map((x) => x.sku_id) ?? []) : l.skuId ? [l.skuId] : [])),
    [lines, groupMap],
  );
  const known = useSkusByIds(ids);
  const skuOf = (id: string) => picked.get(id) ?? known.data?.get(id);

  const built = completeItems(lines, groupMap);
  const body = built.items.length > 0 ? { ...recipeRequest(recipe, built.items), ...(retail !== null ? { kit_price: retail } : {}) } : null;
  // Typing changes the body on every keystroke; the preview waits for a pause (keyed by content).
  const debounced = useDebounce(body === null ? "" : JSON.stringify(body), DEBOUNCE_MS);
  const previewBody = useMemo(() => (debounced ? (JSON.parse(debounced) as RecipeRequest & { kit_price?: string }) : null), [debounced]);
  const preview = useDraftCost(previewBody);
  const cost = body === null ? undefined : preview.data;
  // Costs map back to lines by position, so a preview of other lines than the draft's maps nothing.
  const keys = previewBody !== null && previewBody.items.length === built.keys.length ? built.keys : [];
  const costs = cost ? costsByLine(cost.components, keys) : new Map<string, ComponentCost[]>();

  return {
    picker,
    units,
    colors,
    groups,
    cost,
    skuOf,
    /** Recipe items to save, or the sentence blocking the save. */
    items: () => recipeItems(lines, groupMap, units.name),
    editor: {
      groups,
      costs,
      totals: cost,
      picker,
      skuOf,
      onPicked: (sku: SkuListItem) => setPicked((m) => new Map(m).set(sku.id, sku)),
      unitName: units.name,
      colors,
    },
  };
}

/**
 * What an editor opened on a saved kit needs before its draft can be built: the mix groups (a
 * stopped one included, so its saved line is shown rather than dropped) and the kit's own SKU and
 * component SKUs (prices, minimum stock, labels).
 */
export function useRecipeSeed(kit: Kit | undefined) {
  const groups = useMixGroups();
  const ids = useMemo(() => (kit ? [kit.sku_id, ...kit.latest.items.flatMap((i) => (i.sku_id ? [i.sku_id] : []))] : []), [kit]);
  const skus = useSkusByIds(ids);
  const groupMap = useMemo(() => new Map<string, MixGroup>((groups.data ?? []).map((g) => [g.id, g])), [groups.data]);
  return {
    ready: groups.isSuccess && (ids.length === 0 || skus.isSuccess),
    failed: groups.isError || skus.isError,
    groups: groupMap,
    skus: skus.data ?? EMPTY_SKUS,
  };
}

const EMPTY_SKUS: ReadonlyMap<string, SkuListItem> = new Map();
