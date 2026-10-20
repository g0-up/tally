import { useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { useApiClient } from "../../../shared/api/api-context";
import { errorMessage } from "../../../shared/api/api-error";
import { toMoney } from "../../../shared/domain/money";
import type { ScanOutcome } from "../../../shared/lib/barcode";
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
import { toast } from "../../../shared/ui/toast-store";
import { baseUnitName, lookupSkuCode, useSkuPicker, useUnits, type SkuCodeMatch } from "../../catalog";
import { NOT_ACCEPTING, PhonePairingCard, useScanTarget, type ScanHandler } from "../../scanner";
import { PROCUREMENT_MESSAGES } from "../api/adapters";
import {
  addScan,
  emptyBuyLine,
  isBuyable,
  lineAmount,
  lineName,
  newProductLine,
  scanNewProduct,
  tracksExpiry,
  tracksLot,
  withSku,
  withUnit,
  type BarcodeError,
  type BuyLine,
  type ScanStep,
} from "../buy-lines";

export interface BuyEditorProps {
  lines: readonly BuyLine[];
  onChange: (lines: BuyLine[]) => void;
  /** Nhận hàng: adds the "Lô · hạn dùng" column. */
  receive?: boolean;
  /** Receiving against an order: the rows are the ordered lines, quantities in base units. */
  fixed?: boolean;
  /** Barcodes the server refused, by row key; shown while the row still holds the refused code. */
  barcodeErrors?: ReadonlyMap<string, BarcodeError>;
  /** The document is being saved or posted: its rows are already sent, so a phone's code is turned away. */
  busy?: boolean;
}

const BUY_HINT =
  "Quy cách là ghi chú tự do (VD: 24 hộp/thùng, hộp thiếc vuông). Giá bán lẻ sửa ở đây được áp dụng khi lưu chứng từ, cho các giao dịch bán mới; chứng từ bán cũ giữ giá đã chụp và mọi thay đổi giá được ghi vào lịch sử giá.";

/** Shorter than the combobox default so it fits the product column without clipping. */
const SKU_PLACEHOLDER = "Mã, tên hoặc mã vạch…";

/** Accessible name of a row control: its product ("Số lượng – Trà ô long"), or its row before one is picked ("Số lượng – dòng 2"). */
const rowLabel = (label: string, line: BuyLine, index: number) => `${label} – ${lineName(line, `dòng ${index + 1}`)}`;

/**
 * Prototype `buyEditor`: the line table of Đơn nhập and Nhận hàng. A row is an existing SKU picked
 * from the catalog, an inline new product ("Hàng mới"), or — receiving against an order — an
 * ordered line counted in base units at the ordered price. A paired phone's scans add to the rows.
 */
export function BuyEditor({ lines, onChange, receive = false, fixed = false, barcodeErrors, busy = false }: BuyEditorProps) {
  const units = useUnits();
  const picker = useSkuPicker(null);
  /** New-product row made from a search or a scan that found nothing; its name field takes focus. */
  const [created, setCreated] = useState<string | null>(null);

  // Scans resolve after the server answers, so two of them can land before the dialog renders
  // again: each adds to the latest rows held here, not to the ones of the render it began in.
  const client = useApiClient();
  const latest = useRef(lines);
  const busyNow = useRef(busy);
  useLayoutEffect(() => {
    latest.current = lines;
    busyNow.current = busy;
  }, [lines, busy]);

  const set = (index: number, next: BuyLine) => onChange(lines.map((l, i) => (i === index ? next : l)));
  const remove = (index: number) => onChange(lines.filter((_, i) => i !== index));

  // A phone's code is turned away while the document is being saved (its rows are already sent, so
  // a code added now would be lost) and while another dialog is open over this one. The editor sits
  // in a dialog itself, so the app-wide `hasBlockingLayer()` is always true here; instead, a modal
  // dialog marks everything outside it aria-hidden, which covers this table only when a dialog is
  // opened above the one holding it.
  const table = useRef<HTMLDivElement>(null);
  const refusing = () => busyNow.current || table.current?.closest('[aria-hidden="true"]') != null;

  const applyScan = (step: ScanStep): ScanOutcome => {
    if (step.notice !== undefined) toast.error(step.notice);
    if (step.lines !== null) {
      latest.current = step.lines;
      onChange(step.lines);
    }
    if (step.created !== undefined) setCreated(step.created);
    return step.outcome;
  };

  /** Adds one code sent by a paired phone. */
  const scanCode: ScanHandler = async (code) => {
    if (refusing()) return NOT_ACCEPTING;
    const wanted = code.trim();
    if (wanted === "") return { outcome: "not_found" };
    // A code of a new product typed on this screen is not in the catalog yet.
    const known = fixed ? null : scanNewProduct(latest.current, wanted);
    if (known) return applyScan(known);
    let found: SkuCodeMatch | null;
    try {
      found = await lookupSkuCode(client, wanted);
    } catch (e) {
      toast.error(PROCUREMENT_MESSAGES.scanLookupFailed(wanted, errorMessage(e)));
      return { outcome: "not_found" };
    }
    // A save may have started while the code was being looked up.
    if (refusing()) return NOT_ACCEPTING;
    return applyScan(addScan(latest.current, wanted, found, { fixed, unitIdByName: units.idByName }));
  };

  useScanTarget(busy ? null : scanCode);

  const spec = (l: BuyLine, i: number) => (
    <td>
      <Input style={{ width: 112 }} value={l.spec} onChange={(e) => set(i, { ...l, spec: e.target.value })} placeholder="VD: 24 hộp/thùng" aria-label={rowLabel("Quy cách", l, i)} />
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

  const barcodeInput = (l: BuyLine, i: number) => {
    const refused = barcodeErrors?.get(l.key);
    const error = refused !== undefined && refused.code === l.barcode.trim() ? refused.message : null;
    const errorId = `bp-barcode-err-${l.key}`;
    // The error wraps onto its own line under the field.
    return (
      <span style={{ flexWrap: "wrap" }}>
        Mã vạch
        <Input
          style={{ width: 132 }}
          value={l.barcode}
          onChange={(e) => set(i, { ...l, barcode: e.target.value })}
          placeholder="Không bắt buộc"
          maxLength={64}
          invalid={error !== null}
          aria-describedby={error === null ? undefined : errorId}
          aria-label={rowLabel("Mã vạch", l, i)}
        />
        {error === null ? null : (
          <span id={errorId} className="err-text" role="alert" style={{ flexBasis: "100%", whiteSpace: "normal" }}>
            {error}
          </span>
        )}
      </span>
    );
  };

  const swatches = (l: BuyLine, i: number) => <ColorSwatchGroup value={l.color} onChange={(color) => set(i, { ...l, color })} label={rowLabel("Màu sắc", l, i)} />;

  const newRow = (l: BuyLine, i: number) => {
    const base = units.name(l.baseUnitId);
    const big = l.bigUnitId !== null && l.bigUnitId !== l.baseUnitId ? l.bigUnitId : null;
    const unitName = l.inBig && big !== null ? units.name(big) : base;
    const factor = l.inBig && big !== null ? dec(l.factor ?? 0) : dec(1);
    return (
      <tr key={l.key} className="bp-new">
        <td style={{ minWidth: 260 }}>
          <div className="bp-name">
            <Input value={l.name} onChange={(e) => set(i, { ...l, name: e.target.value })} placeholder="Tên hàng hóa mới" aria-label="Tên hàng mới" autoFocus={l.key === created} />
            <span className="tag">Hàng mới</span>
          </div>
          <div className="bp-units">
            <span>
              ĐV cơ sở
              <select
                className="in"
                style={{ width: 88 }}
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
            </span>
            <span>
              Quy đổi 1
              <select
                className="in"
                style={{ width: 88 }}
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
              =
              <QtyInput size="sm" style={{ width: 56 }} value={l.factor} onChange={(factor) => set(i, { ...l, factor })} placeholder="hệ số" aria-label={rowLabel("Hệ số quy đổi", l, i)} />
              {base}
            </span>
            {barcodeInput(l, i)}
          </div>
          <div style={{ marginTop: 6, fontSize: 12 }}>
            <Checkbox label="Quản lý lô/hạn" aria-label={rowLabel("Quản lý lô/hạn", l, i)} checked={l.trackLot} onChange={(e) => set(i, { ...l, trackLot: e.target.checked })} />
          </div>
          {swatches(l, i)}
        </td>
        <td>
          <select className="in" style={{ minWidth: 96 }} value={l.inBig && big !== null ? "big" : "base"} onChange={(e) => set(i, { ...l, inBig: e.target.value === "big" })} aria-label={rowLabel("Đơn vị tính", l, i)}>
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
        <td className="num amt">{money(lineAmount(l))}</td>
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
                onCreate={(name) => {
                  const line = { ...newProductLine(units.idByName), name, color: l.color };
                  setCreated(line.key);
                  set(i, line);
                }}
                createLabel={(name) => `+ Tạo hàng mới “${name}”`}
                placeholder={SKU_PLACEHOLDER}
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
        <td className="num amt">{money(lineAmount(l))}</td>
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
      <PhonePairingCard />
      <div ref={table} className="tw flat bp">
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
          <Button size="sm" onClick={() => onChange([...lines, emptyBuyLine()])}>
            + Hàng có sẵn
          </Button>
          <Button size="sm" onClick={() => onChange([...lines, newProductLine(units.idByName)])}>
            + Hàng hóa mới
          </Button>
        </ButtonRow>
      )}
      <p className="hint">{BUY_HINT}</p>
    </>
  );
}
