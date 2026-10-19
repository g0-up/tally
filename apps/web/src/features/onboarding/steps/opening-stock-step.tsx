import { useEffect, useMemo, useRef, useState } from "react";
import { COLORS, NO_COLOR_LABEL, colorLabel, isColorCode, type ColorCode } from "../../../shared/domain/color";
import type { Money } from "../../../shared/domain/money";
import type { Qty } from "../../../shared/domain/qty";
import { dec } from "../../../shared/lib/decimal";
import { fd, num, vnd } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Combobox } from "../../../shared/ui/combobox";
import { CellText } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { TableCaption } from "../../../shared/ui/data-table/table-caption";
import { DatePicker } from "../../../shared/ui/date-picker";
import { Input } from "../../../shared/ui/input";
import { LinkButton } from "../../../shared/ui/link-button";
import { ListLoading } from "../../../shared/ui/list-footer";
import { MoneyInput } from "../../../shared/ui/money-input";
import { QtyInput } from "../../../shared/ui/qty-input";
import { toast } from "../../../shared/ui/toast-store";
import { ScreenError } from "../../../app/route-fallbacks";
import { QuickProductDialog, useColorDirectory, useSkuList, useSkuPicker, useSkusByIds, type SkuListItem } from "../../catalog";
import { usePostOpeningStock, type OpeningStockLine } from "../api/mutations";
import { ACTIVE_SKUS, useOpeningStockLines } from "../api/queries";
import { CutoffGate, LockedCallout, NoneButton, OnbEmpty } from "../components/opening-gate";
import { useOnbFail } from "../hooks/use-onb-fail";
import { OnbErr } from "../components/onb-field";
import { errRef, withoutError, type FieldErrors } from "../components/field-errors";
import { useOnboardingScreen } from "../onboarding-context";
import type { OpeningStockDocLine } from "../schemas";

interface LineDraft {
  uid: number;
  sku: SkuListItem | null;
  color: ColorCode | null;
  qty: Qty | null;
  cost: Money | null;
  lot: string;
  exp: string | null;
}

let nextUid = 1;
const emptyLine = (): LineDraft => ({ uid: nextUid++, sku: null, color: null, qty: null, cost: null, lot: "", exp: null });

/** Prototype `obLineAmt`: quantity times cost, 0 until both are numbers. */
const lineAmount = (l: Pick<LineDraft, "qty" | "cost">) => (l.qty !== null && l.cost !== null ? dec(l.qty).times(l.cost) : dec(0));

const isBlank = (l: LineDraft) => l.sku === null && l.qty === null && l.cost === null;

/** Error key and control id of a line's input (prototype `stock.lines.{i}.{field}`). */
const key = (i: number, field: string) => `lines.${i}.${field}`;
const ctlId = (i: number, field: string) => `ob-stock-lines-${i}-${field}`;

/** The API's line fields, for the errors of a refused posting. */
const API_FIELDS: Readonly<Record<string, string>> = { sku_id: "sku", color_id: "color", qty: "qty", unit_cost: "cost", lot_no: "lot", expiry_date: "exp" };

/** Prototype `A.onbPostStock` checks: blank lines are left out, the others must be complete. */
function validate(lines: readonly LineDraft[]): FieldErrors {
  const filled = lines.map((l, i) => ({ l, i })).filter(({ l }) => !isBlank(l));
  if (filled.length === 0) return { [key(0, "sku")]: "Chọn hàng cho ít nhất một dòng." };
  const e: Record<string, string> = {};
  for (const { l, i } of filled) {
    if (l.sku === null) e[key(i, "sku")] = "Chọn hàng.";
    if (l.qty === null || !dec(l.qty).gt(0)) e[key(i, "qty")] = "Số lượng phải lớn hơn 0.";
    if (l.cost === null || dec(l.cost).lt(0)) e[key(i, "cost")] = "Nhập giá vốn (0 nếu hàng tặng).";
    if (l.sku?.track_lot && l.lot.trim() === "") e[key(i, "lot")] = "Nhập số lô.";
    if (l.sku?.track_expiry && l.exp === null) e[key(i, "exp")] = "Chọn hạn dùng.";
  }
  return e;
}

/** Prototype `OB_BODY.opening_stock`: lines posted as one opening adjustment dated on the cutoff. */
export function OpeningStockStep() {
  const { cutoff, confirmed, goTo } = useOnboardingScreen();
  const posted = useOpeningStockLines();
  const postedView = posted.isError ? <ScreenError error={posted.error} onRetry={() => void posted.refetch()} /> : posted.data ? <PostedStock lines={posted.data.lines} more={posted.data.more} /> : null;
  if (confirmed) {
    return (
      <>
        <LockedCallout cutoff={cutoff} />
        {postedView}
      </>
    );
  }
  if (cutoff === null) return <CutoffGate onOpenProfile={() => goTo("store_profile")} />;
  return (
    <>
      <StockEntry cutoff={cutoff} noneShown={posted.data?.lines.length === 0} />
      {postedView}
    </>
  );
}

function StockEntry({ cutoff, noneShown }: { cutoff: string; noneShown: boolean }) {
  const { cta } = useOnboardingScreen();
  const skus = useSkuList(ACTIVE_SKUS);
  const picker = useSkuPicker(null);
  const colors = useColorDirectory();
  const [lines, setLines] = useState<LineDraft[]>(() => [emptyLine()]);
  const [errors, setErrors] = useState<FieldErrors>({});
  const fail = useOnbFail(setErrors);
  const [adding, setAdding] = useState(false);
  const [newSkuId, setNewSkuId] = useState<string | null>(null);
  const focusLine = useRef<number | null>(null);
  /** Draft index of each line sent, for the errors of a refused posting. */
  const sent = useRef<number[]>([]);

  // The API completes the step with the first opening posting.
  const post = usePostOpeningStock({
    setFieldError: (field, message) => {
      const m = /^lines[.[](\d+)\]?\.(\w+)$/.exec(field);
      const at = m ? sent.current[Number(m[1])] : undefined;
      const f = m ? API_FIELDS[m[2] ?? ""] : undefined;
      if (at !== undefined && f) setErrors((e) => ({ ...e, [key(at, f)]: message }));
    },
    after: () => {
      setLines([emptyLine()]);
      setErrors({});
    },
  });

  // A product added from this step goes on the first empty line once the API lists it.
  const newIds = useMemo(() => (newSkuId ? [newSkuId] : []), [newSkuId]);
  const byId = useSkusByIds(newIds).data;
  const created = newSkuId ? byId?.get(newSkuId) : undefined;
  if (created) {
    // Adjusted while rendering, not in an effect: the line shows the product in the same paint.
    setNewSkuId(null);
    const pick = (l: LineDraft): LineDraft => ({ ...l, sku: created, color: null, lot: "", exp: null, cost: l.cost ?? created.ref_cost ?? created.last_purchase_price });
    const at = lines.findIndex((l) => l.sku === null);
    setLines(at >= 0 ? lines.map((l, i) => (i === at ? pick(l) : l)) : [...lines, pick(emptyLine())]);
  }

  useEffect(() => {
    if (focusLine.current === null) return;
    document.getElementById(ctlId(focusLine.current, "sku"))?.focus();
    focusLine.current = null;
  }, [lines.length]);

  const update = (i: number, field: string, patch: Partial<LineDraft>) => {
    setLines((ls) => ls.map((l, j) => (j === i ? { ...l, ...patch } : l)));
    setErrors((e) => withoutError(e, key(i, field)));
  };
  const addLine = () => {
    focusLine.current = lines.length;
    setLines((ls) => [...ls, emptyLine()]);
  };
  const removeLine = (i: number) => {
    setLines((ls) => ls.filter((_, j) => j !== i));
    setErrors({});
    toast.success(`Đã xóa dòng ${i + 1}.`);
  };

  const submit = () => {
    const found = validate(lines);
    if (Object.keys(found).length > 0) return fail(found);
    setErrors({});
    const body: OpeningStockLine[] = [];
    sent.current = [];
    lines.forEach((l, i) => {
      if (l.sku === null || l.qty === null || l.cost === null) return;
      const colorId = colors.idOf(l.color);
      sent.current.push(i);
      body.push({
        sku_id: l.sku.id,
        qty: l.qty,
        unit_cost: l.cost,
        ...(colorId ? { color_id: colorId } : {}),
        ...(l.sku.track_lot ? { lot_no: l.lot.trim() } : {}),
        ...(l.sku.track_expiry && l.exp !== null ? { expiry_date: l.exp } : {}),
      });
    });
    void post.run({ businessDate: cutoff, lines: body });
  };

  const dialog = adding ? <QuickProductDialog onClose={() => setAdding(false)} onCreated={(r) => setNewSkuId(r.product.skus[0]?.id ?? null)} /> : null;
  if (skus.isLoading) return <ListLoading />;
  if (skus.error && skus.items.length === 0) return <ScreenError error={skus.error} onRetry={() => void skus.refetch()} />;
  if (skus.items.length === 0) {
    return (
      <>
        <OnbEmpty
          title="Chưa có hàng hóa để nhập tồn"
          text="Tồn kho đầu kỳ cần hàng hóa trong danh mục. Thêm mặt hàng ngay tại đây hoặc ở bước Danh mục."
          actions={
            <>
              <Button variant={cta} onClick={() => setAdding(true)}>
                + Thêm hàng hóa
              </Button>
              {noneShown ? <NoneButton step="opening_stock" /> : null}
            </>
          }
        />
        {dialog}
      </>
    );
  }

  const err = (i: number, field: string) => errors[key(i, field)];
  /** Error state of a line's input: invalid, pointing at the message under it. */
  const mark = (i: number, field: string) => {
    const id = ctlId(i, field);
    const describedBy = errRef(id, err(i, field));
    return { id, invalid: describedBy !== undefined, ...(describedBy ? { "aria-describedby": describedBy } : {}) };
  };
  const total = lines.reduce((s, l) => s.plus(lineAmount(l)), dec(0));

  return (
    <>
      <div className="tw flat">
        <table className="t onb-tbl onb-stack">
          <TableCaption>Tồn kho đầu kỳ đang nhập</TableCaption>
          <thead>
            <tr>
              <th scope="col">Hàng · Màu</th>
              <th scope="col" className="num">
                Số lượng
              </th>
              <th scope="col" className="num">
                Giá vốn (₫)
              </th>
              <th scope="col">Số lô · Hạn dùng</th>
              <th scope="col" className="num">
                Thành tiền
              </th>
              <th scope="col">
                <span className="sr-only">Xóa dòng</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {lines.map((l, i) => {
              const n = i + 1;
              return (
                <tr key={l.uid}>
                  <td data-l="Hàng · Màu">
                    <div className="onb-lot">
                      <Combobox
                        source={picker.source}
                        filter={(it) => picker.picked(it.id)?.product_type !== "combo"}
                        value={l.sku?.id ?? null}
                        valueLabel={l.sku ? `${l.sku.code} · ${l.sku.display_name}` : ""}
                        onChange={(it) => {
                          const sku = picker.picked(it.id);
                          // Another product starts its color and lot again (prototype `change` on the SKU).
                          if (sku) update(i, "sku", { sku, color: null, lot: "", exp: null, cost: l.cost ?? sku.ref_cost ?? sku.last_purchase_price });
                        }}
                        aria-label={`Hàng dòng ${n}`}
                        {...mark(i, "sku")}
                      />
                      <OnbErr id={ctlId(i, "sku")} error={err(i, "sku")} />
                      {l.sku ? (
                        <>
                          <select
                            className="in"
                            id={ctlId(i, "color")}
                            aria-label={`Màu dòng ${n}`}
                            aria-invalid={err(i, "color") ? true : undefined}
                            aria-describedby={errRef(ctlId(i, "color"), err(i, "color"))}
                            value={l.color ?? ""}
                            onChange={(e) => update(i, "color", { color: isColorCode(e.target.value) ? e.target.value : null })}
                          >
                            <option value="">{NO_COLOR_LABEL}</option>
                            {COLORS.map((c) => (
                              <option key={c.key} value={c.key}>
                                {c.label}
                              </option>
                            ))}
                          </select>
                          <OnbErr id={ctlId(i, "color")} error={err(i, "color")} />
                        </>
                      ) : null}
                    </div>
                  </td>
                  <td data-l="Số lượng">
                    <QtyInput className="num" value={l.qty} onChange={(qty) => update(i, "qty", { qty })} aria-label={`Số lượng dòng ${n}`} {...mark(i, "qty")} />
                    <OnbErr id={ctlId(i, "qty")} error={err(i, "qty")} />
                  </td>
                  <td data-l="Giá vốn (₫)">
                    <MoneyInput className="num" value={l.cost} onChange={(cost) => update(i, "cost", { cost })} aria-label={`Giá vốn dòng ${n}`} {...mark(i, "cost")} />
                    <OnbErr id={ctlId(i, "cost")} error={err(i, "cost")} />
                  </td>
                  <td data-l="Số lô · Hạn dùng">
                    {l.sku?.track_lot || l.sku?.track_expiry ? (
                      <div className="onb-lot">
                        {l.sku.track_lot ? (
                          <>
                            <Input value={l.lot} placeholder="Số lô" onChange={(e) => update(i, "lot", { lot: e.target.value })} aria-label={`Số lô dòng ${n}`} {...mark(i, "lot")} />
                            <OnbErr id={ctlId(i, "lot")} error={err(i, "lot")} />
                          </>
                        ) : null}
                        {l.sku.track_expiry ? (
                          <>
                            <DatePicker value={l.exp} onChange={(exp) => update(i, "exp", { exp })} aria-label={`Hạn dùng dòng ${n}`} {...mark(i, "exp")} />
                            <OnbErr id={ctlId(i, "exp")} error={err(i, "exp")} />
                          </>
                        ) : null}
                      </div>
                    ) : (
                      <span className="hint">Không theo lô</span>
                    )}
                  </td>
                  <td className="num tab-n" data-l="Thành tiền">
                    {vnd(lineAmount(l))}
                  </td>
                  <td className="acts">
                    {lines.length > 1 ? (
                      <Button className="onb-x" aria-label={`Xóa dòng ${n}`} onClick={() => removeLine(i)}>
                        ×
                      </Button>
                    ) : null}
                  </td>
                </tr>
              );
            })}
          </tbody>
          <tfoot>
            <tr>
              <td colSpan={4}>Tổng giá trị dòng đang nhập</td>
              <td className="num tab-n">{vnd(total)}</td>
              <td />
            </tr>
          </tfoot>
        </table>
      </div>
      <ButtonRow>
        <Button size="sm" onClick={addLine}>
          + Thêm dòng
        </Button>
        <LinkButton onClick={() => setAdding(true)}>+ Hàng hóa mới</LinkButton>
      </ButtonRow>
      <ButtonRow>
        <Button variant={cta} pending={post.pending} onClick={submit}>
          Ghi tồn kho đầu kỳ
        </Button>
        {noneShown ? <NoneButton step="opening_stock" /> : null}
      </ButtonRow>
      {dialog}
    </>
  );
}

/** Prototype "Đã ghi tồn đầu kỳ": every posted opening line, with the total value. */
function PostedStock({ lines, more }: { lines: readonly OpeningStockDocLine[]; more: boolean }) {
  const colors = useColorDirectory();
  if (lines.length === 0) return null;
  const amount = (l: OpeningStockDocLine) => dec(l.qty).times(l.unit_cost ?? 0);
  const columns: ReadonlyArray<Column<OpeningStockDocLine>> = [
    { key: "sku", header: "Hàng", cell: (l) => <CellText main={l.name} sub={l.variant_label} />, footer: "Tổng giá trị tồn đầu kỳ" },
    { key: "color", header: "Màu", cell: (l) => (l.color_id ? colorLabel(colors.codeOf(l.color_id)) : "—") },
    { key: "qty", header: "Số lượng", numeric: true, cell: (l) => <span className="tab-n">{num(l.qty)}</span> },
    { key: "cost", header: "Giá vốn", numeric: true, cell: (l) => vnd(l.unit_cost) },
    { key: "lot", header: "Số lô · Hạn dùng", cell: (l) => [l.lot_no, l.expiry_date && fd(l.expiry_date)].filter(Boolean).join(" · ") || "—" },
    { key: "amount", header: "Thành tiền", numeric: true, cell: (l) => vnd(amount(l)), footer: vnd(lines.reduce((s, l) => s.plus(amount(l)), dec(0))) },
  ];
  return (
    <>
      <h3 className="sec-h">
        Đã ghi tồn đầu kỳ <small className="hint">{lines.length} dòng</small>
      </h3>
      <DataTable caption="Đã ghi tồn đầu kỳ" columns={columns} rows={lines} rowKey={(l) => l.id} flat />
      {more ? <p className="hint">Chỉ hiện các chứng từ gần nhất.</p> : null}
    </>
  );
}
