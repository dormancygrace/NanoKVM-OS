import assert from 'node:assert/strict';
import test from 'node:test';

import { builtinMenu, orderMenu } from '../src/pages/desktop/menu/settings/menu.ts';
import { isSettingsGroup, settingsParentOf } from '../src/pages/desktop/menu/settings/tabs.ts';

const ids = (entries) => entries.map((entry) => entry.id);

test('every built-in entry is reachable and listed once', () => {
  const ordered = ids(orderMenu(builtinMenu));
  assert.equal(new Set(ordered).size, ordered.length);
  assert.deepEqual([...ordered].sort(), ids(builtinMenu).sort());
});

test('top-level entries follow their order, groups keep their pages under them', () => {
  const ordered = ids(orderMenu(builtinMenu));
  const top = ordered.filter((id) => !settingsParentOf(id));
  assert.deepEqual(top, [
    'dashboard',
    'video',
    'usb',
    'network',
    'vpn',
    'services',
    'system',
    'ai',
    'software',
    'appearance',
    'account',
    'about'
  ]);
  for (const [index, id] of ordered.entries()) {
    const parent = settingsParentOf(id);
    if (!parent) continue;
    // Everything between the parent and this page belongs to the same parent.
    const at = ordered.indexOf(parent);
    assert.ok(at >= 0 && at < index, id);
    assert.ok(
      ordered.slice(at + 1, index).every((between) => settingsParentOf(between) === parent),
      id
    );
  }
});

test('Services holds Redfish/IPMI and RustDesk', () => {
  const ordered = ids(orderMenu(builtinMenu));
  const at = ordered.indexOf('services');
  assert.deepEqual(ordered.slice(at, at + 3), ['services', 'services-bmc', 'services-rustdesk']);
});

test('an entry added later goes between the built-ins by its order', () => {
  const ordered = ids(
    orderMenu([
      ...builtinMenu,
      { id: 'services-ipmi', order: 15, title: { text: 'IPMI' } },
      { id: 'tools', order: 65, title: { text: 'Tools' } }
    ])
  );
  const at = ordered.indexOf('services');
  assert.deepEqual(ordered.slice(at, at + 4), [
    'services',
    'services-bmc',
    'services-ipmi',
    'services-rustdesk'
  ]);
  assert.equal(ordered[ordered.indexOf('tools') + 1], 'system');
});

test('an entry whose group is missing is left out; hidden stays hidden', () => {
  const ordered = orderMenu([
    { id: 'network-x', order: 10, title: { text: 'x' } },
    { id: 'vpn', order: 10, title: { text: 'VPN' } },
    { id: 'vpn-x', order: 10, hidden: true, title: { text: 'x' } }
  ]);
  assert.deepEqual(ids(ordered), ['vpn', 'vpn-x']);
  assert.equal(ordered[1].hidden, true);
});

test('every group in the navigation has its own entry', () => {
  for (const entry of builtinMenu) {
    const parent = settingsParentOf(entry.id);
    if (parent)
      assert.ok(
        builtinMenu.some((item) => item.id === parent),
        entry.id
      );
    if (isSettingsGroup(entry.id)) assert.equal(entry.hidden, undefined);
  }
});
