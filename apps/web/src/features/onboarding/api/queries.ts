import { useQuery } from "@tanstack/react-query";
import { useApiClient } from "../../../shared/api/api-context";
import { listOf } from "../../../shared/api/decimal-schemas";
import { fundAccountSchema, reasonSchema } from "../schemas";

// Read side of the onboarding screen. The checklist (`useOnboarding`) and the store profile
// (`useTenant`) are the shell's queries, shared with the gate and the settings; these are the
// lookups the steps add.

export const onboardingKeys = {
  all: ["onboarding"] as const,
  funds: () => ["onboarding", "funds"] as const,
  reasons: (kind: string) => ["onboarding", "reasons", kind] as const,
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
