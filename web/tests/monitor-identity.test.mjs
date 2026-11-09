import assert from 'node:assert/strict';
import test from 'node:test';

import * as identity from '../src/lib/monitor-identity.ts';
import * as model from '../src/lib/video-model.ts';

// A base block like a real monitor's: ACR, product 0x0BF4, serial 0x01020304,
// week 12 of 2019, a name and a serial number descriptor.
function edid() {
  const e = new Uint8Array(128);
  e.set([0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0]);
  e[8] = 0x04;
  e[9] = 0x72;
  e[10] = 0xf4;
  e[11] = 0x0b;
  e.set([4, 3, 2, 1], 12);
  e[16] = 12;
  e[17] = 29;
  const text = (offset, tag, s) => {
    e.set([0, 0, 0, tag, 0], offset);
    const bytes = new TextEncoder().encode((s + '\n').padEnd(13, ' ').slice(0, 13));
    e.set(bytes, offset + 5);
  };
  e[54] = 0x02; // a detailed timing, skipped
  text(72, 0xff, 'T7ZAA0024321');
  text(90, 0xfc, 'XB271HU');
  e[127] = (256 - (e.slice(0, 127).reduce((a, b) => a + b, 0) & 0xff)) & 0xff;
  return e;
}

const acer = {
  manufacturer: 'ACR',
  productCode: 0x0bf4,
  name: 'XB271HU',
  serialNumber: 0x01020304,
  serialText: 'T7ZAA0024321',
  week: 12,
  year: 2019
};

test('an EDID file gives the monitor identity', () => {
  assert.deepEqual(identity.identityFromEDID(edid()), { ok: true, identity: acer });
});

test('a text hex dump of an EDID is read too', () => {
  const dump = [...edid()]
    .map(
      (b, i) =>
        (i % 16 === 0 ? `\n${i.toString(16).padStart(3, '0')} | ` : '') +
        b.toString(16).padStart(2, '0')
    )
    .join(' ');
  assert.deepEqual(identity.identityFromEDID(new TextEncoder().encode(dump)).identity, acer);
});

test('damaged or foreign files are refused', () => {
  const bad = edid();
  bad[20] ^= 1;
  assert.deepEqual(identity.identityFromEDID(bad), { ok: false, error: 'checksum' });
  assert.deepEqual(identity.identityFromEDID(new TextEncoder().encode('hello')), {
    ok: false,
    error: 'not-edid'
  });
});

test('identity validation matches the server', () => {
  assert.equal(identity.identityIssue(acer), undefined);
  assert.equal(identity.identityIssue({ ...acer, serialNumber: null, serialText: '' }), undefined);
  for (const [field, change] of [
    ['manufacturer', { manufacturer: 'acr' }],
    ['productCode', { productCode: 0x10000 }],
    ['productCode', { productCode: NaN }],
    ['name', { name: '' }],
    ['name', { name: 'ABCDEFGHIJKLMN' }],
    ['name', { name: 'Écran' }],
    ['serialNumber', { serialNumber: 2 ** 32 }],
    ['serialText', { serialNumber: null }],
    ['week', { week: 55 }],
    ['year', { year: 1989 }]
  ]) {
    assert.equal(identity.identityIssue({ ...acer, ...change }), field, JSON.stringify(change));
  }
  assert.equal(identity.identityIssue({ ...acer, week: 255 }), undefined);
});

test('the draft key round-trips and orders its fields', () => {
  const key = identity.identityKey(acer);
  assert.deepEqual(identity.identityFromKey(key), acer);
  const shuffled = Object.fromEntries(Object.entries(acer).reverse());
  assert.equal(identity.identityKey(shuffled), key);
  assert.equal(identity.identityKey(null), '');
  assert.equal(identity.identityFromKey(''), null);
  // A field the form cannot parse stays invalid.
  assert.equal(
    identity.identityIssue(identity.identityFromKey(identity.identityKey({ ...acer, year: NaN }))),
    'year'
  );
});

test('an empty fixed serial number stays fixed and invalid', () => {
  // Switching to a fixed serial before typing it must not read as per profile.
  const fixed = identity.identityFromKey(identity.identityKey({ ...acer, serialNumber: NaN }));
  assert.notEqual(fixed.serialNumber, null);
  assert.equal(identity.identityIssue(fixed), 'serialNumber');
});

test('an identity change rewrites the monitor and is sent once', () => {
  const caps = {
    input: { width: 1920, height: 1080, fps: 60, maxFps: 60 },
    monitor: {
      programmable: true,
      requiresPowerCycle: false,
      powerCyclePending: false,
      followsStreamRate: true,
      selected: 1080,
      refreshHz: 60,
      identity: null,
      modes: [{ id: 1080, height: 1080, width: 1920, rates: [60], available: true }],
      portrait: { enabled: false, resolution: 1920, profiles: [] }
    },
    stream: {
      limits: [{ height: 0, width: 0, available: true }],
      rateTiers: [],
      minFps: 10,
      maxFps: 120
    },
    transports: { direct: ['h264', 'h265'], webrtc: ['h264', 'h265'], mjpeg: ['mjpeg'] },
    videoMemoryMiB: 0
  };
  const saved = {
    monitor: 1080,
    portrait: 0,
    height: 0,
    fps: 60,
    transport: 'direct',
    codec: 'h264',
    bitRate: 5000,
    quality: 80,
    mjpegChroma: 420,
    hevcDirect: null,
    directPlayback: 'paced',
    frameDetect: false,
    identity: ''
  };
  const draft = { ...saved, identity: identity.identityKey(acer) };
  assert.equal(model.draftChanges(saved, draft, caps).monitorRewrite, true);
  assert.deepEqual(model.settingsRequest(saved, draft), {
    monitorIdentity: { custom: true, ...acer }
  });
  assert.deepEqual(model.settingsRequest(draft, saved), { monitorIdentity: { custom: false } });
  assert.deepEqual(
    model.draftIssues(draft, caps, {
      direct: true,
      webrtc: true,
      directH265: true,
      webrtcH265: true
    }),
    {}
  );
  const invalid = { ...saved, identity: identity.identityKey({ ...acer, manufacturer: '' }) };
  assert.equal(
    model.draftIssues(invalid, caps, {
      direct: true,
      webrtc: true,
      directH265: true,
      webrtcH265: true
    }).identity,
    'identity'
  );
  assert.equal(model.settingsRequest(saved, saved).monitorIdentity, undefined);
});
