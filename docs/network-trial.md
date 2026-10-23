# Network changes on trial

A change that can cut the device off is applied at once, on trial. Unless it is confirmed within two minutes from an address the device can still be reached at, the device restores its previous settings. Today this covers the Ethernet settings: on/off, DHCP or static IPv4, and VLAN.

## How it is confirmed

The server answers the change with a trial:
- a public ID;
- a one-time token;
- the time left;
- the addresses to look for the device at, the most likely first: the new static address, the address the page uses now, the mDNS name when avahi runs, then every other IPv4 address of the device.

The page that made the change looks for the device every two seconds. It asks `GET /api/network/trial/probe` at each address. That endpoint is public, shows only the trial ID, and says `applied` once the interface has restarted with the change; before that the device still runs its previous settings, and reaching it proves nothing.

- **Reached at the page's own address:** the page confirms there.
- **Reached at another address:** the browser opens `/#/network/confirm?token=…` there, and that page confirms with the token, before any login. The token sits in the URL fragment, which the browser does not send to the server.
- **Opened by hand:** the dialog also lists the addresses as links, which open in a new tab. Over HTTPS with a self-signed certificate this is the way to a new address. The page cannot probe an address whose certificate the browser does not trust yet, so the certificate has to be accepted in the new tab first.

`POST /api/network/trial/confirm {token}` keeps the change. It is accepted only once, only with the trial's token, and only after the change was applied.

- **The check is app-wide.** The trial is kept in the tab's session storage, so the check goes on with the settings closed and resumes after a reload.
- **An answer that got lost.** If a confirmation's answer is lost, the next probe finds no trial waiting. The dialog then says so instead of reporting a revert, and the page shows the settings in effect.
- **Reaching the device by any path counts.** The device may be reached over Wi-Fi, USB networking or a VPN at the same name: that keeps the change even if Ethernet itself is down. The point is only that the device is still reachable.
- **No known new address.** A change to DHCP, or turning Ethernet on, gives no new address in advance. Then only the page's own address, the mDNS name or the device's other addresses can find it.
- **The token is the authorization.** It has 128 random bits, is compared in constant time and is valid once. Over plain HTTP it travels in the clear, which matters only within the two minutes.

## How it is undone

Before writing anything, the server records the previous contents of `/boot/eth.nodhcp`, `/boot/eth.disabled` and `/boot/eth.vlan` in `/boot/net-trial.json`, and syncs.

The previous settings come back:
- when the two minutes pass;
- when the interface fails to restart with the change, at once;
- when a write fails halfway, without a restart, since the interface never ran the change;
- when the server starts and finds the record. That covers a restart during the trial, also a power cycle by someone who lost access.

The record is written and synced before the rename, and removed only once the change is confirmed or the previous settings are back. A restore that fails keeps it for the next start. While one change waits for confirmation or is being undone, another network change is refused.

On a restart the record only restores the three Ethernet files above. A record that cannot be read, or that names any other file, is moved to `net-trial.json.bad` and logged as an error. The unconfirmed change then stays in effect.
