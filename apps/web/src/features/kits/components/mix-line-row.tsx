import { Fragment } from "react";
import type { Qty } from "../../../shared/domain/qty";
import { dec, floorDec } from "../../../shared/lib/decimal";
import { num, vnd } from "../../../shared/lib/format";
import { Callout } from "../../../shared/ui/callout";
import { Field } from "../../../shared/ui/field";
import { LinkButton } from "../../../shared/ui/link-button";
import { QtyInput } from "../../../shared/ui/qty-input";
import type { SkuListItem } from "../../catalog";
import type { ComponentCost, MixGroup } from "../schemas";
import { componentPosition, mixUsage, type MixDraft } from "../view-model";

/** Prototype `mixCompStatus`: parts chosen against the room in the mix line. */
export function MixLineStatus({ line, group, unitName }: { line: MixDraft; group: MixGroup; unitName: (id: string) => string }) {
  const { cap, used } = mixUsage(line, group);
  const part = unitName(group.part_unit_id);
  if (cap.lte(0)) return <span className="hint">Nhập số lượng mix.</span>;
  if (used.gt(cap)) {
    return (
      <Callout tone="bad">
        Đã chọn {num(used)} {part}, vượt {num(cap)} {part}. Giảm bớt {num(used.minus(cap))} {part}.
      </Callout>
    );
  }
  return (
    <span className="hint">
      Đã chọn{" "}
      <b>
        {num(used)}/{num(cap)} {part}
      </b>{" "}
      ({num(used.times(group.units_per_part))} {unitName(group.piece_unit_id)})
      {used.lt(cap) ? ` · còn trống ${num(cap.minus(used))} ${part}` : " · đủ"}
    </span>
  );
}

export interface MixLineRowProps {
  /** Position of the line in the recipe; names its controls ("Số lượng mix thành phần 2"). */
  index: number;
  line: MixDraft;
  groups: readonly MixGroup[];
  /** Economics of the line's flavors from the last preview. */
  costs: readonly ComponentCost[];
  skuOf: (skuId: string) => SkuListItem | undefined;
  unitName: (id: string) => string;
  columnCount: number;
  onChange: (line: MixDraft) => void;
  onRemove: () => void;
}

/** Parts of a flavor SKU within a group. */
const flavorParts = (line: MixDraft, skuId: string) => dec(line.parts[skuId] ?? "0");

/**
 * Prototype `mixCompRow`: a mix line of a recipe. The group, the room in parts or packs, the price
 * range per part, and a second row with the parts of each flavor and the running status. Each
 * flavor field is named by its visible label inside a group named after the line; the status sits
 * in a live region that is always there, so going over the room is announced.
 */
export function MixLineRow({ index, line, groups, costs, skuOf, unitName, columnCount, onChange, onRemove }: MixLineRowProps) {
  const group = groups.find((g) => g.id === line.groupId);
  if (!group) return null;
  const stopped = group.status !== "active";
  // A stopped group stays selectable only on the line that already holds it.
  const options = groups.filter((g) => g.status === "active" || g.id === group.id);
  const part = unitName(group.part_unit_id);
  const flavors = group.items.filter((x) => x.active || x.sku_id in line.parts);
  const perPart = costs.flatMap((c) => {
    const parts = flavorParts(line, c.sku_id);
    return c.unit_price && parts.gt(0) ? [dec(c.unit_price).times(c.base_qty).div(parts)] : [];
  });
  const lo = perPart.length ? perPart.reduce((a, b) => (b.lt(a) ? b : a)) : null;
  const hi = perPart.length ? perPart.reduce((a, b) => (b.gt(a) ? b : a)) : null;
  const sale = costs.reduce((s, c) => s.plus(c.sale_amount), dec(0));
  const cost = costs.reduce((s, c) => s.plus(c.cost), dec(0));
  const switchGroup = (id: string) => {
    const next = groups.find((g) => g.id === id);
    if (next) onChange({ ...line, groupId: next.id, parts: Object.fromEntries(next.items.filter((x) => x.active).map((x) => [x.sku_id, null])) });
  };
  const setParts = (skuId: string, v: Qty | null) => onChange({ ...line, parts: { ...line.parts, [skuId]: v } });
  const which = componentPosition(index).toLowerCase();

  return (
    <Fragment>
      <tr>
        <td style={{ minWidth: 260 }}>
          <b style={{ color: "var(--navy-900)" }}>Mix vị</b>
          <select className="mixgrp" aria-label={`Chọn nhóm mix vị ${which}`} value={group.id} onChange={(e) => switchGroup(e.target.value)}>
            {options.map((g) => (
              <option key={g.id} value={g.id}>
                {g.status === "active" ? g.name : `${g.name} (đã ngừng)`}
              </option>
            ))}
          </select>
          {stopped ? (
            <span className="sub">
              <b className="dl bad">Nhóm mix đã ngừng</b> – chọn nhóm khác hoặc xóa dòng này trước khi lưu.
            </span>
          ) : (
            <span className="sub">Chọn số {part} cho từng vị; tổng không vượt số lượng mix</span>
          )}
        </td>
        <td className="num">
          <div className="btn-row" style={{ justifyContent: "flex-end", gap: 4 }}>
            <QtyInput size="sm" style={{ width: 52 }} value={line.qty} aria-label={`Số lượng mix ${which}`} onChange={(qty) => onChange({ ...line, qty })} />
            <select aria-label={`Đơn vị mix ${which}`} value={line.unit} onChange={(e) => onChange({ ...line, unit: e.target.value === "pack" ? "pack" : "part" })}>
              <option value="part">{part}</option>
              <option value="pack">{unitName(group.pack_unit_id)}</option>
            </select>
          </div>
        </td>
        <td className="num">
          {lo === null || hi === null ? "—" : lo.eq(hi) ? vnd(lo) : `${vnd(lo)} – ${vnd(hi)}`}
          <span className="sub">/{part}</span>
        </td>
        <td className="num">{costs.length ? vnd(sale) : "—"}</td>
        <td className="num">{costs.length ? vnd(cost) : "—"}</td>
        <td className="num">—</td>
        <td>
          <LinkButton onClick={onRemove} aria-label={`Xóa ${which}`}>
            Xóa
          </LinkButton>
        </td>
      </tr>
      <tr>
        <td colSpan={columnCount} style={{ background: "var(--surface-blue-50)", borderTop: 0 }}>
          <div className="mixgrid" role="group" aria-label={`Số ${part} theo vị ${which}`}>
            {flavors.map((x) => {
              const sellable = skuOf(x.sku_id)?.sellable;
              const left = sellable === undefined ? null : floorDec(dec(sellable).div(group.units_per_part));
              return (
                <Field key={x.sku_id} label={`${x.flavor_label} · còn ${left === null ? "—" : num(left)} ${part}`}>
                  <QtyInput value={line.parts[x.sku_id] ?? null} onChange={(v) => setParts(x.sku_id, v)} />
                </Field>
              );
            })}
          </div>
          <div aria-live="polite">
            <MixLineStatus line={line} group={group} unitName={unitName} />
          </div>
        </td>
      </tr>
    </Fragment>
  );
}
