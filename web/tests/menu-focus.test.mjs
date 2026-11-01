import assert from 'node:assert/strict';
import test from 'node:test';

import { releasePointerMenuFocus } from '../src/lib/menu-focus.ts';

for (const detail of [1, 2]) {
  test('pointer activation releases trigger before dialog remembers focus: ' + detail, () => {
    const body = {};
    const document = { activeElement: null };
    const trigger = {
      blur() {
        if (document.activeElement === this) document.activeElement = body;
      }
    };
    document.activeElement = trigger;
    releasePointerMenuFocus({ detail, currentTarget: trigger });
    // The dialog snapshots the current focus when opening and restores it on
    // close. Enter must not target the toolbar trigger after that restoration.
    const restoreTarget = document.activeElement;
    document.activeElement = {};
    document.activeElement = restoreTarget;
    assert.equal(document.activeElement, body);
  });
}

test('keyboard/assistive activation retains the trigger as the focus return target', () => {
  let blurred = false;
  releasePointerMenuFocus({
    detail: 0,
    currentTarget: {
      blur() {
        blurred = true;
      }
    }
  });
  assert.equal(blurred, false);
});
