import { useQuery } from "@tanstack/react-query";
import { z } from "zod";
import { useApiClient } from "../../../shared/api/api-context";
import type { S } from "./contract";
import { settingsKeys } from "./tenant-settings";

// Support tickets and the time-boxed, read-only support sessions the owner grants on a ticket.

export const TICKET_STATUSES = ["open", "waiting_customer", "in_progress", "resolved", "closed"] as const satisfies readonly S["SupportTicket"]["status"][];
export const TICKET_PRIORITIES = ["low", "normal", "high", "urgent"] as const satisfies readonly S["SupportTicket"]["priority"][];
export type TicketPriority = (typeof TICKET_PRIORITIES)[number];

export const SUPPORT_VIEW_MODULES = ["catalog", "partners", "inventory", "procurement", "sales", "debt_cash", "kits", "campaign", "crm", "reports", "settings"] as const satisfies readonly S["SupportViewModule"][];
export type SupportViewModule = (typeof SUPPORT_VIEW_MODULES)[number];

/** Hours of the grant offered on the screen (the API allows 1..8). */
export const SUPPORT_GRANT_HOURS = 2;
export const TICKET_SUBJECT_MAX = 200;
export const TICKET_BODY_MAX = 10000;

/** The API's refusal of a message that looks like it carries a password or a one-time code. */
export const POSSIBLE_PASSWORD = "possible_password";

export const supportTicketSchema = z.object({
  id: z.string(),
  number: z.string(),
  subject: z.string(),
  status: z.enum(TICKET_STATUSES),
  priority: z.enum(TICKET_PRIORITIES),
  sla_due_at: z.string().nullable(),
  assigned_admin_id: z.string().nullable(),
  created_at: z.string(),
  updated_at: z.string(),
}) satisfies z.ZodType<S["SupportTicket"]>;
export type SupportTicket = z.infer<typeof supportTicketSchema>;

export const ticketMessageSchema = z.object({
  id: z.string(),
  author_type: z.enum(["owner", "platform_admin"]),
  body: z.string(),
  created_at: z.string(),
}) satisfies z.ZodType<Omit<S["SupportTicketMessage"], "author_id" | "attachment_ids">>;
export type TicketMessage = z.infer<typeof ticketMessageSchema>;

export const supportTicketViewSchema = supportTicketSchema.extend({ messages: z.array(ticketMessageSchema) }) satisfies z.ZodType<
  Omit<S["SupportTicketView"], "messages"> & { messages: z.infer<typeof ticketMessageSchema>[] }
>;
export type SupportTicketView = z.infer<typeof supportTicketViewSchema>;

export const supportSessionSchema = z.object({
  id: z.string(),
  ticket_id: z.string().nullable(),
  scope: z.literal("read_only"),
  modules: z.array(z.enum(SUPPORT_VIEW_MODULES)),
  state: z.enum(["active", "expired", "revoked"]),
  granted_at: z.string(),
  expires_at: z.string(),
  revoked_at: z.string().nullable(),
}) satisfies z.ZodType<Omit<S["SupportSession"], "tenant_id" | "admin_id">>;
export type SupportSession = z.infer<typeof supportSessionSchema>;

/** A ticket support can still be granted on: not finished and assigned to an agent. */
export function grantableTicket(t: SupportTicket): boolean {
  return t.status !== "resolved" && t.status !== "closed" && t.assigned_admin_id !== null;
}

export function useSupportTickets() {
  const client = useApiClient();
  return useQuery({
    queryKey: settingsKeys.supportTickets(),
    queryFn: ({ signal }) => client.get("/support-tickets", { schema: z.object({ data: z.array(supportTicketSchema) }), signal }),
    select: (r) => r.data,
  });
}

export function useSupportTicket(id: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: settingsKeys.supportTicket(id ?? ""),
    queryFn: ({ signal }) => client.get(`/support-tickets/${encodeURIComponent(id ?? "")}`, { schema: supportTicketViewSchema, signal }),
    enabled: id !== null,
  });
}

export function useSupportSessions() {
  const client = useApiClient();
  return useQuery({
    queryKey: settingsKeys.supportSessions(),
    queryFn: ({ signal }) => client.get("/support-sessions", { schema: z.object({ data: z.array(supportSessionSchema) }), signal }),
    select: (r) => r.data,
  });
}
