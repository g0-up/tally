import { useId, useMemo } from "react";
import { dec } from "../../../shared/lib/decimal";
import { num, vnd } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { Combobox } from "../../../shared/ui/combobox";
import { Field } from "../../../shared/ui/field";
import { FormGrid } from "../../../shared/ui/form-grid";
import { Input } from "../../../shared/ui/input";
import { LinkButton } from "../../../shared/ui/link-button";
import { TableCaption } from "../../../shared/ui/data-table/table-caption";
import { baseUnitName } from "../api/adapters";
import { useUnits, type SkuPicker } from "../api/queries";
import type { MixErrors, MixFlavorDraft, MixGroupDraft } from "../mix-group-messages";
import type { SkuListItem } from "../schemas";

const LOCK_REASON = "Nhóm đã dùng trên chứng từ nên không đổi được đơn vị và quy cách thùng.";

export interface MixGroupFormFieldsProps {
  draft: MixGroupDraft;
  onChange: (patch: Partial<MixGroupDraft>) => void;
  errors: MixErrors;
  /** Documents use the group: the pack spec is fixed. */
  locked: boolean;
  /** Flavors already sold; they cannot be removed. */
  usedSkuIds: ReadonlySet<string>;
  picker: SkuPicker;
  /** List row of a flavor SKU (units, price, stock); undefined while unknown. */
  skuOf: (skuId: string | null) => SkuListItem | undefined;
  /** A flavor row picked a SKU (the dialog keeps its list row). */
  onPick: (index: number, sku: SkuListItem | undefined, skuId: string) => void;
  onAddRow: () => void;
}

/** Prototype mixModal body: name, pack spec, then the flavor table with "+ Thêm vị". */
export function MixGroupFormFields({ draft, onChange, errors, locked, usedSkuIds, picker, skuOf, onPick, onAddRow }: MixGroupFormFieldsProps) {
  const units = useUnits();
  const unitItems = useMemo(() => units.active.map((u) => ({ id: u.id, name: u.name })), [units.active]);
  const lockId = useId();
  // Read-only, not disabled: the fixed spec stays in the tab order and reads out why it is fixed.
  const lock = locked ? { readOnly: true, "aria-describedby": lockId } : {};

  const setItem = (index: number, patch: Partial<MixFlavorDraft>) => onChange({ items: draft.items.map((x, i) => (i === index ? { ...x, ...patch } : x)) });

  return (
    <>
      <FormGrid>
        <Field label="Tên nhóm mix" error={errors.name}>
          <Input value={draft.name} onChange={(e) => onChange({ name: e.target.value })} placeholder="VD: Nước ngọt lon 330ml" invalid={errors.name !== undefined} />
        </Field>
      </FormGrid>
      {locked ? (
        <p className="hint" id={lockId}>
          {LOCK_REASON}
        </p>
      ) : null}
      <FormGrid>
        <Field label="Đơn vị thùng" hint="Đơn vị khách đặt: thùng, két…" error={errors.units}>
          {locked ? (
            <Input value={units.name(draft.packUnitId)} aria-label="Đơn vị thùng" {...lock} />
          ) : (
            <Combobox items={unitItems} value={draft.packUnitId} onChange={(it) => onChange({ packUnitId: it.id })} aria-label="Đơn vị thùng" placeholder="Chọn đơn vị…" invalid={errors.units !== undefined} />
          )}
        </Field>
        <Field label="Số đơn vị lẻ / thùng" hint="VD: 48 hộp, 24 lon" error={errors.packF}>
          <Input inputMode="numeric" value={draft.packF} onChange={(e) => onChange({ packF: e.target.value })} invalid={errors.packF !== undefined} {...lock} />
        </Field>
        <Field label="Đơn vị chọn vị" hint="Đơn vị khách chọn từng vị: dây, lốc…">
          {locked ? (
            <Input value={units.name(draft.partUnitId)} aria-label="Đơn vị chọn vị" {...lock} />
          ) : (
            <Combobox items={unitItems} value={draft.partUnitId} onChange={(it) => onChange({ partUnitId: it.id })} aria-label="Đơn vị chọn vị" placeholder="Chọn đơn vị…" invalid={errors.units !== undefined} />
          )}
        </Field>
        <Field label="Số đơn vị lẻ / đơn vị chọn vị" hint="VD: 1 dây = 4 hộp" error={errors.partF}>
          <Input inputMode="numeric" value={draft.partF} onChange={(e) => onChange({ partF: e.target.value })} invalid={errors.partF !== undefined} {...lock} />
        </Field>
      </FormGrid>
      {errors.spec === undefined ? null : (
        <Callout tone="bad" role="alert">
          {errors.spec}
        </Callout>
      )}
      <div className="tw flat">
        <table className="t">
          <TableCaption>Các vị trong nhóm</TableCaption>
          <thead>
            <tr>
              <th scope="col">Mặt hàng (vị)</th>
              <th scope="col">Tên vị ngắn</th>
              <th scope="col" className="num">
                Đơn vị lẻ
              </th>
              <th scope="col" className="num">
                Giá lẻ
              </th>
              <th scope="col" className="num">
                Có thể bán
              </th>
              <th scope="col">
                <span className="sr-only">Thao tác</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {draft.items.map((x, i) => {
              const sku = skuOf(x.skuId);
              const n = i + 1;
              return (
                <tr key={x.key}>
                  <td style={{ minWidth: 280 }}>
                    <Combobox
                      source={picker.source}
                      value={x.skuId}
                      valueLabel={x.skuId === null ? "" : `${x.skuCode} · ${x.skuName}`}
                      onChange={(it) => onPick(i, picker.picked(it.id), it.id)}
                      aria-label={`Mặt hàng vị ${n}`}
                    />
                  </td>
                  <td>
                    <Input style={{ width: 150 }} value={x.label} onChange={(e) => setItem(i, { label: e.target.value })} placeholder="VD: Dâu" aria-label={`Tên vị ngắn ${x.skuCode || n}`} />
                  </td>
                  <td className="num">{sku ? baseUnitName(sku) : "—"}</td>
                  <td className="num">{sku?.retail_price ? vnd(sku.retail_price) : "—"}</td>
                  <td className="num">{sku ? num(sku.sellable) : "—"}</td>
                  <td className="acts">
                    {x.skuId !== null && usedSkuIds.has(x.skuId) ? (
                      <span className="hint">đã bán</span>
                    ) : (
                      <LinkButton aria-label={`Gỡ vị ${x.skuName || n}`} onClick={() => onChange({ items: draft.items.filter((_, j) => j !== i) })}>
                        Gỡ
                      </LinkButton>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      {errors.items === undefined ? null : (
        <Callout tone="bad" role="alert">
          {errors.items}
        </Callout>
      )}
      <ButtonRow>
        <Button onClick={onAddRow}>+ Thêm vị</Button>
      </ButtonRow>
    </>
  );
}

export interface MixGroupLiveProps {
  draft: MixGroupDraft;
  packName: string;
  partName: string;
  skuOf: (skuId: string | null) => SkuListItem | undefined;
}

/** Prototype mixModal live messages: the pack arithmetic, mixed piece units, differing prices. */
export function MixGroupLive({ draft, packName, partName, skuOf }: MixGroupLiveProps) {
  const skus = draft.items.flatMap((x) => {
    const s = skuOf(x.skuId);
    return s ? [s] : [];
  });
  const bases = [...new Set(skus.map(baseUnitName))];
  const prices = new Set(skus.map((s) => (s.retail_price === null ? "" : dec(s.retail_price).toString())));
  const pf = Number(draft.packF);
  const qf = Number(draft.partF);
  return (
    <>
      {pf > 0 && qf > 0 ? (
        pf % qf !== 0 ? (
          <Callout tone="bad">
            {num(pf)} không chia hết cho {num(qf)}: 1 {packName} không ra số {partName} tròn.
          </Callout>
        ) : (
          <span className="hint">
            1 {packName} ={" "}
            <b>
              {num(pf / qf)} {partName}
            </b>{" "}
            = {num(pf)} {bases[0] ?? "đơn vị lẻ"}. Khách chọn tối đa {num(pf / qf)} {partName} mỗi {packName}.
          </span>
        )
      ) : null}
      {bases.length > 1 ? <Callout tone="bad">Các vị đang khác đơn vị lẻ ({bases.join(", ")}). Không lưu được.</Callout> : null}
      {prices.size > 1 ? (
        <Callout>Các vị có giá lẻ khác nhau. Trên báo giá/hóa đơn, thùng mix tính một giá/thùng (mặc định = giá bình quân); trong giỏ quà, giá tính theo từng vị.</Callout>
      ) : null}
    </>
  );
}
