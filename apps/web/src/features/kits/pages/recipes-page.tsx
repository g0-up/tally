import { useMemo, useState } from "react";
import { Button } from "../../../shared/ui/button";
import { Card } from "../../../shared/ui/card";
import { FilterBar } from "../../../shared/ui/filter-bar";
import { Grid } from "../../../shared/ui/grid";
import { ListFooter, ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { ToggleGroup } from "../../../shared/ui/toggle-group";
import { baseUnitName, useColorDirectory, useSkusByIds } from "../../catalog";
import { SeenMarker, useSeenIds } from "../../inventory";
import { usePublishKit } from "../api/mutations";
import { useKitCosts, useKitList, useKits, useSellingVersions, type KitVersion } from "../api/queries";
import { RecipeCard } from "../components/recipe-card";
import { AssemblyDialog } from "../containers/assembly-dialog";
import { ComboEditDialog } from "../containers/combo-edit-dialog";
import { KitDeleteDialog } from "../containers/kit-delete-dialog";
import { KitDialog } from "../containers/kit-dialog";
import { KitPreviewDrawer } from "../containers/kit-preview-drawer";
import { OldRecipeVersions } from "../containers/old-recipe-versions";
import { RecipeVersionDialog } from "../containers/recipe-version-dialog";
import type { Kit, KitListItem } from "../schemas";
import { hasPendingDraft } from "../view-model";

type Filter = "all" | "draft" | "selling";

type Dialog =
  | { kind: "new" }
  | { kind: "edit"; kit: Kit }
  | { kind: "delete"; kit: Kit }
  | { kind: "version"; kit: Kit }
  | { kind: "combo"; kit: Kit }
  | { kind: "assemble"; kitId: string };

/** Each card loads its kit and economics, so the list pages in smaller steps than the tables. */
const CARD_PAGE = 20;

/** Versions before the selling one (or before the latest when none sells). */
const supersededOf = (k: KitListItem) => Array.from({ length: Math.max((k.active_version ?? k.latest_version) - 1, 0) }, (_, i) => i + 1);

/**
 * Giỏ quà & combo (/gift-baskets/recipes): one card per kit or combo with its selling recipe, or
 * its latest one when none sells yet.
 */
export default function RecipesPage() {
  const list = useKitList(undefined, CARD_PAGE);
  const [filter, setFilter] = useState<Filter>("all");
  const [dialog, setDialog] = useState<Dialog | null>(null);
  const [preview, setPreview] = useState<string | null>(null);
  const publish = usePublishKit();
  const colors = useColorDirectory();

  const shown = useMemo(
    () => list.items.filter((k) => filter === "all" || (filter === "draft" ? k.status === "draft" : k.status !== "draft")),
    [list.items, filter],
  );
  // Each card costs a detail and a preview request, so only the cards shown so far load them.
  const { seen, markSeen } = useSeenIds();
  const ids = useMemo(() => shown.flatMap((k) => (seen.has(k.product_id) ? [k.product_id] : [])), [shown, seen]);
  const kits = useKits(ids);
  const costs = useKitCosts(ids);
  const waiting = useMemo(
    () => [...kits.values()].flatMap((k): KitVersion[] => (hasPendingDraft(k) && k.active_version ? [{ kitId: k.product_id, version: k.active_version }] : [])),
    [kits],
  );
  const selling = useSellingVersions(waiting);
  const componentIds = useMemo(
    () => [...costs.values(), ...[...selling.values()].map((s) => s.cost)].flatMap((c) => c.components.map((x) => x.sku_id)),
    [costs, selling],
  );
  const skus = useSkusByIds(componentIds).data;
  const unitOf = (id: string) => {
    const s = skus?.get(id);
    return s ? baseUnitName(s) : "";
  };

  const drafts = list.items.filter((k) => k.status === "draft").length;
  const close = () => setDialog(null);

  return (
    <>
      <PageHeader
        lead={
          <>
            Giỏ mới tạo ở trạng thái <b>Nháp</b> để xem trước giá, lãi và mẫu trình bày; chỉ bán được sau khi <b>Phát hành</b>. Giỏ đóng sẵn có tồn riêng và phải
            qua lệnh đóng; combo ghép khi bán không có tồn.
          </>
        }
        actions={
          <Button variant="primary" onClick={() => setDialog({ kind: "new" })}>
            Tạo giỏ / combo
          </Button>
        }
      />
      <FilterBar>
        <ToggleGroup
          label="Lọc giỏ/combo"
          value={filter}
          onChange={setFilter}
          options={[
            { value: "all", label: `Tất cả (${list.items.length})` },
            { value: "draft", label: `Nháp (${drafts})` },
            { value: "selling", label: `Đang bán (${list.items.length - drafts})` },
          ]}
        />
      </FilterBar>
      {list.isLoading ? (
        <ListLoading />
      ) : shown.length === 0 ? (
        <p className="hint">Không có giỏ/combo.</p>
      ) : (
        <Grid cols="g2">
          {shown.map((item) => {
            const kit = kits.get(item.product_id);
            if (!kit) {
              return (
                <Card key={item.product_id} title={item.name}>
                  <SeenMarker id={item.product_id} onSeen={markSeen} />
                  <p className="hint">Đang tải…</p>
                </Card>
              );
            }
            const latest = costs.get(kit.product_id);
            const sold = hasPendingDraft(kit) ? selling.get(kit.product_id) : { recipe: kit.latest, cost: latest };
            return (
              <RecipeCard
                key={kit.product_id}
                kit={kit}
                shown={sold?.recipe}
                cost={sold?.cost}
                price={latest?.kit_price ?? null}
                old={kit.type === "kit" ? <OldRecipeVersions kitId={kit.product_id} versions={supersededOf(item)} /> : null}
                colors={colors}
                unitOf={unitOf}
                busy={publish.pending}
                onPreview={() => setPreview(kit.product_id)}
                onEdit={() => setDialog({ kind: "edit", kit })}
                onPublish={() => void publish.run(kit)}
                onDelete={() => setDialog({ kind: "delete", kit })}
                onAssemble={() => setDialog({ kind: "assemble", kitId: kit.product_id })}
                onNewVersion={() => setDialog({ kind: "version", kit })}
                onEditCombo={() => setDialog({ kind: "combo", kit })}
              />
            );
          })}
        </Grid>
      )}
      <ListFooter list={list} />
      {preview !== null ? <KitPreviewDrawer kitId={preview} onClose={() => setPreview(null)} onEdit={(kit) => setDialog({ kind: "edit", kit })} /> : null}
      {dialog?.kind === "new" ? <KitDialog onClose={close} onPreview={setPreview} /> : null}
      {dialog?.kind === "edit" ? <KitDialog kit={dialog.kit} onClose={close} onPreview={setPreview} /> : null}
      {dialog?.kind === "delete" ? (
        <KitDeleteDialog
          kit={dialog.kit}
          onClose={close}
          onDeleted={() => {
            close();
            setPreview(null);
          }}
        />
      ) : null}
      {dialog?.kind === "version" ? <RecipeVersionDialog kit={dialog.kit} onClose={close} /> : null}
      {dialog?.kind === "combo" ? <ComboEditDialog kit={dialog.kit} onClose={close} /> : null}
      {dialog?.kind === "assemble" ? <AssemblyDialog kitId={dialog.kitId} onClose={close} /> : null}
    </>
  );
}
