import { Link } from "react-router";
import { isColorCode } from "../../../shared/domain/color";
import type { Money } from "../../../shared/domain/money";
import { dec } from "../../../shared/lib/decimal";
import { num, vnd } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { ColorSwatchGroup } from "../../../shared/ui/color/color-swatch-group";
import { Combobox } from "../../../shared/ui/combobox";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { Field } from "../../../shared/ui/field";
import { LinkButton } from "../../../shared/ui/link-button";
import { MoneyInput } from "../../../shared/ui/money-input";
import { QtyInput } from "../../../shared/ui/qty-input";
import { baseUnitName, type ColorDirectory, type SkuListItem, type SkuPicker } from "../../catalog";
import type { ComponentCost, CostView, KitType, MixGroup } from "../schemas";
import { componentKind, componentPosition, emptyComponent, emptyMix, stockColors, type ComponentDraft, type LineDraft } from "../view-model";
import { MixLineRow } from "./mix-line-row";

export interface RecipeEditorProps {
  type: KitType;
  lines: readonly LineDraft[];
  onChange: (lines: LineDraft[]) => void;
  /** Every mix group; only the active ones can be added, a stopped one stays on its saved line. */
  groups: readonly MixGroup[];
  /** Economics of each draft line from the last preview. */
  costs: ReadonlyMap<string, ComponentCost[]>;
  /** Totals of the last preview. */
  totals: CostView | undefined;
  picker: SkuPicker;
  skuOf: (skuId: string) => SkuListItem | undefined;
  onPicked: (sku: SkuListItem) => void;
  unitName: (unitId: string) => string;
  colors: ColorDirectory;
  /** Kits show the packing cost field. */
  packing?: { value: Money | null; onChange: (v: Money | null) => void };
}

const COLUMN_COUNT = 7;

/** Goods and materials go into a recipe; kits and combos do not nest. */
const isComponentType = (type: string | undefined) => type === "goods" || type === "material";

/**
 * Prototype `compEditor`: the components of a recipe with their sale price, cost and stock; goods
 * and materials are picked by search, colored goods take a color, mix lines pick flavors by part.
 */
export function RecipeEditor({ type, lines, onChange, groups, costs, totals, picker, skuOf, onPicked, unitName, colors, packing }: RecipeEditorProps) {
  const active = groups.filter((g) => g.status === "active");
  const set = (key: string, next: LineDraft) => onChange(lines.map((l) => (l.key === key ? next : l)));
  const remove = (key: string) => onChange(lines.filter((l) => l.key !== key));

  const componentRow = (line: ComponentDraft, index: number) => {
    const sku = line.skuId ? skuOf(line.skuId) : undefined;
    const cost = costs.get(line.key)?.[0];
    const base = sku ? baseUnitName(sku) : "";
    const palette = stockColors(sku, colors);
    const color = colors.codeOf(line.colorId);
    const price = sku?.retail_price ?? cost?.unit_price ?? null;
    const amount = cost ? cost.sale_amount : price !== null && line.qty !== null ? dec(price).times(line.qty) : null;
    const which = componentPosition(index);
    return (
      <tr key={line.key}>
        <td style={{ minWidth: 260 }}>
          <Combobox
            source={picker.source}
            value={line.skuId}
            valueLabel={line.label}
            aria-label={which}
            filter={(item) => isComponentType(picker.picked(item.id)?.product_type)}
            onChange={(item) => {
              const picked = picker.picked(item.id);
              if (picked) onPicked(picked);
              set(line.key, { ...line, skuId: item.id, label: item.inputLabel ?? item.name, colorId: null, kind: picked ? componentKind(picked) : "goods" });
            }}
          />
          {palette.length > 0 ? (
            <ColorSwatchGroup
              value={isColorCode(color) ? color : null}
              colors={palette}
              onChange={(c) => set(line.key, { ...line, colorId: colors.idOf(c) })}
              label={`Màu ${which.toLowerCase()}`}
            />
          ) : null}
        </td>
        <td className="num">
          <QtyInput size="sm" value={line.qty} aria-label={`Số lượng ${which.toLowerCase()}`} onChange={(qty) => set(line.key, { ...line, qty })} />
        </td>
        <td className="num">
          {sku ? (
            price !== null && dec(price).gt(0) ? (
              <>
                {vnd(price)}
                <CellSub>/{base}</CellSub>
              </>
            ) : (
              <span className="hint">vật tư</span>
            )
          ) : (
            "—"
          )}
        </td>
        <td className="num">{amount === null ? "—" : vnd(amount)}</td>
        <td className="num">{cost ? vnd(cost.cost) : "—"}</td>
        <td className="num">{sku ? `${num(sku.sellable)} ${base}` : "—"}</td>
        <td>
          <LinkButton onClick={() => remove(line.key)} aria-label={`Xóa ${which.toLowerCase()}`}>
            Xóa
          </LinkButton>
        </td>
      </tr>
    );
  };

  return (
    <>
      <div className="tw flat">
        <table className="t">
          <caption className="sr-only">Thành phần</caption>
          <thead>
            <tr>
              <th scope="col">Thành phần</th>
              <th scope="col" className="num">
                SL / {type === "combo" ? "combo" : "giỏ"}
              </th>
              <th scope="col" className="num">
                Giá bán
              </th>
              <th scope="col" className="num">
                Thành tiền (giá bán)
              </th>
              <th scope="col" className="num">
                Giá vốn
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
            {lines.map((line, i) =>
              line.mix ? (
                <MixLineRow
                  key={line.key}
                  index={i}
                  line={line}
                  groups={groups}
                  costs={costs.get(line.key) ?? []}
                  skuOf={skuOf}
                  unitName={unitName}
                  columnCount={COLUMN_COUNT}
                  onChange={(next) => set(line.key, next)}
                  onRemove={() => remove(line.key)}
                />
              ) : (
                componentRow(line, i)
              ),
            )}
          </tbody>
          <tfoot>
            <tr>
              <td>Tổng</td>
              <td />
              <td />
              <td className="num">{vnd(totals?.components_sale ?? 0)}</td>
              <td className="num">{vnd(totals?.components_cost ?? 0)}</td>
              <td />
              <td />
            </tr>
          </tfoot>
        </table>
      </div>
      <ButtonRow>
        <Button onClick={() => onChange([...lines, emptyComponent()])}>+ Thêm thành phần</Button>
        {active[0] ? (
          <Button onClick={() => onChange([...lines, emptyMix(active[0] as MixGroup)])}>+ Thêm thùng mix vị</Button>
        ) : (
          <span className="hint">
            Chưa có nhóm mix vị nào đang dùng. Khai báo tại <Link to="/catalog/mix-groups">Danh mục › Nhóm mix vị</Link>.
          </span>
        )}
      </ButtonRow>
      {packing ? (
        <Field label="Chi phí đóng gói / giỏ (nhân công, màng co…)" hint="Được vốn hóa vào giá vốn giỏ khi đóng">
          <MoneyInput value={packing.value} onChange={packing.onChange} />
        </Field>
      ) : null}
    </>
  );
}
