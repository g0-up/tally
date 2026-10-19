import type { ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useApiClient } from "../../../shared/api/api-context";
import { isApiError } from "../../../shared/api/api-error";
import { useCommit } from "../../../shared/hooks/use-commit";
import { consoleKeys } from "./keys";
import {
  closureViewSchema,
  featureFlagSchema,
  incidentSchema,
  invoiceSchema,
  partnerSchema,
  subscriptionSchema,
  ticketMessageSchema,
  ticketSchema,
  type BodyOf,
  type Ticket,
} from "./schemas";

// Write side of the admin console. Every write goes through useCommit: the button shows the
// pending label, a refusal keeps the dialog open with the translated error, and the success toast
// only follows a 2xx. The admin API takes no Idempotency-Key, so none is sent.

export type ChangePlanBody = BodyOf<"adminChangeTenantPlan">;
export type ExtendBody = BodyOf<"adminExtendTenantSubscription">;
export type SetStatusBody = BodyOf<"adminSetTenantSubscriptionStatus">;
export type IssueInvoiceBody = BodyOf<"adminIssueSaasInvoice">;
export type SetFlagBody = BodyOf<"adminSetFeatureFlag">;
export type CreateIncidentBody = BodyOf<"adminCreateIncident">;
export type UpdateIncidentBody = BodyOf<"adminUpdateIncident">;
export type CreatePartnerBody = BodyOf<"adminCreatePartner">;
export type UpdateTicketBody = BodyOf<"adminUpdateSupportTicket">;

/** Store-facing reads a subscription change touches: the store's row, its detail and the invoices. */
function tenantKeys(tenantId: string) {
  return [consoleKeys.tenants(), consoleKeys.tenant(tenantId), consoleKeys.invoices()] as const;
}

interface Done<T> {
  success: (data: T) => ReactNode;
  after?: (data: T) => void;
  /** Field reasons of a refused write, already in Vietnamese. */
  setFieldError?: (field: string, message: string) => void;
}

export function useChangePlan(tenantId: string, done: Done<unknown>) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (body: ChangePlanBody) => client.post(`/tenants/${tenantId}/subscription:change`, body, { schema: subscriptionSchema }),
    ...done,
    invalidate: tenantKeys(tenantId),
  });
}

export function useExtendSubscription(tenantId: string, done: Done<unknown>) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (body: ExtendBody) => client.post(`/tenants/${tenantId}/subscription:extend`, body, { schema: subscriptionSchema }),
    ...done,
    invalidate: tenantKeys(tenantId),
  });
}

export function useSetSubscriptionStatus(tenantId: string, done: Done<unknown>) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (body: SetStatusBody) => client.post(`/tenants/${tenantId}/subscription:set-status`, body, { schema: subscriptionSchema }),
    ...done,
    invalidate: tenantKeys(tenantId),
  });
}

export function useRetryClosureExport(tenantId: string, done: Done<unknown>) {
  const client = useApiClient();
  return useCommit({
    mutationFn: () => client.post(`/tenants/${tenantId}/account-closure:retry-export`, undefined, { schema: closureViewSchema }),
    ...done,
    invalidate: [consoleKeys.tenant(tenantId)],
  });
}

export function useIssueInvoice(done: Done<{ number: string }>) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (body: IssueInvoiceBody) => client.post("/saas-invoices", body, { schema: invoiceSchema }),
    ...done,
    invalidate: (_data, body) => tenantKeys(body.tenant_id),
  });
}

export function useMarkInvoicePaid(done: Done<{ number: string }>) {
  const client = useApiClient();
  return useCommit({
    mutationFn: ({ id, reference }: { id: string; reference: string }) => client.post(`/saas-invoices/${id}:mark-paid`, { reference }, { schema: invoiceSchema }),
    ...done,
    invalidate: [consoleKeys.invoices(), consoleKeys.tenants()],
  });
}

export function useVoidInvoice(done: Done<{ number: string }>) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (id: string) => client.post(`/saas-invoices/${id}:void`, undefined, { schema: invoiceSchema }),
    ...done,
    invalidate: [consoleKeys.invoices(), consoleKeys.tenants()],
  });
}

export function useSetFeatureFlag(done: Done<SetFlagBody>) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (body: SetFlagBody) => client.put("/feature-flags", body, { schema: featureFlagSchema }),
    ...done,
    invalidate: (data) => [consoleKeys.flags(), consoleKeys.flagChanges(data.key)],
  });
}

/**
 * `DELETE /tenants/{id}/feature-flags/{key}`: the store follows the global value again. A 404
 * `flag_override_not_found` means the value was already gone, so the flag list is read again.
 */
export function useRemoveTenantFlag(tenantId: string, done: Done<{ key: string }>) {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useCommit({
    mutationFn: async (key: string) => {
      try {
        await client.delete(`/tenants/${tenantId}/feature-flags/${encodeURIComponent(key)}`);
      } catch (e) {
        if (isApiError(e) && e.code === "flag_override_not_found") void queryClient.invalidateQueries({ queryKey: consoleKeys.flags() });
        throw e;
      }
      return { key };
    },
    ...done,
    invalidate: (data) => [consoleKeys.flags(), consoleKeys.flagChanges(data.key)],
  });
}

export function useCreateIncident(done: Done<{ title: string }>) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (body: CreateIncidentBody) => client.post("/incidents", body, { schema: incidentSchema }),
    ...done,
    invalidate: [consoleKeys.incidents()],
  });
}

export function useUpdateIncident(incidentId: string, done: Done<{ title: string }>) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (body: UpdateIncidentBody) => client.patch(`/incidents/${incidentId}`, body, { schema: incidentSchema }),
    ...done,
    invalidate: [consoleKeys.incidents()],
  });
}

export function useCreatePartner(done: Done<{ name: string }>) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (body: CreatePartnerBody) => client.post("/partners", body, { schema: partnerSchema }),
    ...done,
    invalidate: [consoleKeys.partners()],
  });
}

export function useUpdateTicket(ticketId: string, done: Done<Ticket>) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (body: UpdateTicketBody) => client.patch(`/support-tickets/${ticketId}`, body, { schema: ticketSchema }),
    ...done,
    invalidate: [consoleKeys.tickets()],
  });
}

export function useReplyTicket(ticketId: string, done: Done<unknown>) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (body: string) => client.post(`/support-tickets/${ticketId}/messages`, { body }, { schema: ticketMessageSchema }),
    ...done,
    invalidate: [consoleKeys.tickets()],
  });
}
