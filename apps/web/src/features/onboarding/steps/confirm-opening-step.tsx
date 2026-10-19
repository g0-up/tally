import { useState } from "react";
import { useNavigate } from "react-router";
import { HOME_PATH } from "../../../app/menu";
import { NO_COLOR_LABEL } from "../../../shared/domain/color";
import { dec } from "../../../shared/lib/decimal";
import { fd, num, vnd } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { Checkbox } from "../../../shared/ui/checkbox";
import { TableCaption } from "../../../shared/ui/data-table/table-caption";
import { KeyValueGrid } from "../../../shared/ui/key-value-grid";
import { LinkButton } from "../../../shared/ui/link-button";
import type { OnboardingStepKey, OpeningTotals } from "../../../shared/api/endpoints";
import { useConfirmOpeningBalances } from "../api/mutations";
import { CUTOFF_MISSING } from "../components/opening-gate";
import { StepCard } from "../components/step-card";
import { isOnboardingStepKey, stepLabel, type ChecklistStep } from "../steps";

const CONFIRM_LABELS = {
  agree: "Tôi đã kiểm tra các số dư trên và đồng ý khóa sổ đến hết ngày chốt.",
  confirm: "Xác nhận số dư đầu kỳ",
  pendingFirst: "Còn bước chưa xong:",
} as const;

/** The opening figures as of the cutoff, in the order the owner checks them. */
function totalsItems(t: OpeningTotals) {
  const fundsTotal = t.funds.reduce((s, f) => s.plus(f.balance), dec(0));
  return [
    { label: "Ngày chốt", value: fd(t.cutoff) },
    { label: "Giá trị tồn kho", value: vnd(t.stock_value) },
    { label: "Khách hàng nợ (phải thu)", value: vnd(t.receivables) },
    { label: "Nợ nhà cung cấp (phải trả)", value: vnd(t.payables) },
    { label: "Khách ứng trước", value: vnd(t.customer_advances) },
    { label: "Ứng trước nhà cung cấp", value: vnd(t.supplier_advances) },
    ...t.funds.map((f) => ({ label: f.name, value: vnd(f.balance) })),
    { label: "Tổng tiền các quỹ", value: vnd(fundsTotal) },
  ];
}

function StockByColor({ totals }: { totals: OpeningTotals }) {
  if (totals.stock_by_color.length === 0) return null;
  return (
    <div className="tw flat">
      <table className="t">
        <TableCaption>Tồn kho đầu kỳ theo màu</TableCaption>
        <thead>
          <tr>
            <th scope="col">Màu</th>
            <th scope="col" className="num">
              Số lượng
            </th>
            <th scope="col" className="num">
              Giá trị
            </th>
          </tr>
        </thead>
        <tbody>
          {totals.stock_by_color.map((c) => (
            <tr key={c.color_id ?? "none"}>
              <td>{c.color_name ?? NO_COLOR_LABEL}</td>
              <td className="num tab-n">{num(c.qty)}</td>
              <td className="num tab-n">{vnd(c.value)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

interface ConfirmStepProps {
  step: ChecklistStep;
  steps: readonly ChecklistStep[];
  totals: OpeningTotals | null;
  lockedThrough: string | null;
  onOpen: (key: OnboardingStepKey) => void;
}

/**
 * Confirmation of the opening balances: the totals at the cutoff, then a password check, then
 * every period up to the cutoff is locked. Back to the dashboard on success.
 */
export function ConfirmOpeningStep({ step, steps, totals, lockedThrough, onOpen }: ConfirmStepProps) {
  const navigate = useNavigate();
  const [agreed, setAgreed] = useState(false);
  const [refused, setRefused] = useState<OnboardingStepKey[]>([]);
  const confirm = useConfirmOpeningBalances({
    setFieldError: (field) => {
      if (isOnboardingStepKey(field)) setRefused((r) => (r.includes(field) ? r : [...r, field]));
    },
    after: () => navigate(HOME_PATH),
  });
  const done = step.status === "completed";
  const pending = steps.filter((s) => s.key !== step.key && !s.skippable && s.status === "pending").map((s) => s.key);
  // The API's refusal names the steps it still sees pending; it is the authority over a stale list.
  const blocking = [...new Set([...pending, ...refused])];

  const run = () => {
    setRefused([]);
    void confirm.run();
  };

  return (
    <StepCard step={step} note={done && lockedThrough ? `Sổ sách đã khóa đến hết ngày ${fd(lockedThrough)}` : "Cần nhập lại mật khẩu khi xác nhận"}>
      {totals === null ? (
        <Callout tone="warn">
          {CUTOFF_MISSING} <LinkButton onClick={() => onOpen("store_profile")}>Mở Thông tin cửa hàng</LinkButton>
        </Callout>
      ) : (
        <>
          <KeyValueGrid items={totalsItems(totals)} />
          <StockByColor totals={totals} />
        </>
      )}
      {!done && blocking.length > 0 ? (
        <Callout tone="warn">
          {CONFIRM_LABELS.pendingFirst}{" "}
          {blocking.map((k, i) => (
            <span key={k}>
              {i > 0 ? ", " : null}
              <LinkButton onClick={() => onOpen(k)}>{stepLabel(k)}</LinkButton>
            </span>
          ))}
        </Callout>
      ) : null}
      {done ? (
        <Callout tone="ok">Số dư đầu kỳ đã xác nhận. Chứng từ từ ngày chốt trở về trước không ghi thêm được.</Callout>
      ) : (
        <>
          <Callout tone="info">Sau khi xác nhận, không ghi hay sửa được chứng từ nào trong kỳ đến hết ngày chốt. Số dư đầu kỳ không đổi được nữa.</Callout>
          <Checkbox label={CONFIRM_LABELS.agree} checked={agreed} onChange={(e) => setAgreed(e.target.checked)} />
          <ButtonRow>
            <Button variant="danger" disabled={!agreed} pending={confirm.pending} onClick={run}>
              {CONFIRM_LABELS.confirm}
            </Button>
          </ButtonRow>
        </>
      )}
    </StepCard>
  );
}
