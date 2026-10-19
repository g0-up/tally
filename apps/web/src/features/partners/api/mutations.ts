import { useRef, type ReactNode } from "react";
import { useApiClient } from "../../../shared/api/api-context";
import { listOf } from "../../../shared/api/decimal-schemas";
import type { HttpClient } from "../../../shared/api/http";
import { useCommit } from "../../../shared/hooks/use-commit";
import { contactSchema, supplierListItemSchema, supplierResultSchema, type Contact, type Supplier, type SupplierResult } from "../schemas";
import {
  duplicateSupplierErrors,
  supplierCreateRequest,
  supplierFormErrors,
  supplierUpdateRequest,
  withSupplierMessage,
  type SupplierDraft,
  type SupplierErrors,
} from "../supplier-form-values";
import { supplierKeys } from "./queries";

// Write side of the supplier screens. Saving a supplier is up to three requests (the supplier,
// its primary contact, the customer role on the same partner); each finished step is remembered so
// a retry after a refused later step does not create the supplier twice.

/** Etag of a partner version. */
const etagOf = (version: number) => `"${version}"`;

/** Rows the duplicate check compares against: suppliers matching the typed name or phone. */
async function suppliersLike(client: HttpClient, d: SupplierDraft, signal?: AbortSignal) {
  const terms = [d.name.trim(), d.phone.trim()].filter((t) => t !== "");
  const pages = await Promise.all(terms.map((q) => client.get("/suppliers", { query: { q, limit: 50 }, schema: listOf(supplierListItemSchema), ...(signal ? { signal } : {}) })));
  return pages.flatMap((p) => p.data);
}

/** Makes the primary contact carry the typed name: rename it, add it, or remove it when cleared. */
async function syncPrimaryContact(client: HttpClient, partyId: string, name: string, current: Contact | null) {
  const typed = name.trim();
  if (current === null) {
    if (typed !== "") await client.post(`/parties/${partyId}/contacts`, { name: typed, is_primary: true }, { schema: contactSchema });
    return;
  }
  if (typed === current.name) return;
  if (typed === "") await client.delete<unknown>(`/parties/${partyId}/contacts/${current.id}`);
  else await client.patch(`/parties/${partyId}/contacts/${current.id}`, { name: typed }, { schema: contactSchema });
}

export interface SaveSupplierOptions {
  /** Null adds a supplier. */
  supplier: Supplier | null;
  /** Current primary contact of the edited supplier. */
  contact: Contact | null;
  /** The quick form (name, phone, contact, terms) of the purchase documents. */
  quick?: boolean;
  setErrors: (errors: SupplierErrors) => void;
  success: (saved: SupplierResult) => ReactNode;
  after: (saved: SupplierResult) => void;
}

/** Prototype saveSupplier: duplicate check, supplier, primary contact, customer role. */
export function useSaveSupplier({ supplier, contact, quick = false, setErrors, success, after }: SaveSupplierOptions) {
  const client = useApiClient();
  const saved = useRef<SupplierResult | null>(null);
  const contactDone = useRef(false);

  return useCommit({
    mutationFn: async (d: SupplierDraft, { idempotencyKey }) => {
      try {
        const known = saved.current ?? supplier;
        const dup = duplicateSupplierErrors(d, await suppliersLike(client, d), known?.id ?? null);
        if (dup.name !== undefined || dup.phone !== undefined) {
          setErrors(dup);
          throw new Error(dup.name ?? dup.phone);
        }
        const result =
          known === null
            ? await client.post("/suppliers", supplierCreateRequest(d, quick), { idempotencyKey, schema: supplierResultSchema })
            : await client.patch(`/suppliers/${known.id}`, quick ? supplierCreateRequest(d, true) : supplierUpdateRequest(d), { ifMatch: etagOf(known.version), schema: supplierResultSchema });
        saved.current = result;
        if (!contactDone.current) {
          await syncPrimaryContact(client, result.id, d.contact, contact);
          contactDone.current = true;
        }
        if (d.alsoCustomer && !result.is_customer) {
          await client.post<unknown>("/customers", { party_id: result.id }, { idempotencyKey: `${idempotencyKey}-customer` });
          return { ...result, is_customer: true };
        }
        return result;
      } catch (e) {
        const fields = supplierFormErrors(e);
        if (Object.keys(fields).length > 0) setErrors(fields);
        throw e;
      }
    },
    success,
    invalidate: [supplierKeys.all, ["partners"]],
    after,
  });
}

/** "Ngừng" / "Mở lại" of a supplier row; `after` runs once the server accepted it. */
export function useSupplierStatus(after?: () => void) {
  const client = useApiClient();
  return useCommit({
    mutationFn: async ({ supplier, on }: { supplier: Supplier; on: boolean }, { idempotencyKey }) => {
      try {
        return await client.post<unknown>(`/suppliers/${supplier.id}:${on ? "reopen" : "stop"}`, undefined, { idempotencyKey });
      } catch (e) {
        throw withSupplierMessage(e);
      }
    },
    success: (_, { on }) => (on ? "Đã mở lại giao dịch." : "Đã ngừng giao dịch. NCC không còn hiện khi tạo đơn nhập."),
    invalidate: [supplierKeys.all],
    ...(after ? { after } : {}),
  });
}

/** supDel: deletes a supplier with no documents. */
export function useDeleteSupplier(supplier: Supplier, after: () => void) {
  const client = useApiClient();
  return useCommit<unknown>({
    mutationFn: async () => {
      try {
        return await client.delete<unknown>(`/suppliers/${supplier.id}`);
      } catch (e) {
        throw withSupplierMessage(e);
      }
    },
    success: () => "Đã xóa nhà cung cấp.",
    invalidate: [supplierKeys.all],
    after,
  });
}
