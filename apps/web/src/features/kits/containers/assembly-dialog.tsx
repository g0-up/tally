import { useMemo, useState } from "react";
import { toQty, type Qty } from "../../../shared/domain/qty";
import { useDebounce } from "../../../shared/hooks/use-debounce";
import { dec } from "../../../shared/lib/decimal";
import { CLOSE_UNSETTLED_MESSAGE } from "../../../shared/hooks/create-once";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { Field } from "../../../shared/ui/field";
import { FormGrid } from "../../../shared/ui/form-grid";
import { QtyInput } from "../../../shared/ui/qty-input";
import { toast } from "../../../shared/ui/toast-store";
import { useColorDirectory, useSkusByIds } from "../../catalog";
import { useLoadAll } from "../../inventory";
import { KIT_MESSAGES } from "../api/adapters";
import { useCreateAssembly } from "../api/mutations";
import { useAssemblyPreview, useKit, useKitList, useOpenInvoices, useRecipe } from "../api/queries";
import type { AssemblyRequest, ComponentColor } from "../api/requests";
import { OpenColorFields } from "../components/open-color-fields";
import { ShortageTable } from "../components/shortage-table";
import { openColors } from "../view-model";

export interface AssemblyDialogProps {
  /** Kit preselected from its card. */
  kitId?: string;
  onClose: () => void;
}

/**
 * Prototype `newASM` "Tạo lệnh đóng giỏ": plans kits of a published recipe, optionally for an
 * invoice. A component whose color the recipe leaves open takes the color picked here.
 */
export function AssemblyDialog({ kitId, onClose }: AssemblyDialogProps) {
  const list = useKitList("kit");
  // Every published kit is offered, not only the first page.
  useLoadAll(list);
  const invoices = useOpenInvoices(true).data ?? [];
  const colors = useColorDirectory();
  const [picked, setPicked] = useState<string | null>(kitId ?? null);
  const [plan, setPlan] = useState<Qty | null>(toQty(20));
  const [invoice, setInvoice] = useState("");
  const [picks, setPicks] = useState<Record<string, string>>({});
  const create = useCreateAssembly(onClose);

  const listed = useMemo(() => list.items.filter((k) => k.status === "active"), [list.items]);
  const kit = picked ?? listed[0]?.product_id ?? null;
  const detail = useKit(kit);
  // A kit preselected from its card may sit past the first page of the list.
  const shown = detail.data;
  const extra = shown && shown.status === "active" && !listed.some((k) => k.product_id === shown.product_id) ? shown : null;
  const recipe = useRecipe(shown ? kit : null, shown?.active_version ?? null);
  const items = useMemo(() => recipe.data?.items.filter((it) => it.kind !== "mix_pack") ?? [], [recipe.data]);
  const skuIds = useMemo(() => items.flatMap((it) => (it.sku_id ? [it.sku_id] : [])), [items]);
  const skus = useSkusByIds(skuIds);
  const open = useMemo(() => openColors(items, skus.data, colors), [items, skus.data, colors]);
  // Until the recipe and its SKUs are known, an open colored component cannot be told apart.
  const ready = recipe.data !== undefined && (skuIds.length === 0 || skus.data !== undefined);

  const chosen: ComponentColor[] = open.flatMap((o) => (picks[o.skuId] ? [{ sku_id: o.skuId, color_id: picks[o.skuId] as string }] : []));
  const missing = open.filter((o) => !picks[o.skuId]);
  const request: AssemblyRequest | null =
    kit !== null && plan !== null && dec(plan).gt(0)
      ? { kit_id: kit, planned_qty: plan, ...(invoice ? { invoice_id: invoice } : {}), ...(chosen.length > 0 ? { colors: chosen } : {}) }
      : null;
  const debounced = useDebounce(request === null ? "" : JSON.stringify({ kit_id: request.kit_id, planned_qty: request.planned_qty, colors: request.colors }), 300);
  const previewBody = useMemo((): AssemblyRequest | null => {
    if (!debounced) return null;
    const b = JSON.parse(debounced) as { kit_id: string; planned_qty: string; colors?: ComponentColor[] };
    return { kit_id: b.kit_id, planned_qty: toQty(b.planned_qty), ...(b.colors ? { colors: b.colors } : {}) };
  }, [debounced]);
  const preview = useAssemblyPreview(previewBody);

  const pickKit = (id: string) => {
    setPicked(id);
    setPicks({});
  };

  const submit = () => {
    if (kit === null) return toast.commitError(KIT_MESSAGES.kitRequired);
    if (request === null) return toast.commitError(KIT_MESSAGES.planPositive);
    if (recipe.isError || skus.isError) return toast.commitError(KIT_MESSAGES.recipeUnavailable);
    if (!ready) return toast.commitError(KIT_MESSAGES.recipeLoading);
    if (missing.length > 0) return toast.commitError(KIT_MESSAGES.colorPick(missing.map((o) => o.name)));
    void create.run(request);
  };

  return (
    <CommitDialog
      open
      onClose={onClose}
      {...(create.unsettled ? { closeWarning: CLOSE_UNSETTLED_MESSAGE } : {})}
      title="Tạo lệnh đóng giỏ"
      submitLabel="Tạo lệnh"
      onSubmit={submit}
      pending={create.pending}
      live={
        <>
          {request !== null && preview.data ? <ShortageTable lines={preview.data.lines} colors={colors} /> : null}
          <p className="hint">Tạo lệnh chưa giữ hay tiêu hao nguyên liệu.</p>
        </>
      }
    >
      <FormGrid>
        <Field label="Công thức">
          <select className="in" value={kit ?? ""} onChange={(e) => pickKit(e.target.value)}>
            {listed.length === 0 && extra === null ? <option value="">Chưa có giỏ đang bán</option> : null}
            {extra ? (
              <option value={extra.product_id}>
                {extra.name} v{extra.active_version ?? extra.latest.version_no}
              </option>
            ) : null}
            {listed.map((k) => (
              <option key={k.product_id} value={k.product_id}>
                {k.name} v{k.active_version ?? k.latest_version}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Số giỏ kế hoạch">
          <QtyInput value={plan} onChange={setPlan} />
        </Field>
        <Field label="Gắn đơn bán (tùy chọn)">
          <select className="in" value={invoice} onChange={(e) => setInvoice(e.target.value)}>
            <option value="">—</option>
            {invoices.map((o) => (
              <option key={o.id} value={o.id}>
                {o.doc_no}
              </option>
            ))}
          </select>
        </Field>
      </FormGrid>
      {open.length > 0 ? (
        <>
          <p className="hint">Công thức chưa cố định màu cho các thành phần dưới đây; chọn màu sẽ dùng cho lệnh này.</p>
          <OpenColorFields open={open} picks={picks} onPick={(skuId, colorId) => setPicks((p) => ({ ...p, [skuId]: colorId }))} colors={colors} />
        </>
      ) : null}
    </CommitDialog>
  );
}
