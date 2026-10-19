import { z } from "zod";
import type { components } from "../../shared/api/schema";

// Response schema of the notification endpoints (OpenAPI is the authority). Only the fields the
// screen uses are validated; unknown fields are dropped. The kind stays a plain string so a kind
// added on the server later shows under its own chip instead of failing the whole list.

type S = components["schemas"];

export const severitySchema = z.enum(["info", "warn", "bad"]) satisfies z.ZodType<S["Notification"]["severity"]>;
export type Severity = z.infer<typeof severitySchema>;

export const notificationSchema = z.object({
  id: z.string(),
  kind: z.string(),
  severity: severitySchema,
  title: z.string(),
  body: z.string().nullable(),
  entity_type: z.string().nullable(),
  entity_id: z.string().nullable(),
  /** The web route that handles the notification; null when there is nothing to open. */
  action_path: z.string().nullable(),
  /** The document (or lot) the notification is about. */
  doc_no: z.string().nullable(),
  created_at: z.string(),
  read_at: z.string().nullable(),
  resolved_at: z.string().nullable(),
  snoozed_until: z.string().nullable(),
}) satisfies z.ZodType<Pick<S["Notification"], "id" | "severity" | "title" | "body" | "entity_type" | "entity_id" | "action_path" | "doc_no" | "created_at" | "read_at" | "resolved_at" | "snoozed_until">>;
export type Notification = z.infer<typeof notificationSchema>;
