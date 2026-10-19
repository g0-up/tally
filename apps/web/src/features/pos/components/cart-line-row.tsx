import { Fragment } from "react";
import type { ColorKey } from "../../../shared/domain/color";
import { vnd } from "../../../shared/lib/format";
import { StockColorPicker } from "../../../shared/ui/color/stock-color-picker";
import { LinkButton } from "../../../shared/ui/link-button";
import { QtyInput } from "../../../shared/ui/qty-input";
import { useSkuDetail, useSkuStock, type ColorMaps } from "../../sales/api/queries";
import type { ItemDraft } from "../../sales/components/line-items/draft";
import { PercentInput } from "../../sales/components/line-items/percent-input";
import { itemLineName } from "../../sales/components/line-items/row-label";
import { useColorOptions } from "../../sales/components/line-items/use-color-options";
import type { LinePricing } from "../../sales/components/line-items/use-line-pricing";

export interface CartLineRowProps {
  line: ItemDraft;
  /** Name shown in the row and in its field labels. */
  name: string;
  pricing: LinePricing;
  colors: ColorMaps;
  onChange: (line: ItemDraft) => void;
  onPickColor: (color: ColorKey) => void;
  onSplit: () => void;
  onRemove: () => void;
}

/**
 * One cart line: Tên sản phẩm · Giá · CK % · ĐVT · SL · Thành tiền · Xóa, and for a colored product a
 * second row "Màu khách lấy:" with the colors still sellable and "+ Thêm màu khác". Control names
 * carry the product and picked color, since one product split by color fills several rows.
 */
export function CartLineRow({ line, name, pricing, colors, onChange, onPickColor, onSplit, onRemove }: CartLineRowProps) {
  const detail = useSkuDetail(line.skuId);
  const stock = useSkuStock(line.skuId);
  const options = useColorOptions(line.skuId, colors, line.color);
  const colored = (stock.data?.colors ?? []).some((c) => c.color_id !== null);
  const units = (detail.data?.units ?? []).filter((u) => u.active || u.unit_id === line.unitId);
  const price = line.price ?? pricing.listPrice(line.key)?.amount ?? null;
  const amount = pricing.lineAmount(line.key);
  const who = itemLineName(line) || name;

  return (
    <Fragment>
      <tr className={colored ? "has-cp" : undefined}>
        <td>{name}</td>
        <td className="num">{price === null ? "—" : vnd(price)}</td>
        <td className="num">
          <PercentInput value={line.discountPct} label={`Chiết khấu % – ${who}`} onChange={(discountPct) => onChange({ ...line, discountPct })} />
        </td>
        <td>
          <select
            className="in"
            style={{ width: "auto" }}
            aria-label={`Đơn vị – ${who}`}
            value={line.unitId ?? ""}
            disabled={units.length === 0}
            onChange={(e) => onChange({ ...line, unitId: e.target.value || null, price: null })}
          >
            {units.length === 0 ? <option value={line.unitId ?? ""}>—</option> : null}
            {units.map((u) => (
              <option key={u.unit_id} value={u.unit_id}>
                {u.unit_name}
              </option>
            ))}
          </select>
        </td>
        <td className="num">
          <QtyInput size="sm" style={{ width: 56 }} value={line.qty} aria-label={`Số lượng – ${who}`} onChange={(qty) => onChange({ ...line, qty })} />
        </td>
        <td className="num">{amount === null ? "—" : vnd(amount)}</td>
        <td>
          <LinkButton aria-label={`Xóa ${who}`} onClick={onRemove}>
            Xóa
          </LinkButton>
        </td>
      </tr>
      {colored ? (
        <tr className="cp-row">
          <td colSpan={7}>
            <div className="cp-wrap">
              <span className="hint">Màu khách lấy:</span>
              <StockColorPicker options={options} value={line.color} onChange={onPickColor} base={stock.data?.unit_name ?? ""} label={`Màu khách lấy – ${name}`} />
              <LinkButton aria-label={`Thêm màu khác – ${name}`} onClick={onSplit}>
                + Thêm màu khác
              </LinkButton>
            </div>
          </td>
        </tr>
      ) : null}
    </Fragment>
  );
}
