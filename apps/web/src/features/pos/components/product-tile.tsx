import { colorBackground, colorLabel, type ColorCode } from "../../../shared/domain/color";
import type { Money } from "../../../shared/domain/money";
import { dec } from "../../../shared/lib/decimal";
import { num, vnd } from "../../../shared/lib/format";
import type { PosTile } from "../../sales/schemas";

export interface ProductTileProps {
  tile: PosTile;
  /** Price of one base unit for the current customer; null when the SKU has no price yet. */
  price: Money | null;
  /** Sellable quantity in the base unit (all colors). */
  available: string;
  /** Colors still sellable, shown as dots. */
  colors: readonly ColorCode[];
  onAdd: (tile: PosTile) => void;
}

/**
 * Prototype POS tile: name, "price / base unit", "Có thể bán: N" or "Hết hàng có thể bán" with color
 * dots. The dots are decoration; the colors are spelled out in hidden text so they are part of the
 * button's name.
 */
export function ProductTile({ tile, price, available, colors, onAdd }: ProductTileProps) {
  const name = tile.variant_label ? `${tile.name} ${tile.variant_label}` : tile.name;
  const soldOut = !dec(available).gt(0);
  return (
    <button type="button" className="tile" disabled={soldOut} onClick={() => onAdd(tile)}>
      <b>{name}</b>
      <span className="tab-n">
        {price === null ? "—" : vnd(price)} / {tile.unit_name}
      </span>
      <span>
        {soldOut ? "Hết hàng có thể bán" : `Có thể bán: ${num(available)}`}
        {colors.length > 0 ? (
          <>
            {" "}
            <span className="sr-only">{`· Màu còn bán: ${colors.map(colorLabel).join(", ")}`}</span>
            <span className="tdots" aria-hidden="true">
              {colors.map((c) => (
                <i key={c} style={{ background: colorBackground(c) }} />
              ))}
            </span>
          </>
        ) : null}
      </span>
    </button>
  );
}
