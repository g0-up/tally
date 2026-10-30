import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import { dec } from "../../../shared/lib/decimal";
import { num, vnd } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { Checkbox } from "../../../shared/ui/checkbox";
import { Combobox } from "../../../shared/ui/combobox";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { ImpactSummary, type ImpactItem } from "../../../shared/ui/impact-summary";
import { LinkButton } from "../../../shared/ui/link-button";
import { MoneyInput } from "../../../shared/ui/money-input";
import { QtyInput } from "../../../shared/ui/qty-input";
import { toast } from "../../../shared/ui/toast-store";
import { createUnitErrorMessage, useCreateUnit } from "../api/mutations";
import { useUnits } from "../api/queries";
import {
  autoPrice,
  MAX_UNIT_ROWS,
  ownPrice,
  purchaseRowAfterSave,
  UNIT_LOCK_LABELS,
  unitPriceBelowBase,
  unitRow,
  type ProductErrors,
  type SavedUnits,
  type UnitRow,
  type UnitTable,
} from "./product-draft";

const BASE_FALLBACK = "ĐV cơ sở";

export interface UnitPriceTableProps {
  value: UnitTable;
  onChange: (patch: Partial<UnitTable>) => void;
  /** Keys `base_unit_id`, `retail_price`, `wholesale_price` and `units.<i>.unit|factor|retail|wholesale`. */
  errors: ProductErrors;
  /** The base unit is chosen outside the table (the purchase-order line): shown, not picked. */
  baseUnitName?: string;
  /** Unit the purchase-order line counts in (null = the base unit); it becomes the default purchase unit. */
  purchaseUnitId?: string | null;
  /** The purchase-order line picks its unit here: each row gets a "Nhập theo" choice. */
  onPurchaseUnitChange?: (unitId: string | null) => void;
  /**
   * Editing a saved SKU ("Đơn vị & giá"): a locked unit offers "Ngừng dùng" instead of removal,
   * and the hints say what a factor change or deactivation does to documents, scans and purchases.
   */
  saved?: SavedUnits;
}

interface Created {
  id: string;
  name: string;
}

/**
 * Prototype `uomTable`: the base unit with its retail and wholesale prices, then the conversion
 * units, each with its factor and own prices. A blank price shows base price × factor as its
 * placeholder and stays blank (sold at that computed price). A unit typed that is not in the
 * directory is added to it when picked ("+ Tạo mới …").
 */
export function UnitPriceTable({ value, onChange, errors, baseUnitName, purchaseUnitId = null, onPurchaseUnitChange, saved }: UnitPriceTableProps) {
  const units = useUnits();
  const createUnit = useCreateUnit();
  const id = useId();
  const addRef = useRef<HTMLButtonElement>(null);
  const [creating, setCreating] = useState<ReadonlySet<string>>(() => new Set());
  const [createErrors, setCreateErrors] = useState<Readonly<Record<string, string>>>({});
  /** Row whose unit field takes focus once it is drawn ("+ Thêm đơn vị"). */
  const focusRow = useRef<string | null>(null);
  // A unit added to the directory answers later; it is applied to the table as it is then.
  const latest = useRef({ value, onChange });
  useLayoutEffect(() => {
    latest.current = { value, onChange };
  });

  const items = useMemo(() => units.active.map((u) => ({ id: u.id, name: u.name })), [units.active]);
  const base = (baseUnitName ?? units.name(value.baseUnitId)).trim() || BASE_FALLBACK;
  const fieldId = (key: string) => `${id}-${key.replace(/\./g, "-")}`;
  const errorId = (key: string) => `${fieldId(key)}-e`;
  const errorOf = (key: string, own?: string) => errors[key] ?? own;
  const err = (key: string, own?: string) => {
    const text = errorOf(key, own);
    return text ? (
      <span className="ut-err" id={errorId(key)}>
        {text}
      </span>
    ) : null;
  };
  const described = (key: string, own?: string) => (errorOf(key, own) ? { invalid: true, "aria-describedby": errorId(key) } : {});
  // Row errors are keyed by the row's index when they were found: a row removed or added since then
  // leaves every error on the row it was found on, and a removed row's errors go with it.
  const [found, setFound] = useState({ errors, keys: value.units.map((u) => u.key) });
  if (found.errors !== errors) setFound({ errors, keys: value.units.map((u) => u.key) });
  const rowErrorPrefix = (key: string) => {
    const i = found.keys.indexOf(key);
    return i < 0 ? `units.${key}.` : `units.${i}.`;
  };

  useEffect(() => {
    if (focusRow.current === null) return;
    document.getElementById(fieldId(`row.${focusRow.current}`))?.focus();
    focusRow.current = null;
  });

  const patchRow = (key: string, patch: Partial<UnitRow>) => onChange({ units: value.units.map((u) => (u.key === key ? { ...u, ...patch } : u)) });

  /** Picks the unit with this name, adding it to the directory first when it is not there. */
  const pickByName = (slot: string, name: string, pending: () => void, apply: (unit: Created) => void) => {
    const existing = units.idByName(name);
    if (existing !== null) {
      apply({ id: existing, name: units.name(existing) });
      return;
    }
    pending();
    setCreating((s) => new Set(s).add(slot));
    setCreateErrors(({ [slot]: _, ...rest }) => rest);
    createUnit
      .mutateAsync(name)
      .then(apply, (e: unknown) => setCreateErrors((m) => ({ ...m, [slot]: createUnitErrorMessage(e, name) })))
      .finally(() =>
        setCreating((s) => {
          const next = new Set(s);
          next.delete(slot);
          return next;
        }),
      );
  };

  const createRowUnit = (key: string, name: string) =>
    pickByName(
      key,
      name,
      () => patchRow(key, { unitId: null, unitName: name }),
      (unit) => {
        const t = latest.current.value;
        // Applied only while the row still waits for this unit, not after another unit was picked.
        if (!t.units.some((u) => u.key === key && u.unitId === null && u.unitName === name)) return;
        latest.current.onChange({ units: t.units.map((u) => (u.key === key ? { ...u, unitId: unit.id, unitName: unit.name } : u)) });
      },
    );

  const createBaseUnit = (name: string) =>
    pickByName(
      "base",
      name,
      () => onChange({ baseUnitId: null }),
      (unit) => latest.current.onChange({ baseUnitId: unit.id }),
    );

  const full = value.units.length >= MAX_UNIT_ROWS;
  const add = () => {
    const row = unitRow();
    focusRow.current = row.key;
    onChange({ units: [...value.units, row] });
  };

  const remove = (row: UnitRow) => {
    onChange({ units: value.units.filter((u) => u.key !== row.key) });
    setCreateErrors(({ [row.key]: _, ...rest }) => rest);
    toast.success(
      <>
        Đã bỏ đơn vị <b>{row.unitName.trim() || "mới"}</b> khỏi bảng.
      </>,
    );
    addRef.current?.focus();
  };

  const low = unitPriceBelowBase(value);
  const purchaseRow = saved ? purchaseRowAfterSave(value, saved.purchaseUnitId) : undefined;
  const isPurchaseRow = (row: UnitRow) => (saved ? row === purchaseRow : purchaseUnitId !== null && row.unitId === purchaseUnitId);

  /** "Nhập theo": the unit the purchase-order line counts in; a row is offered once its unit is picked. */
  const buyChoice = (unitId: string | null, name: string, isBase = false) =>
    onPurchaseUnitChange ? (
      <td data-l="Nhập theo">
        <label className="ut-buy">
          <input
            type="radio"
            name={fieldId("purchase")}
            checked={isBase ? purchaseUnitId === null : unitId !== null && unitId === purchaseUnitId}
            disabled={!isBase && unitId === null}
            onChange={() => onPurchaseUnitChange(unitId)}
            aria-label={`Nhập theo ${name}`}
          />
          <span aria-hidden="true">Nhập theo</span>
        </label>
      </td>
    ) : null;

  const baseRow = (
    <tr>
      <td data-l="Đơn vị">
        {baseUnitName !== undefined ? (
          <b>{base}</b>
        ) : (
          <>
            <Combobox
              id={fieldId("base_unit_id")}
              items={items}
              value={value.baseUnitId}
              onChange={(it) => onChange({ baseUnitId: it.id })}
              onCreate={createBaseUnit}
              aria-label="Đơn vị cơ sở"
              placeholder="Chọn đơn vị…"
              {...described("base_unit_id", createErrors.base)}
            />
            {err("base_unit_id", createErrors.base)}
            {creating.has("base") ? <span className="hint">Đơn vị mới, sẽ thêm vào danh mục đơn vị.</span> : null}
          </>
        )}
      </td>
      <td data-l="Quy đổi">
        <span className="ut-mid">
          <span className="tag alt">Đơn vị cơ sở</span>
        </span>
      </td>
      <td data-l="Giá bán lẻ">
        <MoneyInput className="ut-n" value={value.retailPrice} onChange={(retailPrice) => onChange({ retailPrice })} aria-label={`Giá bán lẻ / ${base}`} {...described("retail_price")} />
        {err("retail_price")}
      </td>
      <td data-l="Giá sỉ">
        <MoneyInput className="ut-n" value={value.wholesalePrice} onChange={(wholesalePrice) => onChange({ wholesalePrice })} aria-label={`Giá sỉ / ${base}`} {...described("wholesale_price")} />
        {err("wholesale_price")}
      </td>
      {buyChoice(null, base, true)}
      <td className="acts" />
    </tr>
  );

  const rows = value.units.map((row, i) => {
    const at = rowErrorPrefix(row.key);
    const name = row.unitName.trim() || `đơn vị ${i + 2}`;
    const pending = creating.has(row.key);
    const was = saved && row.unitId !== null ? saved.units.get(row.unitId) : undefined;
    const lock = was?.lockedBy ?? null;
    const off = row.active === false;
    const refactored = was !== undefined && row.factor !== null && dec(row.factor).gt(0) && !dec(row.factor).eq(was.factor);
    return (
      <tr key={row.key} className={off ? "ut-dim" : undefined}>
        <td data-l="Đơn vị">
          {lock !== null ? (
            <>
              <b>{name}</b>
              <CellSub>{UNIT_LOCK_LABELS[lock]}. Không xóa được, chọn Ngừng dùng.</CellSub>
              {err(`${at}unit`)}
            </>
          ) : (
            <>
              <Combobox
                id={fieldId(`row.${row.key}`)}
                items={items}
                value={row.unitId}
                onChange={(it) => patchRow(row.key, { unitId: it.id, unitName: it.name })}
                onCreate={(text) => createRowUnit(row.key, text)}
                aria-label={`Tên đơn vị ${i + 2}`}
                placeholder={pending ? row.unitName : "VD: thùng"}
                {...described(`${at}unit`, createErrors[row.key])}
              />
              {err(`${at}unit`, createErrors[row.key])}
              {pending ? <span className="hint">Đơn vị mới, sẽ thêm vào danh mục đơn vị.</span> : null}
            </>
          )}
        </td>
        <td data-l="Quy đổi">
          <div className="ut-f">
            <span aria-hidden="true">=</span>
            <QtyInput className="ut-n" value={row.factor} onChange={(factor) => patchRow(row.key, { factor })} aria-label={`Quy đổi: 1 ${name} bằng bao nhiêu ${base}`} {...described(`${at}factor`)} />
            <span>{base}</span>
          </div>
          {err(`${at}factor`)}
          {refactored && lock === "documents" ? <span className="hint">Chứng từ cũ giữ hệ số cũ ({num(was.factor)}).</span> : null}
          {refactored && was.barcode && !off ? <span className="hint">Kiểm kê quét mã này sẽ cộng theo hệ số mới.</span> : null}
          {isPurchaseRow(row) && !onPurchaseUnitChange ? <span className="hint">Đơn vị nhập mặc định.</span> : null}
          {saved && off && row.unitId === saved.purchaseUnitId ? (
            <span className="hint">Đơn vị nhập mặc định chuyển sang {purchaseRow?.unitName.trim() || base}.</span>
          ) : null}
        </td>
        <td data-l="Giá bán lẻ">
          <MoneyInput
            className="ut-n"
            value={row.retail}
            onChange={(retail) => patchRow(row.key, { retail })}
            aria-label={`Giá bán lẻ / ${name}`}
            placeholder={`${num(autoPrice(value.retailPrice, row.factor))} tự tính`}
            {...described(`${at}retail`)}
          />
          {err(`${at}retail`)}
        </td>
        <td data-l="Giá sỉ">
          <MoneyInput
            className="ut-n"
            value={row.wholesale}
            onChange={(wholesale) => patchRow(row.key, { wholesale })}
            aria-label={`Giá sỉ / ${name}`}
            placeholder={`${num(autoPrice(value.wholesalePrice, row.factor))} tự tính`}
            {...described(`${at}wholesale`)}
          />
          {err(`${at}wholesale`)}
        </td>
        {buyChoice(row.unitId, name)}
        <td className="acts">
          {was !== undefined && (lock !== null || !was.active) ? (
            <>
              <Checkbox
                className="ut-off"
                label="Ngừng dùng"
                aria-label={`Ngừng dùng ${name}`}
                checked={off}
                onChange={(e) => patchRow(row.key, { active: !e.target.checked })}
                {...(lock !== null && errorOf(`${at}unit`) ? { "aria-invalid": true, "aria-describedby": errorId(`${at}unit`) } : {})}
              />
              {off && was.barcode ? <span className="hint">Mã vạch của đơn vị này sẽ không quét được ở bán hàng và kiểm kê.</span> : null}
            </>
          ) : null}
          {lock === null ? (
            <LinkButton aria-label={`Xóa đơn vị ${name}`} onClick={() => remove(row)}>
              Xóa
            </LinkButton>
          ) : null}
        </td>
      </tr>
    );
  });

  return (
    <div className="ut" role="group" aria-labelledby={fieldId("title")}>
      <div className="ut-h">
        <b id={fieldId("title")}>Đơn vị tính &amp; giá bán</b>
        <span className="hint">Tồn kho lưu theo đơn vị cơ sở ({base}). Nhập theo đơn vị nào, chứng từ ghi đúng đơn vị đó và tự quy đổi. Bỏ trống giá để tự tính theo hệ số.</span>
      </div>
      <div className="tw flat">
        <table className="t ut-t" aria-labelledby={fieldId("title")}>
          <thead>
            <tr>
              <th>Đơn vị</th>
              <th>Quy đổi</th>
              <th className="num">Giá bán lẻ (₫)</th>
              <th className="num">Giá sỉ (₫)</th>
              {onPurchaseUnitChange ? <th>Nhập theo</th> : null}
              <th>
                <span className="sr-only">Thao tác</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {baseRow}
            {rows}
          </tbody>
        </table>
      </div>
      {low.length > 0 ? (
        <Callout tone="warn" role="status">
          {low.map((u, i) => (
            <span key={u.key}>
              {i > 0 ? "; " : null}Giá lẻ 1 <b>{u.unitName}</b> ({vnd(u.retail)}) thấp hơn giá 1 <b>{base}</b> ({vnd(value.retailPrice)})
            </span>
          ))}
          . Kiểm tra lại: có thể đang nhập giá của 1 {base}. Vẫn lưu được nếu đúng ý.
        </Callout>
      ) : null}
      <ButtonRow>
        <Button ref={addRef} size="sm" onClick={add} disabled={full} {...(full ? { "aria-describedby": fieldId("units-full") } : {})}>
          + Thêm đơn vị
        </Button>
        {full ? (
          <span className="hint" id={fieldId("units-full")}>
            Đã đủ {MAX_UNIT_ROWS} đơn vị, tối đa cho một hàng hóa.
          </span>
        ) : null}
        {err("units")}
      </ButtonRow>
    </div>
  );
}

/** Prototype `utPreview`: the price each active unit sells at, updated while typing (the dialog's live region). */
export function UnitPricePreview({ value, baseUnitName }: { value: UnitTable; baseUnitName?: string }) {
  const units = useUnits();
  const base = (baseUnitName ?? units.name(value.baseUnitId)).trim() || BASE_FALLBACK;
  const items: ImpactItem[] = [{ label: `1 ${base}`, value: `${vnd(value.retailPrice)} · sỉ ${vnd(value.wholesalePrice)}` }];
  for (const u of value.units) {
    const name = u.unitName.trim();
    if (u.active === false || name === "" || u.factor === null || !dec(u.factor).gt(0)) continue;
    const retail = ownPrice(u.retail);
    const wholesale = ownPrice(u.wholesale);
    items.push({
      label: `1 ${name} = ${num(u.factor)} ${base}`,
      value: (
        <>
          {vnd(retail ?? autoPrice(value.retailPrice, u.factor))} · sỉ {vnd(wholesale ?? autoPrice(value.wholesalePrice, u.factor))}
          {retail !== null && wholesale !== null ? null : <span className="hint"> (có phần tự tính)</span>}
        </>
      ),
    });
  }
  return <ImpactSummary items={items} />;
}
