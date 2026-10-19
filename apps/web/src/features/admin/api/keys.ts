import { adminKeys } from "../../../shared/api/query-keys";

// Query keys of the admin console, all under adminKeys.all (["admin"]), so signing out drops them
// with the session probe and nothing of one admin's session is shown to the next. Cursor-paged lists
// append their filters and page size to the list key (useCursorList).

export const consoleKeys = {
  all: adminKeys.all,
  plans: () => ["admin", "plans"] as const,
  tenants: () => ["admin", "tenants"] as const,
  tenantList: () => ["admin", "tenants", "list"] as const,
  tenant: (id: string) => ["admin", "tenants", "detail", id] as const,
  admins: () => ["admin", "admins"] as const,
  usage: () => ["admin", "usage"] as const,
  invoices: () => ["admin", "invoices"] as const,
  invoiceList: () => ["admin", "invoices", "list"] as const,
  flags: () => ["admin", "feature-flags"] as const,
  flagChanges: (key: string) => ["admin", "feature-flags", "changes", key] as const,
  incidents: () => ["admin", "incidents"] as const,
  partners: () => ["admin", "partners"] as const,
  referrals: (partnerId: string) => ["admin", "partners", "referrals", partnerId] as const,
  tickets: () => ["admin", "support", "tickets"] as const,
  ticketList: () => ["admin", "support", "tickets", "list"] as const,
  ticket: (id: string) => ["admin", "support", "tickets", "detail", id] as const,
  sessions: () => ["admin", "support", "sessions"] as const,
  supportView: (tenantId: string, module: string, path: string) => ["admin", "support", "view", tenantId, module, path] as const,
};
