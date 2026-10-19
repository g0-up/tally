import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useApiClient } from "../../../shared/api/api-context";
import { ApiError, errorMessage, isApiError } from "../../../shared/api/api-error";
import { useCommit } from "../../../shared/hooks/use-commit";
import { EXCEPTION_SUB, useConfirmException, type ExceptionPrompt } from "../../../shared/hooks/use-confirm-exception";
import { toast } from "../../../shared/ui/toast-store";
import { POSTING_KEYS, withDoc, withExceptionIds, withExceptions } from "../../sales/api/mutations";
import { posSaleSchema } from "../../sales/schemas";
import { offlineKeys, syncExceptionSchema, type ResolveBody } from "./offline-queries";
import { removeFromOutbox } from "./outbox-repository";
import { syncErrorText } from "./sync-errors";

// The manager's two answers to a parked offline sale. "Ghi sổ lại" posts it now, as the device sent
// it, and may need an owner exception (minimum price, stock); "Bỏ qua giao dịch" closes it without
// a document. Either way the sale then leaves this device's outbox.

/** A parked sale the manager acts on: its server id, label and what "Ghi sổ lại" sends. */
export interface ParkedSaleRef {
  id: string;
  offlineClientId: string;
  label: string;
  body: ResolveBody;
}

/** Codes of a parked sale that was settled meanwhile (by another manager or device). */
const SETTLED_CODES = new Set(["sync_exception_closed", "offline_sale_already_posted"]);

/** Refusals of the exception endpoints in Vietnamese; a settled sale also refreshes the queue. */
function translateSyncError(e: unknown, queryClient: QueryClient): unknown {
  if (!isApiError(e) || e.status !== 409 || e.exceptionKind !== null) return e;
  if (SETTLED_CODES.has(e.code)) void queryClient.invalidateQueries({ queryKey: offlineKeys.exceptions() });
  return new ApiError({ status: e.status, code: e.code, message: syncErrorText(e.code, e.message), fields: { ...e.fields }, retryAfterSeconds: e.retryAfterSeconds });
}

function resolvePrompt(kind: string, sale: ParkedSaleRef): ExceptionPrompt {
  return {
    kind,
    subject: { doc_type: "pos_sync_exception", doc_id: sale.id, doc_version: 0 },
    payload: sale.body,
    title: `Xác nhận ngoại lệ cho ${sale.label}`,
    sub: EXCEPTION_SUB,
  };
}

/** Drops the settled sale from this device; a failure is shown, and the next full queue read prunes it. */
async function dropFromDevice(queryClient: QueryClient, tenantId: string | null, offlineClientId: string): Promise<void> {
  try {
    await removeFromOutbox(offlineClientId);
  } catch (e) {
    toast.error(`Không cập nhật được giao dịch ngoại tuyến trên máy: ${errorMessage(e)}`);
    return;
  }
  if (tenantId !== null) await queryClient.invalidateQueries({ queryKey: offlineKeys.outbox(tenantId) });
}

/** "Ghi sổ lại": POST /pos/sync-exceptions/{id}:resolve with the sale as parked. */
export function useResolveSyncException(tenantId: string | null, after: () => void) {
  const client = useApiClient();
  const queryClient = useQueryClient();
  const confirm = useConfirmException();
  return useCommit({
    mutationFn: async (sale: ParkedSaleRef, { idempotencyKey }) => {
      try {
        return await withExceptions(
          (ids) => client.post(`/pos/sync-exceptions/${sale.id}:resolve`, withExceptionIds(sale.body, ids), { idempotencyKey, schema: posSaleSchema }),
          confirm,
          (kind) => resolvePrompt(kind, sale),
        );
      } catch (e) {
        throw translateSyncError(e, queryClient);
      }
    },
    success: (posted, sale) => withDoc(`Đã ghi sổ ${sale.label} thành `, posted.sales_document.doc_no, "."),
    invalidate: POSTING_KEYS,
    after: (_posted, sale) => {
      void dropFromDevice(queryClient, tenantId, sale.offlineClientId);
      after();
    },
  });
}

/** "Bỏ qua giao dịch": POST /pos/sync-exceptions/{id}:discard; no document is created. */
export function useDiscardSyncException(tenantId: string | null, after: () => void) {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useCommit({
    mutationFn: async (sale: ParkedSaleRef) => {
      try {
        return await client.post(`/pos/sync-exceptions/${sale.id}:discard`, undefined, { schema: syncExceptionSchema });
      } catch (e) {
        throw translateSyncError(e, queryClient);
      }
    },
    success: (_closed, sale) => `Đã bỏ qua ${sale.label}. Giao dịch không được ghi sổ.`,
    invalidate: [offlineKeys.exceptions()],
    after: (_closed, sale) => {
      void dropFromDevice(queryClient, tenantId, sale.offlineClientId);
      after();
    },
  });
}
