import type { ReactNode } from "react";
import { colorLabel, type ColorKey } from "../../../../shared/domain/color";
import { Button, ButtonRow } from "../../../../shared/ui/button";
import { ACTIONS_HEADER } from "../../../../shared/ui/data-table/data-table";
import { TableCaption } from "../../../../shared/ui/data-table/table-caption";
import { toast } from "../../../../shared/ui/toast-store";
import type { ColorMaps } from "../../api/queries";
import type { MixGroup } from "../../schemas";
import { MixPackRow } from "../mix-pack-row";
import { newItemLine, newMixLine, pickColor, type LineDraft } from "./draft";
import { ItemLineRow } from "./item-line-row";
import { shortName } from "./row-label";
import type { LinePricing } from "./use-line-pricing";

export interface LineItemsEditorProps {
  /** "sell": invoices and exchanges; "quote": quotes (Quy cách and retail price columns). */
  preset: "sell" | "quote";
  lines: readonly LineDraft[];
  onChange: (lines: LineDraft[]) => void;
  pricing: LinePricing;
  retail?: LinePricing | undefined;
  /** Active mix groups offered by "+ Thêm thùng mix vị". */
  groups: readonly MixGroup[];
  colors: ColorMaps;
  unitName: (id: string) => string;
  /** Text of the add-line button ("+ Thêm hàng khác" in an exchange). */
  addLabel?: string;
  /** Offer "+ Thêm thùng mix vị" (an exchange takes back single items only). */
  allowMix?: boolean;
  /** Shown instead of the table while there is no line. */
  empty?: ReactNode;
}

/** Prototype `lineEditor` / `quoteEditor`: the editable line table of a sales document. */
export function LineItemsEditor({ preset, lines, onChange, pricing, retail, groups, colors, unitName, addLabel = "+ Thêm sản phẩm", allowMix = true, empty }: LineItemsEditorProps) {
  const update = (key: string, next: LineDraft) => onChange(lines.map((l) => (l.key === key ? next : l)));
  const remove = (key: string) => onChange(lines.filter((l) => l.key !== key));
  const choose = (key: string, color: ColorKey) => {
    const r = pickColor(lines, key, color);
    onChange(r.lines);
    if (r.mergedInto) toast.success(`Đã gộp vào dòng ${shortName(r.mergedInto)} ${colorLabel(color)}.`);
  };
  const firstGroup = groups[0];

  const table = (
    <div className="tw flat">
      <table className="t">
        <TableCaption>Dòng hàng</TableCaption>
        <thead>
          {preset === "sell" ? (
            <tr>
              <th>Tên sản phẩm</th>
              <th className="num">Giá</th>
              <th className="num">Chiết khấu</th>
              <th>Đơn vị tính</th>
              <th className="num">Số lượng</th>
              <th className="num">Thành tiền</th>
              <th aria-label={ACTIONS_HEADER} />
            </tr>
          ) : (
            <tr>
              <th>Tên sản phẩm</th>
              <th>Đơn vị tính</th>
              <th className="num">Số lượng</th>
              <th>Quy cách</th>
              <th className="num">Đơn giá</th>
              <th className="num">CK</th>
              <th className="num">Thành tiền</th>
              <th className="num">Giá bán lẻ</th>
              <th aria-label={ACTIONS_HEADER} />
            </tr>
          )}
        </thead>
        <tbody>
          {lines.map((l, index) =>
            l.kind === "item" ? (
              <ItemLineRow
                key={l.key}
                preset={preset}
                line={l}
                index={index}
                pricing={pricing}
                retail={retail}
                colors={colors}
                onChange={(next) => update(l.key, next)}
                onPickColor={(c) => choose(l.key, c)}
                onRemove={() => remove(l.key)}
              />
            ) : (
              <MixPackRow key={l.key} preset={preset} line={l} index={index} groups={groups} pricing={pricing} unitName={unitName} onChange={(next) => update(l.key, next)} onRemove={() => remove(l.key)} />
            ),
          )}
        </tbody>
      </table>
    </div>
  );

  return (
    <>
      {empty !== undefined && lines.length === 0 ? empty : table}
      <ButtonRow>
        <Button onClick={() => onChange([...lines, newItemLine()])}>{addLabel}</Button>
        {!allowMix ? null : firstGroup ? (
          <Button onClick={() => onChange([...lines, newMixLine(firstGroup)])}>+ Thêm thùng mix vị</Button>
        ) : (
          <span className="hint">Chưa có nhóm mix vị nào đang dùng. Khai báo tại Danh mục › Nhóm mix vị.</span>
        )}
      </ButtonRow>
      {preset === "quote" ? <p className="hint">Đơn giá là giá báo cho khách; cột Giá bán lẻ là giá niêm yết để so sánh. Quy cách là ghi chú tự do.</p> : null}
    </>
  );
}
