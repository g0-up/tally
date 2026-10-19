// Per-tenant fixtures read in setup() from the seeded reference data: one signed-in session per
// device, search terms, SKU ids, sellable lines, customers, suppliers and the cash method.
import { fail, sleep } from 'k6';
import { call, env, must, signIn, tenantSlugs } from './tally.js';

const SAMPLE_TILES = 200;
const DETAIL_SKUS = Number(__ENV.LOAD_FIXTURE_SKUS || 30);

function words(text) {
  return String(text || '')
    .split(/\s+/)
    .map((w) => w.replace(/[.,;:()[\]{}\/"'`]/g, ''))
    .filter((w) => w.length >= 3);
}

function sample(list, n) {
  if (list.length <= n) {
    return list.slice();
  }
  const step = list.length / n;
  const out = [];
  for (let i = 0; i < n; i++) {
    out.push(list[Math.floor(i * step)]);
  }
  return out;
}

function positive(qty) {
  return Number(qty) > 0;
}

function tenantFixture(slug, index, devices) {
  const sessions = [];
  for (let d = 0; d < devices; d++) {
    sessions.push(signIn(slug, index, d));
  }
  const s = sessions[0];
  const q = { kind: 'setup' };

  const boot = must(call(s, 'GET', '/pos/bootstrap', null, { ...q, name: '/pos/bootstrap' }), 200, `${slug} pos bootstrap`).json();
  const cash = (boot.payment_methods || []).find((m) => m.cash && m.active);
  if (!cash) {
    fail(`${slug}: no active cash payment method`);
  }
  const tiles = sample(boot.tiles || [], SAMPLE_TILES);
  if (tiles.length === 0) {
    fail(`${slug}: the POS has no tiles; seed the tenant with \`tally seed load\` first`);
  }
  const terms = [];
  for (const t of tiles) {
    terms.push(t.sku_code);
    for (const w of words(t.name)) {
      if (terms.length < 400) {
        terms.push(w);
      }
    }
  }

  // Lines the posting scenarios may sell, invoice and receive: stocked, not lot-tracked, priced
  // in the base unit, one line per color in stock.
  const lines = [];
  for (const t of sample(tiles.filter((x) => (x.stock || []).some((st) => positive(st.qty))), DETAIL_SKUS)) {
    const sku = must(call(s, 'GET', `/skus/${t.sku_id}`, null, { ...q, name: '/skus/:id' }), 200, `${slug} sku`).json();
    if (sku.track_lot || sku.track_expiry || sku.status !== 'active' || sku.base_unit_id !== t.unit_id) {
      continue;
    }
    for (const st of t.stock) {
      if (positive(st.qty)) {
        lines.push({ sku_id: t.sku_id, unit_id: t.unit_id, color_id: st.color_id, cost: sku.ref_cost });
      }
    }
  }

  const customers = must(
    call(s, 'GET', '/customers', null, { ...q, name: '/customers', query: { status: 'active', limit: 100 } }),
    200,
    `${slug} customers`,
  ).json('data') || [];
  const suppliers = must(
    call(s, 'GET', '/suppliers', null, { ...q, name: '/suppliers', query: { status: 'active', limit: 100 } }),
    200,
    `${slug} suppliers`,
  ).json('data') || [];
  const partyTerms = [];
  for (const p of customers.concat(suppliers)) {
    partyTerms.push(...words(p.name).slice(0, 1));
  }

  return {
    slug,
    sessions,
    cashMethodId: cash.id,
    terms,
    skuIds: tiles.map((t) => t.sku_id),
    lines,
    // A credit limit would make invoices and debt sales wait for an owner exception.
    customerIds: customers.filter((c) => c.credit_limit === null).map((c) => c.id),
    supplierIds: suppliers.map((p) => p.id),
    partyTerms: partyTerms.length > 0 ? partyTerms : ['a'],
  };
}

// loadFixtures signs every tenant's devices in and reads its fixtures.
export function loadFixtures() {
  const out = [];
  tenantSlugs().forEach((slug, i) => {
    out.push(tenantFixture(slug, i, env.devices));
    sleep(0.2);
  });
  return out;
}

// deviceOf maps the running VU to its tenant fixture and device session (VUs round-robin over
// tenants first, so every tenant gets its devices as the load ramps up).
export function deviceOf(fixtures) {
  const n = fixtures.length;
  const vu = __VU - 1;
  const fx = fixtures[vu % n];
  return { fx, s: fx.sessions[Math.floor(vu / n) % fx.sessions.length] };
}
