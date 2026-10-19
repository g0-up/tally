import { useState } from "react";
import type { OnboardingStepKey, OpeningTotals } from "../../../shared/api/endpoints";
import { colorLabel } from "../../../shared/domain/color";
import { fdt } from "../../../shared/lib/dates";
import { dec, type Decimal } from "../../../shared/lib/decimal";
import { fd, num, vnd } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { Checkbox } from "../../../shared/ui/checkbox";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { KeyValueGrid } from "../../../shared/ui/key-value-grid";
import { LinkButton } from "../../../shared/ui/link-button";
import { useColorDirectory } from "../../catalog";
import { useConfirmOpeningBalances } from "../api/mutations";
import { useOpeningFunds, useOpeningStockLines } from "../api/queries";
import { CutoffGate } from "../components/opening-gate";
import { useOnboardingScreen } from "../onboarding-context";
import type { OpeningStockDocLine } from "../schemas";
import { blockers, isOnboardingStepKey, stepLabel } from "../steps";

/** Prototype `.onb-sum`: the opening figures as of the cutoff, in the order the owner checks them. */
function OpeningSummary({ totals }: { totals: OpeningTotals }) {
  const funds = useOpeningFunds();
  const unsaved = (id: string) => funds.data?.find((f) => f.id === id)?.opening_date === null;
  const fundsTotal = totals.funds.reduce((s, f) => s.plus(f.balance), dec(0));
  return (
    <div className="onb-sum">
      <KeyValueGrid
        items={[
          { label: "Ngày chốt", value: fd(totals.cutoff) },
          { label: "Giá trị tồn kho", value: vnd(totals.stock_value) },
          { label: "Khách nợ (phải thu)", value: vnd(totals.receivables) },
          { label: "Nợ nhà cung cấp (phải trả)", value: vnd(totals.payables) },
          ...totals.funds.map((f) => ({ label: f.name, value: unsaved(f.id) ? <span className="hint">Chưa lưu</span> : vnd(f.balance) })),
          { label: "Tổng tiền các quỹ", value: vnd(fundsTotal) },
        ]}
      />
    </div>
  );
}

interface ColorRow {
  key: string;
  name: string;
  colorId: string | null;
  qty: Decimal;
  value: Decimal;
}

/** The posted opening stock lines summed per SKU and color. */
function byColor(lines: readonly OpeningStockDocLine[]): ColorRow[] {
  const rows = new Map<string, ColorRow>();
  for (const l of lines) {
    const key = `${l.sku_id}|${l.color_id ?? ""}`;
    const row = rows.get(key) ?? { key, name: l.name, colorId: l.color_id, qty: dec(0), value: dec(0) };
    rows.set(key, { ...row, qty: row.qty.plus(l.qty), value: row.value.plus(dec(l.qty).times(l.unit_cost ?? 0)) });
  }
  return [...rows.values()];
}

/** Prototype "Tồn kho theo màu". */
function StockByColor() {
  const colors = useColorDirectory();
  const stock = useOpeningStockLines();
  const columns: ReadonlyArray<Column<ColorRow>> = [
    { key: "sku", header: "Hàng", cell: (r) => r.name },
    { key: "color", header: "Màu", cell: (r) => (r.colorId ? colorLabel(colors.codeOf(r.colorId)) : "—") },
    { key: "qty", header: "Số lượng", numeric: true, cell: (r) => <span className="tab-n">{num(r.qty)}</span> },
    { key: "value", header: "Giá trị", numeric: true, cell: (r) => vnd(r.value) },
  ];
  return (
    <>
      <h3 className="sec-h">Tồn kho theo màu</h3>
      <DataTable caption="Tồn kho theo màu" columns={columns} rows={byColor(stock.data?.lines ?? [])} rowKey={(r) => r.key} flat empty="Không có tồn kho đầu kỳ." />
    </>
  );
}

/**
 * Prototype `OB_BODY.confirm_opening_balances`: the totals at the cutoff, then a password check, then
 * every period up to the cutoff is locked and the first sale opens.
 */
export function ConfirmOpeningStep() {
  const { steps, cutoff, confirmed, confirmedAt, totals, goTo } = useOnboardingScreen();
  const [agreed, setAgreed] = useState(false);
  // The steps the API refused over, with the checklist they were refused against.
  const [refused, setRefused] = useState<{ against: string; keys: OnboardingStepKey[] }>({ against: "", keys: [] });
  const pending = blockers(steps);
  const confirm = useConfirmOpeningBalances(cutoff, {
    setFieldError: (field) => {
      if (isOnboardingStepKey(field)) setRefused((r) => ({ against: pending.join(), keys: r.keys.includes(field) ? r.keys : [...r.keys, field] }));
    },
    after: () => goTo("first_sale"),
  });
  if (!confirmed && cutoff === null) return <CutoffGate onOpenProfile={() => goTo("store_profile")} />;
  const figures = (
    <>
      {totals ? <OpeningSummary totals={totals} /> : null}
      <StockByColor />
    </>
  );
  if (confirmed) {
    return (
      <>
        <Callout tone="ok" role="note">
          <b>Đã xác nhận lúc {fdt(confirmedAt)}.</b> Sổ đã khóa đến hết ngày {fd(cutoff)}. POS và các màn bán hàng đã mở.
        </Callout>
        {figures}
      </>
    );
  }
  // The API's refusal is the authority over a stale checklist, until the checklist changes.
  const blocking = [...new Set([...pending, ...(refused.against === pending.join() ? refused.keys : [])])];
  const disabled = !agreed || blocking.length > 0;
  return (
    <>
      {figures}
      {blocking.length > 0 ? (
        <Callout tone="warn" role="note">
          <b>Còn {blocking.length} bước bắt buộc chưa xong:</b>
          <ul className="onb-ul" style={{ marginTop: 6 }}>
            {blocking.map((k) => (
              <li key={k}>
                <LinkButton onClick={() => goTo(k)}>{stepLabel(k)}</LinkButton>
              </li>
            ))}
          </ul>
        </Callout>
      ) : null}
      <Callout role="note">
        <b>Khi xác nhận:</b>
        <ul className="onb-ul" style={{ marginTop: 6 }}>
          <li>Tồn kho, công nợ, tiền đầu kỳ và ngày chốt bị khóa, không sửa được.</li>
          <li>Sổ khóa đến hết ngày {fd(cutoff)}. Sai lệch sau đó xử lý bằng chứng từ điều chỉnh.</li>
          <li>POS và các màn bán hàng được mở.</li>
        </ul>
      </Callout>
      <Checkbox id="ob-agree" label="Tôi đã kiểm tra các số dư trên và đồng ý khóa sổ đến hết ngày chốt." checked={agreed} onChange={(e) => setAgreed(e.target.checked)} />
      <ButtonRow>
        <Button
          variant="danger"
          disabled={disabled}
          pending={confirm.pending}
          onClick={() => {
            setRefused({ against: "", keys: [] });
            void confirm.run();
          }}
          {...(disabled ? { "aria-describedby": "ob-confirm-h" } : {})}
        >
          Xác nhận số dư đầu kỳ
        </Button>
        <span className="hint" id="ob-confirm-h">
          {blocking.length > 0 ? "Hoàn tất các bước còn thiếu trước." : !agreed ? "Đánh dấu ô đồng ý để xác nhận." : "Cần nhập lại mật khẩu khi xác nhận."}
        </span>
      </ButtonRow>
    </>
  );
}
