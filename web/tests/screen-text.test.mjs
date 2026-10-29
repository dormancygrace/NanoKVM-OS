import assert from 'node:assert/strict';
import test from 'node:test';

import { boxesInRegion, boxesToText, isScreenTextLanguage } from '../src/lib/screen-text.ts';

const box = (text, x, y, w = 40, h = 16) => ({ text, score: 0.9, x, y, w, h });

test('joins words into lines in reading order', () => {
  const boxes = [
    box('world', 60, 10),
    box('second', 10, 40),
    box('Hello', 10, 12), // a little lower than its neighbour: same line
    box('line', 70, 41)
  ];
  assert.equal(boxesToText(boxes), 'Hello world\nsecond line');
});

test('keeps close but separate lines apart', () => {
  assert.equal(boxesToText([box('b', 10, 20), box('a', 10, 0)]), 'a\nb');
});

test('a tall box does not swallow the lines beside it', () => {
  const boxes = [box('Title', 0, 0, 200, 60), box('one', 300, 5), box('two', 300, 40)];
  assert.equal(boxesToText(boxes), 'one\nTitle\ntwo');
});

test('slanted lines of small text stay apart (a photo of a kernel panic)', () => {
  // As the server gave them: boxes higher than the line pitch, lines rising.
  const boxes = [
    box('db_trace_self_wrapper()', 480, 518, 188, 20),
    box('at db_trace_sel', 668, 522, 129, 19),
    box('f_wrapper+0x2b/frame', 797, 524, 163, 20),
    box('0xfffffe00d783d640', 960, 528, 148, 20),
    box('anic() at vpanic+0x136/', 500, 528, 184, 21),
    box('frame 0xfffffe00d783d770', 684, 532, 194, 21),
    box('panic()', 484, 544, 60, 11),
    box('at panic+0x43/frame', 540, 544, 164, 18),
    box('0xfffffe00d783d7d0', 704, 547, 152, 17)
  ];
  assert.equal(
    boxesToText(boxes),
    'db_trace_self_wrapper() at db_trace_sel f_wrapper+0x2b/frame 0xfffffe00d783d640\n' +
      'anic() at vpanic+0x136/ frame 0xfffffe00d783d770\n' +
      'panic() at panic+0x43/frame 0xfffffe00d783d7d0'
  );
});

test('no boxes, no text', () => {
  assert.equal(boxesToText([]), '');
});

test('selects the boxes whose centre is in the region, in picture fractions', () => {
  const result = {
    width: 1920,
    height: 1080,
    boxes: [box('in', 960, 540), box('out', 100, 100), box('edge', 1900, 540, 100)]
  };
  const region = { left: 0.4, top: 0.4, width: 0.6, height: 0.2 };
  assert.deepEqual(
    boxesInRegion(result, region).map((b) => b.text),
    ['in']
  );
  const whole = { left: 0, top: 0, width: 1, height: 1 };
  assert.equal(boxesInRegion(result, whole).length, 2); // edge's centre is past the picture
});

test('knows the add-on languages', () => {
  assert.ok(isScreenTextLanguage('latin'));
  assert.ok(isScreenTextLanguage('cyrillic'));
  assert.ok(isScreenTextLanguage('chinese'));
  assert.ok(!isScreenTextLanguage('korean'));
  assert.ok(!isScreenTextLanguage(null));
});
