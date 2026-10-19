import { useState } from "react";
import type { Money } from "../../../shared/domain/money";
import { toMoney } from "../../../shared/domain/money";
import { toQty } from "../../../shared/domain/qty";
import { Button } from "../../../shared/ui/button";
import { CLOSE_UNSETTLED_MESSAGE } from "../../../shared/hooks/create-once";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { Field } from "../../../shared/ui/field";
import { FormGrid } from "../../../shared/ui/form-grid";
import { Input } from "../../../shared/ui/input";
import { MoneyInput } from "../../../shared/ui/money-input";
import { QtyInput } from "../../../shared/ui/qty-input";
import { toast } from "../../../shared/ui/toast-store";
import type { SkuListItem } from "../../catalog";
import { KIT_MESSAGES, KIT_TYPE_OPTIONS, KIT_TYPE_TAGS } from "../api/adapters";
import { skuSettings, useCreateKit, useUpdateKitDraft } from "../api/mutations";
import { KitEconomics } from "../components/kit-economics";
import { RecipeEditor } from "../components/recipe-editor";
import { SeedPendingDialog } from "../components/seed-pending-dialog";
import type { Kit, KitType, MixGroup } from "../schemas";
import { componentLabel, createRequest, draftOfKit, emptyComponent, hasPendingDraft, headerError, linesOf, recipeRequest, type KitDraft } from "../view-model";
import { useRecipeEditor, useRecipeSeed } from "./use-recipe-editor";

export interface KitDialogProps {
  /** The draft to edit; omitted to create a kit or combo. */
  kit?: Kit;
  /** Type preselected for a new one. */
  type?: KitType;
  onClose: () => void;
  /** Opens the preview of a kit saved as a draft. */
  onPreview: (kitId: string) => void;
}

const SUB = "Lưu nháp để xem trước; giỏ nháp chưa bán, chưa đóng được. Phát hành khi đã chốt mẫu.";

/** Editing the draft version of a kit on sale: the recipe waits, the SKU settings apply at once. */
const subOfWaiting = (kit: Kit) =>
  `Giỏ vẫn bán theo v${kit.active_version} cho tới khi phát hành v${kit.latest.version_no}. Tên, giá và tồn tối thiểu áp dụng ngay khi lưu.`;

/** Prototype `kitModal`: creates a kit or combo (draft or published), or edits a draft. */
export function KitDialog({ kit, type = "kit", onClose, onPreview }: KitDialogProps) {
  const seed = useRecipeSeed(kit);
  if (!kit) return <KitForm initial={newDraft(type)} onClose={onClose} onPreview={onPreview} />;
  const title = `Sửa nháp · ${kit.name}`;
  if (!seed.ready) return <SeedPendingDialog title={title} failed={seed.failed} onClose={onClose} />;
  return <KitForm kit={kit} kitSku={seed.skus.get(kit.sku_id)} initial={editDraft(kit, seed.skus, seed.groups)} onClose={onClose} onPreview={onPreview} />;
}

/** The prototype defaults of a new kit: packing 20.000 ₫, minimum stock 10. */
const newDraft = (type: KitType): (() => KitDraft) => () => ({
  type,
  code: "",
  name: "",
  retail: null,
  wholesale: null,
  minStock: toQty(10),
  packing: toMoney(20000),
  note: "",
  lines: [emptyComponent()],
});

const editDraft =
  (kit: Kit, skus: ReadonlyMap<string, SkuListItem>, groups: ReadonlyMap<string, MixGroup>): (() => KitDraft) =>
  () =>
    draftOfKit(kit, skus.get(kit.sku_id), linesOf(kit.latest.items, groups, (id) => componentLabel(skus.get(id))));

interface KitFormProps {
  kit?: Kit;
  kitSku?: SkuListItem | undefined;
  initial: () => KitDraft;
  onClose: () => void;
  onPreview: (kitId: string) => void;
}

function KitForm({ kit, kitSku, initial, onClose, onPreview }: KitFormProps) {
  const creating = kit === undefined;
  const [draft, setDraft] = useState<KitDraft>(initial);
  const set = (patch: Partial<KitDraft>) => setDraft((d) => ({ ...d, ...patch }));
  const recipe = useRecipeEditor(draft.lines, draft, draft.retail);
  // The create request carries the prices and minimum stock; the API's refusal of one shows under it.
  const [errors, setErrors] = useState<{ retail?: string | undefined; wholesale?: string | undefined; minStock?: string | undefined }>({});
  const create = useCreateKit(
    (saved, published) => {
      onClose();
      if (!published) onPreview(saved.product_id);
    },
    (field, message, reason) => {
      if (field === "retail_price") setErrors((e) => ({ ...e, retail: reason === "must_not_be_negative" ? KIT_MESSAGES.retailNegative : message }));
      if (field === "wholesale_price") setErrors((e) => ({ ...e, wholesale: reason === "must_not_be_negative" ? KIT_MESSAGES.wholesaleNegative : message }));
      if (field === "min_stock")
        setErrors((e) => ({ ...e, minStock: reason === "not_allowed" ? KIT_MESSAGES.minStockOnCombo : reason === "must_not_be_negative" ? KIT_MESSAGES.minStockNegative : message }));
    },
  );
  const update = useUpdateKitDraft((saved) => {
    onClose();
    onPreview(saved.product_id);
  });
  const isKit = draft.type === "kit";

  /** The validated items, or null after telling the user what blocks the save. */
  const validItems = () => {
    const header = headerError(draft, creating);
    if (header) return void toast.commitError(header);
    const items = recipe.items();
    if (typeof items === "string") return void toast.commitError(items);
    return items;
  };

  const baseUnitId = () => {
    const u = recipe.units;
    const names = isKit ? ["giỏ", "hộp", "cái"] : ["combo", "bộ", "cái"];
    return names.map((n) => u.idByName(n)).find((id) => id !== null) ?? u.active[0]?.id ?? null;
  };

  const save = (publish: boolean) => {
    const items = validItems();
    if (!items) return;
    if (creating) {
      const unit = baseUnitId();
      if (unit === null) return toast.commitError(KIT_MESSAGES.noUnit);
      void create.run({ request: createRequest(draft, unit, items), publish });
      return;
    }
    void update.run({
      kit,
      name: draft.name.trim(),
      recipe: recipeRequest(draft, items),
      settings: skuSettings(draft.retail, draft.wholesale, isKit ? draft.minStock : null),
      current: skuSettings(kitSku?.retail_price, kitSku?.wholesale_price, isKit ? kitSku?.min_stock : null),
    });
  };

  const pending = create.pending || update.pending;
  const money = (key: "retail" | "wholesale") => (v: Money | null) => {
    set({ [key]: v });
    setErrors((e) => ({ ...e, [key]: undefined }));
  };

  return (
    <CommitDialog
      open
      onClose={onClose}
      {...(create.unsettled ? { closeWarning: CLOSE_UNSETTLED_MESSAGE } : {})}
      wide
      title={creating ? "Tạo giỏ quà / combo" : `Sửa nháp · ${kit.name}`}
      sub={kit && hasPendingDraft(kit) ? subOfWaiting(kit) : SUB}
      submitLabel={creating ? "Lưu & phát hành" : "Lưu nháp"}
      onSubmit={() => save(creating)}
      pending={pending}
      extra={
        creating ? (
          <Button variant="navy" onClick={() => save(false)} disabled={pending}>
            Lưu nháp
          </Button>
        ) : null
      }
      live={<KitEconomics type={draft.type} retail={draft.retail} cost={recipe.cost} />}
    >
      <FormGrid>
        <Field label="Loại">
          {creating && !create.stored ? (
            <select className="in" value={draft.type} onChange={(e) => set({ type: e.target.value === "combo" ? "combo" : "kit" })}>
              <option value="kit">{KIT_TYPE_OPTIONS.kit}</option>
              <option value="combo">{KIT_TYPE_OPTIONS.combo}</option>
            </select>
          ) : (
            <select className="in" disabled>
              <option>{KIT_TYPE_TAGS[draft.type]}</option>
            </select>
          )}
        </Field>
        <Field label="Mã">
          {creating && !create.stored ? (
            <Input value={draft.code} placeholder="VD: GQAK" autoFocus onChange={(e) => set({ code: e.target.value })} />
          ) : (
            <Input value={draft.code} disabled />
          )}
        </Field>
        <Field label="Tên">
          <Input value={draft.name} placeholder="VD: Giỏ quà An Khang" onChange={(e) => set({ name: e.target.value })} />
        </Field>
        <Field label="Giá bán lẻ" error={errors.retail}>
          <MoneyInput value={draft.retail} onChange={money("retail")} />
        </Field>
        <Field label="Giá sỉ / doanh nghiệp" error={errors.wholesale}>
          <MoneyInput value={draft.wholesale} onChange={money("wholesale")} />
        </Field>
        {isKit ? (
          <Field label="Tồn tối thiểu (giỏ)" error={errors.minStock}>
            <QtyInput
              value={draft.minStock}
              onChange={(minStock) => {
                set({ minStock });
                setErrors((e) => ({ ...e, minStock: undefined }));
              }}
            />
          </Field>
        ) : null}
      </FormGrid>
      {create.stored ? <p className="hint">Giỏ đã được lưu nháp nên Loại và Mã không đổi được nữa; bấm lưu lại để gửi những gì còn thiếu và các chỉnh sửa khác.</p> : null}
      <RecipeEditor
        type={draft.type}
        lines={draft.lines}
        onChange={(lines) => set({ lines })}
        {...recipe.editor}
        {...(isKit ? { packing: { value: draft.packing, onChange: (packing: Money | null) => set({ packing }) } } : {})}
      />
      {isKit ? (
        <Field label="Ghi chú">
          <Input value={draft.note} onChange={(e) => set({ note: e.target.value })} />
        </Field>
      ) : null}
    </CommitDialog>
  );
}
