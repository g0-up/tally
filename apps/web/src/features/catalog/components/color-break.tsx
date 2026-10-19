import { COLORS, HATCH_CSS, colorBackground } from "../../../shared/domain/color";
import { dec } from "../../../shared/lib/decimal";
import { num } from "../../../shared/lib/format";
import type { ColorDirectory } from "../api/queries";
import type { SkuListItem } from "../schemas";

const CHIP_GAP = { margin: "0 8px 0 0" } as const;

/**
 * Prototype `colorBreak()`: on-hand quantity per color under the product name ("Đỏ 12  Vàng 4
 * Chưa gán 3"), in the fixed color order. Nothing when no stock carries a color.
 */
export function ColorBreak({ sku, colors }: { sku: Pick<SkuListItem, "on_hand_by_color">; colors: ColorDirectory }) {
  const byCode = new Map<string, string>();
  let untagged = dec(0);
  for (const c of sku.on_hand_by_color) {
    if (dec(c.qty).isZero()) continue;
    const code = colors.codeOf(c.color_id);
    if (code === "none") untagged = untagged.plus(c.qty);
    else byCode.set(code, dec(byCode.get(code) ?? 0).plus(c.qty).toString());
  }
  if (byCode.size === 0) return null;
  return (
    <span className="sub">
      {COLORS.filter((c) => byCode.has(c.key)).map((c) => (
        <span key={c.key} className="sw-chip" style={CHIP_GAP}>
          <i style={{ background: colorBackground(c.key) }} aria-hidden="true" />
          {c.label} {num(byCode.get(c.key) ?? 0)}
        </span>
      ))}
      {untagged.isZero() ? null : (
        <span className="sw-chip" style={{ margin: 0 }}>
          <i style={{ background: HATCH_CSS }} aria-hidden="true" />
          Chưa gán {num(untagged)}
        </span>
      )}
    </span>
  );
}
