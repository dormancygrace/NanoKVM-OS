import assert from 'node:assert/strict';
import test from 'node:test';

import { UpdateNoticeTracker } from '../src/pages/desktop/menu/settings/updates/notice.ts';

const installed = { state: 'installed', id: 'new-package' };

test('fresh interface hides a persisted successful result, including repeated polls', () => {
  const tracker = new UpdateNoticeTracker();
  for (let i = 0; i < 3; i++) {
    tracker.observe(installed);
    assert.equal(tracker.visible(installed), false);
  }
});

test('successful install remains visible until a full interface reload', () => {
  const tracker = new UpdateNoticeTracker();
  tracker.expect(installed.id);
  // The server can restart before the browser observes an installing poll.
  tracker.observe(installed);
  assert.equal(tracker.visible(installed), true);
  assert.equal(tracker.visible({ state: 'installed', id: 'old-package' }), false);
  assert.equal(new UpdateNoticeTracker().visible(installed), false);
});

test('observing an in-progress installation also requires reload on success', () => {
  const tracker = new UpdateNoticeTracker();
  tracker.observe({ state: 'installing', id: installed.id });
  tracker.observe(installed);
  assert.equal(tracker.visible(installed), true);
});

test('explicit rejection does not turn a historical result into a new success', () => {
  const tracker = new UpdateNoticeTracker();
  tracker.expect(installed.id);
  tracker.forget(installed.id);
  assert.equal(tracker.visible(installed), false);
});

test('errors, rollback and preparation remain visible on a fresh interface', () => {
  const tracker = new UpdateNoticeTracker();
  for (const state of ['failed', 'rolled-back', 'prepared', 'installing']) {
    assert.equal(tracker.visible({ state }), true);
  }
  assert.equal(tracker.visible({ state: 'installed' }), false);
});
