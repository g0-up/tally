import { vnd } from "../../../shared/lib/format";
import { Combobox } from "../../../shared/ui/combobox";
import { LinkButton } from "../../../shared/ui/link-button";
import { WALK_IN_NAME, customerSearchSource, useCustomer, useDebtLedger } from "../../sales/api/queries";
import type { PosCustomer } from "../hooks/use-pos-cart";

export interface CustomerBarProps {
  customer: PosCustomer | null;
  onChange: (customer: PosCustomer | null) => void;
  /** "Nguồn giá: …" of the cart as the server priced it; null when nothing is priced yet. */
  priceSource: string | null;
}

/**
 * "Khách hàng" block of the cart: name, phone, credit limit and current debt, the customer picker
 * (shared party Combobox) and where the prices come from.
 */
export function CustomerBar({ customer, onChange, priceSource }: CustomerBarProps) {
  const detail = useCustomer(customer?.id);
  const ledger = useDebtLedger(customer?.id);
  const c = customer ? detail.data : undefined;
  const facts = [
    c?.phone ?? "",
    c && c.credit_limit !== null ? `hạn mức nợ ${vnd(c.credit_limit)}${ledger.data ? ` · đang nợ ${vnd(ledger.data.receivable)}` : ""}` : "",
  ].filter(Boolean);
  const source = priceSource ?? (customer === null ? "Giá bán lẻ" : null);
  return (
    <div className="custbar">
      <span>Khách hàng</span>
      <b>{customer?.name ?? WALK_IN_NAME}</b>
      <small>{facts.join(" · ")}</small>
      <div style={{ gridColumn: "1 / -1", marginTop: 6 }}>
        <Combobox
          kind="party"
          source={customerSearchSource}
          value={customer?.id ?? null}
          valueLabel={customer?.name ?? ""}
          aria-label="Đổi khách hàng"
          onChange={(item) => onChange({ id: item.id, name: item.name })}
        />
      </div>
      {customer !== null ? (
        <span className="hint">
          <LinkButton onClick={() => onChange(null)}>Bán cho {WALK_IN_NAME.toLowerCase()}</LinkButton>
        </span>
      ) : null}
      {source !== null ? <span className="hint">Nguồn giá: {source}</span> : null}
    </div>
  );
}
