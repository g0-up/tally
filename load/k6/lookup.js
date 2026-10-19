// Lookup mix (NFR-03): picker search, SKU and stock detail, POS bootstrap and party lookups by
// LOAD_DEVICES devices per tenant. Budget: p95 < 1000 ms, unexpected responses < 0.5 %.
import { sleep } from 'k6';
import { env, rampingVus, tenantSlugs, thinkSeconds } from './lib/tally.js';
import { deviceOf, loadFixtures } from './lib/fixtures.js';
import { lookupAction } from './lib/flows.js';

const budgets = JSON.parse(open('./thresholds.json'));

export const options = {
  setupTimeout: '15m',
  scenarios: { lookup: rampingVus(tenantSlugs().length * env.devices, 'lookup') },
  thresholds: budgets.lookup,
};

export function setup() {
  return { fixtures: loadFixtures() };
}

export function lookup(data) {
  const { fx, s } = deviceOf(data.fixtures);
  lookupAction(fx, s);
  sleep(thinkSeconds());
}
