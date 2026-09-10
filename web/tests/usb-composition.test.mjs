import assert from 'node:assert/strict';
import test from 'node:test';
import { canToggleDevice, endpointUsage, fitsBudget, matchingPreset, sameComposition, toggleDevice, usbDevices, usbPresets } from '../src/lib/usb-composition.ts';

const status = {
  budget: { inLimit: 6, outLimit: 7 },
  costs: {
    keyboard: { in: 1, out: 1 }, relative: { in: 1, out: 1 }, absolute: { in: 1, out: 1 },
    network: { in: 2, out: 1 }, disk: { in: 1, out: 1 }, serial: { in: 2, out: 1 }
  }
};

test('every preset fits and can be identified', () => {
  for (const { id, composition } of usbPresets) {
    assert.equal(fitsBudget(composition, status), true, id);
    assert.equal(matchingPreset(composition), id);
  }
});

test('all 64 selections: only impossible additions are blocked; removals always work', () => {
  for (let mask = 0; mask < 64; mask++) {
    const draft = { mode: 'normal', ...Object.fromEntries(usbDevices.map((name, bit) => [name, Boolean(mask & (1 << bit))])) };
    const inUsed = Number(draft.keyboard) + Number(draft.relative) + Number(draft.absolute) + 2 * Number(draft.network) + Number(draft.disk) + 2 * Number(draft.serial);
    assert.deepEqual(endpointUsage(draft, status.costs), { in: inUsed, out: usbDevices.filter(name => draft[name]).length });
    for (const name of usbDevices) {
      assert.equal(canToggleDevice(draft, name, status), draft[name] || inUsed + status.costs[name].in <= 6, `${mask}/${name}`);
    }
  }
});

test('serial can coexist with USB network after releasing two HID endpoints', () => {
  let draft = { ...usbPresets[0].composition };
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
  assert.equal(canToggleDevice({ ...usbPresets[3].composition }, 'disk', { ...status, budget: { inLimit: 99, outLimit: 3 } }), false);
});

test('manual network or storage additions leave compatibility mode', () => {
  const compatibility = usbPresets.find(preset => preset.id === 'compatibility').composition;
  for (const name of ['network', 'disk']) assert.equal(toggleDevice(compatibility, name).mode, 'normal');
  assert.equal(sameComposition(compatibility, usbPresets.find(preset => preset.id === 'control').composition), false);
});
