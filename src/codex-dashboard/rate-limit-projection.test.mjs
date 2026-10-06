import assert from 'node:assert/strict';
import test from 'node:test';
import { buildDashboardView } from './dashboard-ui.mjs';

test('rate projections reject invalid windows and distinguish below, at and above the limit', () => {
  const now = Date.parse('2026-10-06T12:00:00Z');
  const day = 24 * 60 * 60 * 1000;
  const cases = [
    { reset: null, minutes: 10080, percent: 50, available: false },
    { reset: now + day, minutes: Number.NaN, percent: 50, available: false },
    { reset: now + day, minutes: 0, percent: 50, available: false },
    { reset: now - 1, minutes: 10080, percent: 50, available: false },
    { reset: now + 8 * day, minutes: 10080, percent: 50, available: false },
    { reset: now + 3.5 * day, minutes: 10080, percent: 50, available: true, value: '100%', meta: 'At current pace · on track to reach the limit' },
    { reset: now + 5 * day, minutes: 10080, percent: 50, available: true, value: '175%', meta: 'At current pace · 75 points over limit' },
    { reset: now + 2 * day, minutes: 10080, percent: 10, available: true, value: '14%', meta: 'At current pace · 86 points below limit' },
    { reset: now + 30 * 60000, minutes: 60, percent: 25, available: true, value: '25%', meta: 'At current pace · 75 points below limit' },
  ];
  for (const entry of cases) {
    const view = buildDashboardView({ available: true, sessions: [], rateLimits: {
      primary: { usedPercent: entry.percent, windowMinutes: entry.minutes, resetsAt: entry.reset === null ? null : entry.reset / 1000 },
    } }, now);
    assert.equal(view.rateLimit.projection.available, entry.available, JSON.stringify(entry));
    if (entry.available) {
      assert.equal(view.rateLimit.projection.value, entry.value);
      assert.equal(view.rateLimit.projection.meta, entry.meta);
    } else {
      assert.equal(view.rateLimit.projection.value, 'Unavailable');
      assert.equal(view.rateLimit.projection.meta, 'Projection unavailable');
    }
  }
});
