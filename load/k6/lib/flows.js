// One device action per call: the lookup, posting and report mixes shared by the scenario
// scripts and the soak test. Request paths and bodies follow apps/api/api/openapi.yaml.
import { sleep } from 'k6';
import { call, errorCode, expect, idemKey, must, pick, storeDate, weighted } from './tally.js';

// Races the API resolves on purpose: a retry under the same Idempotency-Key is the documented answer.
const RETRY_409 = ['sale_changed', 'shift_changed'];
const MAX_ATTEMPTS = 3;

function int(min, max) {
  return min + Math.floor(Math.random() * (max - min + 1));
}

export function saleLine(l, qty) {
  const line = { sku_id: l.sku_id, unit_id: l.unit_id, qty: String(qty) };
  if (l.color_id) {
    line.color_id = l.color_id;
  }
  return line;
}

// post sends a posting command under one Idempotency-Key, retrying the documented races with
// the same key, and returns { ok, code, res } of the last attempt.
export function post(s, path, body, opts, label, okStatuses, allowed409) {
  const key = idemKey();
  const allowed = RETRY_409.concat(allowed409 || []);
  for (let attempt = 1; ; attempt++) {
    const res = call(s, 'POST', path, body, { ...opts, idem: key });
    const r = expect(res, label, opts.kind, okStatuses, allowed);
    if (res.status === 409 && RETRY_409.includes(r.code) && attempt < MAX_ATTEMPTS) {
      sleep(0.05 * attempt);
      continue;
    }
    return { ok: r.ok, code: r.code, res };
  }
}

// posCheckout prices the cart as the POS screen does, then sells it to a walk-in customer paid
// exactly in cash. insufficient_stock is an expected answer once a color runs out.
export function posCheckout(s, cashMethodId, lines, kind) {
  const priced = call(s, 'POST', '/pricing/calculate', { lines }, { kind: 'pricing', name: '/pricing/calculate' });
  if (!expect(priced, 'pricing calculate', 'pricing', [200]).ok) {
    return { ok: false, code: errorCode(priced), res: priced };
  }
  const total = priced.json('total_amount');
  const body = {
    lines,
    payments: [{ payment_method_id: cashMethodId, amount: total }],
    cash_tendered_amount: total,
  };
  return post(s, '/pos/checkout', body, { kind, name: '/pos/checkout' }, 'pos checkout', [201], ['insufficient_stock']);
}

// ---- lookup ----

export function lookupAction(fx, s) {
  const kind = 'lookup';
  let res;
  switch (weighted({ searchSku: 40, sku: 15, stock: 10, bootstrap: 10, searchCustomer: 10, searchSupplier: 5, customers: 5, suppliers: 5 })) {
    case 'searchSku':
      res = call(s, 'GET', '/search', null, { kind, name: '/search?type=sku', query: { type: 'sku', q: pick(fx.terms), limit: 20 } });
      expect(res, 'search sku', kind, [200]);
      break;
    case 'sku':
      res = call(s, 'GET', `/skus/${pick(fx.skuIds)}`, null, { kind, name: '/skus/:id' });
      expect(res, 'sku detail', kind, [200]);
      break;
    case 'stock':
      res = call(s, 'GET', `/stock/${pick(fx.skuIds)}`, null, { kind, name: '/stock/:skuId' });
      expect(res, 'stock detail', kind, [200]);
      break;
    case 'bootstrap':
      res = call(s, 'GET', '/pos/bootstrap', null, { kind, name: '/pos/bootstrap' });
      expect(res, 'pos bootstrap', kind, [200]);
      break;
    case 'searchCustomer':
      res = call(s, 'GET', '/search', null, { kind, name: '/search?type=customer', query: { type: 'customer', q: pick(fx.partyTerms), limit: 20 } });
      expect(res, 'search customer', kind, [200]);
      break;
    case 'searchSupplier':
      res = call(s, 'GET', '/search', null, { kind, name: '/search?type=supplier', query: { type: 'supplier', q: pick(fx.partyTerms), limit: 20 } });
      expect(res, 'search supplier', kind, [200]);
      break;
    case 'customers':
      res = call(s, 'GET', '/customers', null, { kind, name: '/customers', query: { q: pick(fx.partyTerms), limit: 50 } });
      expect(res, 'list customers', kind, [200]);
      break;
    default:
      res = call(s, 'GET', '/suppliers', null, { kind, name: '/suppliers', query: { q: pick(fx.partyTerms), limit: 50 } });
      expect(res, 'list suppliers', kind, [200]);
  }
}

// ---- posting ----

function cart(fx, max) {
  const out = [];
  const used = {};
  const n = int(1, max);
  for (let i = 0; i < n * 3 && out.length < n; i++) {
    const l = pick(fx.lines);
    const id = `${l.sku_id}/${l.color_id}`;
    if (!used[id]) {
      used[id] = true;
      out.push(l);
    }
  }
  return out;
}

function invoiceFlow(fx, s, kind) {
  const colored = fx.lines.filter((l) => l.color_id);
  if (colored.length === 0 || fx.customerIds.length === 0) {
    return false;
  }
  const lines = [pick(colored)].map((l) => saleLine(l, int(1, 2)));
  const created = post(s, '/invoices', { customer_party_id: pick(fx.customerIds), lines }, { kind, name: '/invoices' }, 'invoice create', [201]);
  if (!created.ok) {
    return true;
  }
  const id = created.res.json('id');
  const confirmed = post(s, `/invoices/${id}:confirm`, {}, { kind, name: '/invoices/:id:confirm', ifMatch: created.res.headers.Etag }, 'invoice confirm', [200]);
  if (!confirmed.ok || confirmed.res.status !== 200) {
    return true;
  }
  const out = (confirmed.res.json('lines') || [])
    .filter((ln) => Number(ln.reserved_base_qty) > 0)
    .map((ln) => ({ invoice_line_id: ln.id, base_qty: ln.reserved_base_qty }));
  if (out.length > 0) {
    post(s, `/invoices/${id}/stock-outs`, { lines: out }, { kind, name: '/invoices/:id/stock-outs' }, 'invoice stock-out', [201]);
  }
  return true;
}

function receiptFlow(fx, s, kind) {
  if (fx.supplierIds.length === 0) {
    return false;
  }
  const lines = cart(fx, 3).map((l) => ({
    sku_id: l.sku_id,
    unit_id: l.unit_id,
    color_id: l.color_id,
    qty: String(int(5, 20)),
    unit_price: Number(l.cost) > 0 ? l.cost : null,
  }));
  post(s, '/receipts', { supplier_party_id: pick(fx.supplierIds), lines }, { kind, name: '/receipts' }, 'receipt', [201]);
  return true;
}

function paymentFlow(fx, s, kind) {
  if (fx.customerIds.length === 0) {
    return false;
  }
  const body = {
    direction: 'in',
    purpose: 'advance',
    party_id: pick(fx.customerIds),
    payment_method_id: fx.cashMethodId,
    amount: String(1000 * int(10, 500)),
  };
  post(s, '/payments', body, { kind, name: '/payments' }, 'payment', [201]);
  return true;
}

export function postingAction(fx, s) {
  const kind = 'posting';
  if (fx.lines.length === 0) {
    return;
  }
  const action = weighted({ pos: 50, invoice: 20, receipt: 15, payment: 15 });
  if (action === 'invoice' && invoiceFlow(fx, s, kind)) {
    return;
  }
  if (action === 'receipt' && receiptFlow(fx, s, kind)) {
    return;
  }
  if (action === 'payment' && paymentFlow(fx, s, kind)) {
    return;
  }
  posCheckout(s, fx.cashMethodId, cart(fx, 3).map((l) => saleLine(l, int(1, 2))), kind);
}

// ---- reports ----

// reportCatalogue reads GET /reports once and keeps the reports a client can run without a
// required filter, each with the query its range kind needs.
export function reportCatalogue(s) {
  const specs = must(call(s, 'GET', '/reports', null, { kind: 'setup', name: '/reports' }), 200, 'report catalogue').json('data') || [];
  const out = [];
  for (const spec of specs) {
    if ((spec.filters || []).some((f) => f.required)) {
      continue;
    }
    const span = Math.min(30, spec.max_days || 30);
    let query = {};
    if (spec.range === 'period') {
      query = { from: storeDate(1 - span), to: storeDate(0) };
    } else if (spec.range === 'as_of') {
      query = { as_of: storeDate(0) };
    } else if (spec.range === 'future') {
      query = { days: span };
    }
    out.push({ slug: spec.slug, query });
  }
  return out;
}

export function reportsAction(s, reports) {
  const kind = 'report';
  if (reports.length === 0 || Math.random() < 0.3) {
    const res = call(s, 'GET', '/reports/dashboard', null, { kind, name: '/reports/dashboard', query: { period: pick(['day', '7d', 'month', 'year']) } });
    expect(res, 'dashboard', kind, [200]);
    return;
  }
  const r = pick(reports);
  const res = call(s, 'GET', `/reports/${r.slug}`, null, { kind, name: `/reports/${r.slug}`, query: r.query });
  expect(res, `report ${r.slug}`, kind, [200]);
}
