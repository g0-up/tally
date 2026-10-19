import { toMoney, type Money } from "../../shared/domain/money";
import { dec, sumDec, type Decimal } from "../../shared/lib/decimal";
import type { Payment } from "./schemas";

// Spreading a payment over open documents (prototype `suggestAlloc`): the oldest due first, each
// document up to what is still open; whatever is left becomes an advance. Every sum is Decimal.

export interface OpenDocument {
  id: string;
  open: Money;
  /** Due date of the oldest unpaid installment. */
  next_due: string;
  business_date: string;
  source_doc_no: string;
}

const minDec = (a: Decimal, b: Decimal) => (a.lt(b) ? a : b);

/** Open documents oldest due first; ties by document date, then number. */
export function byOldestDue<T extends OpenDocument>(docs: readonly T[]): T[] {
  return docs
    .filter((d) => dec(d.open).gt(0))
    .sort((a, b) => a.next_due.localeCompare(b.next_due) || a.business_date.localeCompare(b.business_date) || a.source_doc_no.localeCompare(b.source_doc_no));
}

/** The oldest-due-first split of `amount` over `docs`, by document id (zero for untouched ones). */
export function suggestAllocation(amount: Money | null, docs: readonly OpenDocument[]): Map<string, Money> {
  let left: Decimal = amount === null ? dec(0) : dec(amount);
  const out = new Map<string, Money>();
  for (const d of byOldestDue(docs)) {
    const take = left.gt(0) ? minDec(left, dec(d.open)) : dec(0);
    out.set(d.id, toMoney(take));
    left = left.minus(take);
  }
  return out;
}

export interface AllocationLine {
  id: string;
  open: Money;
  amount: Money | null;
}

export interface AllocationCheck {
  allocated: Decimal;
  /** What the payment leaves unallocated: an advance (never negative). */
  advance: Decimal;
  /** The lines take more than the payment. */
  overPayment: boolean;
  /** Lines that take more than their document still owes. */
  overOpen: string[];
}

/** Totals and blocking problems of an allocation before it is posted. */
export function checkAllocation(amount: Money | null, lines: readonly AllocationLine[]): AllocationCheck {
  const total = amount === null ? dec(0) : dec(amount);
  const allocated = sumDec(lines.map((l) => l.amount ?? "0"));
  const rest = total.minus(allocated);
  return {
    allocated,
    advance: rest.gt(0) ? rest : dec(0),
    overPayment: allocated.gt(total),
    overOpen: lines.filter((l) => l.amount !== null && dec(l.amount).gt(l.open)).map((l) => l.id),
  };
}

/** A confirmed advance with a party and money left can still be spread over open documents. */
export function canAllocate(p: Pick<Payment, "status" | "party_id" | "purpose" | "unallocated">): boolean {
  return p.status === "confirmed" && p.party_id !== null && p.purpose !== "refund" && dec(p.unallocated).gt(0);
}
