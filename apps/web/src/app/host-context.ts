import { queryOptions, useQuery } from "@tanstack/react-query";
import { API_BASE, PATHS, hostContextSchema, type HostContext, type HostKind } from "../shared/api/endpoints";

// One SPA bundle serves three host kinds: the root host (sign-up, find-store), each store's
// subdomain (the app) and the admin host. The API classifies the Host header, so the SPA asks
// it once before creating the router (GET /api/v1/host-context) and picks the route tree.

/**
 * Host kind from the browser's hostname, used only when the API cannot be asked (endpoint not
 * deployed yet, or a 404 from an older API). It mirrors the API rules for the hosts it can be
 * sure about: "admin.*" → admin, "<slug>.localhost" → tenant, bare localhost/IP → root, and for
 * public hosts one label in front of a registrable "name.tld" pair → tenant.
 */
export function inferHostKind(hostname: string): HostKind {
  const host = hostname.toLowerCase().replace(/\.$/, "");
  if (host === "localhost" || /^[\d.]+$/.test(host) || host.includes(":")) return "root";
  const labels = host.split(".");
  if (labels[0] === "admin") return "admin";
  if (labels[0] === "www") return "root";
  if (labels[labels.length - 1] === "localhost") return labels.length === 2 ? "tenant" : "root";
  return labels.length >= 3 ? "tenant" : "root";
}

export type HostContextResult = { context: HostContext; source: "api" | "hostname" };

/**
 * Asks the API which kind of host the SPA is running on. A missing endpoint (404) or an
 * unreadable answer falls back to the hostname rules; a network failure is thrown so the boot
 * screen can offer a retry.
 */
export async function loadHostContext(fetchImpl: typeof fetch = (...args) => fetch(...args), hostname: string = window.location.hostname): Promise<HostContextResult> {
  const res = await fetchImpl(API_BASE + PATHS.hostContext, { credentials: "include", headers: { Accept: "application/json" } });
  if (res.ok) {
    const parsed = hostContextSchema.safeParse(await res.json().catch(() => null));
    if (parsed.success) return { context: parsed.data, source: "api" };
  }
  if (res.status === 421) throw new Error("misdirected_request");
  return { context: { kind: inferHostKind(hostname) }, source: "hostname" };
}

/** Cache key of the host context; outside platformKeys so signing in or out does not drop it. */
export const HOST_CONTEXT_KEY = ["host-context"] as const;

/**
 * The host context main.tsx loaded at boot (seeded into the cache there). Screens read it for
 * URLs of other hosts (root_url for sign-up and find-store links); it does not change while the
 * page is open.
 */
export function hostContextQuery() {
  return queryOptions({
    queryKey: HOST_CONTEXT_KEY,
    queryFn: async () => (await loadHostContext()).context,
    staleTime: Infinity,
    gcTime: Infinity,
    retry: 1,
  });
}

export function useHostContext() {
  return useQuery(hostContextQuery());
}
