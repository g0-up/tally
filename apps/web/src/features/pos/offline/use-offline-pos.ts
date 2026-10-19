import { useEffect, useMemo } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { HOST_CONTEXT_KEY } from "../../../app/host-context";
import { useApiClient } from "../../../shared/api/api-context";
import { isApiError } from "../../../shared/api/api-error";
import { saveBootSnapshot } from "./boot-snapshot";
import { connectivity, useConnectivity, type ConnectivityState } from "./connectivity";
import { BUNDLE_REFRESH_MS, loadBundle, refreshBundle, type BundleOwner } from "./offline-bundle-repository";
import { claimTenant, offlineStorageAvailable, type OfflineBundle } from "./offline-db";
import { seedOfflineLookups } from "./offline-lookups";
import { offlineKeys, useDeviceSession } from "./offline-queries";
import { useOfflineSync, type OfflineSync } from "./use-offline-sync";

// Everything the POS screen needs to sell without the server: where the API stands, the catalog of
// this tenant and login session, the outbox and the exception queue. While online the catalog is
// downloaded on opening the POS, on reconnecting and every 30 minutes, together with the snapshot
// that lets the app start offline after a reload.

/**
 * The saved catalog of this tenant and session, refreshed while online. A refresh that fails keeps
 * the saved one: its expiry already bounds how long it can be sold from, and the banner shows when
 * it was priced.
 */
function useOfflineBundle(owner: BundleOwner | null, online: boolean): OfflineBundle | null {
  const client = useApiClient();
  const queryClient = useQueryClient();
  const enabled = owner !== null && offlineStorageAvailable();
  const tenantId = owner?.tenantId ?? "";
  const sessionId = owner?.sessionId ?? "";

  const stored = useQuery({
    queryKey: offlineKeys.bundle(tenantId, sessionId),
    queryFn: () => loadBundle({ tenantId, sessionId }),
    enabled,
    networkMode: "always",
    staleTime: Infinity,
  });

  useEffect(() => {
    if (!enabled || !online) return;
    let cancelled = false;
    const refresh = async () => {
      try {
        await claimTenant(tenantId);
        const bundle = await refreshBundle(client, { tenantId, sessionId }, new Date(), () => cancelled);
        if (cancelled) return;
        queryClient.setQueryData(offlineKeys.bundle(tenantId, sessionId), bundle);
        await saveBootSnapshot(queryClient, HOST_CONTEXT_KEY, tenantId);
      } catch (e) {
        // Only a request with no answer says the API is out of reach; a server error is retried by
        // the next refresh, without flipping the POS offline and back.
        if (isApiError(e) && e.isNetwork) connectivity.reportFailure();
      }
    };
    void refresh();
    const timer = setInterval(() => void refresh(), BUNDLE_REFRESH_MS);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [client, queryClient, enabled, online, tenantId, sessionId]);

  return stored.data ?? null;
}

export interface OfflinePos extends OfflineSync {
  state: ConnectivityState;
  /** The API cannot be reached (or the toggle simulates it): the POS sells from the device. */
  offline: boolean;
  /** "Mô phỏng mất mạng" is on. */
  simulated: boolean;
  /** Sandbox tenant: the simulation toggle is offered. */
  sandbox: boolean;
  tenantId: string | null;
  bundle: OfflineBundle | null;
}

export function useOfflinePos(): OfflinePos {
  const queryClient = useQueryClient();
  const { state, simulated } = useConnectivity();
  const online = state === "online";
  const session = useDeviceSession();
  const tenantId = session.data?.tenant.id ?? null;
  const sessionId = session.data?.session.id ?? null;
  const owner = useMemo(() => (tenantId !== null && sessionId !== null ? { tenantId, sessionId } : null), [tenantId, sessionId]);
  const bundle = useOfflineBundle(owner, online);
  const sync = useOfflineSync(tenantId);

  useEffect(() => {
    if (!online && bundle) seedOfflineLookups(queryClient, bundle);
  }, [online, bundle, queryClient]);

  return { ...sync, state, offline: !online, simulated, sandbox: session.data?.tenant.is_sandbox ?? false, tenantId, bundle };
}
