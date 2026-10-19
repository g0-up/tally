import { useId } from "react";
import type { Money } from "../../../shared/domain/money";
import { dec } from "../../../shared/lib/decimal";
import { vnd } from "../../../shared/lib/format";
import { Callout } from "../../../shared/ui/callout";
import { Checkbox } from "../../../shared/ui/checkbox";
import { MoneyInput } from "../../../shared/ui/money-input";
import { paymentFigures } from "../api/adapters";
import { CashChips } from "./cash-chips";

/** One way the customer hands money over; each becomes a payment of the checkout. */
interface PaymentStrategy {
  key: "cash" | "bank";
  label: string;
}

const STRATEGIES: readonly PaymentStrategy[] = [
  { key: "cash", label: "Tiền mặt khách đưa" },
  { key: "bank", label: "Chuyển khoản / QR" },
];

export const OFFLINE_BANK_TITLE = "QR cần xác nhận từ ngân hàng, không dùng khi ngoại tuyến";
export const OFFLINE_CREDIT_SUFFIX = "(không cho phép khi ngoại tuyến)";

export interface PaymentMethodPanelProps {
  /** "Khách cần trả" from the server; null until the cart is priced. */
  total: Money | null;
  cash: Money | null;
  bank: Money | null;
  onCash: (value: Money | null) => void;
  onBank: (value: Money | null) => void;
  /** A cash chip was clicked. */
  onChip: (value: Money) => void;
  credit: boolean;
  onCredit: (value: boolean) => void;
  /** A named customer is selected (credit is never offered to a walk-in buyer). */
  namedCustomer: boolean;
  /** "offline": cash only, paid in full; transfer and credit are shown disabled. */
  mode?: "online" | "offline";
}

/**
 * Cash and transfer amounts, quick cash chips, "Bán chịu phần còn lại X" when the customer pays
 * less than the total, and "Trả lại khách: X" when they hand over more. Offline, transfer needs
 * the bank's confirmation and credit needs the debt ledger, so both are disabled.
 */
export function PaymentMethodPanel({ total, cash, bank, onCash, onBank, onChip, credit, onCredit, namedCustomer, mode = "online" }: PaymentMethodPanelProps) {
  const offline = mode === "offline";
  const figures = paymentFigures(total ?? "0", cash ?? "", offline ? "" : (bank ?? ""));
  const values = { cash, bank };
  const setters = { cash: onCash, bank: onBank };
  const owing = total !== null && dec(total).gt(0) && dec(figures.owe).gt(0);
  const bankReasonId = useId();
  return (
    <>
      <div className="fg" style={{ gridTemplateColumns: "1fr 1fr" }}>
        {STRATEGIES.map((s) => (
          <label className="f" key={s.key}>
            <span>{s.label}</span>
            {offline && s.key === "bank" ? (
              <MoneyInput value={null} placeholder="0" onChange={setters[s.key]} disabled title={OFFLINE_BANK_TITLE} aria-describedby={bankReasonId} />
            ) : (
              <MoneyInput value={values[s.key]} placeholder="0" onChange={setters[s.key]} />
            )}
          </label>
        ))}
      </div>
      {/* A disabled field takes no focus, so its reason is on screen rather than only in a tooltip. */}
      {offline ? (
        <p className="hint" id={bankReasonId}>
          {OFFLINE_BANK_TITLE}
        </p>
      ) : null}
      <CashChips total={total} onPick={onChip} />
      {owing && offline ? <Checkbox label={`Bán chịu phần còn lại ${vnd(figures.owe)} ${OFFLINE_CREDIT_SUFFIX}`} checked={false} disabled onChange={() => undefined} /> : null}
      {owing && namedCustomer && !offline ? <Checkbox label={`Bán chịu phần còn lại ${vnd(figures.owe)}`} checked={credit} onChange={(e) => onCredit(e.target.checked)} /> : null}
      {dec(figures.change).gt(0) ? (
        <Callout tone="ok" role="status">
          Trả lại khách: <b className="tab-n">{vnd(figures.change)}</b>
        </Callout>
      ) : null}
    </>
  );
}
