import assert from 'node:assert/strict';
import test from 'node:test';

import { isSameGeometry } from '../src/pages/desktop/screen/geometry.ts';

test('polled regions keep their state object while the geometry is unchanged', () => {
  const region = { frameWidth: 1920, frameHeight: 1080, left: 0, top: 0, width: 1440, height: 1080 };
  assert.equal(isSameGeometry(region, { ...region }), true);
  assert.equal(isSameGeometry(region, { ...region, left: 1 }), false);
  assert.equal(isSameGeometry({ width: 1, height: 2 }, { width: 1, height: 2, left: 0 }), false);
});

test('null regions compare by identity', () => {
  assert.equal(isSameGeometry(null, null), true);
  assert.equal(isSameGeometry(null, { width: 1, height: 1 }), false);
  assert.equal(isSameGeometry({ width: 1, height: 1 }, null), false);
});
