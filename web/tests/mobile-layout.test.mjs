import assert from 'node:assert/strict';
import test from 'node:test';

import { normalizeInputAdapterMode, resolveInputAdapter } from '../src/lib/input-adapter.ts';
import {
  clampMobileMenuTop,
  getInitialMobileMenuPlacement,
  getResponsiveDeviceState
} from '../src/lib/mobile-layout.ts';

test('portrait phones use a rail, landscape and desktop use the horizontal bar', () => {
  const phone = { width: 390, height: 844, hasTouch: true, coarsePointer: true, userAgent: '' };
  assert.equal(getResponsiveDeviceState(phone).isMobilePortrait, true);
  assert.equal(
    getResponsiveDeviceState({ ...phone, width: 844, height: 390 }).isMobilePortrait,
    false
  );
  assert.equal(
    getResponsiveDeviceState({ ...phone, hasTouch: false, coarsePointer: false }).isMobilePortrait,
    false
  );
});
test('saved rail position remains reachable after rotation or a smaller viewport', () => {
  assert.equal(clampMobileMenuTop(800, 390, 350), 48);
  assert.deepEqual(getInitialMobileMenuPlacement({ edge: 'left', top: 800 }, 390, 350), {
    edge: 'left',
    top: 48
  });
  assert.equal(getInitialMobileMenuPlacement({ edge: 'right', top: NaN }, 844, 400).edge, 'right');
});
test('auto input handles a phone and preserves explicit desktop pointer lock', () => {
  const phone = { hasTouch: true, coarsePointer: true, finePointer: false, canPointerLock: true };
  assert.equal(resolveInputAdapter('auto', 'relative', phone), 'touchpad');
  assert.equal(resolveInputAdapter('pointer-lock', 'relative', phone), 'pointer-lock');
  assert.equal(
    resolveInputAdapter('auto', 'relative', {
      ...phone,
      hasTouch: false,
      coarsePointer: false,
      finePointer: true
    }),
    'pointer-lock'
  );
  assert.equal(normalizeInputAdapterMode('direct-touch'), 'touchpad');
  assert.equal(normalizeInputAdapterMode('invalid'), 'auto');
});
