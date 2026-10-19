import { useQuery } from "@tanstack/react-query";
import { useApiClient } from "../../../shared/api/api-context";
import type { Period } from "../../../shared/ui/period";
import { dashboardSchema } from "../schemas";

// Read side of the dashboard. Keys start with "dashboard", the prefix payments and other postings
// already refresh.

export const dashboardKeys = {
  all: ["dashboard"] as const,
  period: (period: Period, date: string | null) => ["dashboard", period, date] as const,
};

/** Cash and debt of the period holding `date`; without a date, the store's today (`GET /reports/dashboard`). */
export function useDashboard(period: Period, date: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: dashboardKeys.period(period, date),
    queryFn: ({ signal }) => client.get("/reports/dashboard", { query: { period, date }, schema: dashboardSchema, signal }),
    placeholderData: (prev) => prev,
  });
}
