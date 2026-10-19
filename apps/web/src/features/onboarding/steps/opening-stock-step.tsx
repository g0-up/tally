import { useId, useState } from "react";
import { COLORS, NO_COLOR_LABEL, isColorCode, type ColorCode } from "../../../shared/domain/color";
import type { Money } from "../../../shared/domain/money";
import type { Qty } from "../../../shared/domain/qty";
import { useFocusFirstInvalid } from "../../../shared/hooks/use-focus-first-invalid";
import { dec } from "../../../shared/lib/decimal";
import { fd, vnd } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { Combobox } from "../../../shared/ui/combobox";
import { TableCaption } from "../../../shared/ui/data-table/table-caption";
import { DatePicker } from "../../../shared/ui/date-picker";
import { Input } from "../../../shared/ui/input";
import { MoneyInput } from "../../../shared/ui/money-input";
import { QtyInput } from "../../../shared/ui/qty-input";
import { baseUnitName, useColorDirectory, useSkuPicker, type SkuListItem } from "../../catalog";
import { usePostOpeningStock, useStepCommand, type OpeningStockLine } from "../api/mutations";
import { OpeningGate } from "../components/opening-gate";
import { StepCard } from "../components/step-card";
import type { ChecklistStep } from "../steps";

const STOCK_LABELS = {
  product: "Hàng",
  color: "Màu",
  qty: "Số lượng",
  unitCost: "Giá vốn",
  lot: "Số lô",
  expiry: "Hạn dùng",
  addLine: "+ Thêm dòng",
  removeLine: "Xóa dòng",
  post: "Ghi tồn kho đầu kỳ",
} as const;

interface LineDraft {
  uid: number;
  sku: SkuListItem | null;
  color: ColorCode | null;
  qty: Qty | null;
  unitCost: Money | null;
  lotNo: string;
  expiry: string | null;
}

let nextUid = 1;
const emptyLine = (): LineDraft => ({ uid: nextUid++, sku: null, color: null, qty: null, unitCost: null, lotNo: "", expiry: null });

const lineValue = (l: LineDraft) => (l.qty !== null && l.unitCost !== null ? dec(l.qty).times(l.unitCost) : null);

type LineField = "product" | "qty" | "unitCost" | "lot" | "expiry";

/** A problem of the lines: its message and, when it is about one input, that line and input. */
interface LineProblem {
  message: string;
  uid: number | null;
  field: LineField | null;
}

/** The first problem of the lines, in the order the owner reads them; null when they can be posted. */
function lineProblem(lines: readonly LineDraft[]): LineProblem | null {
  if (lines.length === 0) return { message: "Thêm ít nhất một dòng hàng.", uid: null, field: null };
  for (const l of lines) {
    const at = (field: LineField, message: string): LineProblem => ({ message, uid: l.uid, field });
    if (l.sku === null) return at("product", "Chọn hàng cho từng dòng.");
    if (l.qty === null || !dec(l.qty).gt(0)) return at("qty", `Số lượng của ${l.sku.code} phải lớn hơn 0.`);
    if (l.unitCost === null || dec(l.unitCost).lt(0)) return at("unitCost", `Nhập giá vốn của ${l.sku.code}.`);
    if (l.sku.track_lot && l.lotNo.trim() === "") return at("lot", `Nhập số lô của ${l.sku.code}.`);
    if (l.sku.track_expiry && l.expiry === null) return at("expiry", `Chọn hạn dùng của ${l.sku.code}.`);
  }
  return null;
}

/**
 * What names a line in its inputs' labels: the SKU code once one is picked ("dòng n" before, and
 * after the code when the same SKU is on several lines).
 */
function lineName(lines: readonly LineDraft[], l: LineDraft, n: number): string {
  if (l.sku === null) return `dòng ${n}`;
  const code = l.sku.code;
  return lines.filter((x) => x.sku?.code === code).length > 1 ? `${code} dòng ${n}` : code;
}

/**
 * Opening stock: lines of product, quantity, cost and lot, posted as one opening adjustment dated
 * on the cutoff (posting it completes the step on the server too).
 */
export function OpeningStockStep({ step, cutoff, confirmed, onOpenProfile }: { step: ChecklistStep; cutoff: string | null; confirmed: boolean; onOpenProfile: () => void }) {
  return (
    <StepCard step={step} note={cutoff ? `Tồn kho cuối ngày ${fd(cutoff)}` : undefined} markDone={!confirmed}>
      <OpeningGate cutoff={cutoff} confirmed={confirmed} onOpenProfile={onOpenProfile}>
        {(date) => <StockLines cutoff={date} />}
      </OpeningGate>
    </StepCard>
  );
}

function StockLines({ cutoff }: { cutoff: string }) {
  const picker = useSkuPicker(null);
  const colors = useColorDirectory();
  const [lines, setLines] = useState<LineDraft[]>(() => [emptyLine()]);
  const complete = useStepCommand("opening_stock", "complete", { quiet: true });
  const post = usePostOpeningStock({
    after: () => {
      setLines([emptyLine()]);
      void complete.run();
    },
  });

  const [problem, setProblem] = useState<LineProblem | null>(null);
  const problemId = useId();
  const focusFirstInvalid = useFocusFirstInvalid();
  const update = (uid: number, patch: Partial<LineDraft>) => {
    setLines((ls) => ls.map((l) => (l.uid === uid ? { ...l, ...patch } : l)));
    setProblem(null);
  };
  const total = lines.reduce((sum, l) => sum.plus(lineValue(l) ?? 0), dec(0));
  /** Marks the input a problem is about and points it at the message. */
  const mark = (l: LineDraft, field: LineField) =>
    problem !== null && problem.uid === l.uid && problem.field === field ? { invalid: true, "aria-describedby": problemId } : {};

  const submit = () => {
    const found = lineProblem(lines);
    setProblem(found);
    if (found !== null) return focusFirstInvalid();
    const body: OpeningStockLine[] = [];
    for (const l of lines) {
      if (l.sku === null || l.qty === null || l.unitCost === null) continue;
      const colorId = colors.idOf(l.color);
      body.push({
        sku_id: l.sku.id,
        qty: l.qty,
        unit_cost: l.unitCost,
        ...(colorId ? { color_id: colorId } : {}),
        ...(l.sku.track_lot && l.lotNo.trim() !== "" ? { lot_no: l.lotNo.trim() } : {}),
        ...(l.expiry !== null ? { expiry_date: l.expiry } : {}),
      });
    }
    void post.run({ businessDate: cutoff, lines: body });
  };

  return (
    <>
      <div className="tw flat">
        <table className="t">
          <TableCaption>Tồn kho đầu kỳ</TableCaption>
          <thead>
            <tr>
              <th scope="col">{STOCK_LABELS.product}</th>
              <th scope="col">{STOCK_LABELS.color}</th>
              <th scope="col" className="num">
                {STOCK_LABELS.qty}
              </th>
              <th scope="col" className="num">
                {STOCK_LABELS.unitCost}
              </th>
              <th scope="col">
                {STOCK_LABELS.lot} · {STOCK_LABELS.expiry}
              </th>
              <th scope="col" className="num">
                Thành tiền
              </th>
              <th scope="col">
                <span className="sr-only">Thao tác</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {lines.map((l, i) => {
              const n = i + 1;
              const name = lineName(lines, l, n);
              const value = lineValue(l);
              return (
                <tr key={l.uid}>
                  <td>
                    <Combobox
                      source={picker.source}
                      filter={(it) => picker.picked(it.id)?.product_type !== "combo"}
                      value={l.sku?.id ?? null}
                      valueLabel={l.sku ? `${l.sku.code} · ${l.sku.display_name}` : ""}
                      onChange={(it) => {
                        const sku = picker.picked(it.id);
                        if (sku) update(l.uid, { sku, unitCost: l.unitCost ?? sku.ref_cost ?? sku.last_purchase_price });
                      }}
                      aria-label={`${STOCK_LABELS.product} ${name}`}
                      {...mark(l, "product")}
                    />
                  </td>
                  <td>
                    <select className="in" aria-label={`${STOCK_LABELS.color} ${name}`} value={l.color ?? ""} onChange={(e) => update(l.uid, { color: isColorCode(e.target.value) ? e.target.value : null })}>
                      <option value="">{NO_COLOR_LABEL}</option>
                      {COLORS.map((c) => (
                        <option key={c.key} value={c.key}>
                          {c.label}
                        </option>
                      ))}
                    </select>
                  </td>
                  <td className="num">
                    <QtyInput value={l.qty} onChange={(qty) => update(l.uid, { qty })} aria-label={`${STOCK_LABELS.qty} ${name}${l.sku ? ` (${baseUnitName(l.sku)})` : ""}`} {...mark(l, "qty")} />
                  </td>
                  <td className="num">
                    <MoneyInput value={l.unitCost} onChange={(unitCost) => update(l.uid, { unitCost })} aria-label={`${STOCK_LABELS.unitCost} ${name}`} {...mark(l, "unitCost")} />
                  </td>
                  <td>
                    {l.sku?.track_lot ? <Input value={l.lotNo} onChange={(e) => update(l.uid, { lotNo: e.target.value })} aria-label={`${STOCK_LABELS.lot} ${name}`} {...mark(l, "lot")} /> : null}
                    {l.sku?.track_expiry ? <DatePicker value={l.expiry} onChange={(expiry) => update(l.uid, { expiry })} aria-label={`${STOCK_LABELS.expiry} ${name}`} {...mark(l, "expiry")} /> : null}
                    {l.sku && !l.sku.track_lot && !l.sku.track_expiry ? "—" : null}
                  </td>
                  <td className="num tab-n">{value === null ? "—" : vnd(value)}</td>
                  <td className="acts">
                    <Button
                      size="sm"
                      aria-label={`${STOCK_LABELS.removeLine} ${n}${l.sku ? ` · ${l.sku.code}` : ""}`}
                      disabled={lines.length === 1}
                      onClick={() => {
                        setLines((ls) => ls.filter((x) => x.uid !== l.uid));
                        setProblem(null);
                      }}
                    >
                      ×
                    </Button>
                  </td>
                </tr>
              );
            })}
          </tbody>
          <tfoot>
            <tr>
              <th scope="row" colSpan={5}>
                Tổng giá trị
              </th>
              <td className="num tab-n">{vnd(total)}</td>
              <td />
            </tr>
          </tfoot>
        </table>
      </div>
      {problem !== null ? (
        <Callout tone="bad" role="alert">
          <span id={problemId}>{problem.message}</span>
        </Callout>
      ) : null}
      <ButtonRow>
        <Button
          onClick={() => {
            setLines((ls) => [...ls, emptyLine()]);
            setProblem(null);
          }}
        >
          {STOCK_LABELS.addLine}
        </Button>
        <Button variant="primary" pending={post.pending} onClick={submit}>
          {STOCK_LABELS.post}
        </Button>
      </ButtonRow>
    </>
  );
}
