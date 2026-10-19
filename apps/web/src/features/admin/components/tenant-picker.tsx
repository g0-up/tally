import { useMemo } from "react";
import { cursorPageSchema } from "../../../shared/hooks/use-cursor-list";
import { Combobox, type ComboItem } from "../../../shared/ui/combobox";
import type { ComboAdapter } from "../../../shared/ui/combobox-pools";
import { TENANT_SEARCH_MAX_LENGTH } from "../api/queries";
import { tenantSchema } from "../api/schemas";

export const TENANT_PICKER_PLACEHOLDER = "Gõ tên hoặc địa chỉ cửa hàng để tìm…";
const PICKER_LIMIT = 50;
/** Id of the "no store" option (all stores, or the global flag value). */
const NONE = "";
const tenantPageSchema = cursorPageSchema(tenantSchema);

export interface PickedTenant {
  id: string;
  display_name: string;
  slug: string;
}

export interface TenantPickerProps {
  value: PickedTenant | null;
  onChange: (tenant: PickedTenant | null) => void;
  "aria-label": string;
  /** Adds a first option that clears the choice ("Mọi cửa hàng", "Toàn hệ thống"); it is also the placeholder. */
  noneLabel?: string;
  /** Hides sandbox stores, which have no subscription or invoices. */
  billable?: boolean;
  invalid?: boolean;
}

/**
 * Store picker searching `GET /tenants?q=` as the admin types (slug or name), first page only, so
 * it works for any number of stores. The option's second line is the store's slug.
 */
export function TenantPicker({ value, onChange, "aria-label": ariaLabel, noneLabel, billable = false, invalid = false }: TenantPickerProps) {
  const source = useMemo<{ key: string } & ComboAdapter>(
    () => ({
      key: `admin-tenants:${billable ? "billable" : "all"}:${noneLabel ?? ""}`,
      search: async ({ client, query, signal }) => {
        // The picker's box has no length limit, so a longer search is cut to what the API accepts.
        const q = query.trim().slice(0, TENANT_SEARCH_MAX_LENGTH);
        const page = await client.get("/tenants", { query: { q, limit: PICKER_LIMIT }, schema: tenantPageSchema, signal });
        const items: ComboItem[] = page.data.filter((t) => !billable || !t.is_sandbox).map((t) => ({ id: t.id, name: t.display_name, detail: t.slug, keywords: [t.slug] }));
        return noneLabel !== undefined && q === "" ? [{ id: NONE, name: noneLabel }, ...items] : items;
      },
    }),
    [billable, noneLabel],
  );
  return (
    <Combobox
      source={source}
      value={value?.id ?? null}
      {...(value ? { valueLabel: value.display_name } : {})}
      onChange={(item) => onChange(item.id === NONE ? null : { id: item.id, display_name: item.name, slug: item.detail ?? "" })}
      placeholder={noneLabel ?? TENANT_PICKER_PLACEHOLDER}
      aria-label={ariaLabel}
      invalid={invalid}
    />
  );
}
