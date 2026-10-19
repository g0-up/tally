// Shared plumbing of the Tally load scenarios: environment, tenant targets, sign-in and a request
// helper that speaks the API's conventions (cookie session, double-submit CSRF, Idempotency-Key,
// If-Match, decimal strings). Every scenario reads the same environment; see load/README.md.
import http from 'k6/http';
import { check, fail } from 'k6';
import { Rate } from 'k6/metrics';

const API_PREFIX = '/api/v1';

function str(name, fallback) {
  const v = __ENV[name];
  return v === undefined || v === '' ? fallback : v;
}

function int(name, fallback, min) {
  const raw = str(name, '');
  if (raw === '') {
    return fallback;
  }
  const n = Number(raw);
  if (!Number.isInteger(n) || n < min) {
    throw new Error(`${name} must be an integer >= ${min}, got "${raw}"`);
  }
  return n;
}

function trimSlash(s) {
  return s.replace(/\/+$/, '');
}

export const env = {
  // BASE_URL is where requests go; the tenant is picked by the Host header (TENANT_HOST).
  // TENANT_URL instead sends each tenant's requests to its own URL (DNS must resolve it).
  baseUrl: trimSlash(str('BASE_URL', 'http://localhost')),
  tenantHost: str('TENANT_HOST', '{slug}.localhost'),
  tenantUrl: trimSlash(str('TENANT_URL', '')),
  tenants: int('LOAD_TENANTS', 50, 1),
  slugPrefix: str('LOAD_SLUG_PREFIX', 'load-42-'),
  slugs: str('LOAD_SLUGS', ''),
  ownerLogin: str('LOAD_OWNER_LOGIN', 'owner@{slug}.load.tally.test'),
  ownerPassword: str('LOAD_OWNER_PASSWORD', ''),
  devices: int('LOAD_DEVICES', 3, 1),
  duration: str('LOAD_DURATION', '10m'),
  rampUp: str('LOAD_RAMP_UP', '1m'),
  thinkMin: int('LOAD_THINK_MIN_MS', 1000, 0),
  thinkMax: int('LOAD_THINK_MAX_MS', 3000, 0),
  // CF-Connecting-IP per device, honoured by the API only from TRUSTED_PROXY_CIDRS (the local
  // Traefik). Without it every device shares the load host's address and its rate limit.
  clientIps: str('LOAD_CLIENT_IPS', 'on') !== 'off',
};

if (env.thinkMax < env.thinkMin) {
  throw new Error('LOAD_THINK_MAX_MS must not be below LOAD_THINK_MIN_MS');
}

// tenantSlugs lists the tenants under load: LOAD_SLUGS when given, else LOAD_SLUG_PREFIX + 1..N,
// the "<slug-prefix>-<seed>-<n>" names of `tally seed load --tenants N --seed 42`.
export function tenantSlugs() {
  if (env.slugs !== '') {
    return env.slugs.split(',').map((s) => s.trim()).filter((s) => s !== '');
  }
  const out = [];
  for (let i = 1; i <= env.tenants; i++) {
    out.push(env.slugPrefix + String(i));
  }
  return out;
}

// clientIp is a stable address from 198.18.0.0/15 (RFC 2544 benchmarking) per tenant and device.
function clientIp(tenantIndex, device) {
  const n = tenantIndex * 8 + device + 1;
  return `198.${18 + ((n >> 16) & 1)}.${(n >> 8) & 255}.${n & 255}`;
}

function target(slug) {
  if (env.tenantUrl !== '') {
    return { base: env.tenantUrl.replace('{slug}', slug) + API_PREFIX, host: '' };
  }
  return { base: env.baseUrl + API_PREFIX, host: env.tenantHost.replace('{slug}', slug) };
}

// unexpected counts every measured response the scenario did not expect (any 4xx/5xx other than
// the 409 codes of a race the API resolves on purpose); thresholds.json caps its rate.
export const unexpected = new Rate('tally_unexpected_responses');

// Statuses k6 itself does not count in http_req_failed; 409 is judged by code in expect().
http.setResponseCallback(http.expectedStatuses({ min: 200, max: 399 }, 409));

function cookieHeader(cookies) {
  return Object.keys(cookies).map((k) => `${k}=${cookies[k]}`).join('; ');
}

function absorbCookies(session, res) {
  for (const name of Object.keys(res.cookies)) {
    if (name.includes('tally_admin')) {
      continue;
    }
    const list = res.cookies[name];
    if (list.length > 0) {
      session.cookies[name] = list[0].value;
    }
  }
}

// call sends one request as session s. opts: kind (threshold group tag), name (route template
// tag), idem (Idempotency-Key), ifMatch (ETag value), query (object).
export function call(s, method, path, body, opts) {
  const o = opts || {};
  const headers = { Accept: 'application/json' };
  if (s.host !== '') {
    headers.Host = s.host;
  }
  if (s.ip !== '') {
    headers['CF-Connecting-IP'] = s.ip;
  }
  const cookie = cookieHeader(s.cookies);
  if (cookie !== '') {
    headers.Cookie = cookie;
  }
  if (method !== 'GET' && method !== 'HEAD') {
    headers['X-CSRF-Token'] = s.csrf;
  }
  if (o.idem) {
    headers['Idempotency-Key'] = o.idem;
  }
  if (o.ifMatch) {
    headers['If-Match'] = o.ifMatch;
  }
  let payload = null;
  if (body !== null && body !== undefined) {
    headers['Content-Type'] = 'application/json';
    payload = JSON.stringify(body);
  }
  let url = s.base + path;
  if (o.query) {
    const qs = Object.keys(o.query)
      .filter((k) => o.query[k] !== undefined && o.query[k] !== null)
      .map((k) => `${encodeURIComponent(k)}=${encodeURIComponent(String(o.query[k]))}`)
      .join('&');
    if (qs !== '') {
      url += '?' + qs;
    }
  }
  // A fresh jar per request keeps one VU's jar from mixing the cookies of several tenants; the
  // session's own cookies travel in the Cookie header.
  return http.request(method, url, payload, {
    headers,
    jar: new http.CookieJar(),
    tags: { kind: o.kind || 'other', name: o.name || path, tenant: s.slug },
  });
}

// errorCode returns the API error code of a response, or ''.
export function errorCode(res) {
  try {
    const b = res.json();
    return b && b.error && typeof b.error.code === 'string' ? b.error.code : '';
  } catch (_) {
    return '';
  }
}

// expect records whether res is one of okStatuses or a 409 whose code is in allowed409, adds it to
// tally_unexpected_responses and a named check, and returns { ok, code }.
export function expect(res, label, kind, okStatuses, allowed409) {
  const code = res.status >= 400 ? errorCode(res) : '';
  const ok = okStatuses.includes(res.status) || (res.status === 409 && (allowed409 || []).includes(code));
  unexpected.add(!ok, { kind });
  check(res, { [`${label} expected`]: () => ok });
  if (!ok && __ENV.LOAD_VERBOSE === '1') {
    console.warn(`${label}: ${res.status} ${code}`);
  }
  return { ok, code };
}

// must fails the run (setup) unless res has the status.
export function must(res, status, label) {
  if (res.status !== status) {
    fail(`${label}: want ${status}, got ${res.status} ${errorCode(res)}`);
  }
  return res;
}

// signIn opens a session on the tenant's host as its owner: an anonymous CSRF token from
// GET /host-context, then POST /auth/login, keeping the session and session-bound CSRF cookies.
export function signIn(slug, tenantIndex, device) {
  if (env.ownerPassword === '') {
    fail('LOAD_OWNER_PASSWORD is required');
  }
  const t = target(slug);
  const s = {
    slug,
    base: t.base,
    host: t.host,
    ip: env.clientIps ? clientIp(tenantIndex, device) : '',
    cookies: {},
    csrf: '',
  };
  const hc = must(call(s, 'GET', '/host-context', null, { kind: 'setup', name: '/host-context' }), 200, `${slug} host-context`);
  if (hc.json('kind') !== 'tenant') {
    fail(`${slug}: host-context answered kind=${hc.json('kind')}; check TENANT_HOST / TENANT_URL`);
  }
  absorbCookies(s, hc);
  s.csrf = hc.json('csrf_token');
  const login = env.ownerLogin.replace('{slug}', slug);
  const lr = must(
    call(s, 'POST', '/auth/login', { login, password: env.ownerPassword }, { kind: 'setup', name: '/auth/login' }),
    200,
    `${slug} login`,
  );
  if (lr.json('status') !== 'signed_in') {
    fail(`${slug}: login answered ${lr.json('status')}; load owners must not have two-step verification`);
  }
  absorbCookies(s, lr);
  s.csrf = lr.json('csrf_token');
  return s;
}

// idemKey is a fresh Idempotency-Key.
export function idemKey() {
  return crypto.randomUUID();
}

export function pick(list) {
  return list[Math.floor(Math.random() * list.length)];
}

// weighted picks a key of {key: weight}.
export function weighted(weights) {
  const keys = Object.keys(weights);
  let total = 0;
  for (const k of keys) {
    total += weights[k];
  }
  let r = Math.random() * total;
  for (const k of keys) {
    r -= weights[k];
    if (r < 0) {
      return k;
    }
  }
  return keys[keys.length - 1];
}

// thinkSeconds is the pause between two actions of a device.
export function thinkSeconds() {
  return (env.thinkMin + Math.random() * (env.thinkMax - env.thinkMin)) / 1000;
}

// storeDate is a date in the store time zone (APP_TZ default Asia/Ho_Chi_Minh, UTC+7, no DST)
// offset by days.
export function storeDate(days) {
  const ms = Date.now() + 7 * 3600 * 1000 + (days || 0) * 86400 * 1000;
  return new Date(ms).toISOString().slice(0, 10);
}

// rampingVus is the load shape of a constant-device scenario.
export function rampingVus(vus, exec) {
  return {
    executor: 'ramping-vus',
    exec,
    startVUs: 0,
    stages: [
      { duration: env.rampUp, target: vus },
      { duration: env.duration, target: vus },
      { duration: '30s', target: 0 },
    ],
    gracefulRampDown: '30s',
  };
}

