import { useQueryClient } from "@tanstack/react-query";
import { useRef } from "react";
import { useApiClient } from "../../../shared/api/api-context";
import { useCommit } from "../../../shared/hooks/use-commit";
import { etagOf } from "../../catalog";
import { campaignCloseSchema, campaignDetailSchema, checklistSchema, type Campaign } from "../schemas";
import { withCampaignMessage } from "./adapters";
import { campaignKeys } from "./queries";

// Write side of the Chiến dịch mùa vụ screen. Activating and closing take If-Match and no
// Idempotency-Key, and ticking the checklist takes neither (OpenAPI).

/** Runs a request and maps a refusal onto the screen's sentence. */
async function campaignCall<T>(run: () => Promise<T>): Promise<T> {
  try {
    return await run();
  } catch (e) {
    throw withCampaignMessage(e);
  }
}

/** "Bắt đầu mùa": a planned campaign starts (`POST /campaigns/{id}:activate`). */
export function useActivateCampaign() {
  const client = useApiClient();
  return useCommit({
    mutationFn: (c: Campaign) => campaignCall(() => client.post(`/campaigns/${c.id}:activate`, undefined, { ifMatch: etagOf(c.version), schema: campaignDetailSchema })),
    success: (c) => `Đã bắt đầu ${c.name}.`,
    invalidate: [campaignKeys.all, ["reports"]],
  });
}

/** "Kết mùa": closes the campaign; open checklist items are reported, never blocking. */
export function useCloseCampaign(after: () => void) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (c: Campaign) => campaignCall(() => client.post(`/campaigns/${c.id}:close`, undefined, { ifMatch: etagOf(c.version), schema: campaignCloseSchema })),
    success: (r) => {
      const open = r.checklist.open.length;
      return open > 0 ? `Đã kết mùa ${r.campaign.name}. Còn ${open} mục checklist chưa xong.` : `Đã kết mùa ${r.campaign.name}.`;
    },
    invalidate: [campaignKeys.all, ["reports"]],
    after,
  });
}

/**
 * Ticks or unticks one checklist item (`PATCH /campaigns/{id}/checklist`). The request carries that
 * item alone, so every field the server refuses (`items.0.id`, …) is put on it.
 */
export function useToggleChecklistItem(campaignId: string, setItemError: (itemId: string, message: string) => void) {
  const client = useApiClient();
  const queryClient = useQueryClient();
  const sent = useRef<string | null>(null);
  return useCommit({
    mutationFn: (v: { itemId: string; done: boolean }) => {
      sent.current = v.itemId;
      return campaignCall(() => client.patch(`/campaigns/${campaignId}/checklist`, { items: [{ id: v.itemId, done: v.done }] }, { schema: checklistSchema }));
    },
    after: (checklist) => queryClient.setQueryData(campaignKeys.checklist(campaignId), checklist),
    setFieldError: (_field, message) => {
      if (sent.current !== null) setItemError(sent.current, message);
    },
  });
}
