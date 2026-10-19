import { useQueryClient } from "@tanstack/react-query";
import { createElement, Fragment, useRef, type ReactNode } from "react";
import { useApiClient } from "../../../shared/api/api-context";
import type { HttpClient } from "../../../shared/api/http";
import { createOnce, newCreateState, useUnsettled } from "../../../shared/hooks/create-once";
import { payloadHash } from "../../../shared/lib/canonical-hash";
import { runFollowUps, useCommit } from "../../../shared/hooks/use-commit";
import { etagOf } from "../../catalog";
import { consentChanges, consentRequest, customerFormErrors, customerRequest, withCrmMessage, type CustomerDraft, type CustomerErrors } from "../customer-form-values";
import { customerResultSchema, reorderDraftSchema, type ConsentChannel, type Customer, type CustomerResult, type ReorderDraft } from "../schemas";
import { crmKeys } from "./queries";

// Write side of the Khách hàng (CRM) screen. Saving a customer is the customer itself, then one
// consent record per changed channel; each finished step is remembered so a retry after a refused
// consent does not add the customer twice. The creates (customer, consent, re-order draft) take
// the intent's Idempotency-Key, so a retry after a lost answer replays what was stored; only the
// customer update takes If-Match (OpenAPI).

/**
 * Every list that shows a customer's name, group, limit, terms or consents (the customer list
 * carries them); the saved customer's consents are refreshed by the save itself, even when a
 * consent step was refused.
 */
const CUSTOMER_KEYS = [crmKeys.customers, ["sales"], ["debt"], ["partners"]] as const;

type CustomerBody = ReturnType<typeof customerRequest>;

/** Writes the form onto a stored customer, from the version it was read or created at. */
const updateCustomer = (client: HttpClient, known: Pick<Customer, "id" | "version">, b: CustomerBody) =>
  client.patch(`/customers/${known.id}`, b, { ifMatch: etagOf(known.version), schema: customerResultSchema });

export interface SaveCustomerOptions {
  /** Null adds a customer. */
  customer: Customer | null;
  /** Channels with a current marketing consent, as the form opened. */
  granted: readonly ConsentChannel[];
  setErrors: (errors: CustomerErrors) => void;
  success: (saved: CustomerResult) => ReactNode;
  after: () => void;
}

/** "Thêm khách hàng" / "Sửa khách hàng": the customer, then its consent changes. */
export function useSaveCustomer({ customer, granted, setErrors, success, after }: SaveCustomerOptions) {
  const client = useApiClient();
  const queryClient = useQueryClient();
  const saved = useRef<CustomerResult | null>(null);
  const create = useRef(newCreateState<CustomerBody, CustomerResult>());
  const done = useRef(new Set<string>());
  const lost = useUnsettled();

  const commit = useCommit({
    mutationFn: async (d: CustomerDraft, { idempotencyKey }) => {
      try {
        const body = customerRequest(d);
        const update = (known: Pick<Customer, "id" | "version">, b: CustomerBody) => updateCustomer(client, known, b);
        const known = saved.current ?? customer;
        const result =
          known === null
            ? (
                await lost.watch(create.current, () =>
                  createOnce(create.current, body, { send: (b) => client.post("/customers", b, { idempotencyKey, schema: customerResultSchema }), update }),
                )
              ).result
            : await update(known, body);
        saved.current = result;
        const steps = consentChanges(d, granted).map((change) => {
          const step = `${change.channel}-${String(change.granted)}`;
          return {
            id: step,
            label: change.granted ? "ghi đồng ý nhận tin" : "ghi rút đồng ý nhận tin",
            run: async () => {
              const consent = consentRequest(change, d.consentSource);
              // The key carries the body's hash: a source edited after a lost answer is another record, not a reused key (409).
              const key = `${idempotencyKey}:consent-${step}-${(await payloadHash(consent)).slice(0, 16)}`;
              return client.post<unknown>(`/customers/${result.id}/consents`, consent, { idempotencyKey: key });
            },
          };
        });
        try {
          await runFollowUps(steps, done.current, (missing, reason) => `Đã lưu ${result.name} nhưng chưa ${missing}: ${reason}`, withCrmMessage);
        } finally {
          if (steps.length > 0) void queryClient.invalidateQueries({ queryKey: crmKeys.consents(result.id) });
        }
        return result;
      } catch (e) {
        const fields = customerFormErrors(e);
        if (Object.keys(fields).length > 0) setErrors(fields);
        throw withCrmMessage(e);
      }
    },
    success,
    invalidate: CUSTOMER_KEYS,
    after,
  });
  return { ...commit, unsettled: lost.unsettled };
}

export interface QuickCustomerOptions {
  setErrors: (errors: CustomerErrors) => void;
  success: (saved: CustomerResult) => ReactNode;
  after: (saved: CustomerResult) => void;
}

/**
 * A customer added from another screen with the quick fields only, so there is no consent to
 * record. The create is sent once under the intent's Idempotency-Key, like the full dialog's: a
 * body whose answer was lost goes out again first, and edits made since are written onto the
 * customer it stored.
 */
export function useQuickCustomer({ setErrors, success, after }: QuickCustomerOptions) {
  const client = useApiClient();
  const create = useRef(newCreateState<CustomerBody, CustomerResult>());
  const lost = useUnsettled();
  const commit = useCommit({
    mutationFn: async (d: CustomerDraft, { idempotencyKey }) => {
      try {
        const made = await lost.watch(create.current, () =>
          createOnce(create.current, customerRequest(d), {
            send: (b) => client.post("/customers", b, { idempotencyKey, schema: customerResultSchema }),
            update: (known, b) => updateCustomer(client, known, b),
          }),
        );
        return made.result;
      } catch (e) {
        const fields = customerFormErrors(e);
        if (Object.keys(fields).length > 0) setErrors(fields);
        throw withCrmMessage(e);
      }
    },
    success,
    invalidate: CUSTOMER_KEYS,
    after,
  });
  return { ...commit, unsettled: lost.unsettled };
}

/** Lines of the earlier purchase the draft could not take (no longer sold, or a pack to choose again). */
export function skippedLines(r: ReorderDraft): number {
  return r.lines.filter((l) => !l.copied).length;
}

/** "Tái đặt đơn": a draft invoice repeating the customer's latest purchase at today's prices. */
export function useReorder(after: (r: ReorderDraft) => void) {
  const client = useApiClient();
  return useCommit({
    // One key per customer within the intent: the server binds a key to the request path.
    mutationFn: async (c: Customer, { idempotencyKey }) => {
      try {
        return await client.post(`/customers/${c.id}/reorder-drafts`, undefined, { idempotencyKey: `${idempotencyKey}:${c.id}`, schema: reorderDraftSchema });
      } catch (e) {
        throw withCrmMessage(e);
      }
    },
    success: (r) => {
      const skipped = skippedLines(r);
      return createElement(
        Fragment,
        null,
        "Đã nạp dòng hàng từ ",
        createElement("b", null, r.source_doc_no),
        " vào ",
        createElement("b", null, r.invoice_doc_no),
        " với giá hiện hành. Kiểm tra lại trước khi xác nhận.",
        skipped > 0 ? ` ${skipped} dòng không chép được (ngừng bán hoặc cần chọn lại).` : null,
      );
    },
    invalidate: [["sales"]],
    after,
  });
}
