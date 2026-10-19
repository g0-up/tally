import { parseQtyInput } from "../../../../shared/domain/qty";
import { dec } from "../../../../shared/lib/decimal";
import { DecimalInput } from "../../../../shared/ui/money-input";

/** Reads a discount percent (0–100, comma decimal mark); null when it is not one. */
function parsePercent(text: string): string | null {
  const q = parseQtyInput(text);
  return q !== null && dec(q).gte(0) && dec(q).lte(100) ? q : null;
}

/** "Chiết khấu %" cell input (width 58); an empty or invalid entry counts as no discount. */
export function PercentInput({ value, onChange, label }: { value: string; onChange: (pct: string) => void; label: string }) {
  return <DecimalInput<string> size="sm" style={{ width: 58 }} value={value === "0" ? null : value} parse={parsePercent} aria-label={label} onChange={(v) => onChange(v ?? "0")} />;
}
