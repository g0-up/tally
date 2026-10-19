import type { ReactNode } from "react";
import { toMoney } from "../../../shared/domain/money";
import { dec } from "../../../shared/lib/decimal";
import { money, num, vnd } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Checkbox } from "../../../shared/ui/checkbox";
import { ColorSwatchGroup } from "../../../shared/ui/color/color-swatch-group";
import { Combobox } from "../../../shared/ui/combobox";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { ACTIONS_HEADER } from "../../../shared/ui/data-table/data-table";
import { TableCaption } from "../../../shared/ui/data-table/table-caption";
import { DatePicker } from "../../../shared/ui/date-picker";
import { Input } from "../../../shared/ui/input";
import { LinkButton } from "../../../shared/ui/link-button";
import { MoneyInput } from "../../../shared/ui/money-input";
import { QtyInput } from "../../../shared/ui/qty-input";
import { baseUnitName, useSkuPicker, useUnits, type SkuListItem } from "../../catalog";
import { emptyBuyLine, lineAmount, lineName, newProductLine, tracksExpiry, tracksLot, withSku, withUnit, type BuyLine } from "../buy-lines";

export interface BuyEditorProps {
  lines: readonly BuyLine[];
  onChange: (lines: BuyLine[]) => void;
  /** Nhận hàng: adds the "Lô · hạn dùng" column. */
  receive?: boolean;
  /** Receiving against an order: the rows are the ordered lines, quantities in base units. */
  fixed?: boolean;
}

const BUY_HINT =
  "Quy cách là ghi chú tự do (VD: 24 hộp/thùng, hộp thiếc vuông). Giá bán lẻ sửa ở đây được áp dụng khi lưu chứng từ, cho các giao dịch bán mới; chứng từ bán cũ giữ giá đã chụp và mọi thay đổi giá được ghi vào lịch sử giá.";

/** Goods and materials are bought; kits and combos are not. */
const isBuyable = (sku: SkuListItem | undefined) => sku?.product_type === "goods" || sku?.product_type === "material";

/** Accessible name of a row control: its product ("Số lượng – Trà ô long"), or its row before one is picked ("Số lượng – dòng 2"). */
const rowLabel = (label: string, line: BuyLine, index: number) => `${label} – ${lineName(line, `dòng ${index + 1}`)}`;

/**
 * Prototype `buyEditor`: the line table of Đơn nhập and Nhận hàng. A row is an existing SKU picked
 * from the catalog, an inline new product ("Hàng mới"), or — receiving against an order — an
 * ordered line counted in base units at the ordered price.
 */
export function BuyEditor({ lines, onChange, receive = false, fixed = false }: BuyEditorProps) {
  const units = useUnits();
  const picker = useSkuPicker(null);

  const set = (index: number, next: BuyLine) => onChange(lines.map((l, i) => (i === index ? next : l)));
  const remove = (index: number) => onChange(lines.filter((_, i) => i !== index));

  const spec = (l: BuyLine, i: number) => (
    <td>
      <Input style={{ width: 140 }} value={l.spec} onChange={(e) => set(i, { ...l, spec: e.target.value })} placeholder="VD: 24 hộp/thùng" aria-label={rowLabel("Quy cách", l, i)} />
    </td>
  );

  const lot = (l: BuyLine, i: number): ReactNode => {
    if (!receive) return null;
    if (!tracksLot(l) && !tracksExpiry(l)) return <td>—</td>;
    return (
      <td>
        {tracksLot(l) ? (
          <Input
            style={{ width: 96 }}
            value={l.lotNo}
            onChange={(e) => set(i, { ...l, lotNo: e.target.value })}
            placeholder={l.isNew ? "Số lô" : "Bắt buộc"}
            aria-label={rowLabel("Số lô", l, i)}
          />
        ) : null}
        {tracksExpiry(l) ? (
          <div style={{ width: 140, marginTop: 4 }}>
            <DatePicker value={l.expiry} onChange={(expiry) => set(i, { ...l, expiry })} aria-label={rowLabel("Hạn dùng", l, i)} />
          </div>
        ) : null}
      </td>
    );
  };

  const swatches = (l: BuyLine, i: number) => <ColorSwatchGroup value={l.color} onChange={(color) => set(i, { ...l, color })} label={rowLabel("Màu sắc", l, i)} />;

  const newRow = (l: BuyLine, i: number) => {
    const base = units.name(l.baseUnitId);
    const big = l.bigUnitId !== null && l.bigUnitId !== l.baseUnitId ? l.bigUnitId : null;
    const unitName = l.inBig && big !== null ? units.name(big) : base;
    const factor = l.inBig && big !== null ? dec(l.factor ?? 0) : dec(1);
    return (
      <tr key={l.key} style={{ background: "var(--warn-bg)" }}>
        <td style={{ minWidth: 260 }}>
          <span className="tag" style={{ background: "var(--accent-crimson-strong)" }}>
            Hàng mới
          </span>
          <Input style={{ marginTop: 6 }} value={l.name} onChange={(e) => set(i, { ...l, name: e.target.value })} placeholder="Tên hàng hóa mới" aria-label="Tên hàng mới" />
          <ButtonRow style={{ marginTop: 6, gap: 6 }}>
            <select
              className="in"
              style={{ width: 96 }}
              value={l.baseUnitId ?? ""}
              onChange={(e) => {
                const id = e.target.value === "" ? null : e.target.value;
                set(i, { ...l, baseUnitId: id, inBig: l.inBig && l.bigUnitId !== null && l.bigUnitId !== id });
              }}
              aria-label={rowLabel("Đơn vị cơ sở", l, i)}
            >
              <option value="">ĐV cơ sở</option>
              {units.active.map((u) => (
                <option key={u.id} value={u.id}>
                  {u.name}
                </option>
              ))}
            </select>
            <select
              className="in"
              style={{ width: 96 }}
              value={l.bigUnitId ?? ""}
              onChange={(e) => {
                const id = e.target.value === "" ? null : e.target.value;
                set(i, { ...l, bigUnitId: id, inBig: l.inBig && id !== null && id !== l.baseUnitId });
              }}
              aria-label={rowLabel("Đơn vị lớn", l, i)}
            >
              <option value="">ĐV lớn</option>
              {units.active.map((u) => (
                <option key={u.id} value={u.id}>
                  {u.name}
                </option>
              ))}
            </select>
            <span className="hint">=</span>
            <QtyInput size="sm" style={{ width: 56 }} value={l.factor} onChange={(factor) => set(i, { ...l, factor })} placeholder="hệ số" aria-label={rowLabel("Hệ số quy đổi", l, i)} />
          </ButtonRow>
          <div style={{ marginTop: 6, fontSize: 12 }}>
            <Checkbox label="Quản lý lô/hạn" aria-label={rowLabel("Quản lý lô/hạn", l, i)} checked={l.trackLot} onChange={(e) => set(i, { ...l, trackLot: e.target.checked })} />
          </div>
          {swatches(l, i)}
        </td>
        <td>
          <select className="in" value={l.inBig && big !== null ? "big" : "base"} onChange={(e) => set(i, { ...l, inBig: e.target.value === "big" })} aria-label={rowLabel("Đơn vị tính", l, i)}>
            <option value="base">{base || "ĐVT"}</option>
            {big !== null ? <option value="big">{units.name(big)}</option> : null}
          </select>
        </td>
        <td className="num">
          <QtyInput size="sm" value={l.qty} onChange={(qty) => set(i, { ...l, qty })} aria-label={rowLabel("Số lượng", l, i)} />
          {factor.gt(1) ? (
            <CellSub>
              = {num(dec(l.qty ?? 0).times(factor))} {base}
            </CellSub>
          ) : null}
        </td>
        {spec(l, i)}
        <td className="num">
          <MoneyInput size="md" value={l.price} onChange={(price) => set(i, { ...l, price })} aria-label={rowLabel("Đơn giá", l, i)} />
          <CellSub>/{unitName || "ĐVT"}</CellSub>
        </td>
        <td className="num">{money(lineAmount(l))}</td>
        <td className="num">
          <MoneyInput size="md" value={l.sell} onChange={(sell) => set(i, { ...l, sell })} placeholder={`/${base || "ĐV"}`} aria-label={rowLabel("Giá bán lẻ", l, i)} />
        </td>
        {lot(l, i)}
        <td>
          <LinkButton onClick={() => remove(i)} aria-label={rowLabel("Xóa", l, i)}>
            Xóa
          </LinkButton>
        </td>
      </tr>
    );
  };

  const skuRow = (l: BuyLine, i: number) => {
    const sku = l.sku;
    const base = sku ? baseUnitName(sku) : "";
    const unitName = fixed ? base : (sku?.units.find((u) => u.unit_id === l.unitId)?.unit_name ?? base);
    return (
      <tr key={l.key}>
        <td style={{ minWidth: 260 }}>
          {fixed ? (
            <>
              {lineName(l)}
              {l.order ? (
                <CellSub>
                  Còn chờ {num(l.order.pending)} {base}
                </CellSub>
              ) : null}
            </>
          ) : (
            <>
              <Combobox
                source={picker.source}
                filter={(it) => isBuyable(picker.picked(it.id))}
                value={sku?.id ?? null}
                valueLabel={sku ? `${sku.code} · ${sku.display_name}` : ""}
                onChange={(it) => {
                  const picked = picker.picked(it.id);
                  if (picked) set(i, withSku(l, picked));
                }}
                aria-label={rowLabel("Tên hàng", l, i)}
              />
              {sku ? (
                <CellSub>
                  Tồn có thể bán {num(sku.sellable)} {base} · giá nhập gần nhất {sku.last_purchase_price === null ? "—" : vnd(sku.last_purchase_price)}
                </CellSub>
              ) : null}
            </>
          )}
          {swatches(l, i)}
        </td>
        <td>
          {fixed || !sku ? (
            base
          ) : (
            <select className="in" value={l.unitId ?? ""} onChange={(e) => set(i, withUnit(l, e.target.value))} aria-label={rowLabel("Đơn vị tính", l, i)}>
              {sku.units
                .filter((u) => u.active)
                .map((u) => (
                  <option key={u.unit_id} value={u.unit_id}>
                    {u.unit_name}
                  </option>
                ))}
            </select>
          )}
        </td>
        <td className="num">
          <QtyInput size="sm" value={l.qty} onChange={(qty) => set(i, { ...l, qty })} aria-label={rowLabel("Số lượng", l, i)} />
        </td>
        {spec(l, i)}
        <td className="num">
          {fixed ? money(l.price ?? toMoney(0)) : <MoneyInput size="md" value={l.price} onChange={(price) => set(i, { ...l, price })} aria-label={rowLabel("Đơn giá", l, i)} />}
          {sku ? <CellSub>/{unitName}</CellSub> : null}
        </td>
        <td className="num">{money(lineAmount(l))}</td>
        <td className="num">
          {sku ? (
            <>
              <MoneyInput size="md" value={l.sell} onChange={(sell) => set(i, { ...l, sell })} aria-label={rowLabel("Giá bán lẻ", l, i)} />
              <CellSub>
                hiện {sku.retail_price === null ? "—" : vnd(sku.retail_price)}/{base}
              </CellSub>
            </>
          ) : (
            "—"
          )}
        </td>
        {lot(l, i)}
        <td>
          {fixed ? null : (
            <LinkButton onClick={() => remove(i)} aria-label={rowLabel("Xóa", l, i)}>
              Xóa
            </LinkButton>
          )}
        </td>
      </tr>
    );
  };

  return (
    <>
      <div className="tw flat">
        <table className="t">
          <TableCaption>Hàng nhập</TableCaption>
          <thead>
            <tr>
              <th scope="col">Tên sản phẩm</th>
              <th scope="col">Đơn vị tính</th>
              <th scope="col" className="num">
                Số lượng
              </th>
              <th scope="col">Quy cách</th>
              <th scope="col" className="num">
                Đơn giá
              </th>
              <th scope="col" className="num">
                Thành tiền
              </th>
              <th scope="col" className="num">
                Giá bán lẻ
              </th>
              {receive ? <th scope="col">Lô · hạn dùng</th> : null}
              <th scope="col" aria-label={ACTIONS_HEADER} />
            </tr>
          </thead>
          <tbody>{lines.map((l, i) => (l.isNew ? newRow(l, i) : skuRow(l, i)))}</tbody>
        </table>
      </div>
      {fixed ? null : (
        <ButtonRow>
          <Button onClick={() => onChange([...lines, emptyBuyLine()])}>+ Hàng có sẵn</Button>
          <Button variant="navy" size="sm" onClick={() => onChange([...lines, newProductLine(units.idByName)])}>
            + Hàng hóa mới
          </Button>
        </ButtonRow>
      )}
      <p className="hint">{BUY_HINT}</p>
    </>
  );
}
