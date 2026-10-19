import { useState } from "react";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { Field } from "../../../shared/ui/field";
import { Input } from "../../../shared/ui/input";
import { toast } from "../../../shared/ui/toast-store";
import type { SkuListItem } from "../../catalog";
import { KIT_MESSAGES } from "../api/adapters";
import { useNewRecipeVersion } from "../api/mutations";
import { KitEconomics } from "../components/kit-economics";
import { RecipeEditor } from "../components/recipe-editor";
import { SeedPendingDialog } from "../components/seed-pending-dialog";
import type { Kit, MixGroup } from "../schemas";
import { componentLabel, draftOfKit, linesOf, recipeRequest, type KitDraft } from "../view-model";
import { useRecipeEditor, useRecipeSeed } from "./use-recipe-editor";

export interface RecipeVersionDialogProps {
  kit: Kit;
  onClose: () => void;
}

const SUB = "Phiên bản cũ được giữ để truy xuất các lần đóng trước; lệnh đóng nháp chuyển sang phiên bản mới, lệnh đang đóng giữ phiên bản cũ.";

/** Prototype `recipeVer`: a new recipe version of a published kit, effective from today. */
export function RecipeVersionDialog({ kit, onClose }: RecipeVersionDialogProps) {
  const seed = useRecipeSeed(kit);
  const title = `Phiên bản mới cho ${kit.name}`;
  if (!seed.ready) return <SeedPendingDialog title={title} failed={seed.failed} onClose={onClose} />;
  return <VersionForm kit={kit} title={title} skus={seed.skus} groups={seed.groups} onClose={onClose} />;
}

interface VersionFormProps extends RecipeVersionDialogProps {
  title: string;
  skus: ReadonlyMap<string, SkuListItem>;
  groups: ReadonlyMap<string, MixGroup>;
}

function VersionForm({ kit, title, skus, groups, onClose }: VersionFormProps) {
  const [draft, setDraft] = useState<KitDraft>(() => ({
    ...draftOfKit(kit, skus.get(kit.sku_id), linesOf(kit.latest.items, groups, (id) => componentLabel(skus.get(id)))),
    note: "",
  }));
  const set = (patch: Partial<KitDraft>) => setDraft((d) => ({ ...d, ...patch }));
  const recipe = useRecipeEditor(draft.lines, draft, draft.retail);
  const save = useNewRecipeVersion("kit", onClose);

  const submit = () => {
    const items = recipe.items();
    if (typeof items === "string") return toast.commitError(items);
    if (!draft.note.trim()) return toast.commitError(KIT_MESSAGES.noteRequired);
    void save.run({ kit, recipe: recipeRequest(draft, items) });
  };

  return (
    <CommitDialog
      open
      onClose={onClose}
      wide
      title={title}
      sub={`Đang hiệu lực: v${kit.active_version ?? kit.latest.version_no}. ${SUB}`}
      submitLabel="Lưu phiên bản"
      onSubmit={submit}
      pending={save.pending}
      live={<KitEconomics type="kit" retail={draft.retail} cost={recipe.cost} />}
    >
      <RecipeEditor
        type="kit"
        lines={draft.lines}
        onChange={(lines) => set({ lines })}
        {...recipe.editor}
        packing={{ value: draft.packing, onChange: (packing) => set({ packing }) }}
      />
      <Field label="Lý do thay đổi (bắt buộc)">
        <Input value={draft.note} placeholder="VD: thay trà ô long bằng trà sen do thiếu hàng" onChange={(e) => set({ note: e.target.value })} />
      </Field>
    </CommitDialog>
  );
}
