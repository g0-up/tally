import { fieldReasonMessage, isApiError } from "../../shared/api/api-error";

// Mix-group rules in the server's words. The server owns every rule and answers with the
// prototype sentence (saveMixGroup); the client repeats the cheap checks in the same order and
// wording for instant feedback, and maps each server code onto the form field it belongs to.

/** Form areas an error can sit under. */
export type MixField = "name" | "units" | "packF" | "partF" | "items" | "spec";

export type MixErrors = Partial<Record<MixField, string>>;

export interface MixFlavorDraft {
  /** Stable React key of the row. */
  key: string;
  skuId: string | null;
  skuCode: string;
  skuName: string;
  /** Short flavor name shown in the flavor picker ("Dâu"). */
  label: string;
}

export interface MixGroupDraft {
  name: string;
  packUnitId: string | null;
  partUnitId: string | null;
  /** Pieces per pack, as typed. */
  packF: string;
  /** Pieces per part, as typed. */
  partF: string;
  items: MixFlavorDraft[];
}

export const MIX_MESSAGES = {
  nameRequired: "Cần tên nhóm mix.",
  nameTaken: "Tên nhóm mix đã tồn tại.",
  unitsRequired: "Cần đơn vị thùng và đơn vị chọn vị (VD: thùng / dây).",
  sizeInvalid: (unit: string) => `Số đơn vị lẻ trong 1 ${unit} phải là số nguyên dương.`,
  notDivisible: (pack: string, packF: number, part: string, partF: number) => `1 ${pack} (${packF}) phải chia hết cho 1 ${part} (${partF}).`,
  tooFewParts: (pack: string, part: string) => `1 ${pack} phải gồm ít nhất 2 ${part} thì mới mix được.`,
  duplicateFlavor: "Một mặt hàng đang được chọn hai lần.",
  tooFewFlavors: "Nhóm mix cần ít nhất 2 vị.",
  mixedPieceUnits: (units: readonly string[]) => `Các vị phải cùng đơn vị lẻ, đang lẫn: ${units.join(", ")}.`,
  labelRequired: (name: string) => `Cần tên vị ngắn cho ${name}.`,
} as const;

/** Server code → the form area its sentence belongs under. */
export const MIX_ERROR_FIELD: Readonly<Record<string, MixField>> = {
  mix_group_name_required: "name",
  mix_group_name_taken: "name",
  mix_group_units_required: "units",
  mix_group_part_size_invalid: "partF",
  mix_group_pack_size_invalid: "packF",
  mix_group_pack_not_divisible: "packF",
  mix_group_too_few_parts: "packF",
  mix_group_duplicate_flavor: "items",
  mix_group_too_few_flavors: "items",
  mix_group_flavor_not_goods: "items",
  mix_group_mixed_piece_units: "items",
  mix_group_flavor_label_required: "items",
  mix_group_pack_locked: "spec",
  mix_group_flavor_in_use: "items",
};

/** Request field path → form area, for plain validation_failed answers. */
function fieldOfPath(path: string): MixField {
  if (path === "name") return "name";
  if (path === "units_per_pack") return "packF";
  if (path === "units_per_part") return "partF";
  if (path === "pack_unit_id" || path === "part_unit_id") return "units";
  return "items";
}

/** Form errors of a refused mix-group command: the server sentence under its field. */
export function mixFormErrors(e: unknown): MixErrors {
  if (!isApiError(e)) return {};
  const coded = MIX_ERROR_FIELD[e.code];
  if (coded) return { [coded]: e.message };
  const out: MixErrors = {};
  for (const [path, reason] of Object.entries(e.fields)) {
    const field = fieldOfPath(path);
    out[field] ??= fieldReasonMessage(reason);
  }
  return out;
}

export interface MixCheckContext {
  packName: string;
  partName: string;
  /** Names of the other groups, for the duplicate-name check. */
  otherNames: readonly string[];
  /** Base unit name of a picked flavor SKU; empty while unknown. */
  pieceUnitOf: (skuId: string) => string;
}

function positiveInt(text: string): number | null {
  const n = Number(text.trim());
  return text.trim() !== "" && Number.isInteger(n) && n > 0 ? n : null;
}

/** First failing cheap check in the server's order, or null. */
export function checkMixDraft(d: MixGroupDraft, ctx: MixCheckContext): { field: MixField; message: string } | null {
  const name = d.name.trim();
  if (name === "") return { field: "name", message: MIX_MESSAGES.nameRequired };
  const lower = name.toLocaleLowerCase("vi");
  if (ctx.otherNames.some((n) => n.toLocaleLowerCase("vi") === lower)) return { field: "name", message: MIX_MESSAGES.nameTaken };
  if (d.packUnitId === null || d.partUnitId === null) return { field: "units", message: MIX_MESSAGES.unitsRequired };
  const partF = positiveInt(d.partF);
  if (partF === null) return { field: "partF", message: MIX_MESSAGES.sizeInvalid(ctx.partName) };
  const packF = positiveInt(d.packF);
  if (packF === null) return { field: "packF", message: MIX_MESSAGES.sizeInvalid(ctx.packName) };
  if (packF % partF !== 0) return { field: "packF", message: MIX_MESSAGES.notDivisible(ctx.packName, packF, ctx.partName, partF) };
  if (packF / partF < 2) return { field: "packF", message: MIX_MESSAGES.tooFewParts(ctx.packName, ctx.partName) };
  const picked = d.items.filter((x): x is MixFlavorDraft & { skuId: string } => x.skuId !== null);
  const ids = new Set(picked.map((x) => x.skuId));
  if (ids.size !== picked.length) return { field: "items", message: MIX_MESSAGES.duplicateFlavor };
  if (ids.size < 2) return { field: "items", message: MIX_MESSAGES.tooFewFlavors };
  const units = [...new Set(picked.map((x) => ctx.pieceUnitOf(x.skuId)).filter((u) => u !== ""))];
  if (units.length > 1) return { field: "items", message: MIX_MESSAGES.mixedPieceUnits(units) };
  const unlabeled = picked.find((x) => x.label.trim() === "");
  if (unlabeled) return { field: "items", message: MIX_MESSAGES.labelRequired(unlabeled.skuName) };
  return null;
}

/** `POST/PATCH /mix-groups` body; call only after `checkMixDraft` passed. */
export function mixGroupRequest(d: MixGroupDraft) {
  return {
    name: d.name.trim(),
    pack_unit_id: d.packUnitId,
    units_per_pack: Number(d.packF.trim()),
    part_unit_id: d.partUnitId,
    units_per_part: Number(d.partF.trim()),
    items: d.items.flatMap((x) => (x.skuId === null ? [] : [{ sku_id: x.skuId, flavor_label: x.label.trim() }])),
  };
}
