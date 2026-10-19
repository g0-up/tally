import type { ColorKey } from "../../../shared/domain/color";
import { ACTIONS_HEADER } from "../../../shared/ui/data-table/data-table";
import { TableCaption } from "../../../shared/ui/data-table/table-caption";
import type { ColorMaps } from "../../sales/api/queries";
import type { ItemDraft } from "../../sales/components/line-items/draft";
import type { LinePricing } from "../../sales/components/line-items/use-line-pricing";
import { cartLineName } from "../api/adapters";
import { CartLineRow } from "./cart-line-row";

export interface CartPanelProps {
  lines: readonly ItemDraft[];
  pricing: LinePricing;
  colors: ColorMaps;
  onChange: (line: ItemDraft) => void;
  onPickColor: (key: string, color: ColorKey) => void;
  onSplit: (key: string) => void;
  onRemove: (key: string) => void;
}

/** The cart table, or "Giỏ hàng trống." when nothing has been added. */
export function CartPanel({ lines, pricing, colors, onChange, onPickColor, onSplit, onRemove }: CartPanelProps) {
  if (lines.length === 0) {
    return (
      <div className="empty" style={{ border: "1px dashed var(--gray-300)" }}>
        Giỏ hàng trống. Quét mã hoặc chọn hàng bên trái.
      </div>
    );
  }
  return (
    <div className="tw flat">
      <table className="t">
        <TableCaption>Giỏ hàng</TableCaption>
        <thead>
          <tr>
            <th scope="col">Tên sản phẩm</th>
            <th scope="col" className="num">Giá</th>
            <th scope="col" className="num">CK %</th>
            <th scope="col">ĐVT</th>
            <th scope="col" className="num">SL</th>
            <th scope="col" className="num">Thành tiền</th>
            <th scope="col" aria-label={ACTIONS_HEADER} />
          </tr>
        </thead>
        <tbody>
          {lines.map((l) => (
            <CartLineRow
              key={l.key}
              line={l}
              name={cartLineName(l)}
              pricing={pricing}
              colors={colors}
              onChange={onChange}
              onPickColor={(c) => onPickColor(l.key, c)}
              onSplit={() => onSplit(l.key)}
              onRemove={() => onRemove(l.key)}
            />
          ))}
        </tbody>
      </table>
    </div>
  );
}
