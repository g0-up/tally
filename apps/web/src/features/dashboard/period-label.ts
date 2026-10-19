import { fd, fdS } from "../../shared/lib/format";
import type { DashboardPeriod } from "./schemas";

// The period in the prototype's words (`dashRange().label`), from the dates the API returned.

/** "ngày 12/10/2026", "7 ngày 06/10 – 12/10/2026", "tháng 10/2026", "năm 2026". */
export function periodLabel(p: DashboardPeriod): string {
  switch (p.kind) {
    case "day":
      return `ngày ${fd(p.from)}`;
    case "7d":
      return `7 ngày ${fdS(p.from)} – ${fd(p.to)}`;
    case "month":
      return `tháng ${p.from.slice(5, 7)}/${p.from.slice(0, 4)}`;
    case "year":
      return `năm ${p.from.slice(0, 4)}`;
  }
}

/** The day balances are read at: the period's last day, or today while the period is running. */
export function balanceDate(p: DashboardPeriod, today: string): string {
  return p.to < today ? p.to : today;
}
