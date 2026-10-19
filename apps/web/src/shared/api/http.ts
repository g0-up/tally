import type { ZodType } from "zod";
import { ApiError, ERROR_CODES, apiErrorFromBody, networkError, parseRetryAfter, BAD_RESPONSE_MESSAGE } from "./api-error";
import type { CsrfSource } from "./csrf";

// Fetch wrapper speaking the API contract: same-origin base path, cookies, JSON, CSRF header on
// state-changing methods, Idempotency-Key and If-Match when the caller supplies them, and the
// error envelope turned into ApiError. Responses can be validated with a zod schema at this
// boundary so screens never see unchecked data.

export type HttpMethod = "GET" | "POST" | "PUT" | "PATCH" | "DELETE";

export type QueryValue = string | number | boolean | null | undefined;

export interface RequestOptions<T> {
  query?: Record<string, QueryValue | readonly string[]>;
  body?: unknown;
  /** One key per user intent, reused on every retry of that intent. */
  idempotencyKey?: string;
  /** ETag of the version being changed; sent as If-Match. */
  ifMatch?: string | null;
  signal?: AbortSignal;
  /** Validates the JSON response; a mismatch becomes ApiError bad_response. */
  schema?: ZodType<T>;
  /** Skips the global error handler (session probes, background polling). */
  silent?: boolean;
}

export interface ApiResponse<T> {
  data: T;
  etag: string | null;
  status: number;
}

export interface HttpClientConfig {
  /** Same-origin base path, e.g. "/api/v1" or "/api/admin/v1". */
  baseUrl: string;
  csrf: CsrfSource;
  /** Global reaction to errors (session expiry, read-only plan, stale data). */
  onError?: (error: ApiError, request: { method: HttpMethod; path: string }) => void;
  /** Gets a new CSRF token after a csrf_failed answer; the request is then retried once. */
  refreshCsrf?: () => Promise<void>;
  fetchImpl?: typeof fetch;
}

export interface HttpClient {
  readonly baseUrl: string;
  /** Stores the CSRF token returned by a session or login response. */
  setCsrfToken(token: string | null): void;
  request<T>(method: HttpMethod, path: string, options?: RequestOptions<T>): Promise<ApiResponse<T>>;
  get<T>(path: string, options?: Omit<RequestOptions<T>, "body" | "idempotencyKey">): Promise<T>;
  post<T>(path: string, body?: unknown, options?: Omit<RequestOptions<T>, "body">): Promise<T>;
  patch<T>(path: string, body?: unknown, options?: Omit<RequestOptions<T>, "body">): Promise<T>;
  put<T>(path: string, body?: unknown, options?: Omit<RequestOptions<T>, "body">): Promise<T>;
  delete<T>(path: string, options?: Omit<RequestOptions<T>, "body">): Promise<T>;
}

const SAFE_METHODS: ReadonlySet<HttpMethod> = new Set(["GET"]);

export function buildUrl(baseUrl: string, path: string, query?: RequestOptions<unknown>["query"]): string {
  if (!path.startsWith("/") || path.startsWith("//")) {
    // Requests always stay on the current host; absolute or protocol-relative URLs are refused.
    throw new Error(`API path must be a same-origin absolute path: "${path}"`);
  }
  const params = new URLSearchParams();
  for (const [k, v] of Object.entries(query ?? {})) {
    if (v === null || v === undefined || v === "") continue;
    if (Array.isArray(v)) v.forEach((x) => params.append(k, x));
    else params.append(k, String(v));
  }
  const qs = params.toString();
  return baseUrl.replace(/\/$/, "") + path + (qs ? `?${qs}` : "");
}

async function readJson(res: Response): Promise<unknown> {
  if (res.status === 204 || res.status === 205) return undefined;
  const text = await res.text();
  if (text === "") return undefined;
  try {
    return JSON.parse(text) as unknown;
  } catch {
    return Symbol.for("tally.unreadable");
  }
}

export function createHttpClient(config: HttpClientConfig): HttpClient {
  const doFetch = config.fetchImpl ?? ((...args: Parameters<typeof fetch>) => fetch(...args));

  async function send<T>(method: HttpMethod, path: string, options: RequestOptions<T>, csrfRetried: boolean): Promise<ApiResponse<T>> {
    const headers = new Headers({ Accept: "application/json" });
    if (options.body !== undefined) headers.set("Content-Type", "application/json");
    if (!SAFE_METHODS.has(method)) {
      const token = config.csrf.get();
      if (token) headers.set("X-CSRF-Token", token);
    }
    if (options.idempotencyKey) headers.set("Idempotency-Key", options.idempotencyKey);
    if (options.ifMatch) headers.set("If-Match", options.ifMatch);

    let res: Response;
    try {
      res = await doFetch(buildUrl(config.baseUrl, path, options.query), {
        method,
        headers,
        credentials: "include",
        ...(options.body !== undefined ? { body: JSON.stringify(options.body) } : {}),
        ...(options.signal ? { signal: options.signal } : {}),
      });
    } catch (e) {
      if (e instanceof DOMException && e.name === "AbortError") throw e;
      throw networkError();
    }

    const body = await readJson(res);
    if (!res.ok) {
      const retryAfterSeconds = parseRetryAfter(res.headers.get("Retry-After"));
      const error =
        typeof body === "symbol"
          ? new ApiError({ status: res.status, code: ERROR_CODES.badResponse, message: BAD_RESPONSE_MESSAGE, retryAfterSeconds })
          : apiErrorFromBody(res.status, body, retryAfterSeconds);
      if (error.status === 403 && error.code === ERROR_CODES.csrfFailed && !csrfRetried && config.refreshCsrf) {
        // A failed refresh leaves the original refusal as the answer.
        const refreshed = await config.refreshCsrf().then(
          () => true,
          () => false,
        );
        if (refreshed) return send(method, path, options, true);
      }
      throw error;
    }
    if (typeof body === "symbol") {
      throw new ApiError({ status: res.status, code: ERROR_CODES.badResponse, message: BAD_RESPONSE_MESSAGE });
    }
    let data = body as T;
    if (options.schema) {
      const parsed = options.schema.safeParse(body);
      if (!parsed.success) throw new ApiError({ status: res.status, code: ERROR_CODES.badResponse, message: BAD_RESPONSE_MESSAGE });
      data = parsed.data;
    }
    return { data, etag: res.headers.get("ETag"), status: res.status };
  }

  async function request<T>(method: HttpMethod, path: string, options: RequestOptions<T> = {}): Promise<ApiResponse<T>> {
    try {
      return await send(method, path, options, false);
    } catch (e) {
      if (e instanceof ApiError && !options.silent) config.onError?.(e, { method, path });
      throw e;
    }
  }

  return {
    baseUrl: config.baseUrl,
    setCsrfToken: (token) => config.csrf.set(token),
    request,
    get: async (path, options) => (await request("GET", path, options)).data,
    post: async (path, body, options) => (await request("POST", path, { ...options, body })).data,
    patch: async (path, body, options) => (await request("PATCH", path, { ...options, body })).data,
    put: async (path, body, options) => (await request("PUT", path, { ...options, body })).data,
    delete: async (path, options) => (await request("DELETE", path, options)).data,
  };
}
