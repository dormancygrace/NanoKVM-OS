import assert from 'node:assert/strict';
import test from 'node:test';

import { probeTimeout, replyWindow, StallCheck } from '../src/pages/desktop/screen/stall-check.ts';

// Times are offsets from a nonzero epoch: zero means no frame yet.
const scenarios = [
  [
    'unarmed',
    [
      ['step', 60000, 'none'],
      ['shown', 60000],
      ['step', 61000, 'none']
    ]
  ],
  [
    'frames arriving',
    [
      ['frame', 0],
      ['step', 999, 'none']
    ]
  ],
  [
    'unanswered heartbeat',
    [
      ['frame', 0],
      ['step', 1000, 'heartbeat'],
      ['step', 1100, 'none'],
      ['step', 1399, 'none'],
      ['step', 1400, 'probe'],
      ['confirmsDead', 1450, true]
    ]
  ],
  [
    'answered heartbeat restarts the window',
    [
      ['frame', 0],
      ['step', 1000, 'heartbeat'],
      ['reply', 1005],
      ['step', 1100, 'none'],
      ['step', 2099, 'none'],
      ['step', 2100, 'heartbeat']
    ]
  ],
  [
    'frame while probing',
    [
      ['frame', 0],
      ['step', 1000, 'heartbeat'],
      ['step', 1400, 'probe'],
      ['frame', 1420],
      ['confirmsDead', 1450, false],
      ['unanswered', 0, false]
    ]
  ],
  [
    'late reply while probing',
    [
      ['frame', 0],
      ['step', 1000, 'heartbeat'],
      ['step', 1400, 'probe'],
      ['reply', 1430],
      ['confirmsDead', 1450, false]
    ]
  ],
  [
    'slow connection',
    [
      ['opened', 2100],
      ['frame', 0],
      ['step', 1000, 'heartbeat'],
      ['step', 1600, 'none'],
      ['reply', 1650],
      ['step', 1700, 'none'],
      ['probeTimeout', 0, 4200]
    ]
  ],
  [
    'shown page restarts the clock',
    [
      ['frame', 0],
      ['shown', 5000],
      ['step', 5500, 'none'],
      ['step', 6000, 'heartbeat']
    ]
  ],
  [
    'shown page discards a pending check',
    [
      ['frame', 0],
      ['step', 1000, 'heartbeat'],
      ['shown', 5000],
      ['unanswered', 0, false],
      ['step', 5500, 'none'],
      ['step', 6000, 'heartbeat']
    ]
  ]
];

test('stall decisions and cancellation boundaries', () => {
  for (const [name, actions] of scenarios) {
    const stall = new StallCheck();
    stall.opened(30);
    for (const [method, ms, expected] of actions) {
      const value =
        method === 'unanswered'
          ? stall.heartbeatUnanswered
          : stall[method](method === 'opened' ? ms : 100000 + ms);
      if (expected !== undefined) assert.equal(value, expected, name + ': ' + method);
    }
  }
});

test('reply and probe windows follow the connection time with LAN floors', () => {
  for (const [open, reply, probe] of [
    [0, 400, 800],
    [30, 400, 800],
    [2100, 2100, 4200]
  ]) {
    assert.equal(replyWindow(open), reply);
    assert.equal(probeTimeout(open), probe);
  }
});
