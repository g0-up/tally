import { Fragment, createElement, useCallback, useRef, useState, type ReactNode } from "react";
import { errorMessage } from "../../../shared/api/api-error";
import { toast } from "../../../shared/ui/toast-store";
import type { OfflineBundle, OutboxEntry } from "./offline-db";
import { publishOfflineEvent } from "./offline-events";
import { OfflineSaleError, recordOfflineSale, type OfflineSaleInput } from "./offline-sale-service";

export const NOT_SAVED = "Chưa lưu.";

/** "<b>Chưa lưu.</b> {message}" */
export function notSavedMessage(message: string): ReactNode {
  return createElement(Fragment, null, createElement("b", null, NOT_SAVED), " ", message);
}

/** "Đã lưu OFF-00n trên máy. Sẽ ghi sổ một lần khi có mạng." */
export function savedOfflineMessage(label: string): string {
  return `Đã lưu ${label} trên máy. Sẽ ghi sổ một lần khi có mạng.`;
}

export type OfflineCheckoutInput = Omit<OfflineSaleInput, "bundle">;

/** "Thanh toán · F9" without the server: the sale goes to the outbox, never to the ledger. */
export function useOfflineCheckout(bundle: OfflineBundle | null, after: (entry: OutboxEntry) => void) {
  const [busy, setBusy] = useState(false);
  const running = useRef(false);

  const run = useCallback(
    async (input: OfflineCheckoutInput) => {
      if (running.current) return;
      running.current = true;
      setBusy(true);
      try {
        const entry = await recordOfflineSale({ ...input, bundle });
        toast.success(savedOfflineMessage(entry.label));
        // This tab's outbox and the store's other tabs read the device again.
        publishOfflineEvent({ type: "changed", tenantId: entry.tenant_id });
        after(entry);
      } catch (e) {
        toast.error(notSavedMessage(e instanceof OfflineSaleError ? e.message : errorMessage(e)));
      } finally {
        running.current = false;
        setBusy(false);
      }
    },
    [bundle, after],
  );
  return { run, pending: busy };
}
