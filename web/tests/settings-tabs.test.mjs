import assert from 'node:assert/strict';
import test from 'node:test';

import {
  resolveSettingsTab,
  settingsCollectionOf,
  settingsGroupOf,
  settingsParentOf
} from '../src/pages/desktop/menu/settings/tabs.ts';

test('old settings ids open the pages that replaced them', () => {
  assert.equal(resolveSettingsTab('device'), 'system-general');
  assert.equal(resolveSettingsTab('vpn'), 'vpn');
  assert.equal(resolveSettingsTab('tailscale'), 'vpn-tailscale');
  assert.equal(resolveSettingsTab('vpn-wireguard'), 'vpn-wireguard');
  assert.equal(resolveSettingsTab('network-openvpn'), 'vpn-openvpn');
  assert.equal(resolveSettingsTab('extensions-rustdesk'), 'software-rustdesk');
  assert.equal(resolveSettingsTab('date-time'), 'system-date-time');
  assert.equal(resolveSettingsTab('video'), 'video');
});

test('pages belong to the group named by their prefix', () => {
  assert.equal(settingsGroupOf('network-wifi'), 'network');
  assert.equal(settingsGroupOf('system-memory'), 'system');
  assert.equal(settingsGroupOf('software-rustdesk'), 'software');
  assert.equal(settingsGroupOf('video'), undefined);
});

test('VPN providers are items of the VPN collection, not menu entries', () => {
  assert.equal(settingsCollectionOf('vpn-wireguard'), 'vpn');
  assert.equal(settingsCollectionOf('vpn'), undefined);
  assert.equal(settingsGroupOf('vpn-wireguard'), undefined);
  assert.equal(settingsParentOf('vpn-netbird'), 'vpn');
  assert.equal(settingsParentOf('network-wifi'), 'network');
  assert.equal(settingsParentOf('appearance'), undefined);
});
