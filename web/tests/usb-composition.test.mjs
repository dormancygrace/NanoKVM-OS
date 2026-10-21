import assert from 'node:assert/strict';
import test from 'node:test';

import {
  canToggleDevice,
  defaultUsbIdentity,
  endpointUsage,
  fitsBudget,
  hasUsbDevice,
  identityProblem,
  matchingIdentityPreset,
  matchingPreset,
  normalizeUsbId,
  sameComposition,
  sameIdentity,
  setCompatibilityProfile,
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

test('every preset fits and can be identified', () => {
  for (const { id, composition } of usbPresets) {
    assert.equal(fitsBudget(composition, status), true, id);
    assert.equal(matchingPreset(composition), id);
  }
  assert.deepEqual(
    usbPresets.map(({ id }) => id),
    ['kvm', 'standard', 'console', 'headless', 'remote', 'control']
  );
});

test('a fresh install and the RustDesk composition are presets, not custom', () => {
  // build-alpine-sd-image.py: usb.dev and an empty usb.disk0, nothing else
  const fresh = {
    keyboard: true,
    relative: true,
    absolute: true,
    network: false,
    disk: true,
    serial: false,
    audio: false,
    mode: 'normal'
  };
  assert.equal(matchingPreset(fresh), 'kvm');
  assert.equal(usbPresets[0].id, 'kvm');
  // EnsureRemoteAccessUSB adds the three HID functions and audio to it
  assert.equal(matchingPreset({ ...fresh, audio: true }), 'remote');
  assert.deepEqual(endpointUsage(preset('remote'), status.costs), { in: 4, out: 5 });
});

test('the presets say what they give up', () => {
  // serial does not fit next to HID and network, and network not next to HID and serial
  assert.equal(canToggleDevice(preset('standard'), 'serial', status), false);
  assert.equal(canToggleDevice(preset('console'), 'network', status), false);
  // the headless server is the console and the disk, with room for network and audio
  assert.deepEqual(endpointUsage(preset('headless'), status.costs), { in: 3, out: 2 });
  for (const name of ['network', 'disk', 'audio'])
    assert.equal(canToggleDevice(preset('headless'), name, status), true);
  for (const name of ['keyboard', 'relative', 'absolute'])
    assert.equal(preset('headless')[name], false);
  assert.equal(preset('headless').disk, true);
  // RustDesk adds the three HID functions and audio to it: 5 IN / 5 OUT, still fits
  assert.equal(
    fitsBudget(
      { ...preset('headless'), keyboard: true, relative: true, absolute: true, audio: true },
      status
    ),
    true
  );
  // audio fits on top of the two full KVM presets
  assert.equal(canToggleDevice(preset('standard'), 'audio', status), true);
  assert.equal(canToggleDevice(preset('console'), 'audio', status), true);
});

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

test('serial can coexist with USB network after releasing two HID endpoints', () => {
  let draft = { ...preset('standard') };
  assert.equal(canToggleDevice(draft, 'serial', status), false);
  draft = toggleDevice(draft, 'relative');
  assert.equal(canToggleDevice(draft, 'serial', status), false);
  draft = toggleDevice(draft, 'absolute');
  assert.equal(canToggleDevice(draft, 'serial', status), true);
  draft = toggleDevice(draft, 'serial');
  assert.equal(draft.network, true);
  assert.equal(fitsBudget(draft, status), true);
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
