import { useQuery } from "@tanstack/react-query";
import { useApiClient } from "../../../shared/api/api-context";
import { useCursorList } from "../../../shared/hooks/use-cursor-list";
import { campaignSchema, checklistSchema, demandRowSchema, seasonReportSchema } from "../schemas";

// Read side of the Chiến dịch mùa vụ screen. Season figures come from the season report, so its key
// starts with "reports", the prefix sales and returns postings already refresh.

export const campaignKeys = {
  all: ["campaign"] as const,
  list: ["campaign", "list"] as const,
  checklist: (id: string) => ["campaign", id, "checklist"] as const,
  demand: (id: string) => ["campaign", id, "demand"] as const,
  season: (id: string) => ["reports", "season", id] as const,
};

/** Rows one page of the campaign and demand lists holds. */
const PAGE = 50;

/** Campaigns of every status (`GET /campaigns`). */
export function useCampaigns() {
  return useCursorList({ queryKey: campaignKeys.list, path: "/campaigns", item: campaignSchema, limit: PAGE });
}

/** The campaign's checklist in order. */
export function useChecklist(campaignId: string) {
  const client = useApiClient();
  return useQuery({
    queryKey: campaignKeys.checklist(campaignId),
    queryFn: ({ signal }) => client.get(`/campaigns/${campaignId}/checklist`, { schema: checklistSchema, signal }),
  });
}

/** Plan-versus-actual figures of a campaign (`GET /reports/season?campaign_id=`). */
export function useSeasonReport(campaignId: string) {
  const client = useApiClient();
  return useQuery({
    queryKey: campaignKeys.season(campaignId),
    queryFn: ({ signal }) => client.get("/reports/season", { query: { campaign_id: campaignId }, schema: seasonReportSchema, signal }),
  });
}

/** Day-by-day demand, incoming stock and shortage over the campaign's dates, SKUs by code. */
export function useDemand(campaignId: string | null) {
  return useCursorList({
    queryKey: campaignKeys.demand(campaignId ?? ""),
    path: `/campaigns/${campaignId ?? "-"}/demand`,
    item: demandRowSchema,
    limit: PAGE,
    enabled: campaignId !== null,
  });
}
