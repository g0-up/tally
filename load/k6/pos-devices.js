// Last units under load (AT-03 at NFR-04 scale): POS_DEVICES (default 3) devices per tenant sell
// the same color of one SKU at once until it runs out. Setup brings that color to exactly
// POS_LAST_UNITS (default 5) sellable units with a stock decrease (DC, count_variance reason); the
// devices then sell one unit per checkout, each under its own Idempotency-Key. Pass: every tenant
// sells exactly POS_LAST_UNITS units, the losers get 409 insufficient_stock, and the color ends at
// 0 on hand, never below. This changes the seeded stock: run it last or re-seed afterwards.
import { fail, sleep } from 'k6';
import { Counter, Rate } from 'k6/metrics';
import { call, must, signIn, tenantSlugs } from './lib/tally.js';
import { post, posCheckout, saleLine } from './lib/flows.js';

const budgets = JSON.parse(open('./thresholds.json'));
const DEVICES = Number(__ENV.POS_DEVICES || 3);
const UNITS = Number(__ENV.POS_LAST_UNITS || 5);
const CANDIDATES = 50;
const slugs = tenantSlugs();

if (!Number.isInteger(DEVICES) || DEVICES < 2 || !Number.isInteger(UNITS) || UNITS < 1) {
  throw new Error('POS_DEVICES must be an integer >= 2 and POS_LAST_UNITS an integer >= 1');
}

const sold = new Counter('pos_last_units_sold');
const oversold = new Counter('pos_oversold');
const finalOk = new Rate('pos_final_stock_ok');

const thresholds = { ...budgets.devices };
for (const slug of slugs) {
  thresholds[`pos_last_units_sold{tenant:${slug}}`] = [`count==${UNITS}`];
}

export const options = {
  setupTimeout: '15m',
  scenarios: {
    devices: {
      executor: 'per-vu-iterations',
      vus: slugs.length * DEVICES,
      // More attempts than units, so every device ends on insufficient_stock.
      iterations: UNITS + 3,
      maxDuration: '10m',
    },
  },
  thresholds,
};

function num(v) {
  return Number(v);
}

function reasonId(s) {
  for (const kind of ['count_variance', 'loss']) {
    const list = must(call(s, 'GET', '/reasons', null, { kind: 'setup', name: '/reasons', query: { kind, active: true } }), 200, `${s.slug} reasons`).json('data') || [];
    if (list.length > 0) {
      return list[0].id;
    }
  }
  return fail(`${s.slug}: no active count_variance or loss reason for the stock decrease`);
}

// colorStock returns the stock row of the SKU and the entry of the color.
function colorStock(s, skuId, colorId) {
  const row = must(call(s, 'GET', `/stock/${skuId}`, null, { kind: 'setup', name: '/stock/:skuId' }), 200, `${s.slug} stock`).json();
  return { row, color: (row.colors || []).find((c) => c.color_id === colorId) };
}

// eligible reports whether the color can be brought to exactly UNITS sellable units: whole
// units, nothing reserved on the SKU, nothing quarantined or expired in the color.
function eligible(st) {
  const c = st.color;
  return (
    c &&
    !st.row.track_lot &&
    !st.row.track_expiry &&
    num(st.row.reserved) === 0 &&
    num(c.quarantine) === 0 &&
    num(c.expired) === 0 &&
    Number.isInteger(num(c.sellable)) &&
    num(c.sellable) >= UNITS &&
    num(c.on_hand) === num(c.sellable)
  );
}

function prepareTenant(slug, index) {
  const sessions = [];
  for (let d = 0; d < DEVICES; d++) {
    sessions.push(signIn(slug, index, d));
  }
  const s = sessions[0];
  const boot = must(call(s, 'GET', '/pos/bootstrap', null, { kind: 'setup', name: '/pos/bootstrap' }), 200, `${slug} pos bootstrap`).json();
  const cash = (boot.payment_methods || []).find((m) => m.cash && m.active);
  if (!cash) {
    fail(`${slug}: no active cash payment method`);
  }
  const tiles = (boot.tiles || []).filter((t) => (t.stock || []).some((x) => x.color_id && num(x.qty) >= UNITS)).slice(0, CANDIDATES);
  for (const t of tiles) {
    const sku = must(call(s, 'GET', `/skus/${t.sku_id}`, null, { kind: 'setup', name: '/skus/:id' }), 200, `${slug} sku`).json();
    if (sku.base_unit_id !== t.unit_id) {
      continue;
    }
    const entry = t.stock.find((x) => x.color_id && num(x.qty) >= UNITS);
    const st = colorStock(s, t.sku_id, entry.color_id);
    if (!eligible(st)) {
      continue;
    }
    const line = { sku_id: t.sku_id, unit_id: t.unit_id, color_id: entry.color_id };
    const extra = num(st.color.sellable) - UNITS;
    if (extra > 0) {
      const body = {
        kind: 'decrease',
        reason_id: reasonId(s),
        note: 'k6 pos-devices: leave the last units',
        lines: [{ sku_id: line.sku_id, unit_id: line.unit_id, color_id: line.color_id, qty: String(extra) }],
      };
      const r = post(s, '/stock-adjustments', body, { kind: 'setup', name: '/stock-adjustments' }, 'setup stock decrease', [201]);
      if (!r.ok) {
        fail(`${slug}: stock decrease answered ${r.res.status} ${r.code}`);
      }
    }
    const after = colorStock(s, line.sku_id, line.color_id);
    if (!after.color || num(after.color.sellable) !== UNITS) {
      fail(`${slug}: the color holds ${after.color ? after.color.sellable : 'nothing'} sellable units, want ${UNITS}`);
    }
    return { slug, sessions, cashMethodId: cash.id, line };
  }
  return fail(`${slug}: no colored SKU with ${UNITS}+ whole sellable units, no reservation and no quarantine`);
}

export function setup() {
  const tenants = [];
  slugs.forEach((slug, i) => {
    tenants.push(prepareTenant(slug, i));
    sleep(0.2);
  });
  return { tenants };
}

let drained = false;

export default function (data) {
  if (drained) {
    return;
  }
  const t = data.tenants[Math.floor((__VU - 1) / DEVICES)];
  const s = t.sessions[(__VU - 1) % DEVICES];
  const r = posCheckout(s, t.cashMethodId, [saleLine(t.line, 1)], 'posting');
  if (r.res.status === 201) {
    sold.add(1, { tenant: t.slug });
  } else if (r.code === 'insufficient_stock') {
    drained = true;
  }
}

export function teardown(data) {
  for (const t of data.tenants) {
    const st = colorStock(t.sessions[0], t.line.sku_id, t.line.color_id);
    const onHand = st.color ? num(st.color.on_hand) : 0;
    oversold.add(onHand < 0 ? 1 : 0, { tenant: t.slug });
    finalOk.add(onHand === 0, { tenant: t.slug });
    if (onHand !== 0) {
      console.error(`${t.slug}: the color ends at ${onHand} on hand, want 0`);
    }
  }
}
