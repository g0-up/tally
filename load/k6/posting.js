// Posting mix (NFR-03): POS checkout, invoice confirm with its stock-out, goods receipt and
// customer payment by LOAD_DEVICES devices per tenant. Every posting carries a fresh
// Idempotency-Key (kept on the documented sale_changed / shift_changed retry) and the invoice
// confirm sends If-Match with the ETag of the draft. Budget: p95 < 2000 ms, unexpected
// responses < 0.5 % (insufficient_stock once a color runs out is expected).
import { sleep } from 'k6';
import { env, rampingVus, tenantSlugs, thinkSeconds } from './lib/tally.js';
import { deviceOf, loadFixtures } from './lib/fixtures.js';
import { postingAction } from './lib/flows.js';

const budgets = JSON.parse(open('./thresholds.json'));

export const options = {
  setupTimeout: '15m',
  scenarios: { posting: rampingVus(tenantSlugs().length * env.devices, 'posting') },
  thresholds: budgets.posting,
};

export function setup() {
  return { fixtures: loadFixtures() };
}

export function posting(data) {
  const { fx, s } = deviceOf(data.fixtures);
  postingAction(fx, s);
  sleep(thinkSeconds());
}
