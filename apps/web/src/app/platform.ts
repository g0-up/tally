import { QueryClient } from "@tanstack/react-query";
import { ERROR_CODES, STALE_DATA_MESSAGE, isEntitlementError, isSessionExpired, type ApiError } from "../shared/api/api-error";
import { CSRF_COOKIES, createCsrfSource } from "../shared/api/csrf";
import { ADMIN_API_BASE, API_BASE, PATHS, hostContextSchema, type HostKind } from "../shared/api/endpoints";
import { createHttpClient, type HttpClient } from "../shared/api/http";
import { adminKeys, platformKeys } from "../shared/api/query-keys";
import { toast } from "../shared/ui/toast-store";

// One HTTP client and one QueryClient per host kind. The admin console gets its own of both, so
// nothing cached for a store can ever show up in the admin shell and vice versa.

export interface Platform {
  kind: HostKind;
  client: HttpClient;
  queryClient: QueryClient;
}

export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: { staleTime: 30_000, refetchOnWindowFocus: true, retry: 1 },
      mutations: { retry: false },
    },
  });
}

/**
 * App-wide reaction to API errors (requests made with `silent` skip it):
 * - session expired → refetch the session; RequireAuth then redirects to /login?next=…;
 * - stale data (412/428) → persistent toast and refetch of everything on screen;
 * - subscription or plan refusal → refresh subscription and entitlements so the banner shows.
 * Re-authentication and exception confirmations are handled by the caller (useCommit).
 */
export function handleApiError(queryClient: QueryClient, kind: HostKind, error: ApiError): void {
  if (isSessionExpired(error)) {
    void queryClient.invalidateQueries({ queryKey: kind === "admin" ? adminKeys.me() : platformKeys.me() });
    return;
  }
  if (error.status === 412 || error.code === ERROR_CODES.preconditionRequired) {
    toast.error(STALE_DATA_MESSAGE);
    void queryClient.invalidateQueries();
    return;
  }
  if (isEntitlementError(error)) {
    void queryClient.invalidateQueries({ queryKey: platformKeys.subscription() });
    void queryClient.invalidateQueries({ queryKey: platformKeys.entitlements() });
  }
}

export function createPlatform(kind: HostKind, fetchImpl?: typeof fetch): Platform {
  const queryClient = createQueryClient();
  const admin = kind === "admin";
  const doFetch = fetchImpl ?? ((...args: Parameters<typeof fetch>) => fetch(...args));
  const csrf = createCsrfSource(admin ? CSRF_COOKIES.admin : CSRF_COOKIES.tenant);
  const client: HttpClient = createHttpClient({
    baseUrl: admin ? ADMIN_API_BASE : API_BASE,
    csrf,
    onError: (error) => handleApiError(queryClient, kind, error),
    // GET /host-context (served on every host, admin included) issues a new token for the current
    // session and sets the CSRF cookie; the refused request is then retried once.
    refreshCsrf: async () => {
      const res = await doFetch(API_BASE + PATHS.hostContext, { credentials: "include", headers: { Accept: "application/json" } });
      if (!res.ok) throw new Error(`host-context answered ${res.status}`);
      const parsed = hostContextSchema.safeParse(await res.json().catch(() => null));
      // Without a token in the body the refreshed cookie is used.
      csrf.set(parsed.success ? (parsed.data.csrf_token ?? null) : null);
    },
    ...(fetchImpl ? { fetchImpl } : {}),
  });
  return { kind, client, queryClient };
}
