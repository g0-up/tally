import type { Money } from "../../../shared/domain/money";
import { toMoney } from "../../../shared/domain/money";
import { dec, sumDec } from "../../../shared/lib/decimal";
import type { OutboxEntry, OutboxLineView } from "./offline-db";
import { parkedSale, type ResolveBody, type SyncException } from "./offline-queries";
import { syncErrorText } from "./sync-errors";

// One row of the exception queue as the POS shows it. The server's open list is the truth (it
// also holds sales parked from other devices); this device adds its OFF label and the lines as
// they were shown at the counter. When the list cannot be read (no access, no network), the sales
// this device knows to be parked are shown from the outbox.

export interface ParkedSaleView {
  /** Server exception id; null for a sale only known from this device's outbox. */
  id: string | null;
  offlineClientId: string;
  label: string;
  /** When the sale was made at the counter. */
  createdAt: string | null;
  /** Lines as shown at the counter; null for a sale from another device. */
  lines: readonly OutboxLineView[] | null;
  lineCount: number;
  cash: Money | null;
  error: string;
  /** What "Ghi sổ lại" sends; null when the stored sale cannot be read. */
  body: ResolveBody | null;
}

/** Label of a sale this device did not make: the end of its id, which is random. */
export function foreignLabel(offlineClientId: string): string {
  return `…${offlineClientId.slice(-6)}`;
}

function cashOf(body: Pick<ResolveBody, "cash_tendered_amount" | "payments"> | null): Money | null {
  if (!body) return null;
  if (body.cash_tendered_amount !== undefined) return toMoney(dec(body.cash_tendered_amount));
  if (!body.payments || body.payments.length === 0) return null;
  return toMoney(sumDec(body.payments.map((p) => p.amount)));
}

export function parkedSales(server: readonly SyncException[] | undefined, outbox: readonly OutboxEntry[]): ParkedSaleView[] {
  const local = new Map(outbox.map((e) => [e.offline_client_id, e]));
  if (server === undefined) {
    return outbox
      .filter((e) => e.status === "exception")
      .map((e) => {
        const parsed = parkedSale(e.sale);
        return {
          id: e.exception?.id ?? null,
          offlineClientId: e.offline_client_id,
          label: e.label,
          createdAt: e.sale.created_at,
          lines: e.lines,
          lineCount: e.lines.length,
          cash: cashOf(parsed?.body ?? null),
          error: syncErrorText(e.exception?.error_code, e.exception?.error_detail),
          body: parsed?.body ?? null,
        };
      });
  }
  return server.map((x) => {
    const mine = local.get(x.offline_client_id);
    const parsed = parkedSale(x.payload);
    return {
      id: x.id,
      offlineClientId: x.offline_client_id,
      label: mine?.label ?? foreignLabel(x.offline_client_id),
      createdAt: parsed?.createdAt ?? mine?.sale.created_at ?? x.created_at,
      lines: mine?.lines ?? null,
      lineCount: mine?.lines.length ?? parsed?.body.lines.length ?? 0,
      cash: cashOf(parsed?.body ?? null),
      error: syncErrorText(x.error_code, x.error_detail),
      body: parsed?.body ?? null,
    };
  });
}
