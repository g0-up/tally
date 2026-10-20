import { HttpResponse, http } from "msw";
import { API_BASE } from "../../shared/api/endpoints";
import { apiError, apiUrl } from "./handlers";
import { server } from "./server";

// A stateful stand-in for the desktop routes of phone scanner sessions, installed per test with
// `scannerServer()`. Sessions are numbered ss-1, ss-2…, claim codes CLAIM-1, CLAIM-2…; every request
// is logged as "METHOD /path" (below the API base) so tests assert on what the page sent, and single
// answers can be overridden to play a restarting server or a proxy's error page.

export type ScannerFixtureState = "waiting" | "pending" | "claimed" | "ended";

export interface ScannerFixture {
  state: ScannerFixtureState;
  device_label?: string;
  verify_code?: string;
}

export interface ScanResultCall {
  sessionId: string;
  scanId: string;
  body: { outcome: string; sku_id?: string; unit_id?: string };
}

/** An answer to use instead of the fixture's, or undefined to keep it. */
type Override<A extends unknown[]> = ((...args: A) => Response | undefined) | null;

export interface ScannerServer {
  readonly sessions: Map<string, ScannerFixture>;
  /** "POST /scanner-sessions", "GET /scanner-sessions/ss-1", … in the order they arrived. */
  readonly calls: string[];
  readonly results: ScanResultCall[];
  /** How many claim codes were issued. */
  claimCodes(): number;
  /** Answers GET /scanner-sessions/{id} differently while `fn` returns a response. */
  onGet(fn: Override<[id: string]>): void;
  /** Answers the scan result POST differently while `fn` returns a response. */
  onResult(fn: Override<[call: ScanResultCall]>): void;
  /** Answers POST /scanner-sessions differently while `fn` returns a response. */
  onCreate(fn: Override<[]>): void;
}

const SESSION = /\/scanner-sessions\/([^/:]+)/;

function sessionIdOf(url: string): string {
  const m = SESSION.exec(new URL(url).pathname);
  if (!m?.[1]) throw new Error(`no session id in ${url}`);
  return decodeURIComponent(m[1]);
}

function statusOf(s: ScannerFixture) {
  return { state: s.state, ...(s.device_label !== undefined ? { device_label: s.device_label } : {}), ...(s.verify_code !== undefined ? { verify_code: s.verify_code } : {}) };
}

export function scannerServer(initial: Record<string, ScannerFixture> = {}): ScannerServer {
  const sessions = new Map(Object.entries(initial));
  const calls: string[] = [];
  const results: ScanResultCall[] = [];
  let made = 0;
  let codes = 0;
  let getOverride: Override<[string]> = null;
  let resultOverride: Override<[ScanResultCall]> = null;
  let createOverride: Override<[]> = null;

  const log = (request: Request) => calls.push(`${request.method} ${decodeURIComponent(new URL(request.url).pathname).slice(API_BASE.length)}`);
  const claim = () => ({ claim_code: `CLAIM-${++codes}`, claim_expires_at: new Date(Date.now() + 120_000).toISOString() });
  const missing = () => apiError(404, "not_found");

  server.use(
    http.post(apiUrl("/scanner-sessions"), ({ request }) => {
      log(request);
      const over = createOverride?.();
      if (over) return over;
      const id = `ss-${++made}`;
      sessions.set(id, { state: "waiting" });
      return HttpResponse.json({ id, ...claim() }, { status: 201 });
    }),
    http.post(/\/scanner-sessions\/[^/:]+:refresh-code$/, ({ request }) => {
      log(request);
      const s = sessions.get(sessionIdOf(request.url));
      if (!s || s.state === "ended") return missing();
      if (s.state !== "waiting") return apiError(409, "already_claimed");
      return HttpResponse.json(claim());
    }),
    http.post(/\/scanner-sessions\/[^/:]+:approve$/, ({ request }) => {
      log(request);
      const s = sessions.get(sessionIdOf(request.url));
      if (!s || s.state === "ended") return missing();
      if (s.state !== "pending") return apiError(409, "not_pending");
      s.state = "claimed";
      delete s.verify_code;
      return HttpResponse.json(statusOf(s));
    }),
    http.post(/\/scanner-sessions\/[^/:]+:reject$/, ({ request }) => {
      log(request);
      const s = sessions.get(sessionIdOf(request.url));
      if (!s || s.state === "ended") return missing();
      if (s.state !== "pending") return apiError(409, "not_pending");
      sessions.set(sessionIdOf(request.url), { state: "waiting" });
      return HttpResponse.json(claim());
    }),
    http.post(/\/scanner-sessions\/[^/:]+\/scans\/[^/]+\/result$/, async ({ request }) => {
      log(request);
      const scanId = decodeURIComponent(/\/scans\/([^/]+)\/result$/.exec(new URL(request.url).pathname)?.[1] ?? "");
      const call: ScanResultCall = { sessionId: sessionIdOf(request.url), scanId, body: (await request.json()) as ScanResultCall["body"] };
      results.push(call);
      const over = resultOverride?.(call);
      if (over) return over;
      return HttpResponse.json({ scan_id: scanId, outcome: call.body.outcome });
    }),
    http.get(/\/scanner-sessions\/[^/:]+$/, ({ request }) => {
      log(request);
      const id = sessionIdOf(request.url);
      const over = getOverride?.(id);
      if (over) return over;
      const s = sessions.get(id);
      return s ? HttpResponse.json(statusOf(s)) : missing();
    }),
    http.delete(/\/scanner-sessions\/[^/:]+$/, ({ request }) => {
      log(request);
      const s = sessions.get(sessionIdOf(request.url));
      if (s) s.state = "ended";
      return new HttpResponse(null, { status: 204 });
    }),
  );

  return {
    sessions,
    calls,
    results,
    claimCodes: () => codes,
    onGet: (fn) => {
      getOverride = fn;
    },
    onResult: (fn) => {
      resultOverride = fn;
    },
    onCreate: (fn) => {
      createOverride = fn;
    },
  };
}

// The phone routes (/scan/*): one phone, whose session cookie the fixture stands for. Claim codes
// listed in `claimCodes` can be claimed once; scans answer from `answers` by code (added Mứt gừng ·
// Hộp otherwise) and are logged with their Idempotency-Key.

export type PhoneFixtureState = "none" | "pending" | "claimed" | "ended";

export interface PhoneScanCall {
  code: string;
  key: string | null;
}

export interface PhoneScanServer {
  /** The phone's session as its cookie would open it. */
  session: { state: PhoneFixtureState; verify_code: string };
  readonly claimCodes: Set<string>;
  readonly answers: Map<string, Record<string, unknown>>;
  /** "POST /scan/claim", "GET /scan/session", … in the order they arrived. */
  readonly calls: string[];
  readonly scans: PhoneScanCall[];
  /** Answers POST /scan/scans differently while `fn` returns a response. */
  onScan(fn: Override<[call: PhoneScanCall]>): void;
  /** Answers GET /scan/session differently while `fn` returns a response. */
  onSession(fn: Override<[]>): void;
  /** Answers POST /scan/claim differently while `fn` returns a response. */
  onClaim(fn: Override<[]>): void;
  /** Answers DELETE /scan/session differently while `fn` returns a response. */
  onDelete(fn: Override<[]>): void;
}

export function phoneScanServer({ state = "none", verify_code = "4821", claimCodes = ["CLAIM-1"] }: { state?: PhoneFixtureState; verify_code?: string; claimCodes?: string[] } = {}): PhoneScanServer {
  const session = { state, verify_code };
  const codes = new Set(claimCodes);
  const answers = new Map<string, Record<string, unknown>>();
  const calls: string[] = [];
  const scans: PhoneScanCall[] = [];
  let scanOverride: Override<[PhoneScanCall]> = null;
  let sessionOverride: Override<[]> = null;
  let claimOverride: Override<[]> = null;
  let deleteOverride: Override<[]> = null;

  const log = (request: Request) => calls.push(`${request.method} ${new URL(request.url).pathname.slice(API_BASE.length)}`);
  const ended = () => apiError(401, "scan_session_ended");
  const hasSession = () => session.state === "pending" || session.state === "claimed";

  server.use(
    http.post(apiUrl("/scan/claim"), async ({ request }) => {
      log(request);
      const over = claimOverride?.();
      if (over) return over;
      const { code } = (await request.json()) as { code?: string };
      if (!code || !codes.delete(code)) return apiError(410, "claim_invalid");
      session.state = "pending";
      return HttpResponse.json({ state: "pending", verify_code: session.verify_code });
    }),
    http.get(apiUrl("/scan/session"), ({ request }) => {
      log(request);
      const over = sessionOverride?.();
      if (over) return over;
      if (!hasSession()) return ended();
      return HttpResponse.json({ state: session.state, verify_code: session.verify_code });
    }),
    http.delete(apiUrl("/scan/session"), ({ request }) => {
      log(request);
      const over = deleteOverride?.();
      if (over) return over;
      if (!hasSession()) return ended();
      session.state = "ended";
      return new HttpResponse(null, { status: 204 });
    }),
    http.post(apiUrl("/scan/scans"), async ({ request }) => {
      log(request);
      const call: PhoneScanCall = { code: ((await request.json()) as { code: string }).code, key: request.headers.get("Idempotency-Key") };
      scans.push(call);
      const over = scanOverride?.(call);
      if (over) return over;
      if (!hasSession()) return ended();
      if (session.state === "pending") return apiError(409, "scan_pending_approval");
      return HttpResponse.json(answers.get(call.code) ?? { outcome: "added", product_name: "Mứt gừng", unit_name: "Hộp" });
    }),
  );

  return {
    session,
    claimCodes: codes,
    answers,
    calls,
    scans,
    onScan: (fn) => {
      scanOverride = fn;
    },
    onSession: (fn) => {
      sessionOverride = fn;
    },
    onClaim: (fn) => {
      claimOverride = fn;
    },
    onDelete: (fn) => {
      deleteOverride = fn;
    },
  };
}
