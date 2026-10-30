# Redfish and IPMI

Data centre tools can switch the host on and off through the NanoKVM's ATX connection, as they would a server's BMC:

- **Redfish** is part of NanoKVM OS (`fence_redfish`, Ansible's `community.general.redfish_*`).
- **IPMI over LAN** comes with the optional `nanokvm-ipmi` add-on (`ipmitool -I lanplus`, `fence_ipmilan`). Install it from Software > Add-ons or from the settings page; its card there appears once the package repository offers the add-on.

Both are off by default. They are set up in Services > Redfish and IPMI, each on its own switch, with one BMC account for both.

From version 0.2.0 the IPMI add-on also adds an "IPMI status" section to that page, drawn from its manifest (see [ADDONS.md](ADDONS.md)): open sessions, clients locked out for too many logins, and the last power command. It reads them while IPMI is switched on; otherwise the section says the add-on's service is not running.

## What it serves

| Request | Redfish (`ResetType`) | IPMI (`ipmitool chassis power …`) | Buttons |
| --- | --- | --- | --- |
| Power state | `PowerState` of `Systems/1` | `status` | none; the power LED |
| On | `On` | `on` | short press, unless the host is on |
| Off | `ForceOff` | `off` | power button held, unless the host is off |
| Shut down | `GracefulShutdown` | `soft` | short press, unless the host is off |
| Reset | `ForceRestart` | `reset` | reset button |
| Power cycle | `PowerCycle` | `cycle` | hold, wait for the LED to go out (≤10 s), 2 s, short press; an error without the short press if the LED stays lit |
| Power button | `PushPowerButton` | — | short press |

- **"Unless the host is on/off"** uses the power LED; a press that changes nothing is skipped.
- **Power LED connected** (on by default): turn it off when the NanoKVM's power LED header is not wired to the host. An unwired LED line reads "off" whatever the host does, so a forced power-off would be reported done without a press, and fencing would believe a running host is off. With the setting off, Redfish omits `PowerState`, IPMI answers Get Chassis Status with an error ("not in this state"; IPMI has no "unknown", and "on" would make a fence agent skip a power-on), and every power request presses; a forced power-off of a host that is already off holds the button, which leaves it off. Fence agents (`fence_redfish`, `fence_ipmilan`) read the state before acting, so with the setting off they refuse rather than act blindly; direct requests (`ipmitool chassis power off`, a Redfish `Reset`) press.
- **The hold of a forced power-off** is the power menu's long press, 5 s, unless the settings page sets its own.
- **Redfish** serves `/redfish/v1/` (no login), `Systems/1`, `Chassis/1`, `Managers/1` and `SessionService`.
  - It accepts HTTP Basic and `X-Auth-Token` sessions (`POST /redfish/v1/SessionService/Sessions`).
  - With Redfish off, `/redfish/` answers 404.
- **IPMI** is IPMI v2.0 / RMCP+ only.
  - Cipher suites 3 and 17; other suites and IPMI v1.5 sessions are refused.
  - It serves Get Device ID, Get Chassis Status, Chassis Control, Get Channel Cipher Suites and the session commands.
- **Not served:** boot device selection, sensors, SOL. Use the virtual media and netboot features of the web page instead.

## Accounts and security

- **One BMC account for both protocols, separate from the web accounts.**
  - User name: 1–16 characters.
  - Password: 8–20 bytes, the IPMI limit.
  - The `/redfish/` routes take no web login; the server checks the BMC account, as any BMC does.
- **The password is kept as written** in `/etc/kvm/bmc/config.json` (root only). RMCP+ uses it as the key of its login handshake.
- **RMCP+ gives out a password hash.** It sends a hash of the password to anyone who starts a login (RAKP message 2), so an attacker who can reach UDP 623 can guess the password offline. Hence IPMI is an add-on, and off until turned on. Use a long password used nowhere else, and open the port only to trusted networks.
- **IPMI answers are larger than the requests.** The ASF pong and RAKP message 2 are 2–3 times the size of their requests, which can be abused for reflection from forged addresses. This is inherent to the protocol.
- **Login limits.**
  - Redfish refuses a client for a minute after 5 failed logins. The client is the connection's address (for IPv6, its /64); an `X-Forwarded-For` header is ignored. Each attempt is counted before the password is compared, so parallel requests cannot get more guesses. Behind a reverse proxy of your own (nginx, Tailscale Serve), all clients share the proxy's address and its limit.
  - Redfish refuses state-changing requests that are not JSON (`Content-Type: application/json`) or that a browser marks as cross-site, so another web page cannot use credentials the browser has cached.
  - IPMI refuses a client for a minute after 20 logins in a minute, failed or not. A wrong password shows only on the client's side, and each login hands out a hash. The table of clients is bounded, since UDP addresses can be forged.
- **Power control does not wait for the browser's input control.** A fence agent must be able to switch the host off whoever holds the keyboard.

## How it works

- **Redfish** runs in the web server itself, on its port and certificate: no extra process. A change of settings applies at once and ends the open Redfish sessions; fence agents log in for each action.
- **IPMI** runs in the add-on's daemon `nkos-ipmi` (OpenRC service `nanokvm-ipmi`, about 1 MiB of its own memory).
  - It is apart from the server so that a fault in parsing untrusted UDP packets cannot stop the server and its video.
  - It listens on UDP 623 (configurable).
  - It reaches the buttons through the server's loopback-only `/api/internal/atx`, which takes the internal token. It submits complete power operations, including a cycle’s wait and second press, and never opens a GPIO line itself.
  - A power action is answered at once and then carried out, since a forced power-off holds the button for longer than `ipmitool` waits.
- **BMC operations are exclusive:** Redfish and IPMI share a server-side guard for the whole operation. An overlapping BMC action fails as busy; Redfish returns HTTP 503. IPMI acknowledges queued commands before execution, so execution failures appear in the application log.
- **The buttons and LEDs are the web page's:** the same GPIO lines, pulse lock and LED monitor.

The add-on has a version of its own and is released apart from NanoKVM OS: its updates reach devices through the package repository, without an OS release. It needs NanoKVM OS 3.5.0 or later (the package depends on `nanokvm-os>=3.5.0`). Removing the add-on keeps `/etc/kvm/bmc` and Redfish. NanoKVM Lite has no ATX lines: power requests fail there, and Redfish omits `PowerState`.

## Trying it

```
curl -k -u 'admin:<password>' https://<device>/redfish/v1/Systems/1
curl -k -u 'admin:<password>' -H 'Content-Type: application/json' -d '{"ResetType":"PowerCycle"}' \
  https://<device>/redfish/v1/Systems/1/Actions/ComputerSystem.Reset
ipmitool -I lanplus -H <device> -U admin -P '<password>' chassis power status
```

## Source and development

The IPMI daemon, protocol tests and package recipe live in [`addons/ipmi/`](../addons/ipmi/README.md), a separate Go module. Shared configuration, power operations and the internal ATX client live in `server/pkg/bmc`; Redfish stays in `server/internal/bmc/redfish`. Both protocols use the same shared BMC implementation.
