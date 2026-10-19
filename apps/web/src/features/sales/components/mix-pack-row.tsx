import { Fragment } from "react";
import { useQueries } from "@tanstack/react-query";
import { useApiClient } from "../../../shared/api/api-context";
import { toMoney, type Money } from "../../../shared/domain/money";
import type { Qty } from "../../../shared/domain/qty";
import { dec, floorDec, sumDec } from "../../../shared/lib/decimal";
import { num, vnd } from "../../../shared/lib/format";
import { Callout } from "../../../shared/ui/callout";
import { Input } from "../../../shared/ui/input";
import { LinkButton } from "../../../shared/ui/link-button";
import { QtyInput } from "../../../shared/ui/qty-input";
import { skuStockQuery } from "../api/queries";
import type { MixGroup } from "../schemas";
import { PercentInput } from "./line-items/percent-input";
import { rowLabel } from "./line-items/row-label";
import { flavorPieces, mixUsage, switchMixGroup, type MixDraft } from "./line-items/draft";
import type { LinePricing } from "./line-items/use-line-pricing";

/** Prototype `mixStatus`: parts chosen against the room in the packs. */
export function MixStatus({ line, group, unitName }: { line: MixDraft; group: MixGroup; unitName: (id: string) => string }) {
  const { cap, used } = mixUsage(line, group);
  const part = unitName(group.part_unit_id);
  const pack = unitName(group.pack_unit_id);
  if (!cap) return <span className="hint">Nhập số thùng.</span>;
  if (used > cap) {
    return (
      <Callout tone="bad">
        Đã chọn {num(used)} {part}, vượt {num(line.packs ?? "0")} {pack} (tối đa {num(cap)} {part}). Giảm bớt {num(used - cap)} {part}.
      </Callout>
    );
  }
  return (
    <span className="hint">
      Đã chọn{" "}
      <b>
        {num(used)}/{num(cap)} {part}
      </b>
      {used < cap ? ` · còn trống ${num(cap - used)} ${part}; tính tiền theo số ${part} thực chọn` : " · đủ thùng"}
    </span>
  );
}

/**
 * Price of one pack as the server stores and prints it on the pack line: the gross amount of the
 * flavors chosen (before discounts, in the price basis of the store, so tax is in it exactly when
 * prices include tax) over the packs, rounded like money. The calculated gross is used when there
 * is one, since document rounding can move it a dong off price × quantity; until then, the flavor
 * list prices times their pieces. With no flavor chosen yet, the PRD default: the average flavor
 * price times the pieces in a pack.
 */
function mixPackPrice(line: MixDraft, group: MixGroup, flavorIds: readonly string[], pricing: Pick<LinePricing, "flavorPrice" | "grossAmount">): Money | null {
  const chosen = Object.entries(line.parts).filter((e): e is [string, Qty] => e[1] !== null && dec(e[1]).gt(0));
  const packs = dec(line.packs ?? "0");
  if (chosen.length > 0 && packs.gt(0)) {
    const listed = chosen.map(([skuId, parts]) => {
      const price = pricing.flavorPrice(line.key, skuId);
      return price === null ? null : dec(flavorPieces(parts, group)).times(price);
    });
    const gross = pricing.grossAmount(line.key) ?? (listed.every((x) => x !== null) ? sumDec(listed) : null);
    return gross === null ? null : toMoney(dec(gross).div(packs));
  }
  return fullPackPrice(line, group, flavorIds, pricing);
}

/** Prototype mixPackPrice: the average flavor price times the pieces in a full pack. */
function fullPackPrice(line: MixDraft, group: MixGroup, flavorIds: readonly string[], pricing: Pick<LinePricing, "flavorPrice">): Money | null {
  const prices = flavorIds.flatMap((id) => {
    const p = pricing.flavorPrice(line.key, id);
    return p === null ? [] : [p];
  });
  return prices.length > 0 ? toMoney(sumDec(prices).div(prices.length).times(group.units_per_pack)) : null;
}

export interface MixPackRowProps {
  preset: "sell" | "quote";
  line: MixDraft;
  /** Position in the table, telling two packs of the same group apart. */
  index: number;
  groups: readonly MixGroup[];
  pricing: LinePricing;
  unitName: (id: string) => string;
  onChange: (line: MixDraft) => void;
  onRemove: () => void;
}

/**
 * "Thùng mix vị" line (prototype `lineEditor` / `quoteEditor` mix rows): the group, the pack price
 * the server derives from the flavors, discount, packs and a second row with the parts per flavor.
 */
export function MixPackRow({ preset, line, index, groups, pricing, unitName, onChange, onRemove }: MixPackRowProps) {
  const client = useApiClient();
  const group = groups.find((g) => g.id === line.groupId);
  const flavors = group?.items.filter((x) => x.active || x.sku_id in line.parts) ?? [];
  const stocks = useQueries({ queries: flavors.map((x) => skuStockQuery(client, x.sku_id)) });
  if (!group) return null;

  const pack = unitName(group.pack_unit_id);
  const part = unitName(group.part_unit_id);
  const piece = unitName(group.piece_unit_id);
  const flavorIds = flavors.map((x) => x.sku_id);
  const packPrice = mixPackPrice(line, group, flavorIds, pricing);
  // The retail column of a quote is a reference for a full pack, whatever this one holds.
  const referencePrice = fullPackPrice(line, group, flavorIds, pricing);
  const amount = pricing.lineAmount(line.key);
  const perPack = group.units_per_part > 0 ? group.units_per_pack / group.units_per_part : 0;
  const label = (text: string) => rowLabel(text, `${group.name}, dòng ${index + 1}`, index);

  const head = (
    <td style={{ minWidth: 240 }}>
      <b style={{ color: "var(--navy-900)" }}>Thùng mix vị</b>
      <select
        className="mixgrp"
        aria-label={label("Chọn nhóm mix vị")}
        value={group.id}
        onChange={(e) => {
          const next = groups.find((g) => g.id === e.target.value);
          if (next) onChange(switchMixGroup(line, next));
        }}
      >
        {groups.map((g) => (
          <option key={g.id} value={g.id}>
            {g.name}
          </option>
        ))}
      </select>
      <span className="sub">
        Mỗi {pack} = {num(perPack)} {part} = {num(group.units_per_pack)} {piece}; chọn vị theo {part}
      </span>
    </td>
  );
  const priceCell = (
    <td className="num">
      {packPrice === null ? "—" : vnd(packPrice)}
      <span className="sub">{preset === "quote" ? `/${pack}` : `/ ${pack}`}</span>
    </td>
  );
  const discountCell = (
    <td className="num">
      <PercentInput value={line.discountPct} label={label("Chiết khấu %")} onChange={(discountPct) => onChange({ ...line, discountPct })} />
      <span className="sub">%</span>
    </td>
  );
  const packsCell = (
    <td className="num">
      <QtyInput size="sm" style={{ width: 64 }} value={line.packs} aria-label={label("Số thùng")} onChange={(packs) => onChange({ ...line, packs })} />
    </td>
  );
  const amountCell = (
    <td className="num">
      <b>{amount === null ? "—" : vnd(amount)}</b>
    </td>
  );
  const removeCell = (
    <td>
      <LinkButton onClick={onRemove} aria-label={label("Xóa")}>
        Xóa
      </LinkButton>
    </td>
  );

  return (
    <Fragment>
      {preset === "sell" ? (
        <tr>
          {head}
          {priceCell}
          {discountCell}
          <td>{pack}</td>
          {packsCell}
          {amountCell}
          {removeCell}
        </tr>
      ) : (
        <tr>
          {head}
          <td>{pack}</td>
          {packsCell}
          <td>
            <Input style={{ width: 140 }} value={line.spec} placeholder="VD: hộp thiếc, 12 hộp/thùng" aria-label={label("Quy cách")} onChange={(e) => onChange({ ...line, spec: e.target.value })} />
          </td>
          {priceCell}
          {discountCell}
          {amountCell}
          <td className="num">
            {referencePrice === null ? "—" : vnd(referencePrice)}
            <span className="sub">/{pack}</span>
          </td>
          {removeCell}
        </tr>
      )}
      <tr>
        <td colSpan={preset === "sell" ? 7 : 9} style={{ background: "var(--surface-blue-50)", borderTop: 0 }}>
          <div className="mixgrid">
            {flavors.map((x, i) => {
              const sellable = stocks[i]?.data?.available;
              return (
                <label className="f" key={x.sku_id}>
                  <span>
                    {x.flavor_label} · còn {sellable === undefined ? "—" : num(floorDec(dec(sellable).div(group.units_per_part)))} {part}
                  </span>
                  <QtyInput
                    value={line.parts[x.sku_id] ?? null}
                    aria-label={label(`Số ${part} vị ${x.flavor_label}`)}
                    onChange={(v) => onChange({ ...line, parts: { ...line.parts, [x.sku_id]: v } })}
                  />
                </label>
              );
            })}
          </div>
          {/* Always on the page so the count and the over-cap warning are announced as they change. */}
          <div className="m-live-mix" aria-live="polite">
            <MixStatus line={line} group={group} unitName={unitName} />
          </div>
        </td>
      </tr>
    </Fragment>
  );
}
