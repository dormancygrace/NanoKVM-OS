import assert from 'node:assert/strict';
import test from 'node:test';

import { expand, render, sampleValues } from '../src/lib/oled/layout.ts';

// The cases tests/test-oled-layout.py checks in kvm_system's drawing.
const values = { ...sampleValues, hostname: 'kvm-1234', fps: 5, eth: false };
const lit = (frame, x, y) => frame.pixels[y * frame.width + x] === 1;

test('variables expand as kvm_system expands them', () => {
  assert.equal(
    expand('{hostname}|{fps:3}|{fps:-3}|{hostname:3}|{nope}|{{fps}}', values),
    'kvm-1234|  5|5  |kvm|{nope}|{5}'
  );
});

test('images, text boxes and conditions draw as on the panel', () => {
  const image = render(
    {
      panel: 'narrow',
      rotation: 0,
      burnin: 'none',
      items: [{ type: 'image', x: 2, y: 1, width: 9, height: 2, data: '80804000' }]
    },
    values
  );
  assert.ok(lit(image, 2, 1) && lit(image, 10, 1) && lit(image, 3, 2) && !lit(image, 2, 2));

  const layout = {
    panel: 'wide',
    rotation: 0,
    burnin: 'none',
    items: [
      { type: 'rect', x: 0, y: 0, width: 128, height: 64, fill: true },
      {
        type: 'text',
        x: 0,
        y: 0,
        font: 8,
        align: 'right',
        width: 12,
        off: true,
        when: '!eth',
        text: ' A'
      }
    ]
  };
  let frame = render(layout, values);
  assert.ok(lit(frame, 0, 0) && lit(frame, 127, 63) && lit(frame, 2, 1) && !lit(frame, 8, 1));
  frame = render(layout, { ...values, eth: true });
  assert.ok(lit(frame, 8, 1));
});

test('a rotated panel is drawn in its rotated size', () => {
  const frame = render({ panel: 'wide', rotation: 90, burnin: 'none', items: [] }, values);
  assert.deepEqual([frame.width, frame.height], [64, 128]);
});
