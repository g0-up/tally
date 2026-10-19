import { useQuery } from "@tanstack/react-query";
import { z } from "zod";
import { useApiClient } from "../../../shared/api/api-context";
import { moneySchema } from "../../../shared/api/decimal-schemas";
import type { S } from "./contract";

// Store settings (GET/PATCH /tenant/settings). A change sends only the fields it edits, under
// If-Match with the version the screen read; a stale version answers 412 and the shared handler
// asks to reload.

/** Query keys of every settings screen; invalidating ["settings"] refreshes them all. */
export const settingsKeys = {
  tenant: () => ["settings", "tenant"] as const,
  owner: () => ["settings", "owner"] as const,
  sessions: () => ["settings", "sessions"] as const,
  periodLocks: () => ["settings", "period-locks"] as const,
  exportJob: (id: string) => ["settings", "export-job", id] as const,
  recentExports: () => ["settings", "recent-exports"] as const,
  closure: () => ["settings", "account-closure"] as const,
  plans: () => ["settings", "plans"] as const,
  planPreview: (planId: string) => ["settings", "plan-preview", planId] as const,
  invoices: () => ["settings", "saas-invoices"] as const,
  supportSessions: () => ["settings", "support-sessions"] as const,
  supportTickets: () => ["settings", "support-tickets"] as const,
  supportTicket: (id: string) => ["settings", "support-tickets", id] as const,
  auditEvents: () => ["settings", "audit-events"] as const,
};

/** Bounds of "Số giờ tối đa không đồng bộ" accepted by the API. */
export const OFFLINE_HOURS_MIN = 1;
export const OFFLINE_HOURS_MAX = 72;

export const tenantSettingsSchema = z.object({
  offline_pos_enabled: z.boolean(),
  offline_pos_max_amount: moneySchema,
  offline_pos_max_hours: z.number().int(),
  updated_at: z.string(),
  version: z.number().int(),
}) satisfies z.ZodType<Pick<S["Settings"], "offline_pos_enabled" | "offline_pos_max_amount" | "offline_pos_max_hours" | "updated_at" | "version">>;
export type TenantSettings = z.infer<typeof tenantSettingsSchema>;

export interface OfflinePolicyPatch {
  offline_pos_enabled: boolean;
  offline_pos_max_amount: string;
  offline_pos_max_hours: number;
}

export function useTenantSettings() {
  const client = useApiClient();
  return useQuery({
    queryKey: settingsKeys.tenant(),
    queryFn: ({ signal }) => client.get("/tenant/settings", { schema: tenantSettingsSchema, signal }),
  });
}
