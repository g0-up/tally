import { useQuery } from "@tanstack/react-query";
import { useApiClient } from "../../../shared/api/api-context";
import { listOf } from "../../../shared/api/decimal-schemas";
import { useCursorList } from "../../../shared/hooks/use-cursor-list";
import type { ComboAdapter } from "../../../shared/ui/combobox-pools";
import { contactSchema, supplierListItemSchema } from "../schemas";

// Read side of the supplier screens. Every key starts with "partners" so a supplier write
// refreshes the list and the pickers with one prefix.

export const supplierKeys = {
  all: ["partners", "suppliers"] as const,
  list: (q: string) => ["partners", "suppliers", "list", q] as const,
  contacts: (partyId: string) => ["partners", "contacts", partyId] as const,
};

/** Most suppliers one list page holds; the status chips count the loaded rows. */
const SUPPLIER_PAGE = 200;

/** `GET /suppliers?q=` with trading activity; both statuses, filtered by the chips on the client. */
export function useSupplierList(q: string) {
  return useCursorList({ queryKey: supplierKeys.list(q), path: "/suppliers", item: supplierListItemSchema, params: { q }, limit: SUPPLIER_PAGE });
}

/** Candidates one picker search returns. */
const PICKER_LIMIT = 50;

/** Combobox `source` over trading suppliers (`GET /suppliers?q=`); stopped suppliers are hidden from pickers. */
export const activeSupplierSource: { key: string } & ComboAdapter = {
  key: "partners-suppliers-active",
  search: async ({ client, query, signal }) => {
    const r = await client.get("/suppliers", { query: { q: query.trim(), status: "active", limit: PICKER_LIMIT }, schema: listOf(supplierListItemSchema), signal });
    return r.data
      .filter((s) => s.status === "active")
      .map((s) => ({ id: s.id, name: s.name, detail: [s.phone, s.supplied_goods_note].filter(Boolean).join(" · "), ...(s.phone ? { keywords: [s.phone] } : {}) }));
  },
};

/** Contacts of a partner; the first primary one is the supplier's "Người liên hệ". */
export function usePartyContacts(partyId: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: supplierKeys.contacts(partyId ?? ""),
    queryFn: ({ signal }) => client.get(`/parties/${partyId}/contacts`, { schema: listOf(contactSchema), signal }),
    enabled: partyId !== null,
    select: (r) => r.data,
  });
}
