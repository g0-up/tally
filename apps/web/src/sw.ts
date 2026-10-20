import { clientsClaim } from "workbox-core";
import { cleanupOutdatedCaches, createHandlerBoundToURL, precacheAndRoute, type PrecacheEntry } from "workbox-precaching";
import { NavigationRoute, registerRoute } from "workbox-routing";
import { NetworkOnly } from "workbox-strategies";

// Service worker of a store host (registered by main.tsx on tenant hosts only). It keeps the app
// shell available offline and nothing else: the hashed bundle is precached, page navigations get
// the cached index.html, and every API call goes to the network. Offline POS data lives in
// IndexedDB, written by the app itself; exports, downloads and the admin API are never cached
// because no API answer is.

// The worker's global scope, typed by hand: the app's tsconfig uses the DOM lib, and adding the
// WebWorker lib to the same program would clash with it.
interface ServiceWorkerScope {
  __WB_MANIFEST: (PrecacheEntry | string)[];
  skipWaiting(): Promise<void>;
  addEventListener(type: "message", listener: (event: MessageEvent<unknown>) => void): void;
}
declare const self: ServiceWorkerScope;

const API_PREFIX = /^\/api\//;
// The phone scan page always comes from the network, so it gets the current shell and the camera
// and WebAssembly headers the edge sends for /scan only, even on a phone with the app installed.
const SCAN_PAGE = /^\/scan(?:\/|$)/;

precacheAndRoute(self.__WB_MANIFEST);
cleanupOutdatedCaches();

// Deep links (/sales/pos after a reload) open the SPA shell; API paths are never navigations.
registerRoute(new NavigationRoute(createHandlerBoundToURL("/index.html"), { denylist: [API_PREFIX, SCAN_PAGE] }));

// Every method, so a POST is never answered from a cache either.
for (const method of ["GET", "POST", "PUT", "PATCH", "DELETE"] as const) {
  registerRoute(({ url, sameOrigin }) => sameOrigin && API_PREFIX.test(url.pathname), new NetworkOnly(), method);
}

// "Tải lại để cập nhật" (main.tsx) asks the waiting worker to take over.
self.addEventListener("message", (event) => {
  const data = event.data;
  if (typeof data === "object" && data !== null && (data as { type?: unknown }).type === "SKIP_WAITING") void self.skipWaiting();
});

clientsClaim();
