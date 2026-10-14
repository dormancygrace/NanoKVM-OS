import assert from 'node:assert/strict';
import test from 'node:test';

import { halvedVisibleRect } from '../src/pages/desktop/screen/visible-rect.ts';

const rect = (width, height) => ({ x: 0, y: 0, width, height });

test('only an exactly halved visible rectangle is corrected', () => {
  // Edge 155, H.265 2560x1440 (seen on the stand).
  assert.deepEqual(
    halvedVisibleRect({ codedWidth: 2560, codedHeight: 1440, visibleRect: rect(1280, 720) }),
    rect(2560, 1440)
  );
  // A halved 1080p frame (coded 1088), from half the display or half the
  // coded height; which one Edge produces is unverified.
  assert.deepEqual(
    halvedVisibleRect({ codedWidth: 1920, codedHeight: 1088, visibleRect: rect(960, 540) }),
    rect(1920, 1080)
  );
  assert.deepEqual(
    halvedVisibleRect({ codedWidth: 1920, codedHeight: 1088, visibleRect: rect(960, 544) }),
    rect(1920, 1088)
  );
  // Correct frames and real crops are left alone.
  assert.equal(
    halvedVisibleRect({ codedWidth: 2560, codedHeight: 1440, visibleRect: rect(2560, 1440) }),
    null
  );
  assert.equal(
    halvedVisibleRect({ codedWidth: 1920, codedHeight: 1088, visibleRect: rect(1920, 1080) }),
    null
  );
  assert.equal(
    halvedVisibleRect({ codedWidth: 2560, codedHeight: 1440, visibleRect: rect(1000, 700) }),
    null
  );
  assert.equal(
    halvedVisibleRect({
      codedWidth: 2560,
      codedHeight: 1440,
      visibleRect: { x: 8, y: 0, width: 1280, height: 720 }
    }),
    null
  );
  assert.equal(halvedVisibleRect({ displayWidth: 1920 }), null);
});
