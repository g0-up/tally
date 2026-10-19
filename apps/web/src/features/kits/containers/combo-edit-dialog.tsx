import { useState } from "react";
import { dec } from "../../../shared/lib/decimal";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { Field } from "../../../shared/ui/field";
import { MoneyInput } from "../../../shared/ui/money-input";
import { toast } from "../../../shared/ui/toast-store";
import type { SkuListItem } from "../../catalog";
import { KIT_MESSAGES } from "../api/adapters";
import { skuSettings, useNewRecipeVersion } from "../api/mutations";
import { KitEconomics } from "../components/kit-economics";
import { RecipeEditor } from "../components/recipe-editor";
import { SeedPendingDialog } from "../components/seed-pending-dialog";
import type { Kit, MixGroup } from "../schemas";
import { componentLabel, draftOfKit, linesOf, recipeRequest, type KitDraft } from "../view-model";
import { useRecipeEditor, useRecipeSeed } from "./use-recipe-editor";

export interface ComboEditDialogProps {
  kit: Kit;
  onClose: () => void;
}

/**
 * Prototype `comboEdit`: new components (and price) of a published combo. Earlier sales keep the
 * components and cost they were sold with.
 */
export function ComboEditDialog({ kit, onClose }: ComboEditDialogProps) {
  const seed = useRecipeSeed(kit);
  const title = `Sửa combo ${kit.name}`;
  if (!seed.ready) return <SeedPendingDialog title={title} failed={seed.failed} onClose={onClose} />;
  return <ComboForm kit={kit} title={title} sku={seed.skus.get(kit.sku_id)} skus={seed.skus} groups={seed.groups} onClose={onClose} />;
}

interface ComboFormProps extends ComboEditDialogProps {
  title: string;
  sku: SkuListItem | undefined;
  skus: ReadonlyMap<string, SkuListItem>;
  groups: ReadonlyMap<string, MixGroup>;
}

function ComboForm({ kit, title, sku, skus, groups, onClose }: ComboFormProps) {
  const [draft, setDraft] = useState<KitDraft>(() => draftOfKit(kit, sku, linesOf(kit.latest.items, groups, (id) => componentLabel(skus.get(id)))));
  const set = (patch: Partial<KitDraft>) => setDraft((d) => ({ ...d, ...patch }));
  const recipe = useRecipeEditor(draft.lines, draft, draft.retail);
  const save = useNewRecipeVersion("combo", onClose);

  const submit = () => {
    const retail = draft.retail;
    if (retail === null || !dec(retail).gt(0)) return toast.commitError(KIT_MESSAGES.pricePositive);
    const items = recipe.items();
    if (typeof items === "string") return toast.commitError(items);
    void save.run({ kit, recipe: recipeRequest(draft, items), retail, current: skuSettings(sku?.retail_price, sku?.wholesale_price, undefined) });
  };

  return (
    <CommitDialog
      open
      onClose={onClose}
      wide
      title={title}
      sub="Thay đổi áp dụng cho giao dịch mới. Giao dịch đã bán giữ nguyên thành phần và giá vốn đã xuất."
      submitLabel="Lưu"
      onSubmit={submit}
      pending={save.pending}
      live={<KitEconomics type="combo" retail={draft.retail} cost={recipe.cost} />}
    >
      <Field label="Giá bán lẻ">
        <MoneyInput value={draft.retail} onChange={(retail) => set({ retail })} />
      </Field>
      <RecipeEditor type="combo" lines={draft.lines} onChange={(lines) => set({ lines })} {...recipe.editor} />
    </CommitDialog>
  );
}
