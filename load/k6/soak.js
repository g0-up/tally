// Soak: the lookup, posting and report mixes together for SOAK_DURATION
// (default 4h) with the worker running. The k6 budgets are the same as the short runs; memory
// growth, stuck outbox rows and `tally ledger verify` are checked around the run (load/README.md).
import { sleep } from 'k6';
import { env, tenantSlugs, thinkSeconds } from './lib/tally.js';
import { deviceOf, loadFixtures } from './lib/fixtures.js';
import { lookupAction, postingAction, reportCatalogue, reportsAction } from './lib/flows.js';

const budgets = JSON.parse(open('./thresholds.json'));
const duration = __ENV.SOAK_DURATION || '4h';
const tenants = tenantSlugs().length;

function constant(vus, exec) {
  return { executor: 'constant-vus', exec, vus, duration, gracefulStop: '1m' };
}

export const options = {
  setupTimeout: '15m',
  scenarios: {
    lookup: constant(tenants * env.devices, 'lookup'),
    posting: constant(tenants * env.devices, 'posting'),
    reports: constant(tenants, 'reports'),
  },
  thresholds: { ...budgets.lookup, ...budgets.posting, ...budgets.reports },
};

export function setup() {
  const fixtures = loadFixtures();
  return { fixtures, reports: reportCatalogue(fixtures[0].sessions[0]) };
}

export function lookup(data) {
  const { fx, s } = deviceOf(data.fixtures);
  lookupAction(fx, s);
  sleep(thinkSeconds());
}

export function posting(data) {
  const { fx, s } = deviceOf(data.fixtures);
  postingAction(fx, s);
  sleep(thinkSeconds() * 2);
}

export function reports(data) {
  const fx = data.fixtures[(__VU - 1) % data.fixtures.length];
  reportsAction(fx.sessions[0], data.reports);
  sleep(thinkSeconds() * 10);
}
