import { useQuery } from "@tanstack/react-query";
import { z } from "zod";
import { useApiClient } from "../../../shared/api/api-context";
import { listOf } from "../../../shared/api/decimal-schemas";
import type { ResponseOf, S } from "./contract";
import { settingsKeys } from "./tenant-settings";

// The owner's account, devices, store profile, period lock, full data export and account closure
// on /settings/account. Responses are validated in the shapes OpenAPI documents; the platform
// session query only reads the few fields the shell needs, so these screens read their own /me.
// The store profile is the shell's GET /tenant query (`useTenant`).

export const sessionSchema = z.object({
  id: z.string(),
  device_label: z.string(),
  ip: z.string().nullable(),
  created_at: z.string(),
  last_seen_at: z.string(),
  expires_at: z.string(),
  current: z.boolean(),
}) satisfies z.ZodType<Omit<S["Session"], "mfa_verified" | "reauthenticated_at">>;
export type Session = z.infer<typeof sessionSchema>;

export const ownerSchema = z.object({
  user: z.object({
    id: z.string(),
    email: z.string(),
    full_name: z.string(),
    status: z.enum(["pending", "active", "disabled"]),
    mfa_enabled: z.boolean(),
    last_login_at: z.string().nullable(),
  }),
  tenant: z.object({ id: z.string(), slug: z.string(), display_name: z.string(), is_sandbox: z.boolean() }),
  session: sessionSchema,
}) satisfies z.ZodType<{
  user: Omit<S["Me"]["user"], "phone" | "email_verified_at">;
  tenant: Omit<S["Me"]["tenant"], "status" | "sandbox_of">;
  session: z.infer<typeof sessionSchema>;
}>;
export type Owner = z.infer<typeof ownerSchema>;

export const totpEnrolmentSchema = z.object({ secret: z.string(), otpauth_uri: z.string() }) satisfies z.ZodType<ResponseOf<"enrolTOTP">>;
export type TotpEnrolment = z.infer<typeof totpEnrolmentSchema>;
export const recoveryCodesSchema = z.object({ recovery_codes: z.array(z.string()) }) satisfies z.ZodType<S["RecoveryCodes"]>;

/** POST /auth/password and POST /sessions:revoke-all: how many other sessions were signed out. */
export const revokedSessionsSchema = z.object({ revoked: z.number().int() }) satisfies z.ZodType<ResponseOf<"changePassword"> & ResponseOf<"revokeOtherSessions">>;

/** Fields PATCH /tenant accepts; an empty opening_cutoff_date clears it. */
export interface StoreProfilePatch {
  legal_name?: string;
  display_name?: string;
  tax_code?: string | null;
  address?: string | null;
  phone?: string | null;
  opening_cutoff_date?: string;
}

/** Vietnamese tax code: 10 digits, or 10 digits and a 3-digit branch suffix. */
export const TAX_CODE_PATTERN = /^[0-9]{10}(-[0-9]{3})?$/;

export const periodLockEventSchema = z.object({
  id: z.string(),
  action: z.enum(["lock", "reopen"]),
  locked_through: z.string().nullable(),
  reason: z.string().nullable(),
  actor_id: z.string(),
  created_at: z.string(),
}) satisfies z.ZodType<S["PeriodLockEvent"]>;
export type PeriodLockEvent = z.infer<typeof periodLockEventSchema>;

export const periodLocksSchema = z.object({
  locked_through: z.string().nullable(),
  /** Number of lock events; a period_reopen confirmation is bound to it. */
  version: z.number().int(),
  data: z.array(periodLockEventSchema),
  next_cursor: z.string().nullable(),
}) satisfies z.ZodType<ResponseOf<"listPeriodLocks">>;
export type PeriodLocks = z.infer<typeof periodLocksSchema>;

/** Most recent lock events shown under the lock form; older ones stay in the audit log. */
export const PERIOD_LOCK_HISTORY_LIMIT = 50;
export const REOPEN_REASON_MAX = 500;

export interface ReopenBody {
  locked_through: string | null;
  reason: string;
}

export const dataExportSchema = z.object({ id: z.string(), status: z.literal("queued"), created_at: z.string() }) satisfies z.ZodType<S["DataExport"]>;
export type DataExport = z.infer<typeof dataExportSchema>;

export const EXPORT_STATUSES = ["queued", "running", "succeeded", "failed", "expired", "cancelled"] as const;
export type ExportStatus = (typeof EXPORT_STATUSES)[number];

/** GET /export-jobs/{id} as a full data export reads it (its report_key is empty). */
export const fullExportJobSchema = z.object({
  id: z.string(),
  kind: z.enum(["report_csv", "full_data_export"]),
  status: z.enum(EXPORT_STATUSES),
  error: z.string().nullable(),
  created_at: z.string(),
  finished_at: z.string().nullable(),
  expires_at: z.string().nullable(),
  download_path: z.string().nullable(),
}) satisfies z.ZodType<Omit<S["ExportJob"], "report_key" | "params" | "row_count" | "started_at">>;
export type FullExportJob = z.infer<typeof fullExportJobSchema>;

/** How often a queued or running export is read again. */
export const EXPORT_POLL_MS = 3000;

export const CLOSURE_STATUSES = ["requested", "verified", "waiting", "exporting", "export_failed", "completed", "cancelled"] as const;
export type ClosureStatus = (typeof CLOSURE_STATUSES)[number];
/** The owner may still cancel before the final export starts. */
export const CANCELLABLE_CLOSURE: ReadonlySet<ClosureStatus> = new Set(["requested", "verified", "waiting"]);

export const closureRequestSchema = z.object({
  id: z.string(),
  status: z.enum(CLOSURE_STATUSES),
  verified_at: z.string().nullable(),
  export_job_id: z.string().nullable(),
  waiting_until: z.string(),
  legal_hold: z.boolean(),
  created_at: z.string(),
}) satisfies z.ZodType<S["AccountClosureRequest"]>;
export type ClosureRequest = z.infer<typeof closureRequestSchema>;

export const closureViewSchema = z.object({
  request: closureRequestSchema.nullable(),
  export: z
    .object({
      id: z.string(),
      status: z.enum(EXPORT_STATUSES),
      expires_at: z.string().nullable(),
      final: z.boolean(),
      failed_attempts: z.number().int(),
    })
    .nullable(),
}) satisfies z.ZodType<S["AccountClosureView"]>;
export type ClosureView = z.infer<typeof closureViewSchema>;

/** Final exports that may fail before the closure stops at export_failed. */
export const CLOSURE_EXPORT_ATTEMPTS = 3;

export function useOwner() {
  const client = useApiClient();
  return useQuery({
    queryKey: settingsKeys.owner(),
    queryFn: ({ signal }) => client.get("/me", { schema: ownerSchema, signal }),
  });
}

export function useSessions() {
  const client = useApiClient();
  return useQuery({
    queryKey: settingsKeys.sessions(),
    queryFn: ({ signal }) => client.get("/sessions", { schema: z.object({ data: z.array(sessionSchema) }), signal }),
    select: (r) => r.data,
  });
}

export function usePeriodLocks() {
  const client = useApiClient();
  return useQuery({
    queryKey: settingsKeys.periodLocks(),
    queryFn: ({ signal }) => client.get("/period-locks", { query: { limit: PERIOD_LOCK_HISTORY_LIMIT }, schema: periodLocksSchema, signal }),
  });
}

/** Exports read to find the last full data export again; one older than this many newer ones is not shown. */
const RECENT_EXPORTS = 50;

/** A full data export worth showing again: still being written, or written and downloadable. */
const resumable = (j: FullExportJob) =>
  j.kind === "full_data_export" && (j.status === "queued" || j.status === "running" || (j.status === "succeeded" && j.download_path !== null));

/**
 * Id of the signed-in user's newest full data export still running or downloadable
 * (`GET /export-jobs`, newest first), so a reload finds it again. Silent: the card works the same
 * when this read fails, it just shows no earlier export.
 */
export function useRecentFullExport(enabled: boolean) {
  const client = useApiClient();
  return useQuery({
    queryKey: settingsKeys.recentExports(),
    queryFn: ({ signal }) => client.get("/export-jobs", { query: { limit: RECENT_EXPORTS }, schema: listOf(fullExportJobSchema), signal, silent: true }),
    select: (r) => r.data.find(resumable)?.id ?? null,
    enabled,
    retry: false,
    refetchOnWindowFocus: false,
  });
}

/** Follows one full data export until it finishes. */
export function useFullExportJob(id: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: settingsKeys.exportJob(id ?? ""),
    queryFn: ({ signal }) => client.get(`/export-jobs/${encodeURIComponent(id ?? "")}`, { schema: fullExportJobSchema, signal, silent: true }),
    enabled: id !== null,
    refetchInterval: (query) => {
      const status = query.state.data?.status;
      return status === "queued" || status === "running" ? EXPORT_POLL_MS : false;
    },
  });
}

export function useAccountClosure() {
  const client = useApiClient();
  return useQuery({
    queryKey: settingsKeys.closure(),
    queryFn: ({ signal }) => client.get("/account-closure", { schema: closureViewSchema, signal }),
  });
}
