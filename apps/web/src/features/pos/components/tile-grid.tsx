import type { ColorCode } from "../../../shared/domain/color";
import type { Money } from "../../../shared/domain/money";
import type { PosTile } from "../../sales/schemas";
import { ProductTile } from "./product-tile";

export const MAX_TILES = 30;

export interface TileGridProps {
  tiles: readonly PosTile[];
  priceOf: (tile: PosTile) => Money | null;
  availableOf: (tile: PosTile) => string;
  colorsOf: (tile: PosTile) => ColorCode[];
  onAdd: (tile: PosTile) => void;
}

/** Up to 30 tiles matching the scan field. */
export function TileGrid({ tiles, priceOf, availableOf, colorsOf, onAdd }: TileGridProps) {
  if (tiles.length === 0) return <p className="hint">Không tìm thấy hàng phù hợp.</p>;
  return (
    <div className="tiles">
      {tiles.slice(0, MAX_TILES).map((t) => (
        <ProductTile key={t.sku_id} tile={t} price={priceOf(t)} available={availableOf(t)} colors={colorsOf(t)} onAdd={onAdd} />
      ))}
    </div>
  );
}
