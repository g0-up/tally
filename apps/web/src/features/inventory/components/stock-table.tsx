import { colorBackground, colorLabel, type ColorKey } from "../../../shared/domain/color";
import { dec, sumDec } from "../../../shared/lib/decimal";
import { money, num, vnd } from "../../../shared/lib/format";
import { Button } from "../../../shared/ui/button";
import { ColorQtyPill } from "../../../shared/ui/color/color-qty-pill";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { ExpandableName, SubRow } from "../../../shared/ui/data-table/expandable-row";
import { TextLink } from "../../../shared/ui/link-button";
import type { SkuListItem } from "../../catalog";
import { colorKeyOf } from "../api/adapters";
import { colorPriceKey, type ColorPrice, type ColorPriceMap } from "../api/queries";
import type { ColorStock, StockRow } from "../schemas";
import { hasColors, stockCardHref, stockColors, type ColorFilter } from "../view-model";
import { SeenMarker } from "./seen-marker";


type ViewRow = { kind: "main"; row: StockRow; colored: boolean; open: boolean } | { kind: "sub"; row: StockRow; color: ColorStock; key: ColorKey; hl: boolean };

export interface StockTableProps {
  rows: readonly StockRow[];
  /** Catalog rows of the listed SKUs (type, retail and wholesale price). */
  skus: ReadonlyMap<string, SkuListItem> | undefined;
  /** Own color prices of the SKUs shown so far. */
  colorPrices: ColorPriceMap;
  /** A SKU's row came into view and needs its color prices. */
  onSeen: (skuId: string) => void;
  /** More pages remain, so the footer totals cover only the loaded rows. */
  partial: boolean;
  pick: ColorFilter;
  /** Rows whose colors are shown; a row the user never toggled follows the color filter. */
  open: Readonly<Record<string, boolean>>;
  onToggle: (skuId: string, open: boolean) => void;
  onEditPrice: (sku: SkuListItem) => void;
  onColorPrice: (row: StockRow, color: ColorStock) => void;
}

/** "N màu giá riêng" plus the lowest–highest price when the colors differ. */
function priceRange(base: string | null, own: readonly ColorPrice[]) {
  const xs = [...own.flatMap((p) => (p.retail === null ? [] : [dec(p.retail)])), ...(base === null ? [] : [dec(base)])];
  const lo = xs.reduce((a, b) => (b.lt(a) ? b : a), xs[0] ?? dec(0));
  const hi = xs.reduce((a, b) => (b.gt(a) ? b : a), xs[0] ?? dec(0));
  return (
    <CellSub>
      {own.length} màu giá riêng
      {lo.eq(hi) ? null : (
        <>
          <br />
          {vnd(lo)}–{vnd(hi)}
        </>
      )}
    </CellSub>
  );
}

/**
 * Prototype `kho.ton`: one row per SKU with its on-hand and sellable stock, colored stock as inline
 * pills and, when opened, one sub row per color with its own price. Every figure is the server's.
 */
export function StockTable({ rows, skus, colorPrices, onSeen, partial, pick, open, onToggle, onEditPrice, onColorPrice }: StockTableProps) {
  const view: ViewRow[] = rows.flatMap((row) => {
    const colored = hasColors(row);
    const isOpen = colored && (open[row.sku_id] ?? pick !== "all");
    const main: ViewRow = { kind: "main", row, colored, open: isOpen };
    if (!isOpen) return [main];
    return [main, ...stockColors(row).map((color): ViewRow => ({ kind: "sub", row, color, key: colorKeyOf(color.color_code), hl: pick === colorKeyOf(color.color_code) }))];
  });
  const mains = rows.length;
  const colored = rows.filter(hasColors).length;

  const ownPrices = (row: StockRow) => stockColors(row).flatMap((c) => colorPrices.get(colorPriceKey(row.sku_id, c.color_id)) ?? []);

  const nameCell = (r: ViewRow) => {
    if (r.kind === "sub") {
      return (
        <SubRow highlight={r.hl}>
          <i className="cdot" style={{ background: colorBackground(r.key) }} aria-hidden="true" />
          {colorLabel(r.key)}
        </SubRow>
      );
    }
    const sku = skus?.get(r.row.sku_id);
    return (
      <ExpandableName toggle={r.colored ? { name: r.row.name, expanded: r.open, onToggle: () => onToggle(r.row.sku_id, !r.open) } : null}>
        <SeenMarker id={r.row.sku_id} onSeen={onSeen} />
        <b>{r.row.name}</b>
        <CellSub>
          {r.row.code} · ĐV cơ sở: {r.row.unit_name}
          {sku?.product_type === "kit" ? " · giỏ đóng sẵn" : ""}
        </CellSub>
        {r.colored ? (
          <div className="cline">
            {stockColors(r.row).map((c) => (
              <ColorQtyPill key={c.color_id ?? "none"} color={colorKeyOf(c.color_code)} qty={c.on_hand} base={r.row.unit_name} active={pick === colorKeyOf(c.color_code)} />
            ))}
          </div>
        ) : null}
      </ExpandableName>
    );
  };

  const sellableCell = (r: ViewRow) => {
    const quarantine = dec(r.kind === "sub" ? r.color.quarantine : r.row.quarantine);
    const expired = dec(r.kind === "sub" ? r.color.expired : r.row.expired);
    const held = r.kind === "sub" ? dec(0) : dec(r.row.reserved);
    const notes = [
      held.gt(0) ? <span key="held">giữ {num(held)}</span> : null,
      quarantine.gt(0) ? (
        <span key="quarantine" className="dl bad">
          cách ly {num(quarantine)}
        </span>
      ) : null,
      expired.gt(0) ? (
        <span key="expired" className="dl bad">
          hết hạn {num(expired)}
        </span>
      ) : null,
    ].filter((x) => x !== null);
    return (
      <>
        {r.kind === "sub" ? num(r.color.sellable) : <b>{num(r.row.available)}</b>}
        {notes.length > 0 ? <CellSub>{notes.flatMap((n, i) => (i === 0 ? [n] : [" · ", n]))}</CellSub> : null}
      </>
    );
  };

  const priceCell = (r: ViewRow) => {
    const sku = skus?.get(r.row.sku_id);
    const retail = sku?.retail_price ?? r.row.price ?? null;
    if (sku?.product_type === "material" || retail === null) return r.kind === "sub" ? "" : "—";
    if (r.kind === "main") {
      const own = r.colored ? ownPrices(r.row) : [];
      return (
        <>
          {money(retail)}
          <CellSub>sỉ {vnd(sku?.wholesale_price ?? null)}</CellSub>
          {r.colored ? own.length > 0 ? priceRange(retail, own) : <CellSub>giá chung mọi màu</CellSub> : null}
        </>
      );
    }
    const own = colorPrices.get(colorPriceKey(r.row.sku_id, r.color.color_id));
    if (own && (own.retail !== null || own.wholesale !== null)) {
      return (
        <>
          <b>{money(own.retail ?? retail)}</b>
          <CellSub>
            sỉ {vnd(own.wholesale ?? sku?.wholesale_price ?? null)} · giá riêng
          </CellSub>
        </>
      );
    }
    return (
      <>
        <span className="hint">{vnd(retail)}</span>
        <CellSub>theo giá chung</CellSub>
      </>
    );
  };

  const actsCell = (r: ViewRow) => {
    const sku = skus?.get(r.row.sku_id);
    const goods = sku?.product_type === "goods";
    // The row's product (and color) names each action, since "Sửa giá" and "Thẻ kho" repeat on every row.
    if (r.kind === "sub") {
      const who = `${r.row.name} · ${colorLabel(r.key)}`;
      return (
        <>
          {goods && sku.retail_price !== null ? (
            <>
              <Button size="sm" title={`Sửa giá riêng ${colorLabel(r.key).toLowerCase()}`} aria-label={`Sửa giá – ${who}`} onClick={() => onColorPrice(r.row, r.color)}>
                Sửa giá
              </Button>{" "}
            </>
          ) : null}
          <TextLink to={stockCardHref(r.row.sku_id, r.key)} aria-label={`Thẻ kho – ${who}`}>
            Thẻ kho
          </TextLink>
        </>
      );
    }
    return (
      <>
        {goods ? (
          <>
            <Button size="sm" title={r.colored ? "Sửa giá chung cho cả sản phẩm" : "Sửa giá bán"} aria-label={`Sửa giá – ${r.row.name}`} onClick={() => onEditPrice(sku)}>
              Sửa giá
            </Button>{" "}
          </>
        ) : null}
        <TextLink to={stockCardHref(r.row.sku_id)} aria-label={`Thẻ kho – ${r.row.name}`}>
          Thẻ kho
        </TextLink>
      </>
    );
  };

  const columns: Column<ViewRow>[] = [
    { key: "name", header: "Hàng", cell: nameCell, footer: `${mains} mặt hàng${colored ? ` · ${colored} có màu` : ""}${partial ? " (đã tải)" : ""}` },
    { key: "on-hand", header: "Tồn thực", numeric: true, cell: (r) => (r.kind === "sub" ? num(r.color.on_hand) : <b>{num(r.row.on_hand)}</b>) },
    { key: "sellable", header: "Có thể bán", numeric: true, cell: sellableCell },
    { key: "price", header: "Giá bán", numeric: true, cell: priceCell },
    {
      key: "value",
      header: "Giá trị tồn",
      numeric: true,
      cell: (r) => money(r.kind === "sub" ? r.color.value : r.row.stock_value),
      footer: (
        <>
          {money(sumDec(rows.map((r) => r.stock_value)))}
          {partial ? <CellSub>trên các dòng đã tải</CellSub> : null}
        </>
      ),
    },
    { key: "acts", header: "", className: "acts", cell: actsCell },
  ];

  return (
    <DataTable
      caption="Tồn kho"
      columns={columns}
      rows={view}
      rowKey={(r) => (r.kind === "main" ? r.row.sku_id : `${r.row.sku_id}|${r.key}`)}
      empty="Không có hàng phù hợp."
    />
  );
}
