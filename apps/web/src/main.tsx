/// <reference types="vite-plugin-pwa/client" />
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import type { QueryClient } from "@tanstack/react-query";
import { App } from "./app";
import { BootError } from "./app/boot-error";
import { HOST_CONTEXT_KEY, inferHostKind, loadHostContext } from "./app/host-context";
import { createPlatform } from "./app/platform";
import { RouteLoading } from "./app/route-fallbacks";
import { announceUpdate } from "./features/pos/offline/app-update";
import { loadBootSnapshot, seedBootSnapshot, watchSessionForOfflineData, type BootSnapshot } from "./features/pos/offline/boot-snapshot";
import { UpdatePrompt } from "./features/pos/offline/components/update-prompt";
import { bindOnlineManager, connectivity } from "./features/pos/offline/connectivity";
import { errorMessage } from "./shared/api/api-error";
import { hostContextSchema, type HostContext } from "./shared/api/endpoints";
import { toast } from "./shared/ui/toast-store";
import "./index.css";

const container = document.getElementById("root");
if (!container) {
  throw new Error("Missing #root element in index.html");
}
const root = createRoot(container);

/**
 * A store host whose API cannot be reached at boot starts from what the POS saved while online
 * (host context and session answers), so a reload in the middle of an outage still opens the
 * offline POS. Any other failure, or no saved start, goes to the boot error screen.
 */
async function offlineStart(): Promise<{ context: HostContext; snapshot: BootSnapshot } | null> {
  if (inferHostKind(window.location.hostname) !== "tenant") return null;
  const snapshot = await loadBootSnapshot().catch(() => null);
  const host = hostContextSchema.safeParse(snapshot?.host);
  if (!snapshot || !host.success || host.data.kind !== "tenant") return null;
  return { context: host.data, snapshot };
}

/** Service worker of a store host (production builds): app shell offline, update on reload. */
async function registerServiceWorker(): Promise<void> {
  if (!import.meta.env.PROD || !("serviceWorker" in navigator)) return;
  const { registerSW } = await import("virtual:pwa-register");
  const updateSW = registerSW({
    onNeedRefresh: () => announceUpdate(() => updateSW(true)),
    // The app keeps working online without the worker; only a start without network is lost.
    onRegisterError: (e: unknown) => console.warn("Service worker registration failed", e),
  });
}

/**
 * The host context, or why the API counts as out of reach. fetch() rejects with a TypeError when
 * the request never got an answer; a server error (5xx), or a success whose body is not the API's
 * (a proxy or captive-portal page), means the API did not answer either.
 */
async function loadBootContext(): Promise<{ context: HostContext; unreachable: boolean; error: unknown }> {
  let status = 0;
  const fetchImpl: typeof fetch = async (...args) => {
    const res = await fetch(...args);
    status = res.status;
    return res;
  };
  try {
    const { context, source } = await loadHostContext(fetchImpl);
    return { context, unreachable: source === "hostname" && (status >= 500 || (status >= 200 && status < 300)), error: null };
  } catch (error) {
    if (error instanceof TypeError) return { context: { kind: inferHostKind(window.location.hostname) }, unreachable: true, error };
    throw error;
  }
}

/** Only store hosts are installable apps; the other hosts drop the manifest the build links. */
function dropManifestOffStoreHosts(kind: HostContext["kind"]): void {
  if (kind === "tenant") return;
  document.querySelectorAll('link[rel="manifest"]').forEach((link) => link.remove());
}

/**
 * The phone scan page (/scan) is a page of its own on a store host: no service worker, no
 * reachability heartbeat on /me (the phone has no login session) and no offline POS data.
 */
function isScanPage(): boolean {
  return /^\/scan\/?$/.test(window.location.pathname);
}

let storeServicesStarted = false;

/** Store hosts follow the API's reachability and apply the offline data wipe rules (once per page). */
function startStoreServices(queryClient: QueryClient): void {
  if (storeServicesStarted) return;
  storeServicesStarted = true;
  connectivity.start();
  bindOnlineManager();
  watchSessionForOfflineData(queryClient, (e) => toast.error(`Không cập nhật được dữ liệu ngoại tuyến trên máy: ${errorMessage(e)}`));
  void registerServiceWorker().catch((e: unknown) => console.warn("Service worker registration failed", e));
}

// The host kind decides the route tree, so it is known before the router is created.
async function boot(): Promise<void> {
  root.render(<RouteLoading />);
  try {
    const loaded = await loadBootContext();
    let context = loaded.context;
    let snapshot: BootSnapshot | null = null;
    if (loaded.unreachable) {
      const start = isScanPage() ? null : await offlineStart();
      if (start) ({ context, snapshot } = start);
      // No answer at all and nothing saved: the boot error screen offers a retry. A server error
      // without a saved start still opens the app, whose screens report the API errors.
      else if (loaded.error !== null) throw loaded.error;
    }
    dropManifestOffStoreHosts(context.kind);
    const platform = createPlatform(context.kind);
    platform.queryClient.setQueryData(HOST_CONTEXT_KEY, context);
    // Anonymous requests (login, sign-up, password reset) need the host's CSRF token too.
    if (context.csrf_token) platform.client.setCsrfToken(context.csrf_token);
    if (context.kind === "tenant" && !isScanPage()) {
      startStoreServices(platform.queryClient);
      if (snapshot) {
        seedBootSnapshot(platform.queryClient, HOST_CONTEXT_KEY, snapshot);
        // The API did not answer: queries wait (keeping the seeded answers) until it does.
        connectivity.reportFailure();
      }
    }
    root.render(
      <StrictMode>
        <App platform={platform} />
        {context.kind === "tenant" && !isScanPage() ? <UpdatePrompt /> : null}
      </StrictMode>,
    );
  } catch (error) {
    root.render(<BootError error={error} onRetry={() => void boot()} />);
  }
}

void boot();
