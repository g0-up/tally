import { useQuery } from "@tanstack/react-query";
import { z } from "zod";
import { useApiClient } from "../../../shared/api/api-context";
import { moneySchema } from "../../../shared/api/decimal-schemas";
import { planSchema } from "../../../shared/api/endpoints";
import type { ResponseOf, S } from "./contract";
import { settingsKeys } from "./tenant-settings";

// Plans, plan changes and SaaS invoices on /settings/subscription. The plan and subscription
// contract, and the GET /subscription query, are the shell's (shared/api); plan terms come from the
// API and nothing on this screen decides what a plan allows.

export const INVOICE_PURPOSES = ["new_period", "renewal", "plan_change", "addon"] as const satisfies readonly S["SaasInvoicePurpose"][];

/** The fields of a SaaS invoice the screen reads. */
type SaasInvoiceRead = Omit<S["SaasInvoice"], "subscription_id" | "provider" | "provider_ref">;

export const planChangeQuoteSchema = z.object({
  plan: planSchema,
  purpose: z.enum(INVOICE_PURPOSES),
  period_start: z.string(),
  period_end: z.string(),
  amount: moneySchema,
  proration_note: z.string(),
  terms: z.string(),
}) satisfies z.ZodType<Omit<S["PlanChangeQuote"], "plan"> & { plan: z.infer<typeof planSchema> }>;
export type PlanChangeQuote = z.infer<typeof planChangeQuoteSchema>;

export const saasInvoiceSchema = z.object({
  id: z.string(),
  number: z.string(),
  plan_id: z.string().nullable(),
  purpose: z.enum(INVOICE_PURPOSES),
  period_start: z.string(),
  period_end: z.string(),
  amount: moneySchema,
  proration_note: z.string().nullable(),
  status: z.enum(["open", "paid", "void"]),
  issued_at: z.string(),
  paid_at: z.string().nullable(),
}) satisfies z.ZodType<SaasInvoiceRead>;
export type SaasInvoice = z.infer<typeof saasInvoiceSchema>;

export const paymentInstructionsSchema = z.object({ invoice: saasInvoiceSchema, provider: z.string(), instructions: z.string() }) satisfies z.ZodType<
  Omit<ResponseOf<"paySaasInvoice">, "invoice"> & { invoice: SaasInvoiceRead }
>;
export type PaymentInstructions = z.infer<typeof paymentInstructionsSchema>;

export function usePlans() {
  const client = useApiClient();
  return useQuery({
    queryKey: settingsKeys.plans(),
    queryFn: ({ signal }) => client.get("/plans", { schema: z.object({ data: z.array(planSchema) }), signal }),
    select: (r) => r.data,
  });
}

/** Quote of a move to another plan; changes nothing on the server. */
export function usePlanChangeQuote(planId: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: settingsKeys.planPreview(planId ?? ""),
    queryFn: ({ signal }) => client.post("/subscription:preview-change", { plan_id: planId }, { schema: planChangeQuoteSchema, signal, silent: true }),
    enabled: planId !== null,
    retry: false,
    staleTime: 0,
  });
}

export function useSaasInvoices() {
  const client = useApiClient();
  return useQuery({
    queryKey: settingsKeys.invoices(),
    queryFn: ({ signal }) => client.get("/saas-invoices", { schema: z.object({ data: z.array(saasInvoiceSchema) }), signal }),
    select: (r) => r.data,
  });
}
