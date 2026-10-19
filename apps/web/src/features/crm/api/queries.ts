import { useQueries, useQuery } from "@tanstack/react-query";
import { useApiClient } from "../../../shared/api/api-context";
import { listOf } from "../../../shared/api/decimal-schemas";
import { ZERO_MONEY, type Money } from "../../../shared/domain/money";
import { useCursorList } from "../../../shared/hooks/use-cursor-list";
import { isDecimalString } from "../../../shared/lib/decimal";
import { consentsSchema, customerGroupSchema, customerListItemSchema, priceListSchema, revenuePageSchema, type RevenuePage } from "../schemas";

// Read side of the Khách hàng (CRM) screen. Revenue per customer comes from the revenue report,
// so its key starts with "reports", the prefix sales and returns postings already refresh.

export const crmKeys = {
  all: ["crm"] as const,
  customers: ["crm", "customers"] as const,
  list: (q: string) => ["crm", "customers", q] as const,
  consents: (partyId: string) => ["crm", partyId, "consents"] as const,
  groups: ["crm", "customer-groups"] as const,
  priceLists: ["crm", "price-lists"] as const,
  revenue: (from: string, to: string, customerIds: readonly string[]) => ["reports", "revenue-by-customer", from, to, customerIds.join(",")] as const,
};

/** Rows one page of the customer list holds. */
const PAGE = 50;
/** Largest page the report endpoint serves. */
const REPORT_PAGE = 200;
/**
 * Customers one revenue read names (`customer_ids` takes at most 100): a page of the list, so
 * "Xem thêm" reads the new page alone and the pages already read keep their answer.
 */
const REVENUE_IDS_PER_READ = PAGE;

/** `GET /customers?q=`: name, phone or tax code words, both statuses, with the latest purchase and the consents. */
export function useCustomers(q: string) {
  return useCursorList({ queryKey: crmKeys.list(q), path: "/customers", item: customerListItemSchema, params: { q }, limit: PAGE });
}

/** First day of the selling season a business date falls in: 1 October ("Doanh thu từ 01/10"). */
export function seasonStart(today: string): string {
  const year = Number(today.slice(0, 4));
  const month = Number(today.slice(5, 7));
  return `${month >= 10 ? year : year - 1}-10-01`;
}

export interface RevenueByCustomer {
  /** Net revenue of every customer whose read answered; one without a sale in the season reads 0. */
  byCustomer: ReadonlyMap<string, Money>;
  /** The first refused read, null when none was. */
  error: unknown;
}

/**
 * Net revenue from the season start to the business date of the listed customers
 * (`GET /reports/revenue?group_by=customer&customer_ids=`), one read per page of the list. A
 * refused read (no permission, no reporting in the plan) leaves its customers at "—" without a
 * toast. `today` is null until the business date is known, so the season is never read on the
 * device date.
 */
export function useRevenueByCustomer(today: string | null, customerIds: readonly string[]): RevenueByCustomer {
  const client = useApiClient();
  const to = today ?? "";
  const from = today === null ? "" : seasonStart(today);
  const reads: string[][] = [];
  for (let i = 0; i < customerIds.length; i += REVENUE_IDS_PER_READ) reads.push(customerIds.slice(i, i + REVENUE_IDS_PER_READ));
  return useQueries({
    queries: reads.map((ids) => ({
      queryKey: crmKeys.revenue(from, to, ids),
      queryFn: async ({ signal }: { signal: AbortSignal }): Promise<[string, Money][]> => {
        const net = new Map<string, Money>(ids.map((cid) => [cid, ZERO_MONEY]));
        let cursor: string | null = null;
        do {
          const page: RevenuePage = await client.get("/reports/revenue", {
            // A comma-separated list, as the report filter takes it (an array would repeat the parameter).
            query: { from, to, group_by: "customer", customer_ids: ids.join(","), limit: REPORT_PAGE, cursor },
            schema: revenuePageSchema,
            signal,
            silent: true,
          });
          for (const row of page.rows) {
            const cid = row.id;
            const amount = row.net_revenue;
            if (cid && net.has(cid) && amount && isDecimalString(amount)) net.set(cid, amount as Money);
          }
          cursor = page.next_cursor;
        } while (cursor !== null);
        return [...net];
      },
      enabled: today !== null,
    })),
    combine: (results) => ({
      byCustomer: new Map(results.flatMap((r) => r.data ?? [])),
      error: results.find((r) => r.error !== null)?.error ?? null,
    }),
  });
}

/** The customer's current consents per channel and purpose. */
export function useConsents(partyId: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: crmKeys.consents(partyId ?? ""),
    queryFn: ({ signal }) => client.get(`/customers/${partyId}/consents`, { schema: consentsSchema, signal, silent: true }),
    enabled: partyId !== null,
  });
}

/** Active customer groups for the form's "Nhóm khách". */
export function useCustomerGroups() {
  const client = useApiClient();
  return useQuery({
    queryKey: crmKeys.groups,
    queryFn: ({ signal }) => client.get("/customer-groups", { query: { active: true }, schema: listOf(customerGroupSchema), signal }),
    select: (r) => r.data,
  });
}

/** Active price lists, to name the lists a customer's group or the customer gets. */
export function usePriceLists() {
  const client = useApiClient();
  return useQuery({
    queryKey: crmKeys.priceLists,
    queryFn: ({ signal }) => client.get("/price-lists", { query: { active: true }, schema: listOf(priceListSchema), signal }),
    select: (r) => r.data,
  });
}
