import type { Money } from "../../../shared/domain/money";
import { vnd } from "../../../shared/lib/format";
import { ButtonRow } from "../../../shared/ui/button";
import { cashChips } from "../api/adapters";

/** Quick cash amounts: the total, rounded up to 50k and 100k, and 500k (unique, at most 4). */
export function CashChips({ total, onPick }: { total: Money | null; onPick: (value: Money) => void }) {
  const chips = total === null ? [] : cashChips(total);
  if (chips.length === 0) return null;
  return (
    <ButtonRow role="group" aria-label="Tiền khách đưa nhanh">
      {chips.map((v) => (
        <button key={v} type="button" className="chip" onClick={() => onPick(v)}>
          {vnd(v)}
        </button>
      ))}
    </ButtonRow>
  );
}
