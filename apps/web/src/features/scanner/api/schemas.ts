import { z } from "zod";
import type { components } from "../../../shared/api/schema";

type S = components["schemas"];

// Response and event schemas of the desktop side of phone scanner sessions (OpenAPI is the
// authority). They validate only the fields the pairing card and the scan queue use.

export const claimCodeSchema = z.object({ claim_code: z.string().min(1), claim_expires_at: z.string() });
export type ClaimCode = z.infer<typeof claimCodeSchema>;

export const sessionCreatedSchema = claimCodeSchema.extend({ id: z.string().min(1) });
export type SessionCreated = z.infer<typeof sessionCreatedSchema>;

export const sessionStateSchema = z.enum(["waiting", "pending", "claimed", "ended"]);
export type SessionState = z.infer<typeof sessionStateSchema>;

export const sessionStatusSchema = z.object({
  state: sessionStateSchema,
  device_label: z.string().optional(),
  verify_code: z.string().optional(),
});
export type SessionStatus = z.infer<typeof sessionStatusSchema>;

export const scanResultSchema = z.object({ outcome: z.string() });

/** Outcomes the desktop reports for a scan. */
export type DesktopOutcome = Extract<S["ScanResult"]["outcome"], "added" | "not_found" | "not_accepting" | "needs_desktop_edit" | "new_product" | "not_in_order">;

/* ---------- events of GET /scanner-sessions/{id}/events ---------- */

export const claimRequestedSchema = z.object({ device_label: z.string().optional(), verify_code: z.string().min(1) });
export type ClaimRequested = z.infer<typeof claimRequestedSchema>;

export const scanEventSchema = z.object({ scan_id: z.string().min(1), code: z.string(), ttl_ms: z.number().int().positive() });
export type ScanEvent = z.infer<typeof scanEventSchema>;

/** Who ended the session: "desktop", "phone", or "system" when the server closed it on its own. */
export const revokedEventSchema = z.object({ by: z.string().optional() });
export type RevokedEvent = z.infer<typeof revokedEventSchema>;
