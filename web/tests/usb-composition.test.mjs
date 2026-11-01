import assert from 'node:assert/strict';
import test from 'node:test';

import {
  canToggleDevice,
  defaultStorageIdentity,
  defaultUsbIdentity,
  endpointUsage,
  hasUsbDevice,
  identityProblem,
  matchingIdentityPreset,
  matchingPreset,
  normalizeUsbId,
  sameComposition,
  sameIdentity,
  sameStorageIdentity,
  setCompatibilityProfile,
  storageIdentityProblem,
  toggleDevice,
  usbDevices,
  usbIdentityPresets,
  usbPresets
} from '../src/lib/usb-composition.ts';

test('the identity is part of the composition and has its own presets', () => {
  const kvm = usbPresets[0].composition;
  // a server without an identity field, and one at the factory identity, are the same preset
  assert.equal(matchingPreset({ ...kvm, identity: defaultUsbIdentity }), 'kvm');
  assert.equal(
    sameComposition(kvm, { ...kvm, identity: { ...defaultUsbIdentity, vendorId: '0X3346' } }),
    true
  );
  const generic = usbIdentityPresets.find((item) => item.id === 'generic').identity;
  assert.equal(sameComposition(kvm, { ...kvm, identity: generic }), false);
  assert.equal(
    matchingPreset({ ...kvm, identity: generic }),
    'kvm',
    'the composition preset ignores the identity'
  );
  assert.equal(matchingIdentityPreset(undefined), 'nanokvm');
  assert.equal(matchingIdentityPreset(generic), 'generic');
  assert.equal(matchingIdentityPreset({ ...generic, vendorId: '0x046d' }), 'custom');
  // no preset carries another vendor's ID
  for (const { identity } of usbIdentityPresets) assert.equal(identity.vendorId, '0x3346');
  assert.equal(normalizeUsbId(' 0X46D '), '0x046d');
  assert.equal(
    sameIdentity({ ...generic, productId: '0x1009' }, { ...generic, productId: '0X1009' }),
    true
  );
  assert.equal(identityProblem(generic), undefined);
  assert.equal(identityProblem({ ...generic, vendorId: '046d' }), 'id');
  assert.equal(identityProblem({ ...generic, productId: '0x0000' }), 'id');
  assert.equal(identityProblem({ ...generic, product: '' }), 'string');
  assert.equal(identityProblem({ ...generic, product: ' padded' }), 'string');
  assert.equal(identityProblem({ ...generic, manufacturer: 'ünïcode' }), 'string');
  assert.equal(identityProblem({ ...generic, manufacturer: 'x'.repeat(127) }), 'string');
});

test('the serial number is part of the identity, but not of its presets', () => {
  const generic = usbIdentityPresets.find((item) => item.id === 'generic').identity;
  const serial = { ...generic, serialNumber: 'KVM-0001' };
  // empty and missing are both the chip UID
  assert.equal(sameIdentity(generic, { ...generic, serialNumber: '' }), true);
  assert.equal(sameIdentity(generic, serial), false);
  assert.equal(matchingIdentityPreset(serial), 'generic');
  assert.equal(matchingIdentityPreset({ ...defaultUsbIdentity, serialNumber: 'x' }), 'nanokvm');
  assert.equal(identityProblem(serial), undefined);
  assert.equal(identityProblem({ ...generic, serialNumber: '' }), undefined);
  for (const bad of ['has space', 'quo"te', '$(id)', 'x'.repeat(127)])
    assert.equal(identityProblem({ ...generic, serialNumber: bad }), 'serial', bad);
});

const status = {
  budget: { inLimit: 6, outLimit: 7 },
  costs: {
    keyboard: { in: 1, out: 1 },
    relative: { in: 1, out: 1 },
    absolute: { in: 1, out: 1 },
    network: { in: 2, out: 1 },
    disk: { in: 1, out: 1 },
    serial: { in: 2, out: 1 },
    audio: { in: 0, out: 1 }
  }
};
const preset = (id) => usbPresets.find((item) => item.id === id).composition;
test('all 128 selections: only impossible additions are blocked; removals always work', () => {
  for (let mask = 0; mask < 128; mask++) {
    const draft = {
      mode: 'normal',
      ...Object.fromEntries(usbDevices.map((name, bit) => [name, Boolean(mask & (1 << bit))]))
    };
    const inUsed =
      Number(draft.keyboard) +
      Number(draft.relative) +
      Number(draft.absolute) +
      2 * Number(draft.network) +
      Number(draft.disk) +
      2 * Number(draft.serial);
    assert.deepEqual(endpointUsage(draft, status.costs), {
      in: inUsed,
      out: usbDevices.filter((name) => draft[name]).length
    });
    for (const name of usbDevices) {
      assert.equal(
        canToggleDevice(draft, name, status),
        draft[name] || inUsed + status.costs[name].in <= 6,
        `${mask}/${name}`
      );
    }
  }
});

test('both endpoint directions are enforced', () => {
  assert.equal(
    canToggleDevice({ ...preset('control') }, 'disk', {
      ...status,
      budget: { inLimit: 99, outLimit: 3 }
    }),
    false
  );
});

test('the compatibility profile is a switch: it drops network, disk and audio, and they leave it again', () => {
  const compatibility = setCompatibilityProfile({ ...preset('standard'), audio: true }, true);
  assert.equal(compatibility.mode, 'hid-only');
  for (const name of ['network', 'disk', 'audio']) assert.equal(compatibility[name], false);
  assert.equal(matchingPreset(compatibility), 'custom');
  assert.equal(sameComposition(compatibility, preset('control')), false);
  for (const name of ['network', 'disk', 'audio'])
    assert.equal(toggleDevice(compatibility, name).mode, 'normal');
  assert.equal(toggleDevice(compatibility, 'serial').mode, 'hid-only');
  assert.equal(matchingPreset(setCompatibilityProfile(compatibility, false)), 'control');
});

test('an empty composition turns USB off', () => {
  const off = Object.fromEntries(usbDevices.map((name) => [name, false]));
  assert.equal(hasUsbDevice({ ...off, mode: 'normal' }), false);
  assert.equal(hasUsbDevice({ ...off, serial: true, mode: 'normal' }), true);
  for (const { composition } of usbPresets) assert.equal(hasUsbDevice(composition), true);
});

test('storage names affect dirty state but preserve composition presets', () => {
  const base = usbPresets[0].composition;
  const storageIdentity = { ...defaultStorageIdentity, image: 'Rescue' };
  assert.equal(sameComposition(base, { ...base, storageIdentity }), false);
  assert.equal(matchingPreset({ ...base, storageIdentity }), 'kvm');
  assert.equal(sameStorageIdentity(undefined, defaultStorageIdentity), true);
  assert.equal(
    sameStorageIdentity({ vendor: '', image: '', setup: '', user: '' }, defaultStorageIdentity),
    true
  );
  assert.equal(storageIdentityProblem(storageIdentity), false);
  for (const bad of [
    { vendor: '123456789' },
    { image: '12345678901234567' },
    { setup: ' leading' },
    { user: 'a\nb' },
    { image: 'é' }
  ]) {
    assert.equal(storageIdentityProblem({ ...defaultStorageIdentity, ...bad }), true);
  }
  assert.deepEqual(
    setCompatibilityProfile({ ...base, storageIdentity }, true).storageIdentity,
    storageIdentity
  );
});
