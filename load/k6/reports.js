// Interactive reports (NFR-05): the owner's device per tenant opens the dashboard and runs every
// catalogue report that needs no required filter over the last 30 days (or as of today).
// Budget: p95 < 5000 ms, unexpected responses < 0.5 %.
import { sleep } from 'k6';
import { env, rampingVus, tenantSlugs, thinkSeconds } from './lib/tally.js';
import { loadFixtures } from './lib/fixtures.js';
import { reportCatalogue, reportsAction } from './lib/flows.js';

const budgets = JSON.parse(open('./thresholds.json'));

export const options = {
  setupTimeout: '15m',
  scenarios: { reports: rampingVus(tenantSlugs().length, 'reports') },
  thresholds: budgets.reports,
};

export function setup() {
  const fixtures = loadFixtures();
  return { fixtures, reports: reportCatalogue(fixtures[0].sessions[0]) };
}

export function reports(data) {
  const fx = data.fixtures[(__VU - 1) % data.fixtures.length];
  reportsAction(fx.sessions[0], data.reports);
  sleep(thinkSeconds() * 3);
}
