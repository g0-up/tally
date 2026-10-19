import { useQuery } from "@tanstack/react-query";
import { z } from "zod";
import { useApiClient } from "../../../shared/api/api-context";
import { listOf } from "../../../shared/api/decimal-schemas";
import { cursorPageSchema } from "../../../shared/hooks/use-cursor-list";
import { debtRowSchema, fundAccountSchema, openingDebtSchema, openingStockDocSchema, reasonSchema } from "../schemas";

// Read side of the onboarding screen. The checklist (`useOnboarding`) and the store profile
// (`useTenant`) are the shell's queries, shared with the gate and the settings; these are the
// lookups the steps add.

export const onboardingKeys = {
  all: ["onboarding"] as const,
  funds: () => ["onboarding", "funds"] as const,
  reasons: (kind: string) => ["onboarding", "reasons", kind] as const,
  openingStock: () => ["onboarding", "opening-stock"] as const,
  openingDebts: () => ["onboarding", "opening-debts"] as const,
};

const LOOKUP_STALE_MS = 5 * 60_000;

/** Active fund accounts with their opening balance and version (`GET /funds`). */
export function useOpeningFunds() {
  const client = useApiClient();
  return useQuery({
    queryKey: onboardingKeys.funds(),
    queryFn: ({ signal }) => client.get("/funds", { schema: listOf(fundAccountSchema), signal }),
    select: (r) => r.data.filter((f) => f.active),
  });
}

/** Active reasons of a kind ("debt_adjustment"). */
export function useActiveReasons(kind: string) {
  const client = useApiClient();
  return useQuery({
    queryKey: onboardingKeys.reasons(kind),
    queryFn: ({ signal }) => client.get("/reasons", { query: { kind, active: true }, schema: listOf(reasonSchema), signal }),
    staleTime: LOOKUP_STALE_MS,
    select: (r) => r.data.filter((x) => x.active && x.kind === kind),
  });
}

/**
 * A new store posts one or a few opening documents, so the posted tables read the first page only
 * and say when there is more (`more`).
 */
const POSTED_LIMIT = 50;
// A reversal keeps the opening kind and positive quantities, so it is skipped (its original is no longer posted).
const stockRowSchema = z.object({ id: z.string(), reversal_of: z.string().nullable() });

/** Lines of the posted opening stock documents (`GET /stock-adjustments` then each document). */
export function useOpeningStockLines() {
  const client = useApiClient();
  return useQuery({
    queryKey: onboardingKeys.openingStock(),
    queryFn: async ({ signal }) => {
      const page = await client.get("/stock-adjustments", { query: { kind: "opening", status: "posted", limit: POSTED_LIMIT }, schema: cursorPageSchema(stockRowSchema), signal });
      const docs = await Promise.all(page.data.filter((d) => d.reversal_of === null).map((d) => client.get(`/stock-adjustments/${d.id}`, { schema: openingStockDocSchema, signal })));
      return { lines: docs.flatMap((d) => d.lines), more: page.next_cursor !== null };
    },
  });
}

/**
 * Posted opening balances of customers and suppliers. The list has no kind filter, so it is
 * filtered here; each balance is read whole for the due date of the debt it opened.
 */
export function useOpeningDebts() {
  const client = useApiClient();
  return useQuery({
    queryKey: onboardingKeys.openingDebts(),
    queryFn: async ({ signal }) => {
      const page = await client.get("/debt-adjustments", { query: { limit: POSTED_LIMIT }, schema: cursorPageSchema(debtRowSchema), signal });
      const opening = page.data.filter((d) => d.kind === "opening_balance");
      const debts = await Promise.all(opening.map((d) => client.get(`/debt-adjustments/${d.id}`, { schema: openingDebtSchema, signal })));
      return { debts, more: page.next_cursor !== null };
    },
  });
}

/** Active SKUs: the catalog step lists them and the opening stock lines pick from them. */
export const ACTIVE_SKUS = { status: "active" } as const;
