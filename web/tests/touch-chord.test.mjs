import assert from 'node:assert/strict';
import test from 'node:test';
import { createTouchChord } from '../src/pages/desktop/mouse/touch-chord.ts';
const a = { identifier: 7, clientX: 100, clientY: 100 };
const b = { identifier: 9, clientX: 160, clientY: 100 };
test('held first finger + second tap clicks once; first-finger release does not click', () => {
  const g = createTouchChord();
  g.start([a,b], 10000, true);
  assert.equal(g.end([a], [b], 10100), true);
  assert.equal(g.end([], [a], 10200), false);
  g.start([a,b], 11000, true);
  assert.equal(g.end([b], [a], 11100), false);
});
test('repeat second-finger taps and simultaneous release are supported', () => {
  const g = createTouchChord();
  for (let t = 0; t < 3; t++) {
    g.start([a,b], t*1000, true);
    assert.equal(g.end(t === 2 ? [] : [a], t === 2 ? [a,b] : [b], t*1000+100), true);
  }
});
test('stationary events and small jitter never scroll', () => {
  const g = createTouchChord(); g.start([a,b],0,true);
  assert.equal(g.move([b,a]), null);
  const moved = {...b,clientY:103};
  assert.equal(g.move([a,moved]),null);
  assert.equal(g.end([a],[moved],100),true);
});
test('two-finger vertical/horizontal scrolling suppresses right click', () => {
  for (const axis of ['clientX','clientY']) {
    const g = createTouchChord(); g.start([a,b],0,true);
    const moved = [a,b].map(p => ({...p,[axis]:p[axis]+30}));
    assert.deepEqual(g.move(moved),axis==='clientX'?{x:1,y:0}:{x:0,y:1});
    assert.equal(g.end([],moved,100),false);
  }
});
test('pinch, third finger, long hold, cancellation and prior long-press/drag cannot click', () => {
  const g = createTouchChord();
  g.start([a,b],0,true);
  g.move([{...a,clientX:80},{...b,clientX:180}]);
  assert.equal(g.end([a],[b],100),false);
  g.start([a,b,{...b,identifier:3}],0,true);
  assert.equal(g.end([a],[b],100),false);
  g.start([a,b],0,true); assert.equal(g.end([a],[b],500),false);
  g.start([a,b],0,true); g.reset(); assert.equal(g.end([a],[b],100),false);
  g.start([a,b],0,false); assert.equal(g.end([a],[b],100),false);
});
test('movement delivered only in touchend still cancels tap', () => {
  const g = createTouchChord(); g.start([a,b],0,true);
  assert.equal(g.end([a],[{...b,clientX:200}],100),false);
});
