import { QTY_MAX_FRACTION_DIGITS } from "../../shared/domain/qty";
import { Decimal, dec, toDecimalString } from "../../shared/lib/decimal";
import { inLineUnits } from "./api/adapters";
import type { Invoice, InvoiceLine } from "./schemas";

// Rows of the stock-out dialog: what each invoice line has reserved and not delivered yet, and the
// base quantity sent for what the user typed in the unit of the line. A mix pack or combo goes out
// whole through its header line; the server refuses a flavor or component named on its own.

/** Header lines of a mix pack or combo: they carry no stock and are delivered whole. */
const WHOLE_KINDS = new Set<InvoiceLine["line_kind"]>(["mix_pack", "combo"]);

/** A line that holds stock of its own (the server's `holdsStock`). */
const holdsStock = (l: InvoiceLine) => !WHOLE_KINDS.has(l.line_kind) && l.sku_id !== null;

const remainingOf = (l: InvoiceLine) => dec(l.base_qty).minus(l.cancelled_base_qty).minus(l.delivered_base_qty);

export interface DeliverRow {
  kind: "line";
  line: InvoiceLine;
  /** Reserved and not delivered yet, in base units. */
  cap: string;
  /** The same cap in the unit of the line, rounded down to the four decimals a quantity carries. */
  max: string;
}

export interface WholePart {
  line: InvoiceLine;
  /** Left to deliver, in the unit of the part. */
  remaining: string;
  /** Reserved for it, in the unit of the part. */
  reserved: string;
}

export interface WholeRow {
  kind: "whole";
  /** The mix pack or combo header line. */
  line: InvoiceLine;
  /** Left to deliver of the header in base units, sent as is. */
  cap: string;
  /** The same in the unit of the header (thùng, combo). */
  qty: string;
  /** Flavors or components still to deliver, shown under the header. */
  parts: WholePart[];
  /** Parts holding less than they still have to deliver; the whole goes out only when none does. */
  short: WholePart[];
}

export type StockOutRow = DeliverRow | WholeRow;

export function deliverRows(invoice: Pick<Invoice, "lines">): StockOutRow[] {
  const lines = invoice.lines ?? [];
  return lines.flatMap((l): StockOutRow[] => {
    if (l.parent_line_id !== null) return [];
    const remaining = remainingOf(l);
    if (remaining.lte(0)) return [];
    if (WHOLE_KINDS.has(l.line_kind)) {
      const children = lines.filter((c) => c.parent_line_id === l.id && holdsStock(c) && remainingOf(c).gt(0));
      if (children.length === 0) return [];
      const part = (c: InvoiceLine): WholePart => ({
        line: c,
        remaining: inLineUnits(toDecimalString(remainingOf(c)), c.unit_factor),
        reserved: inLineUnits(c.reserved_base_qty, c.unit_factor),
      });
      const parts = children.map(part);
      // The server's `ready`: every part holds exactly what it still has to deliver.
      const short = children.filter((c) => !dec(c.reserved_base_qty).eq(remainingOf(c))).map(part);
      const cap = toDecimalString(remaining);
      return [{ kind: "whole", line: l, cap, qty: inLineUnits(cap, l.unit_factor), parts, short }];
    }
    const reserved = dec(l.reserved_base_qty);
    const cap = reserved.lt(remaining) ? reserved : remaining;
    const capBase = cap.gt(0) ? toDecimalString(cap) : "0";
    const max = dec(inLineUnits(capBase, l.unit_factor)).toDecimalPlaces(QTY_MAX_FRACTION_DIGITS, Decimal.ROUND_DOWN);
    return [{ kind: "line", line: l, cap: capBase, max: toDecimalString(max) }];
  });
}

/**
 * Base quantity to deliver for q line units. The whole cap is sent as is, since its line-unit
 * value was rounded down (10 hộp of a 24-hộp thùng line); anything else is rounded to the four
 * decimals the server accepts. Null when it exceeds the cap.
 */
export function deliverBaseQty(r: DeliverRow, q: string): string | null {
  if (dec(q).eq(r.max)) return r.cap;
  const base = dec(q).times(r.line.unit_factor).toDecimalPlaces(QTY_MAX_FRACTION_DIGITS);
  return base.gt(r.cap) ? null : toDecimalString(base);
}
