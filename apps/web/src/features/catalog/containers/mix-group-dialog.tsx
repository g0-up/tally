import { useState } from "react";
import { useApiClient } from "../../../shared/api/api-context";
import { useCommit } from "../../../shared/hooks/use-commit";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { ListLoading } from "../../../shared/ui/list-footer";
import { toast } from "../../../shared/ui/toast-store";
import { baseUnitName } from "../api/adapters";
import { etagOf } from "../api/mutations";
import { mixGroupKeys, skuKeys, useSkuPicker, useSkusByIds, useUnits } from "../api/queries";
import { MixGroupFormFields, MixGroupLive } from "../components/mix-group-form";
import { checkMixDraft, mixFormErrors, mixGroupRequest, type MixErrors, type MixFlavorDraft, type MixGroupDraft } from "../mix-group-messages";
import { mixGroupSchema, type MixGroup, type SkuListItem } from "../schemas";

let rowSeq = 0;
const rowKey = () => `flavor-${++rowSeq}`;
const emptyRow = (): MixFlavorDraft => ({ key: rowKey(), skuId: null, skuCode: "", skuName: "", label: "" });

function initialDraft(group: MixGroup | null, unitIdByName: (name: string) => string | null): MixGroupDraft {
  if (group === null) {
    return { name: "", packUnitId: unitIdByName("thùng"), partUnitId: unitIdByName("dây"), packF: "48", partF: "4", items: [emptyRow(), emptyRow()] };
  }
  return {
    name: group.name,
    packUnitId: group.pack_unit_id,
    partUnitId: group.part_unit_id,
    packF: String(group.units_per_pack),
    partF: String(group.units_per_part),
    items: group.items.map((x) => ({ key: rowKey(), skuId: x.sku_id, skuCode: x.sku_code, skuName: x.sku_name, label: x.flavor_label })),
  };
}

export interface MixGroupDialogProps {
  /** Null adds a group. */
  group: MixGroup | null;
  /** All groups, for the duplicate-name check. */
  groups: readonly MixGroup[];
  onClose: () => void;
}

/** Prototype mixModal. Mount per opening. */
export function MixGroupDialog({ group, groups, onClose }: MixGroupDialogProps) {
  const units = useUnits();
  if (units.isLoading) {
    return (
      <CommitDialog open onClose={onClose} title={group ? "Sửa nhóm mix vị" : "Thêm nhóm mix vị"} wide>
        <ListLoading />
      </CommitDialog>
    );
  }
  return <MixGroupForm group={group} groups={groups} onClose={onClose} initial={initialDraft(group, units.idByName)} unitName={units.name} />;
}

function MixGroupForm({ group, groups, onClose, initial, unitName }: MixGroupDialogProps & { initial: MixGroupDraft; unitName: (id: string | null) => string }) {
  const client = useApiClient();
  const [draft, setDraft] = useState(initial);
  const [errors, setErrors] = useState<MixErrors>({});
  const [picked, setPicked] = useState<ReadonlyMap<string, SkuListItem>>(new Map());
  const picker = useSkuPicker("goods");
  const known = useSkusByIds(group?.items.map((x) => x.sku_id) ?? []);
  const locked = group !== null && group.usage.length > 0;
  const usedSkuIds = new Set(group?.used_sku_ids ?? []);
  const packName = unitName(draft.packUnitId);
  const partName = unitName(draft.partUnitId);

  const skuOf = (skuId: string | null) => (skuId === null ? undefined : (picked.get(skuId) ?? known.data?.get(skuId)));

  const commit = useCommit({
    mutationFn: async (d: MixGroupDraft, { idempotencyKey }) => {
      try {
        const body = mixGroupRequest(d);
        return group === null
          ? await client.post("/mix-groups", body, { idempotencyKey, schema: mixGroupSchema })
          : await client.patch(`/mix-groups/${group.id}`, body, { ifMatch: etagOf(group.version), schema: mixGroupSchema });
      } catch (e) {
        setErrors(mixFormErrors(e));
        throw e;
      }
    },
    success: (g) => (
      <>
        Đã lưu nhóm mix <b>{g.name}</b>.
      </>
    ),
    invalidate: [mixGroupKeys.all, skuKeys.all],
    after: onClose,
  });

  const onPick = (index: number, sku: SkuListItem | undefined, skuId: string) => {
    if (sku) setPicked((m) => new Map(m).set(skuId, sku));
    setDraft((d) => ({
      ...d,
      items: d.items.map((x, i) =>
        i === index
          ? {
              ...x,
              skuId,
              skuCode: sku?.code ?? x.skuCode,
              skuName: sku?.display_name ?? x.skuName,
              label: x.label.trim() === "" && sku ? sku.variant_label || sku.display_name : x.label,
            }
          : x,
      ),
    }));
  };

  const submit = () => {
    const failed = checkMixDraft(draft, {
      packName,
      partName,
      otherNames: groups.filter((g) => g.id !== group?.id).map((g) => g.name),
      pieceUnitOf: (id) => {
        const s = skuOf(id);
        return s ? baseUnitName(s) : "";
      },
    });
    if (failed) {
      setErrors({ [failed.field]: failed.message });
      toast.commitError(failed.message);
      return;
    }
    setErrors({});
    void commit.run(draft);
  };

  return (
    <CommitDialog
      open
      onClose={onClose}
      wide
      title={group ? "Sửa nhóm mix vị" : "Thêm nhóm mix vị"}
      sub={
        locked
          ? "Nhóm đã dùng trên chứng từ: có thể đổi tên, đổi tên vị, thêm vị; không đổi quy cách thùng và không gỡ vị đã bán."
          : "Các vị phải cùng đơn vị lẻ (VD: hộp, lon). Tên vị ngắn sẽ hiện ở bảng chọn vị."
      }
      live={<MixGroupLive draft={draft} packName={packName} partName={partName} skuOf={skuOf} />}
      submitLabel={group ? "Lưu" : "Tạo nhóm"}
      onSubmit={submit}
      pending={commit.pending}
    >
      <MixGroupFormFields
        draft={draft}
        onChange={(patch) => setDraft((d) => ({ ...d, ...patch }))}
        errors={errors}
        locked={locked}
        usedSkuIds={usedSkuIds}
        picker={picker}
        skuOf={skuOf}
        onPick={onPick}
        onAddRow={() => setDraft((d) => ({ ...d, items: [...d.items, emptyRow()] }))}
      />
    </CommitDialog>
  );
}
