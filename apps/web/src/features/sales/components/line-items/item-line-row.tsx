import { useCallback, useMemo } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useApiClient } from "../../../../shared/api/api-context";
import { isColorCode, type ColorCode, type ColorKey } from "../../../../shared/domain/color";
import type { Money } from "../../../../shared/domain/money";
import { num, vnd } from "../../../../shared/lib/format";
import { ColorSwatchGroup } from "../../../../shared/ui/color/color-swatch-group";
import { StockColorPicker } from "../../../../shared/ui/color/stock-color-picker";
import { Combobox, type ComboItem } from "../../../../shared/ui/combobox";
import { LinkButton } from "../../../../shared/ui/link-button";
import { MoneyInput } from "../../../../shared/ui/money-input";
import { QtyInput } from "../../../../shared/ui/qty-input";
import { Input } from "../../../../shared/ui/input";
import { toast } from "../../../../shared/ui/toast-store";
import { errorMessage } from "../../../../shared/api/api-error";
import { skuDetailQuery, skuSearchSource, useSkuDetail, useSkuStock, type ColorMaps } from "../../api/queries";
import { PriceSourceBadge } from "../price-source-badge";
import type { ItemDraft } from "./draft";
import { PercentInput } from "./percent-input";
import { itemLineName, rowLabel } from "./row-label";
import { useColorOptions } from "./use-color-options";
import type { LinePricing } from "./use-line-pricing";

export interface ItemLineRowProps {
  preset: "sell" | "quote";
  line: ItemDraft;
  /** Position in the table, naming the row's controls while it has no product. */
  index: number;
  pricing: LinePricing;
  /** Retail list prices (quote preset "Giá bán lẻ" column). */
  retail?: LinePricing | undefined;
  colors: ColorMaps;
  onChange: (line: ItemDraft) => void;
  onPickColor: (color: ColorKey) => void;
  onRemove: () => void;
}

/**
 * One product line of the shared line editor. "sell": Tên sản phẩm · Giá · Chiết khấu · Đơn vị tính ·
 * Số lượng · Thành tiền; "quote" adds Quy cách and the retail price for comparison.
 */
export function ItemLineRow({ preset, line, index, pricing, retail, colors, onChange, onPickColor, onRemove }: ItemLineRowProps) {
  const client = useApiClient();
  const queryClient = useQueryClient();
  const detail = useSkuDetail(line.skuId);
  const stock = useSkuStock(line.skuId);
  const options = useColorOptions(line.skuId, colors, line.color);
  const colored = (stock.data?.colors ?? []).some((c) => c.color_id !== null);
  const swatchColors = useMemo(
    () => (stock.data?.colors ?? []).flatMap((c) => {
      const code = colors.codeOf(c.color_id);
      return isColorCode(code) ? [code] : [];
    }),
    [stock.data, colors],
  );
  const units = (detail.data?.units ?? []).filter((u) => u.active || u.unit_id === line.unitId);
  const unitLabel = units.find((u) => u.unit_id === line.unitId)?.unit_name ?? "";
  const listed = pricing.listPrice(line.key);
  const amount = pricing.lineAmount(line.key);
  const shownPrice: Money | null = line.price ?? listed?.amount ?? null;

  const pickSku = async (item: ComboItem) => {
    if (item.id === line.skuId) return;
    try {
      const sku = await queryClient.fetchQuery(skuDetailQuery(client, item.id));
      const unit = sku.units.find((u) => u.active && u.is_default_sale) ?? sku.units.find((u) => u.is_base);
      onChange({ ...line, skuId: item.id, skuLabel: item.inputLabel ?? item.name, unitId: unit?.unit_id ?? null, price: null, color: null });
    } catch (e) {
      toast.error(errorMessage(e));
    }
  };
  const pickColor = useCallback((c: ColorKey) => onPickColor(c), [onPickColor]);
  const name = itemLineName(line);
  const label = (text: string) => rowLabel(text, name, index);

  const nameCell = (
    <td style={{ minWidth: preset === "quote" ? 260 : 240 }}>
      <Combobox source={skuSearchSource(colors)} value={line.skuId} valueLabel={line.skuLabel} aria-label={rowLabel("Tên sản phẩm", "", index)} onChange={(item) => void pickSku(item)} />
      {line.skuId !== null && stock.data ? (
        <span className="sub">
          Có thể bán: {num(stock.data.available)} {stock.data.unit_name}
          {listed ? (
            <>
              {" · "}
              <PriceSourceBadge source={line.price !== null ? { kind: "manual", price_list_name: null } : listed.price_source} color={line.color} />
            </>
          ) : null}
        </span>
      ) : null}
      {line.skuId !== null && colored && preset === "sell" ? <StockColorPicker options={options} value={line.color} onChange={pickColor} base={stock.data?.unit_name ?? ""} label={label("Màu khách lấy")} /> : null}
      {line.skuId !== null && colored && preset === "quote" ? (
        <ColorSwatchGroup label={label("Màu sắc")} value={line.color !== null && isColorCode(line.color) ? (line.color as ColorCode) : null} colors={swatchColors} onChange={pickColor} />
      ) : null}
    </td>
  );
  const unitCell = (
    <td>
      <select aria-label={label("Đơn vị tính")} value={line.unitId ?? ""} disabled={units.length === 0} onChange={(e) => onChange({ ...line, unitId: e.target.value || null, price: null })}>
        {units.length === 0 ? <option value="">—</option> : null}
        {units.map((u) => (
          <option key={u.unit_id} value={u.unit_id}>
            {u.unit_name}
          </option>
        ))}
      </select>
    </td>
  );
  const qtyCell = (
    <td className="num">
      <QtyInput size="sm" style={{ width: 64 }} value={line.qty} aria-label={label("Số lượng")} onChange={(qty) => onChange({ ...line, qty })} />
    </td>
  );
  const priceInput = (text: string) => <MoneyInput size="md" value={shownPrice} aria-label={label(text)} onChange={(price) => onChange({ ...line, price })} />;
  const discountCell = (
    <td className="num">
      <PercentInput value={line.discountPct} label={label("Chiết khấu %")} onChange={(discountPct) => onChange({ ...line, discountPct })} />
      <span className="sub">%</span>
    </td>
  );
  const amountCell = <td className="num">{amount === null ? "—" : vnd(amount)}</td>;
  const removeCell = (
    <td>
      <LinkButton onClick={onRemove} aria-label={label("Xóa")}>
        Xóa
      </LinkButton>
    </td>
  );

  if (preset === "sell") {
    return (
      <tr>
        {nameCell}
        <td className="num">{priceInput("Giá")}</td>
        {discountCell}
        {unitCell}
        {qtyCell}
        {amountCell}
        {removeCell}
      </tr>
    );
  }
  const retailPrice = retail?.listPrice(line.key)?.amount ?? null;
  return (
    <tr>
      {nameCell}
      {unitCell}
      {qtyCell}
      <td>
        <Input style={{ width: 140 }} value={line.spec} placeholder="VD: hộp thiếc, 12 hộp/thùng" aria-label={label("Quy cách")} onChange={(e) => onChange({ ...line, spec: e.target.value })} />
      </td>
      <td className="num">
        {priceInput("Đơn giá")}
        {unitLabel ? <span className="sub">/{unitLabel}</span> : null}
      </td>
      {discountCell}
      {amountCell}
      <td className="num">
        {retailPrice === null ? (
          "—"
        ) : (
          <>
            {vnd(retailPrice)}
            <span className="sub">/{unitLabel}</span>
          </>
        )}
      </td>
      {removeCell}
    </tr>
  );
}
