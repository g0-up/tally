import { isColorCode } from "../../../shared/domain/color";
import { ColorSwatchGroup } from "../../../shared/ui/color/color-swatch-group";
import type { ColorDirectory } from "../../catalog";
import type { OpenColor } from "../view-model";

export interface OpenColorFieldsProps {
  open: readonly OpenColor[];
  /** Picked color id per SKU. */
  picks: Readonly<Record<string, string>>;
  onPick: (skuId: string, colorId: string) => void;
  colors: ColorDirectory;
}

/** One swatch row per component whose color the recipe leaves open, offering the colors in stock. */
export function OpenColorFields({ open, picks, onPick, colors }: OpenColorFieldsProps) {
  return open.map((o) => {
    const label = `Màu – ${o.name}`;
    const code = colors.codeOf(picks[o.skuId]);
    return (
      // A label element would forward its clicks to the first swatch, so the row is a plain block.
      <div key={o.skuId} className="f">
        <span aria-hidden="true">{label}</span>
        <ColorSwatchGroup
          label={label}
          value={isColorCode(code) ? code : null}
          colors={o.palette}
          onChange={(c) => {
            const id = colors.idOf(c);
            if (id !== null) onPick(o.skuId, id);
          }}
        />
      </div>
    );
  });
}
